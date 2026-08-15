package httpapi

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"

	"github.com/gregwinn/EmovisMicroService/internal/httpapi/gen"
)

// Every error response in this service uses the Error schema from
// api/openapi.yaml: {code, message, fields}.
//
// RFC 9457 Problem Details would be a better shape — typed URIs, a structured
// array of per-field failures, proper content negotiation. It is deliberately
// not used here. The spec is a published contract that producers are already
// coded against, and unilaterally "improving" someone else's error format on an
// integration boundary breaks callers for the author's benefit. Problem Details
// is written up as a proposal instead; see
// docs/adr/0005-error-contract-fidelity.md.
//
// Within the contract as given:
//
//	code    the HTTP status (the spec does not say; documented as an assumption)
//	message a human-readable summary
//	fields  "field: reason; field: reason", because the schema types it as a
//	        single string rather than an array

// FieldError is one field-level validation failure.
type FieldError struct {
	// Field is a dotted path into the request body, e.g. "plate.number".
	Field string
	// Reason explains the failure in terms the producer's integrator can act on.
	Reason string
}

// writeError sends an error response in the contract's Error shape.
func writeError(w http.ResponseWriter, status int, message string, fields []FieldError) {
	code := int32(status) //nolint:gosec // HTTP status codes are three digits
	body := gen.Error{
		Code:    &code,
		Message: &message,
	}

	if formatted := formatFieldErrors(fields); formatted != "" {
		body.Fields = &formatted
	}

	writeJSON(w, status, body)
}

// formatFieldErrors flattens field errors into the single string the contract
// allows, sorted and de-duplicated so that the same invalid payload always
// produces byte-identical output. Producers diff these in their own logs.
func formatFieldErrors(fields []FieldError) string {
	if len(fields) == 0 {
		return ""
	}

	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		switch {
		case f.Field == "" && f.Reason == "":
			continue
		case f.Field == "":
			parts = append(parts, f.Reason)
		default:
			parts = append(parts, f.Field+": "+f.Reason)
		}
	}

	slices.Sort(parts)
	return strings.Join(slices.Compact(parts), "; ")
}

// fieldErrorsFromSpecValidation translates a kin-openapi validation failure into
// field-level errors.
//
// The library reports failures as a tree: a RequestError wrapping either a
// MultiError of SchemaErrors (when the body violates the schema) or a plain
// parse error (when the body is not JSON at all). Walking it here is what turns
// "request body has an error" into "plate.number: minimum string length is 1",
// which is the difference between a producer fixing their integration in
// minutes and opening a support ticket.
func fieldErrorsFromSpecValidation(err error) []FieldError {
	var fields []FieldError
	collectFieldErrors(err, &fields)
	return fields
}

func collectFieldErrors(err error, out *[]FieldError) {
	if err == nil {
		return
	}

	// MultiError first: a RequestError unwraps into one, and checking it here
	// yields every schema violation instead of only the first.
	var multi openapi3.MultiError
	if errors.As(err, &multi) {
		for _, inner := range multi {
			collectFieldErrors(inner, out)
		}
		return
	}

	var schemaErr *openapi3.SchemaError
	if errors.As(err, &schemaErr) {
		*out = append(*out, fieldErrorFromSchema(schemaErr))
		return
	}

	// Not a schema violation: an unparseable body, a missing body, or a bad
	// parameter.
	var reqErr *openapi3filter.RequestError
	if errors.As(err, &reqErr) {
		if reqErr.Parameter != nil {
			*out = append(*out, FieldError{Field: reqErr.Parameter.Name, Reason: reason(reqErr.Reason, reqErr.Err)})
			return
		}
		*out = append(*out, FieldError{Field: "body", Reason: reason(reqErr.Reason, reqErr.Err)})
		return
	}
}

func fieldErrorFromSchema(se *openapi3.SchemaError) FieldError {
	// JSONPointer already resolves to the offending property, including for
	// "required" failures, where it points at the property that is missing
	// rather than at the enclosing object.
	field := strings.Join(se.JSONPointer(), ".")
	if field == "" {
		field = "body"
	}

	switch {
	case se.SchemaField == "required":
		// `property "source" is missing` reads as library internals. The field
		// name is already in the pointer, so the reason only has to say why.
		return FieldError{Field: field, Reason: "is required"}

	case se.SchemaField == "format" && se.Schema != nil && se.Schema.Format != "":
		// kin-openapi renders format failures with the full validation regex
		// inline, which is accurate and useless to the integrator reading it.
		return FieldError{Field: field, Reason: "must be a valid " + se.Schema.Format}
	}

	return FieldError{Field: field, Reason: reason(se.Reason, nil)}
}

func reason(primary string, fallback error) string {
	if primary != "" {
		return primary
	}
	if fallback != nil {
		return fallback.Error()
	}
	return "is invalid"
}
