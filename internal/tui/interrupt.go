/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package tui

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// The terminal sends no SIGINT for Ctrl-C while a TUI holds it in raw mode: the TUI receives a key
// press instead. The task runner, which runs work while its display is on screen, calls interrupt
// so that Ctrl-C stops the command as SIGINT would.
var interruptHandler func()

// SetInterruptHandler sets what a Ctrl-C key press in the task runner does to the command. main
// sets it to what it does on SIGINT: cancel the command's context.
func SetInterruptHandler(handler func()) {
	interruptHandler = handler
}

func interrupt() {
	if interruptHandler != nil {
		interruptHandler()
	}
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
