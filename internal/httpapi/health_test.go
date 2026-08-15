package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gregwinn/EmovisMicroService/internal/platform/health"
)

func testRouter(checker *health.Checker) http.Handler {
	return NewRouter(Deps{
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Health:  checker,
		Version: "test",
	})
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// Liveness must not consult dependencies. A database outage should drain this
// instance from the load balancer, not have the orchestrator restart it.
func TestLivenessIgnoresFailingDependencies(t *testing.T) {
	checker := health.New(time.Second)
	checker.Register("database", func(context.Context) error { return errors.New("connection refused") })

	rec := get(t, testRouter(checker), "/healthz")

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "up", body["status"])
}

func TestReadinessReportsUpWhenDependenciesPass(t *testing.T) {
	checker := health.New(time.Second)
	checker.Register("database", func(context.Context) error { return nil })

	rec := get(t, testRouter(checker), "/readyz")

	assert.Equal(t, http.StatusOK, rec.Code)

	var report health.Report
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
	assert.Equal(t, health.StatusUp, report.Status)
	assert.Equal(t, health.StatusUp, report.Checks["database"].Status)
}

func TestReadinessReturns503WhenADependencyIsDown(t *testing.T) {
	checker := health.New(time.Second)
	checker.Register("database", func(context.Context) error { return nil })
	checker.Register("queue", func(context.Context) error { return errors.New("connection refused") })

	rec := get(t, testRouter(checker), "/readyz")

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var report health.Report
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
	assert.Equal(t, health.StatusDown, report.Status)
	assert.Equal(t, "connection refused", report.Checks["queue"].Error)
}

// Probe endpoints are GET-only; the stdlib mux enforces the method from the
// route pattern.
func TestProbesRejectNonGET(t *testing.T) {
	h := testRouter(health.New(time.Second))

	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
			assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		})
	}
}

func TestUnknownRouteReturns404(t *testing.T) {
	rec := get(t, testRouter(health.New(time.Second)), "/nope")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// Every response carries a correlation id, including probes, so an operator
// chasing a failing check has something to search on.
func TestResponsesCarryCorrelationID(t *testing.T) {
	rec := get(t, testRouter(health.New(time.Second)), "/healthz")
	assert.NotEmpty(t, rec.Header().Get("X-Request-Id"))
}
