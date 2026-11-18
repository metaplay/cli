/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package helmutil

import (
	"log/slog"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// NewDebugLogHandler returns a slog handler that writes Helm's log records to
// the CLI's debug log, so they show up with --verbose. Helm v3 logged through
// zerolog's Printf, which is also debug level.
//
// Helm v4 logs through slog: through the logger in action.Configuration, and in
// some places through the global slog logger. Set this handler for both. The
// default handler of the global logger prints straight to stderr, garbling the
// interactive output.
func NewDebugLogHandler() slog.Handler {
	return newLineHandler(func(line string) { log.Debug().Msg(line) }, slog.LevelDebug)
}

// newTaskOutputLogHandler returns a slog handler that writes Helm's action log
// to a task's output. Helm's debug records are only included with --verbose.
func newTaskOutputLogHandler(emit func(line string)) slog.Handler {
	minLevel := slog.LevelInfo
	if zerolog.GlobalLevel() <= zerolog.DebugLevel {
		minLevel = slog.LevelDebug
	}
	return newLineHandler(emit, minLevel)
}

// newLineHandler formats each record of at least minLevel as one 'key=value'
// line without a timestamp and passes it to emit. The level is only shown for
// warnings and errors, so they stand out.
func newLineHandler(emit func(line string), minLevel slog.Level) slog.Handler {
	return slog.NewTextHandler(lineWriter(emit), &slog.HandlerOptions{
		Level: minLevel,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) > 0 {
				return attr
			}
			if attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			if attr.Key == slog.LevelKey {
				if level, ok := attr.Value.Any().(slog.Level); ok && level < slog.LevelWarn {
					return slog.Attr{}
				}
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
