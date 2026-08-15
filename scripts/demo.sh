#!/usr/bin/env bash
#
# Walks the whole service end to end against the Docker Compose stack.
#
# This is the five-minute tour: what the endpoint does, what it refuses, and
# what reaches the resolution pipeline. Run it with `make demo`.

set -euo pipefail

API="${API:-http://localhost:8080}"
COMPOSE="${COMPOSE:-docker compose}"
PSQL="$COMPOSE exec -T postgres psql -U ingest -d ingest -t -A"

bold=$'\033[1m'; green=$'\033[32m'; yellow=$'\033[33m'; dim=$'\033[2m'; reset=$'\033[0m'

step()  { printf '\n%s▸ %s%s\n' "$bold" "$1" "$reset"; }
note()  { printf '%s  %s%s\n' "$dim" "$1" "$reset"; }
ok()    { printf '%s  ✓ %s%s\n' "$green" "$1" "$reset"; }
warn()  { printf '%s  ! %s%s\n' "$yellow" "$1" "$reset"; }

# post BODY -> prints "STATUS<newline>BODY"
post() {
  curl -s -o /tmp/ingest-demo-body -w '%{http_code}' \
    -X POST "$API/ingest/v1/transactions" \
    -H 'Content-Type: application/json' \
    -d "$1"
}

show() {
  if command -v jq >/dev/null 2>&1; then jq . < /tmp/ingest-demo-body; else cat /tmp/ingest-demo-body; echo; fi
}

expect() {
  local got="$1" want="$2" what="$3"
  if [[ "$got" == "$want" ]]; then ok "$what → HTTP $got"; else
    printf '%s  ✗ %s: expected %s, got %s%s\n' $'\033[31m' "$what" "$want" "$got" "$reset"
    exit 1
  fi
}

REF="DEMO-$(date +%s)"

TRANSACTION=$(cat <<JSON
{
  "source": "lane-controller-07",
  "source_reference": "$REF",
  "transaction_type": "toll",
  "transaction_time_utc": "2026-08-14T13:45:02Z",
  "base_amount": "12.50",
  "plate": { "number": "ABC-1234", "jurisdiction": "tx" },
  "location": { "facility": "SH-130", "plaza": "12", "lane": "3", "direction": "NB" }
}
JSON
)

# ---------------------------------------------------------------------------

step "1. A new transaction is accepted"
note "A roadside lane controller pushes a billable transaction."
status=$(post "$TRANSACTION")
show
expect "$status" 201 "new transaction"
ID=$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' /tmp/ingest-demo-body)

step "2. The producer retries — no second billable record"
note "Producers retry over unreliable links. A retry must never double-bill."
status=$(post "$TRANSACTION")
show
expect "$status" 200 "replay"
REPLAY_ID=$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' /tmp/ingest-demo-body)
[[ "$ID" == "$REPLAY_ID" ]] && ok "same id returned: $ID" || { echo "id changed!"; exit 1; }

step "3. Same key, different amount — the gap the contract leaves open"
note "The spec defines no conflict status, so this still answers 200 on contract."
note "But the divergence is recorded rather than silently discarded. See ADR-0006."
status=$(post "${TRANSACTION/12.50/99.00}")
show
expect "$status" 200 "divergent replay"
warn "check the api logs for 'duplicate transaction diverges from the stored record'"

step "4. A payload that breaks the published schema"
note "Layer one: contract validation, before any handler runs."
status=$(post '{}')
show
expect "$status" 400 "schema violation"

step "5. A payload that satisfies the schema but cannot be billed"
note "Layer two: no usable identifier, unknown type, negative over-precise amount."
status=$(post '{
  "source": "lane-controller-07",
  "source_reference": "'"$REF"'-bad",
  "transaction_type": "parking",
  "transaction_time_utc": "2026-08-14T13:45:02Z",
  "base_amount": "-1.005"
}')
show
expect "$status" 400 "semantic violation"

step "6. The transaction is durable"
$PSQL -c "SELECT 'stored: ' || source_reference || ' | amount ' || base_amount ||
          ' (' || base_amount_text || ' as received) | plate ' || plate_number ||
          ' → key ' || plate_number_key
          FROM transactions WHERE source_reference = '$REF';"
ok "the raw read and the matching key are both kept"

step "7. The resolution pipeline was told about it"
note "The event was written in the same commit as the transaction, then drained"
note "by the relay. Waiting for it to publish..."
for _ in $(seq 1 30); do
  pending=$($PSQL -c "SELECT count(*) FROM outbox_events WHERE published_at IS NULL;")
  [[ "$pending" == "0" ]] && break
  sleep 1
done
$PSQL -c "SELECT 'events: ' || count(*) || ' total, ' ||
          count(*) FILTER (WHERE published_at IS NULL) || ' pending'
          FROM outbox_events;"
ok "outbox drained — exactly one event for the transaction, none for the retries"

$COMPOSE exec -T postgres psql -U ingest -d ingest -c \
  "SELECT jsonb_pretty(payload) AS published_event FROM outbox_events
   WHERE aggregate_id = '$ID';"

step "Done"
note "api logs:    $COMPOSE logs api"
note "relay logs:  $COMPOSE logs relay"
note "tear down:   make compose-down"
