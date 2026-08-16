package middleware

import "net/http"

// DefaultMaxBodyBytes bounds an inbound request.
//
// The contract carries one transaction per request. The largest realistic
// payload is a plate read, a transponder read, an amount, and two free-form
// passthrough objects — comfortably under 100 KB even for a verbose producer.
// 1 MiB leaves an order of magnitude of headroom and still refuses anything
// that could only be an attack or a bug.
//
// Without a limit the endpoint accepts a body of any size. The contract
// declares `security: []`, so anyone who can reach it can send one: a 61 MB
// request was accepted with a 201, cost roughly twice that in heap, and stored
// its payload permanently in the `metadata` column.
const DefaultMaxBodyBytes int64 = 1 << 20

// MaxBody caps how many bytes a handler will read from a request.
//
// http.MaxBytesReader is used rather than a Content-Length check because a
// client controls that header and can lie, or omit it entirely with chunked
// encoding. The reader stops at the limit regardless of what was declared, so
// an attacker cannot stream an unbounded body through it.
func MaxBody(limit int64) Middleware {
	if limit <= 0 {
		limit = DefaultMaxBodyBytes
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}
