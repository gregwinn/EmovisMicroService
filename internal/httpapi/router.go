// Package httpapi wires the service's HTTP surface: routing, middleware, and
// the handlers that adapt HTTP to the domain.
//
// Business rules do not live here. This package translates between the wire
// contract in api/openapi.yaml and the domain packages, and nothing more.
package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/gregwinn/EmovisMicroService/internal/httpapi/middleware"
	"github.com/gregwinn/EmovisMicroService/internal/platform/health"
)

// Deps are the collaborators the HTTP layer needs. Passing them explicitly
// keeps the package free of package-level state and makes handlers trivial to
// test in isolation.
type Deps struct {
	Logger  *slog.Logger
	Health  *health.Checker
	Version string
}

// NewRouter builds the service's HTTP handler.
//
// Middleware order is load-bearing and reads outermost first:
//
//	RequestID — so every later layer can log a correlation id
//	Logger    — outside Recover, so a panic still produces an access log line
//	Recover   — closest to the handlers it protects
func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handleLive())
	mux.HandleFunc("GET /readyz", handleReady(d.Health))

	return middleware.Chain(mux,
		middleware.RequestID,
		middleware.Logger(d.Logger),
		middleware.Recover(d.Logger),
	)
}
