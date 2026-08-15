package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// RequestIDHeader carries the correlation id in and out of the service.
const RequestIDHeader = "X-Request-Id"

type contextKey int

const requestIDKey contextKey = iota

// RequestID ensures every request carries a correlation id, reusing the
// caller's if it supplied one.
//
// Producers retry aggressively, so being able to tie a retry back to the
// original attempt in the logs is worth the two lines it costs here. The
// inbound value is length-capped because it is attacker-controlled and ends up
// in log records.
func RequestID(next http.Handler) http.Handler {
	const maxInboundLen = 128

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" || len(id) > maxInboundLen {
			id = newRequestID()
		}

		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// RequestIDFrom returns the correlation id for ctx, or "" if the request did not
// pass through the RequestID middleware.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func newRequestID() string {
	var b [16]byte
	// rand.Read is documented never to return an error as of Go 1.24.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
