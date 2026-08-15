package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/gregwinn/EmovisMicroService/internal/httpapi/gen"
	"github.com/gregwinn/EmovisMicroService/internal/httpapi/middleware"
	"github.com/gregwinn/EmovisMicroService/internal/platform/metrics"
	"github.com/gregwinn/EmovisMicroService/internal/transaction"
)

// ingestServer implements gen.ServerInterface.
//
// Having the compiler enforce that every operation in api/openapi.yaml has an
// implementation is the second half of ADR-0002: the spec drives the types, and
// the types drive the code that must exist.
type ingestServer struct {
	logger  *slog.Logger
	rules   transaction.Rules
	store   transaction.Store
	metrics *metrics.Metrics
}

// IngestTransaction handles POST /ingest/v1/transactions.
//
// The request has already been validated against the contract by
// specValidator, so the body is well-formed JSON with every required field
// present and correctly typed. What happens here is the rest:
//
//	decode → map to the domain → semantic rules → durable, idempotent ingest
//
// Status codes come straight from the contract: 201 for a new record, 200 for
// a duplicate, 400 for anything the producer got wrong.
func (s *ingestServer) IngestTransaction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	started := time.Now()

	var body gen.IngestRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		// Practically unreachable: the validator already parsed this body
		// against the schema and restored it for us. Handled anyway, because a
		// panic here would take down a process serving other producers.
		s.logger.WarnContext(ctx, "body decoded by the validator but not by the handler",
			slog.String("request_id", middleware.RequestIDFrom(ctx)),
			slog.Any("error", err))

		s.metrics.IngestResult("unknown", metrics.ResultRejected, time.Since(started))
		writeError(w, http.StatusBadRequest, "request body could not be read",
			[]FieldError{{Field: "body", Reason: "is not valid JSON"}})
		return
	}

	tx, problems, err := s.rules.Accept(toSubmission(body))
	if err != nil {
		// The producer's payload was fine; this service could not do its job.
		// Blaming them with a 400 would send them chasing a phantom bug.
		s.logger.ErrorContext(ctx, "could not build transaction",
			slog.String("request_id", middleware.RequestIDFrom(ctx)),
			slog.String("source", body.Source),
			slog.Any("error", err))

		s.metrics.IngestResult(body.Source, metrics.ResultError, time.Since(started))
		writeError(w, http.StatusInternalServerError, "transaction could not be processed", nil)
		return
	}

	if len(problems) > 0 {
		fields := toFieldErrors(problems)

		// Logged at Info: producers sending unbillable payloads is routine
		// operational traffic, and the aggregate rate per producer is the
		// signal. Only field names and reasons are logged — never plate or
		// transponder values.
		s.logger.InfoContext(ctx, "transaction rejected by validation rules",
			slog.String("request_id", middleware.RequestIDFrom(ctx)),
			slog.String("source", body.Source),
			slog.String("source_reference", body.SourceReference),
			slog.String("fields", formatFieldErrors(fields)))

		// One increment per rejected field, so a dashboard can say *which* rule a
		// producer keeps breaking rather than only that something is wrong.
		for _, f := range fields {
			s.metrics.ValidationFailure(body.Source, "semantic", f.Field)
		}
		s.metrics.IngestResult(body.Source, metrics.ResultRejected, time.Since(started))

		writeError(w, http.StatusBadRequest, "transaction failed validation", fields)
		return
	}

	outcome, err := s.store.Ingest(ctx, tx)
	if err != nil {
		s.logger.ErrorContext(ctx, "could not record transaction",
			slog.String("request_id", middleware.RequestIDFrom(ctx)),
			slog.String("source", tx.Source),
			slog.String("source_reference", tx.SourceReference),
			slog.Any("error", err))

		// No detail to the caller: storage errors leak infrastructure shape.
		// A producer's correct response to a 500 is to retry, and idempotency
		// makes that safe.
		s.metrics.IngestResult(tx.Source, metrics.ResultError, time.Since(started))
		writeError(w, http.StatusInternalServerError, "transaction could not be recorded", nil)
		return
	}

	if outcome.Divergent {
		s.logDivergence(r, tx, outcome)
		// Worth alerting on: a producer is sending different content under an
		// idempotency key it has already used.
		s.metrics.DivergentReplay(tx.Source)
	}

	// The contract: a new record is 201, a matched key is 200.
	status := http.StatusCreated
	result := metrics.ResultCreated
	if outcome.Duplicate {
		status = http.StatusOK
		result = metrics.ResultDuplicate
	}

	s.metrics.IngestResult(tx.Source, result, time.Since(started))
	writeJSON(w, status, toResult(outcome))
}

// logDivergence records a replay whose idempotency key matched an existing
// transaction but whose content did not.
//
// This never changes the response. The contract promises a duplicate result for
// any key match and defines no conflict status, so a producer coded against it
// has no branch for one. But a differing payload under the same key is either a
// producer defect or an attempt to change a value the contract declares
// immutable, and answering 200 while silently discarding the difference is the
// one outcome that would be indefensible. Warn level so it can be alerted on.
//
// See docs/adr/0006-idempotency-divergence.md.
func (s *ingestServer) logDivergence(r *http.Request, submitted transaction.Transaction, outcome transaction.IngestOutcome) {
	s.logger.WarnContext(r.Context(), "duplicate transaction diverges from the stored record",
		slog.String("request_id", middleware.RequestIDFrom(r.Context())),
		slog.String("source", submitted.Source),
		slog.String("source_reference", submitted.SourceReference),
		slog.String("stored_id", outcome.Transaction.ID.String()),
		slog.String("stored_fingerprint", outcome.Transaction.Fingerprint()),
		slog.String("submitted_fingerprint", submitted.Fingerprint()),
		// Amounts are the divergence that matters most and are not PII.
		slog.String("stored_amount", outcome.Transaction.BaseAmount.String()),
		slog.String("submitted_amount", submitted.BaseAmount.String()),
	)
}
