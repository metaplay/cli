/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package helmutil

import (
	"log/slog"
	"strings"

	"github.com/rs/zerolog/log"
)

// NewDebugLogHandler returns a slog handler that writes Helm's log records to
// the CLI's debug log, so they show up with --verbose. Helm v3 logged through
// zerolog's Printf, which is also debug level.
//
// Helm v4 logs through slog. action.Configuration takes its logger from the
// global slog logger, and some code logs to the global logger directly, so
// setting this handler as the slog default covers both. Without it, Helm's
// logs are printed straight to stderr.
func NewDebugLogHandler() slog.Handler {
	return newLineHandler(func(line string) { log.Debug().Msg(line) }, true)
}

// newLineHandler formats each record as one 'key=value' line without a
// timestamp and passes it to emit. With showLevel false, the level is left out.
func newLineHandler(emit func(line string), showLevel bool) slog.Handler {
	return slog.NewTextHandler(lineWriter(emit), &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) == 0 && (attr.Key == slog.TimeKey || (attr.Key == slog.LevelKey && !showLevel)) {
				return slog.Attr{}
			}
			return attr
		},
	})
}

// lineWriter receives one formatted record per Write from slog.TextHandler.
type lineWriter func(line string)

func (w lineWriter) Write(p []byte) (int, error) {
	w(strings.TrimRight(string(p), "\r\n"))
	return len(p), nil
}
