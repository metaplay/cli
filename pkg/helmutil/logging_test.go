/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package helmutil

import (
	"log/slog"
	"testing"
)

func TestLineHandler(t *testing.T) {
	var lines []string
	logger := slog.New(newLineHandler(func(line string) { lines = append(lines, line) }, slog.LevelInfo))

	logger.Debug("dropped")
	logger.Info("preparing upgrade", "name", "rel")
	logger.Warn("something odd")

	want := []string{
		`msg="preparing upgrade" name=rel`,
		`level=WARN msg="something odd"`,
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines %q, want %q", len(lines), lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}
