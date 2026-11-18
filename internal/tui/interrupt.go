/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package tui

// The terminal sends no SIGINT for Ctrl-C while a TUI holds it in raw mode: the TUI receives a key
// press instead. A TUI that runs work in the background, such as the task runner, calls interrupt
// so that Ctrl-C stops the command as SIGINT would.
var interruptHandler func()

// SetInterruptHandler sets what a Ctrl-C key press in a TUI does to the command. main sets it to
// cancel the command's context, which is what SIGINT does.
func SetInterruptHandler(handler func()) {
	interruptHandler = handler
}

func interrupt() {
	if interruptHandler != nil {
		interruptHandler()
	}
}
