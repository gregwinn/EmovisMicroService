package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"

	"github.com/gregwinn/EmovisMicroService/internal/httpapi/gen"
	"github.com/gregwinn/EmovisMicroService/internal/httpapi/middleware"
)

// LoadSpec returns the OpenAPI contract embedded into the binary at build time.
//
// Embedding rather than reading api/openapi.yaml from disk means the running
// service cannot disagree with the contract it was built from, and there is no
// file to forget to ship in the container image.
func LoadSpec() (*openapi3.T, error) {
	spec, err := gen.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("load embedded openapi spec: %w", err)
	}

	// The contract declares no `servers` block, and the service is mounted at
	// the root. Clearing it keeps the validator from trying to match request
	// URLs against a server list that does not exist.
	spec.Servers = nil

	return spec, nil
}

// specValidator rejects any request that does not satisfy api/openapi.yaml
// before it reaches a handler.
//
// This is layer one of three (see docs/adr/0003-layered-validation.md). It
// covers everything the schema can express: required fields, types, string
// lengths, date-time formats. Rules the schema cannot express — "at least one
// of plate or transponder", "transaction_time_utc is not in the future" — are
// the handler's job.
//
// Running the spec as executable validation is the whole point of ADR-0002: it
// makes drift between the published contract and the implementation impossible
// rather than merely discouraged.
func specValidator(spec *openapi3.T, logger *slog.Logger) gen.MiddlewareFunc {
	return nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		Options: openapi3filter.Options{
			// Report every schema violation, not just the first. A producer
			// fixing an integration wants the whole list in one response.
			MultiError: true,
			// The contract declares `security: []`. Authentication is a
			// deployment concern layered on top; see
			// docs/adr/0011-authentication-off-by-default.md.
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
		},
		SilenceServersWarning: true,

		ErrorHandlerWithOpts: func(
			ctx context.Context,
			err error,
			w http.ResponseWriter,
			r *http.Request,
			opts nethttpmiddleware.ErrorHandlerOpts,
		) {
			status := opts.StatusCode
			if status == 0 {
				status = http.StatusBadRequest
			}

			fields := fieldErrorsFromSpecValidation(err)

			// Logged at Info, not Warn: a producer sending a malformed payload
			// is routine operational traffic, and the aggregate rate is what
			// matters. The field list is safe to log — it names fields and
			// constraints, never the plate or transponder values themselves.
			logger.LogAttrs(ctx, slog.LevelInfo, "request rejected by contract validation",
				slog.String("request_id", middleware.RequestIDFrom(ctx)),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.String("fields", formatFieldErrors(fields)),
			)

			writeError(w, status, "request does not satisfy the API contract", fields)
		},
	})
}
