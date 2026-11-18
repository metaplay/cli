/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package tui

import "sync"

// The terminal sends no SIGINT for Ctrl-C while a TUI holds it in raw mode: the TUI receives a key
// press instead. A TUI that runs work in the background, such as the task runner, calls interrupt
// so that Ctrl-C stops the command as SIGINT would.
var (
	interruptMu      sync.Mutex
	interruptHandler func()
)

// SetInterruptHandler sets what a Ctrl-C key press in a TUI does to the command. main sets it to
// cancel the command's context, which is what SIGINT does.
func SetInterruptHandler(handler func()) {
	interruptMu.Lock()
	defer interruptMu.Unlock()
	interruptHandler = handler
}

func interrupt() {
	interruptMu.Lock()
	handler := interruptHandler
	interruptMu.Unlock()
	if handler != nil {
		handler()
	}
}
