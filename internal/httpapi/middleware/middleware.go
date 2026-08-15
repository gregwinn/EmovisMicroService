// Package middleware provides the HTTP middleware chain for the service.
//
// These are deliberately small and dependency-free. The routing surface is one
// business endpoint plus probes, which does not justify pulling in a router
// framework for its middleware stack. See docs/adr/0001-go-and-stdlib-http.md.
package middleware

import "net/http"

// Middleware wraps an http.Handler with additional behaviour.
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares to h so that the first argument is the outermost
// wrapper. Reading the call site top to bottom therefore matches the order a
// request travels through them.
func Chain(h http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

// responseRecorder captures the status code and body size for access logging.
// http.ResponseWriter offers no way to read back what was written.
type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	// A handler that writes without calling WriteHeader has implicitly sent 200.
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// statusCode reports the status sent to the client, defaulting to 200 for
// handlers that returned without writing anything.
func (r *responseRecorder) statusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}
