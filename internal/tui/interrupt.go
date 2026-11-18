/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package tui

import (
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// The terminal sends no SIGINT for Ctrl-C while a TUI holds it in raw mode: the TUI receives a key
// press instead. The task runner calls Interrupt on that key press, and main calls it on SIGINT and
// SIGTERM, so that both stop the command the same way.
var (
	interruptHandler func()
	interrupted      atomic.Bool
)

// SetInterruptHandler sets what Interrupt does to the command. main sets it to cancel the command's
// context.
func SetInterruptHandler(handler func()) {
	interruptHandler = handler
}

// Interrupt stops the command: the task runner starts no further tasks except cleanup tasks, and
// the interrupt handler cancels the command's context. It marks the command interrupted before
// calling the handler, so a task that ends because of the cancellation never lets the next one
// start.
func Interrupt() {
	interrupted.Store(true)
	if interruptHandler != nil {
		interruptHandler()
	}
}

// errorDisplay shows an error to the user as the command's final error is shown.
var errorDisplay = func(err error) { _, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err) }

// SetErrorDisplay sets how the task runner shows the failures that the command's returned error
// does not cover: a cleanup task's failure after an earlier failure, and failures after an
// interrupt, which the command reports only as "Canceled.".
func SetErrorDisplay(display func(error)) {
	errorDisplay = display
}

var (
	noticeMu        sync.Mutex
	displayOnScreen bool      // A task runner's display is on screen
	heldNotices     []string  // Notices to print once the display closes
	noticeOutput    io.Writer = os.Stderr
)

// PrintNotice prints a line to stderr. While a task runner's display is on screen, it prints the
// line once the display has closed, since text written over the display garbles it.
func PrintNotice(line string) {
	noticeMu.Lock()
	defer noticeMu.Unlock()
	if displayOnScreen {
		heldNotices = append(heldNotices, line)
		return
	}
	_, _ = fmt.Fprintln(noticeOutput, line)
}

func beginDisplay() {
	noticeMu.Lock()
	defer noticeMu.Unlock()
	displayOnScreen = true
}

// endDisplay prints the notices held back while the display was on screen.
func endDisplay() {
	noticeMu.Lock()
	defer noticeMu.Unlock()
	displayOnScreen = false
	for _, line := range heldNotices {
		_, _ = fmt.Fprintln(noticeOutput, line)
	}
	heldNotices = nil
}
