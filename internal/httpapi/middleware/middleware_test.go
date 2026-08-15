package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// captureLogger returns a logger writing JSON records into buf.
func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hello"))
	})
}

func TestChainAppliesFirstArgumentOutermost(t *testing.T) {
	var order []string

	record := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	h := Chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { order = append(order, "handler") }),
		record("first"), record("second"), record("third"),
	)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, []string{"first", "second", "third", "handler"}, order)
}

func TestRequestIDGeneratesWhenAbsent(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.NotEmpty(t, seen)
	assert.Len(t, seen, 32, "generated ids are 16 random bytes, hex encoded")
	assert.Equal(t, seen, rec.Header().Get(RequestIDHeader), "the id must be echoed to the caller")
}

func TestRequestIDReusesInboundValue(t *testing.T) {
	const inbound = "producer-correlation-abc123"

	var seen string
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, inbound)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, inbound, seen)
	assert.Equal(t, inbound, rec.Header().Get(RequestIDHeader))
}

// The inbound header is attacker-controlled and lands in log records, so an
// oversized value is replaced rather than propagated.
func TestRequestIDRejectsOversizedInboundValue(t *testing.T) {
	oversized := strings.Repeat("x", 129)

	var seen string
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, oversized)
	h.ServeHTTP(httptest.NewRecorder(), req)

	assert.NotEqual(t, oversized, seen)
	assert.Len(t, seen, 32)
}

func TestRequestIDFromWithoutMiddlewareReturnsEmpty(t *testing.T) {
	assert.Empty(t, RequestIDFrom(t.Context()))
}

func TestRecoverConvertsPanicTo500(t *testing.T) {
	var buf bytes.Buffer
	h := Recover(captureLogger(&buf))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("lane controller sent something impossible")
	}))

	rec := httptest.NewRecorder()
	require.NotPanics(t, func() {
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ingest/v1/transactions", nil))
	})

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	// The client is told nothing beyond the status: panic text leaks internals.
	assert.NotContains(t, rec.Body.String(), "lane controller")

	logged := buf.String()
	assert.Contains(t, logged, "panic recovered")
	assert.Contains(t, logged, "lane controller sent something impossible")
	assert.Contains(t, logged, "stack")
}

// http.ErrAbortHandler is the documented way for a handler to drop a connection
// deliberately. Swallowing it would turn an intentional abort into a 500.
func TestRecoverRepanicsOnErrAbortHandler(t *testing.T) {
	h := Recover(discardLogger())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	assert.Panics(t, func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	})
}

func TestRecoverPassesThroughNormalRequests(t *testing.T) {
	h := Recover(discardLogger())(okHandler())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusTeapot, rec.Code)
	assert.Equal(t, "hello", rec.Body.String())
}

func TestLoggerRecordsRequestDetail(t *testing.T) {
	var buf bytes.Buffer
	h := Chain(okHandler(), RequestID, Logger(captureLogger(&buf)))

	req := httptest.NewRequest(http.MethodPost, "/ingest/v1/transactions", nil)
	req.Header.Set("User-Agent", "lane-controller/2.1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	var record map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &record))

	assert.Equal(t, "http request", record["msg"])
	assert.Equal(t, http.MethodPost, record["method"])
	assert.Equal(t, "/ingest/v1/transactions", record["path"])
	assert.InEpsilon(t, float64(http.StatusTeapot), record["status"], 0.0001)
	assert.InEpsilon(t, float64(5), record["bytes"], 0.0001)
	assert.Equal(t, "lane-controller/2.1", record["user_agent"])
	assert.NotEmpty(t, record["request_id"])
}

// Probes fire every few seconds forever. Logging them buries the traffic an
// operator actually needs to see.
func TestLoggerSkipsProbeEndpoints(t *testing.T) {
	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			var buf bytes.Buffer
			h := Logger(captureLogger(&buf))(okHandler())

			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))

			assert.Empty(t, buf.String())
		})
	}
}

// A handler that writes a body without calling WriteHeader has implicitly sent
// 200, and the access log must say so rather than reporting a zero status.
func TestLoggerDefaultsImplicitStatusTo200(t *testing.T) {
	var buf bytes.Buffer
	h := Logger(captureLogger(&buf))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("implicit"))
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	var record map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &record))
	assert.InEpsilon(t, float64(http.StatusOK), record["status"], 0.0001)
}

// A panic must still produce an access-log line, which is why Logger sits
// outside Recover in the chain.
func TestLoggerRecordsPanicsAs500(t *testing.T) {
	var buf bytes.Buffer
	logger := captureLogger(&buf)

	h := Chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }),
		RequestID, Logger(logger), Recover(logger),
	)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ingest/v1/transactions", nil))

	assert.Contains(t, buf.String(), "panic recovered")
	assert.Contains(t, buf.String(), `"msg":"http request"`)
	assert.Contains(t, buf.String(), `"status":500`)
}
