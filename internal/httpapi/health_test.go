package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gregwinn/EmovisMicroService/internal/money"
	"github.com/gregwinn/EmovisMicroService/internal/platform/health"
	"github.com/gregwinn/EmovisMicroService/internal/platform/metrics"
	"github.com/gregwinn/EmovisMicroService/internal/store/memory"
	"github.com/gregwinn/EmovisMicroService/internal/transaction"
)

func testRouter(t *testing.T, checker *health.Checker) http.Handler {
	t.Helper()
	return newHarness(t, checker).Handler
}

// harness is a router wired to a fresh in-memory store, plus handles on the
// collaborators so tests can assert on what actually got recorded.
type harness struct {
	http.Handler

	Store   *memory.Store
	Logs    *bytes.Buffer
	Metrics *metrics.Metrics
}

// testNow is the clock the HTTP tests run against, so "in the future" and
// "backdated" mean something stable.
var testNow = time.Date(2026, 8, 14, 14, 0, 0, 0, time.UTC)

// testRules is the operator configuration the HTTP tests validate against.
func testRules() transaction.Rules {
	usd, _ := money.Lookup("USD")
	return transaction.Rules{
		Types:           transaction.NewTypeSet([]string{"toll", "violation", "fee"}),
		DefaultCurrency: usd,
		MaxClockSkew:    transaction.DefaultMaxClockSkew,
		Now:             func() time.Time { return testNow },
	}
}

func newHarness(t *testing.T, checker *health.Checker) *harness {
	t.Helper()

	var logs bytes.Buffer
	store := memory.New()

	h := &harness{Store: store, Logs: &logs, Metrics: metrics.New()}

	router, err := NewRouter(Deps{
		Logger:  h.Logger(),
		Health:  checker,
		Version: "test",
		Rules:   testRules(),
		Store:   store,
		Metrics: h.Metrics,
	})
	require.NoError(t, err)

	h.Handler = router
	return h
}

// Logger returns a logger writing into the harness's captured output, so tests
// can assert on what was and was not logged.
func (h *harness) Logger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(h.Logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// noChecks is a Checker with no dependencies registered, for tests that care
// about routing or validation rather than readiness.
func noChecks() *health.Checker {
	return health.New(time.Second)
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

	rec := get(t, testRouter(t, checker), "/healthz")

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "up", body["status"])
}

func TestReadinessReportsUpWhenDependenciesPass(t *testing.T) {
	checker := health.New(time.Second)
	checker.Register("database", func(context.Context) error { return nil })

	rec := get(t, testRouter(t, checker), "/readyz")

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

	rec := get(t, testRouter(t, checker), "/readyz")

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var report health.Report
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
	assert.Equal(t, health.StatusDown, report.Status)
	assert.Equal(t, "unavailable", report.Checks["queue"].Reason)
}

// Probe endpoints are GET-only; the stdlib mux enforces the method from the
// route pattern.
func TestProbesRejectNonGET(t *testing.T) {
	h := testRouter(t, health.New(time.Second))

	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
			assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		})
	}
}

func TestUnknownRouteReturns404(t *testing.T) {
	rec := get(t, testRouter(t, health.New(time.Second)), "/nope")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// Every response carries a correlation id, including probes, so an operator
// chasing a failing check has something to search on.
func TestResponsesCarryCorrelationID(t *testing.T) {
	rec := get(t, testRouter(t, health.New(time.Second)), "/healthz")
	assert.NotEmpty(t, rec.Header().Get("X-Request-Id"))
}
