package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gregwinn/EmovisMicroService/internal/httpapi/gen"
)

const ingestPath = "/ingest/v1/transactions"

// validPayload is the minimal request the contract accepts: every required
// field present and nothing else. Tests mutate a copy of it so that each case
// isolates exactly one violation.
func validPayload() map[string]any {
	return map[string]any{
		"source":               "lane-controller-07",
		"source_reference":     "LC07-20260814-000918",
		"transaction_type":     "toll",
		"transaction_time_utc": "2026-08-14T13:45:02Z",
		"base_amount":          "12.50",
	}
}

func postJSON(t *testing.T, h http.Handler, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	switch v := body.(type) {
	case string:
		reader = strings.NewReader(v)
	default:
		encoded, err := json.Marshal(v)
		require.NoError(t, err)
		reader = strings.NewReader(string(encoded))
	}

	req := httptest.NewRequest(http.MethodPost, ingestPath, reader)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// decodeError asserts the response body matches the contract's Error schema and
// returns it.
func decodeError(t *testing.T, rec *httptest.ResponseRecorder) gen.Error {
	t.Helper()

	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body gen.Error
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body),
		"error responses must match the Error schema in api/openapi.yaml")

	require.NotNil(t, body.Code, "Error.code is how producers branch on failures")
	require.NotNil(t, body.Message)
	assert.Equal(t, int32(rec.Code), *body.Code, "Error.code mirrors the HTTP status")

	return body
}

func errorFields(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	body := decodeError(t, rec)
	if body.Fields == nil {
		return ""
	}
	return *body.Fields
}

// A payload satisfying the contract reaches the handler. The 501 is the current
// stub; what matters here is that validation let it through.
func TestValidPayloadPassesContractValidation(t *testing.T) {
	rec := postJSON(t, testRouter(t, noChecks()), validPayload())

	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

// Optional fields are optional, and an explicit null is not the same as a
// violation — the spec marks these nullable.
func TestOptionalAndNullableFieldsAreAccepted(t *testing.T) {
	tests := map[string]func(map[string]any){
		"all optionals omitted": func(map[string]any) {},
		"explicit nulls": func(p map[string]any) {
			p["transponder_number"] = nil
			p["currency"] = nil
		},
		"plate only": func(p map[string]any) {
			p["plate"] = map[string]any{"number": "ABC1234", "jurisdiction": "TX"}
		},
		"transponder only": func(p map[string]any) {
			p["transponder_number"] = "0180012345678"
		},
		"free-form location and metadata": func(p map[string]any) {
			// The spec declares both as additionalProperties:true because shape
			// varies by producer. Validation must not constrain them.
			p["location"] = map[string]any{"facility": "SH-130", "plaza": "12", "lane": "3", "direction": "NB"}
			p["metadata"] = map[string]any{"anything": []any{1, "two", map[string]any{"three": true}}}
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			payload := validPayload()
			mutate(payload)

			rec := postJSON(t, testRouter(t, noChecks()), payload)

			assert.Equal(t, http.StatusNotImplemented, rec.Code,
				"contract validation should have accepted this payload")
		})
	}
}

// Every required field is enforced, and the response names it.
func TestMissingRequiredFieldsAreReported(t *testing.T) {
	required := []string{"source", "source_reference", "transaction_type", "transaction_time_utc", "base_amount"}

	for _, field := range required {
		t.Run("missing "+field, func(t *testing.T) {
			payload := validPayload()
			delete(payload, field)

			rec := postJSON(t, testRouter(t, noChecks()), payload)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, errorFields(t, rec), field+": is required")
		})
	}
}

// A producer fixing an integration should get the whole list at once, not one
// failure per round trip. This is why the validator runs with MultiError.
func TestAllViolationsAreReportedTogether(t *testing.T) {
	rec := postJSON(t, testRouter(t, noChecks()), map[string]any{})

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	fields := errorFields(t, rec)
	for _, field := range []string{"source", "source_reference", "transaction_type", "transaction_time_utc", "base_amount"} {
		assert.Contains(t, fields, field+": is required")
	}
}

func TestSchemaConstraintsAreEnforced(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(map[string]any)
		wantFields string
	}{
		{
			name:       "source exceeds maxLength",
			mutate:     func(p map[string]any) { p["source"] = strings.Repeat("x", 65) },
			wantFields: "source: maximum string length is 64",
		},
		{
			name:       "source_reference exceeds maxLength",
			mutate:     func(p map[string]any) { p["source_reference"] = strings.Repeat("x", 129) },
			wantFields: "source_reference: maximum string length is 128",
		},
		{
			name:       "source below minLength",
			mutate:     func(p map[string]any) { p["source"] = "" },
			wantFields: "source: minimum string length is 1",
		},
		{
			name:       "wrong type for source",
			mutate:     func(p map[string]any) { p["source"] = 123 },
			wantFields: "source: value must be a string",
		},
		{
			name:       "unparseable timestamp",
			mutate:     func(p map[string]any) { p["transaction_time_utc"] = "14/08/2026 13:45" },
			wantFields: "transaction_time_utc: must be a valid date-time",
		},
		{
			name:       "plate missing jurisdiction",
			mutate:     func(p map[string]any) { p["plate"] = map[string]any{"number": "ABC1234"} },
			wantFields: "plate.jurisdiction: is required",
		},
		{
			name: "plate number exceeds maxLength",
			mutate: func(p map[string]any) {
				p["plate"] = map[string]any{"number": strings.Repeat("A", 17), "jurisdiction": "TX"}
			},
			wantFields: "plate.number: maximum string length is 16",
		},
		{
			name:       "transponder exceeds maxLength",
			mutate:     func(p map[string]any) { p["transponder_number"] = strings.Repeat("0", 65) },
			wantFields: "transponder_number: maximum string length is 64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := validPayload()
			tt.mutate(payload)

			rec := postJSON(t, testRouter(t, noChecks()), payload)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, errorFields(t, rec), tt.wantFields)
		})
	}
}

// Nested violations are reported against their full path, so a producer knows
// which object to look in.
func TestNestedViolationsUseDottedPaths(t *testing.T) {
	payload := validPayload()
	payload["plate"] = map[string]any{"number": ""}

	rec := postJSON(t, testRouter(t, noChecks()), payload)

	fields := errorFields(t, rec)
	assert.Contains(t, fields, "plate.number: minimum string length is 1")
	assert.Contains(t, fields, "plate.jurisdiction: is required")
}

func TestMalformedBodiesAreRejected(t *testing.T) {
	tests := map[string]string{
		"not json":         `{not json`,
		"empty body":       ``,
		"json array":       `[]`,
		"truncated object": `{"source":`,
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			rec := postJSON(t, testRouter(t, noChecks()), body)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.NotEmpty(t, errorFields(t, rec))
		})
	}
}

func TestWrongContentTypeIsRejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, ingestPath, strings.NewReader("source=lc-07"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	testRouter(t, noChecks()).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// The contract declares POST only.
func TestIngestRejectsOtherMethods(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			rec := httptest.NewRecorder()
			testRouter(t, noChecks()).ServeHTTP(rec, httptest.NewRequest(method, ingestPath, nil))

			assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		})
	}
}

// The same invalid payload must always produce byte-identical output. Producers
// diff these strings in their own logs, and Go map iteration order would
// otherwise make them unstable.
func TestErrorFieldsAreDeterministic(t *testing.T) {
	router := testRouter(t, noChecks())

	first := errorFields(t, postJSON(t, router, map[string]any{}))
	for range 10 {
		assert.Equal(t, first, errorFields(t, postJSON(t, router, map[string]any{})))
	}
}

func TestFormatFieldErrors(t *testing.T) {
	tests := []struct {
		name string
		in   []FieldError
		want string
	}{
		{name: "empty", in: nil, want: ""},
		{
			name: "single",
			in:   []FieldError{{Field: "source", Reason: "is required"}},
			want: "source: is required",
		},
		{
			name: "sorted regardless of input order",
			in: []FieldError{
				{Field: "source_reference", Reason: "is required"},
				{Field: "base_amount", Reason: "is required"},
			},
			want: "base_amount: is required; source_reference: is required",
		},
		{
			name: "duplicates collapsed",
			in: []FieldError{
				{Field: "source", Reason: "is required"},
				{Field: "source", Reason: "is required"},
			},
			want: "source: is required",
		},
		{
			name: "reason without a field",
			in:   []FieldError{{Reason: "value is required but missing"}},
			want: "value is required but missing",
		},
		{
			name: "wholly empty entries are dropped",
			in:   []FieldError{{}, {Field: "source", Reason: "is required"}},
			want: "source: is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatFieldErrors(tt.in))
		})
	}
}

// The embedded spec is what the validator enforces, so it has to load and it has
// to describe the endpoint the service actually serves.
func TestEmbeddedSpecMatchesTheServedRoute(t *testing.T) {
	spec, err := LoadSpec()
	require.NoError(t, err)

	require.NoError(t, spec.Validate(t.Context()), "the embedded contract must be a valid OpenAPI document")

	path := spec.Paths.Find(ingestPath)
	require.NotNil(t, path, "the contract must describe %s", ingestPath)
	require.NotNil(t, path.Post, "the contract must declare POST on %s", ingestPath)

	for _, status := range []int{200, 201, 400} {
		assert.NotNil(t, path.Post.Responses.Status(status),
			"the contract declares a %d response", status)
	}
}
