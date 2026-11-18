/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/metaplay/cli/cmd"
	"github.com/metaplay/cli/internal/tui"
)

func main() {
	// tui.Interrupt stops the command on SIGINT (Ctrl+C) or SIGTERM, and on Ctrl+C pressed in a TUI,
	// which receives it as a key press, not as SIGINT. It marks the command interrupted, so the task
	// runner starts no further tasks, and then calls the handler below, which cancels ctx.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	// On the first interrupt, tell the user that cleanup is in progress, and stop watching signals,
	// so a second Ctrl+C falls through to Go's default handler and terminates the process
	// immediately. Then cancel the command.
	var interruptOnce sync.Once
	tui.SetInterruptHandler(func() {
		interruptOnce.Do(func() {
			tui.PrintNotice("\nInterrupted, cleaning up... (press Ctrl+C again to force quit)")
			signal.Stop(signals)
			cancel()
		})
	})
	go func() {
		<-signals
		tui.Interrupt()
	}()

	cmd.ExecuteContext(ctx)
}
