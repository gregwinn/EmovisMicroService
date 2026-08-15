-- +goose Up
-- +goose StatementBegin

-- Accepted billable tolling transactions.
--
-- This table is append-mostly and effectively immutable: the contract states
-- base_amount is fixed once accepted and that corrections arrive as separate
-- adjustments. Later lifecycle changes touch only the two status columns.
CREATE TABLE transactions (
    -- UUIDv7: time-ordered, so inserts stay near the right-hand edge of the
    -- primary key index on a table that only ever grows.
    id                     UUID          PRIMARY KEY,

    -- The producer's idempotency key. source_reference is unique *within* a
    -- source, so the pair is the key: two agencies both numbering from 1 must
    -- not collide.
    source                 TEXT          NOT NULL,
    source_reference       TEXT          NOT NULL,

    -- Operator-configurable at runtime, so intentionally TEXT and not an enum
    -- type. Adding a billable event type must not require a migration.
    transaction_type       TEXT          NOT NULL,

    -- When the vehicle used the facility. Legitimately far in the past for a
    -- batch or image-review replay, so there is no lower bound here.
    occurred_at            TIMESTAMPTZ   NOT NULL,
    -- When this service accepted it. The gap is producer submission lag.
    received_at            TIMESTAMPTZ   NOT NULL,

    -- Identifiers are stored twice: verbatim as evidence for disputes, and
    -- canonicalized as a matching key for downstream resolution.
    plate_number           TEXT,
    plate_jurisdiction     TEXT,
    plate_number_key       TEXT,
    plate_jurisdiction_key TEXT,
    transponder_number     TEXT,
    transponder_number_key TEXT,

    -- Money, three ways, deliberately.
    --   base_amount       exact numeric, for arithmetic and reporting
    --   base_amount_text  the producer's bytes, for the evidentiary record
    --   currency          ISO-4217; the minor-unit exponent is not always 2
    base_amount            NUMERIC(19, 4) NOT NULL,
    base_amount_text       TEXT           NOT NULL,
    currency               TEXT           NOT NULL,

    -- Producer passthrough. The contract declares both free-form and says
    -- metadata is "not interpreted", so they are stored whole rather than
    -- modelled into columns that would fight the next producer's shape.
    location               JSONB,
    metadata               JSONB,

    -- Two orthogonal lifecycles, not one status. A transaction can be priced
    -- but unattributed, or attributed and unpaid.
    association_status     TEXT          NOT NULL,
    settlement_status      TEXT          NOT NULL,

    -- SHA-256 over the billable content, used to tell a genuine replay apart
    -- from a different transaction sent under the same key.
    fingerprint            CHAR(64)      NOT NULL,

    -- The idempotency guarantee, enforced by the database rather than by
    -- application logic. Two concurrent pushes of the same key cannot both
    -- win: one gets the row, the other gets a conflict and reads it back.
    CONSTRAINT transactions_idempotency_key
        UNIQUE (source, source_reference),

    -- The domain rule the JSON schema cannot express, restated where it cannot
    -- be bypassed. Application validation is the friendly error message; this
    -- is the guarantee.
    CONSTRAINT transactions_requires_identifier
        CHECK (plate_number_key IS NOT NULL OR transponder_number_key IS NOT NULL),

    -- base_amount is an as-received rate. Credits are separate adjustments.
    CONSTRAINT transactions_amount_not_negative
        CHECK (base_amount >= 0)
);

-- Downstream resolution matches on the canonical identifier forms, never the
-- raw reads.
CREATE INDEX transactions_plate_number_key_idx
    ON transactions (plate_number_key)
    WHERE plate_number_key IS NOT NULL;

CREATE INDEX transactions_transponder_number_key_idx
    ON transactions (transponder_number_key)
    WHERE transponder_number_key IS NOT NULL;

-- Reconciliation and settlement reporting are period queries over when the
-- vehicle passed, not when we happened to receive the record.
CREATE INDEX transactions_occurred_at_idx ON transactions (occurred_at);

-- Operational: "what has this producer sent us recently", and the backlog view
-- for anything still awaiting attribution.
CREATE INDEX transactions_source_received_at_idx ON transactions (source, received_at DESC);

CREATE INDEX transactions_association_status_idx
    ON transactions (association_status)
    WHERE association_status <> 'associated';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE transactions;
-- +goose StatementEnd
