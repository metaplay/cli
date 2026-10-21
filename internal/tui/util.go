/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package tui

import (
	"fmt"
	"os"

	"github.com/mattn/go-isatty"
)

// Is the UI library in interactive mode?
var isInteractiveMode = true

func IsInteractiveMode() bool {
	return isInteractiveMode
}

// IsInteractiveTerminal reports whether output written to f can be redrawn in
// place: the CLI is in interactive mode, and f is itself a terminal.
// Interactive mode is decided from stdout, and stderr can be redirected on its
// own, so output drawn on stderr checks stderr too.
func IsInteractiveTerminal(f *os.File) bool {
	fd := f.Fd()
	return isInteractiveMode && (isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd))
}

// Set the interactive mode of the UI library.
func SetInteractiveMode(isInteractive bool) {
	isInteractiveMode = isInteractive
}

func requireInteractiveMode(dialog string) error {
	if !isInteractiveMode {
		return fmt.Errorf("cannot show the %s in non-interactive mode", dialog)
	}
	return nil
}
