// Package httpapi wires the service's HTTP surface: routing, middleware, and
// the handlers that adapt HTTP to the domain.
//
// Business rules do not live here. This package translates between the wire
// contract in api/openapi.yaml and the domain packages, and nothing more.
package httpapi

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gregwinn/EmovisMicroService/internal/httpapi/gen"
	"github.com/gregwinn/EmovisMicroService/internal/httpapi/middleware"
	"github.com/gregwinn/EmovisMicroService/internal/platform/health"
	"github.com/gregwinn/EmovisMicroService/internal/transaction"
)

// Deps are the collaborators the HTTP layer needs. Passing them explicitly
// keeps the package free of package-level state and makes handlers trivial to
// test in isolation.
type Deps struct {
	Logger  *slog.Logger
	Health  *health.Checker
	Version string

	// Rules is the operator configuration semantic validation runs against.
	Rules transaction.Rules
	// Store is where accepted transactions are durably recorded.
	Store transaction.Store
}

// NewRouter builds the service's HTTP handler.
//
// Routes come from two places, and the split is deliberate:
//
//   - Operations declared in api/openapi.yaml are registered by generated code
//     and wrapped in contract validation. The compiler enforces that each one
//     has an implementation; the validator enforces that each request matches
//     the published schema.
//   - Operational endpoints (probes) are registered directly. They are not part
//     of the producer-facing contract and must keep answering even when a
//     request would fail contract validation.
//
// Middleware order is load-bearing and reads outermost first:
//
//	RequestID — so every later layer can log a correlation id
//	Logger    — outside Recover, so a panic still produces an access log line
//	Recover   — closest to the handlers it protects
func NewRouter(d Deps) (http.Handler, error) {
	spec, err := LoadSpec()
	if err != nil {
		return nil, fmt.Errorf("build router: %w", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handleLive())
	mux.HandleFunc("GET /readyz", handleReady(d.Health))

	server := &ingestServer{
		logger: d.Logger,
		rules:  d.Rules,
		store:  d.Store,
	}

	gen.HandlerWithOptions(server, gen.StdHTTPServerOptions{
		BaseRouter:  mux,
		Middlewares: []gen.MiddlewareFunc{specValidator(spec, d.Logger)},
		// Reached when generated parameter binding fails before validation runs.
		// Without this the generated default replies in text/plain, which would
		// break the contract's error shape.
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, "request does not satisfy the API contract",
				[]FieldError{{Reason: err.Error()}})
		},
	})

	return middleware.Chain(mux,
		middleware.RequestID,
		middleware.Logger(d.Logger),
		middleware.Recover(d.Logger),
	), nil
}
