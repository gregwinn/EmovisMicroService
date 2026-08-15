package httpapi

import (
	"log/slog"
	"net/http"
)

// ingestServer implements gen.ServerInterface.
//
// Having the compiler enforce that every operation in api/openapi.yaml has an
// implementation is the second half of ADR-0002: the spec drives the types, and
// the types drive the code that must exist.
type ingestServer struct {
	logger *slog.Logger
}

// IngestTransaction handles POST /ingest/v1/transactions.
//
// By the time this runs, the request has already been validated against the
// contract by specValidator, so the body is known to be well-formed JSON with
// every required field present and correctly typed. What remains is semantic
// validation and persistence.
//
// TODO(phase-4): replace this stub with domain validation and durable ingest.
func (s *ingestServer) IngestTransaction(w http.ResponseWriter, r *http.Request) {
	s.logger.WarnContext(r.Context(), "ingest endpoint called before the domain is wired")

	writeError(w, http.StatusNotImplemented,
		"transaction ingest is not yet implemented", nil)
}
