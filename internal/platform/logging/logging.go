// Package logging builds the process-wide structured logger.
//
// Every log line is structured and machine-parseable. Ingest runs unattended and
// at volume; grep-friendly prose is not a substitute for fields you can
// aggregate on.
package logging

import (
	"io"
	"log/slog"
)

// Options controls logger construction.
type Options struct {
	Level  slog.Level
	Format string // "json" or "text"

	// Service and Environment are attached to every record so logs from several
	// deployments remain distinguishable once they are pooled.
	Service     string
	Environment string
	Version     string
}

// New returns a logger writing to w.
//
// Any format other than "text" produces JSON: an unrecognized value should not
// silently disable logging, and config.Load has already rejected bad values.
func New(w io.Writer, opts Options) *slog.Logger {
	handlerOpts := &slog.HandlerOptions{Level: opts.Level}

	var handler slog.Handler
	if opts.Format == "text" {
		handler = slog.NewTextHandler(w, handlerOpts)
	} else {
		handler = slog.NewJSONHandler(w, handlerOpts)
	}

	return slog.New(handler).With(
		slog.String("service", opts.Service),
		slog.String("env", opts.Environment),
		slog.String("version", opts.Version),
	)
}
