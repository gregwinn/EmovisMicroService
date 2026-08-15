package httpapi

import (
	"github.com/gregwinn/EmovisMicroService/internal/httpapi/gen"
	"github.com/gregwinn/EmovisMicroService/internal/transaction"
)

// This file is the whole surface where the wire contract meets the domain.
//
// It exists so that regenerating from a changed api/openapi.yaml breaks in one
// small, obvious place rather than rippling through the rules. If a future spec
// renames a field, the compiler points here and nowhere else — which is the
// practical payoff of ADR-0002 treating the spec as the source of truth.

// toSubmission converts a validated request body into the domain's input type.
//
// The request has already passed contract validation, so required fields are
// present and correctly typed. Optional fields arrive as pointers and are
// flattened to their zero values, which the domain reads as "absent".
func toSubmission(req gen.IngestRequest) transaction.Submission {
	s := transaction.Submission{
		Source:          req.Source,
		SourceReference: req.SourceReference,
		Type:            req.TransactionType,
		OccurredAt:      req.TransactionTimeUtc,
		BaseAmount:      req.BaseAmount,
	}

	if req.Plate != nil {
		s.Plate = &transaction.PlateRead{
			Number:       req.Plate.Number,
			Jurisdiction: req.Plate.Jurisdiction,
		}
	}
	if req.TransponderNumber != nil {
		s.Transponder = *req.TransponderNumber
	}
	if req.Currency != nil {
		s.Currency = *req.Currency
	}

	// Location and Metadata are producer passthrough: free-form by contract,
	// carried through uninterpreted.
	if req.Location != nil {
		s.Location = *req.Location
	}
	if req.Metadata != nil {
		s.Metadata = *req.Metadata
	}

	return s
}

// toResult converts a store outcome into the contract's response body.
//
// Note what is absent: IngestOutcome.Divergent has no representation here. The
// contract defines no field for it and no status code that would carry it, and
// inventing one would break producers coded against the published schema. It is
// surfaced through logs and metrics instead — see
// docs/adr/0006-idempotency-divergence.md.
func toResult(outcome transaction.IngestOutcome) gen.IngestResult {
	tx := outcome.Transaction

	return gen.IngestResult{
		Id:                tx.ID.String(),
		AssociationStatus: gen.AssociationStatus(tx.AssociationStatus),
		SettlementStatus:  gen.SettlementStatus(tx.SettlementStatus),
		Duplicate:         outcome.Duplicate,
	}
}

// toFieldErrors converts domain rule failures into the HTTP layer's field
// errors. The two types are deliberately separate: the domain should not know
// how errors are rendered on the wire.
func toFieldErrors(problems []transaction.RuleError) []FieldError {
	fields := make([]FieldError, 0, len(problems))
	for _, p := range problems {
		fields = append(fields, FieldError{Field: p.Field, Reason: p.Reason})
	}
	return fields
}
