/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// interactiveTestRunner returns a task runner whose display reads key presses from the returned
// writer and draws nowhere, and a context that its interrupt handler cancels, as main's does.
func interactiveTestRunner(t *testing.T) (*TaskRunner, io.Writer, context.Context, *atomic.Int32) {
	t.Helper()
	input, keys := io.Pipe()
	t.Cleanup(func() { _ = keys.Close() })

	runner := NewTaskRunner()
	runner.programOptions = []tea.ProgramOption{
		tea.WithInput(input),
		tea.WithOutput(io.Discard),
		tea.WithoutSignalHandler(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	interrupts := &atomic.Int32{}
	SetInterruptHandler(func() {
		interrupts.Add(1)
		cancel()
	})
	t.Cleanup(func() { SetInterruptHandler(nil) })

	return runner, keys, ctx, interrupts
}

// runWithTimeout runs the runner, failing the test if it does not return within 10 seconds.
func runWithTimeout(t *testing.T, runner *TaskRunner) error {
	t.Helper()
	result := make(chan error, 1)
	go func() { result <- runner.runInteractive() }()
	select {
	case err := <-result:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("task runner did not return")
		return nil
	}
}

const ctrlC = "\x03"

// A Ctrl-C key press cancels the command's context, so the running task stops, and the runner
// returns its error without starting the next task.
func TestTaskRunnerCtrlCCancelsRunningTask(t *testing.T) {
	runner, keys, ctx, interrupts := interactiveTestRunner(t)
	secondRan := false
	runner.AddTask("first", func(*TaskOutput) error {
		_, _ = io.WriteString(keys, ctrlC)
		<-ctx.Done()
		return ctx.Err()
	})
	runner.AddTask("second", func(*TaskOutput) error {
		secondRan = true
		return nil
	})

	err := runWithTimeout(t, runner)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if got := interrupts.Load(); got != 1 {
		t.Errorf("interrupt handler called %d times, want 1", got)
	}
	if secondRan {
		t.Error("second task ran after the first one failed")
	}
}

// A task that ignores the context runs to its end after Ctrl-C, and so do the tasks after it, as
// they would without the display. The runner then reports success.
func TestTaskRunnerCtrlCLetsTasksIgnoringContextFinish(t *testing.T) {
	runner, keys, ctx, interrupts := interactiveTestRunner(t)
	secondRan := false
	runner.AddTask("first", func(*TaskOutput) error {
		_, _ = io.WriteString(keys, ctrlC)
		<-ctx.Done() // Finish only after the interrupt, as a task that ignores it would
		return nil
	})
	runner.AddTask("second", func(*TaskOutput) error {
		secondRan = true
		return nil
	})

	if err := runWithTimeout(t, runner); err != nil {
		t.Errorf("error = %v, want nil", err)
	}
	if got := interrupts.Load(); got != 1 {
		t.Errorf("interrupt handler called %d times, want 1", got)
	}
	if !secondRan {
		t.Error("second task did not run")
	}
}

// SIGINT while the display is on screen, which Bubble Tea turns into an InterruptMsg, is handled as
// the Ctrl-C key press: the runner waits for the tasks instead of returning ErrInterrupted.
func TestTaskRunnerSigintWaitsForTasks(t *testing.T) {
	runner, _, ctx, interrupts := interactiveTestRunner(t)
	finished := false
	runner.AddTask("first", func(*TaskOutput) error {
		runner.program.Send(tea.InterruptMsg{})
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // Cleanup that takes a while
		finished = true
		return ctx.Err()
	})

	err := runWithTimeout(t, runner)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if !finished {
		t.Error("runner returned before the task finished")
	}
	if got := interrupts.Load(); got != 1 {
		t.Errorf("interrupt handler called %d times, want 1", got)
	}
}

// Without Ctrl-C, the command is not interrupted.
func TestTaskRunnerWithoutCtrlCDoesNotInterrupt(t *testing.T) {
	runner, _, _, interrupts := interactiveTestRunner(t)
	runner.AddTask("first", func(*TaskOutput) error { return nil })
	runner.AddTask("second", func(*TaskOutput) error { return errors.New("failed") })

	if err := runWithTimeout(t, runner); err == nil || err.Error() != "failed" {
		t.Errorf("error = %v, want the second task's error", err)
	}
	if got := interrupts.Load(); got != 0 {
		t.Errorf("interrupt handler called %d times, want 0", got)
	}
}

// A notice printed while the display is on screen is held back until it closes.
func TestPrintNoticeWaitsForDisplay(t *testing.T) {
	var out strings.Builder
	previous := noticeOutput
	noticeOutput = &out
	t.Cleanup(func() { noticeOutput = previous })

	beginDisplay()
	PrintNotice("Interrupted")
	if out.Len() != 0 {
		t.Fatalf("notice printed over the display: %q", out.String())
	}
	endDisplay()
	if out.String() != "Interrupted\n" {
		t.Errorf("output after the display closed = %q", out.String())
	}

	PrintNotice("Again")
	if out.String() != "Interrupted\nAgain\n" {
		t.Errorf("output without a display = %q", out.String())
	}
}
