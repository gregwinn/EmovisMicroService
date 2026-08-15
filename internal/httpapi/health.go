package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/gregwinn/EmovisMicroService/internal/platform/health"
)

// handleLive answers the liveness probe.
//
// It reports only that the process is running and able to serve HTTP. It
// deliberately does not consult dependencies: a database blip should drain this
// instance from the load balancer (readiness), not have the orchestrator kill
// and reschedule an otherwise healthy process.
func handleLive() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": string(health.StatusUp)})
	}
}

// handleReady answers the readiness probe by evaluating every registered
// dependency check, returning 503 when any of them is down.
func handleReady(checker *health.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		report := checker.Ready(r.Context())

		status := http.StatusOK
		if report.Status != health.StatusUp {
			status = http.StatusServiceUnavailable
		}

		writeJSON(w, status, report)
	}
}

// writeJSON serialises v as the response body.
//
// The status is written before encoding, so an encoding failure mid-body cannot
// be reported to the client — it is logged by the caller's middleware instead.
// Keeping response types simple and always-encodable is the real mitigation.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
