package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recover converts a panic in a handler into a 500 response.
//
// A panic in one request must not take down a process that is concurrently
// accepting billable transactions from other producers. The stack trace is
// logged; the client is told nothing beyond the status, since panic messages
// routinely leak internal detail.
//
// The response body is written by hand rather than through the Error encoder so
// that this middleware has no dependency on the API surface it protects — it
// must keep working even when the layer above it is the thing that is broken.
func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}

				// http.ErrAbortHandler is the documented way for a handler to drop a
				// connection on purpose; it is not a bug and must stay silent.
				if recovered == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, per net/http docs
					panic(recovered)
				}

				logger.LogAttrs(r.Context(), slog.LevelError, "panic recovered",
					slog.String("request_id", RequestIDFrom(r.Context())),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Any("panic", recovered),
					slog.String("stack", string(debug.Stack())),
				)

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"code":500,"message":"internal server error"}`))
			}()

			next.ServeHTTP(w, r)
		})
	}
}
