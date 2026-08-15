package middleware

import "net/http"

// RequestRecorder is the slice of metrics collection the HTTP layer needs.
//
// Declared here rather than importing the metrics package so that middleware
// stays free of a Prometheus dependency and tests can pass a plain func.
type RequestRecorder interface {
	HTTPRequest(method, route string, status int)
}

// Metrics records one counter increment per served request.
//
// The route label is the *registered pattern*, never the raw path. A label
// taken from user input has unbounded cardinality: every 404 for a random URL
// would create a new time series, which is both a memory leak in the metrics
// store and an open invitation to fill it with junk.
func Metrics(recorder RequestRecorder) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &responseRecorder{ResponseWriter: w}

			next.ServeHTTP(rec, r)

			recorder.HTTPRequest(r.Method, routeOf(r), rec.statusCode())
		})
	}
}

// routeOf returns the mux pattern that matched, falling back to a constant for
// requests that matched nothing.
func routeOf(r *http.Request) string {
	// Set by net/http's ServeMux from Go 1.23 onward. Empty when no pattern
	// matched, which is exactly the unbounded-cardinality case to collapse.
	if r.Pattern != "" {
		return r.Pattern
	}
	return "unmatched"
}
