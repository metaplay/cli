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

// displayTest runs a task runner's display, which reads key presses from keys and draws nowhere.
// Its interrupt handler cancels ctx, as main's cancels the command's context.
type displayTest struct {
	runner     *TaskRunner
	keys       io.Writer
	input      io.Reader
	ctx        context.Context
	interrupts atomic.Int32
}

func newDisplayTest(t *testing.T) *displayTest {
	t.Helper()
	input, keys := io.Pipe()
	t.Cleanup(func() { _ = keys.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d := &displayTest{runner: NewTaskRunner(), keys: keys, input: input, ctx: ctx}
	SetInterruptHandler(func() {
		d.interrupts.Add(1)
		cancel()
	})
	t.Cleanup(func() { SetInterruptHandler(nil) })
	return d
}

// run runs the tasks with the display, failing the test if it does not return within 10 seconds.
func (d *displayTest) run(t *testing.T) error {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		result <- d.runner.runInteractive(tea.WithInput(d.input), tea.WithOutput(io.Discard), tea.WithoutSignalHandler())
	}()
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
	d := newDisplayTest(t)
	secondRan := false
	d.runner.AddTask("first", func(*TaskOutput) error {
		_, _ = io.WriteString(d.keys, ctrlC)
		<-d.ctx.Done()
		return d.ctx.Err()
	})
	d.runner.AddTask("second", func(*TaskOutput) error {
		secondRan = true
		return nil
	})

	err := d.run(t)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if got := d.interrupts.Load(); got != 1 {
		t.Errorf("interrupt handler called %d times, want 1", got)
	}
	if secondRan {
		t.Error("second task ran after the first one failed")
	}
}

// A task that ignores the context runs to its end after Ctrl-C, and so do the tasks after it, as
// they would without the display. The runner then reports success.
func TestTaskRunnerCtrlCLetsTasksIgnoringContextFinish(t *testing.T) {
	d := newDisplayTest(t)
	secondRan := false
	d.runner.AddTask("first", func(*TaskOutput) error {
		_, _ = io.WriteString(d.keys, ctrlC)
		<-d.ctx.Done() // Finish only after the interrupt, as a task that ignores it would
		return nil
	})
	d.runner.AddTask("second", func(*TaskOutput) error {
		secondRan = true
		return nil
	})

	if err := d.run(t); err != nil {
		t.Errorf("error = %v, want nil", err)
	}
	if got := d.interrupts.Load(); got != 1 {
		t.Errorf("interrupt handler called %d times, want 1", got)
	}
	if !secondRan {
		t.Error("second task did not run")
	}
}

// SIGINT while the display is on screen, which Bubble Tea turns into an InterruptMsg, is handled as
// the Ctrl-C key press: the runner waits for the tasks instead of returning ErrInterrupted.
func TestTaskRunnerSigintWaitsForTasks(t *testing.T) {
	d := newDisplayTest(t)
	finished := false
	d.runner.AddTask("first", func(*TaskOutput) error {
		d.runner.program.Send(tea.InterruptMsg{})
		<-d.ctx.Done()
		time.Sleep(50 * time.Millisecond) // Cleanup that takes a while
		finished = true
		return d.ctx.Err()
	})

	err := d.run(t)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if !finished {
		t.Error("runner returned before the task finished")
	}
	if got := d.interrupts.Load(); got != 1 {
		t.Errorf("interrupt handler called %d times, want 1", got)
	}
}

// Without Ctrl-C, the command is not interrupted.
func TestTaskRunnerWithoutCtrlCDoesNotInterrupt(t *testing.T) {
	d := newDisplayTest(t)
	d.runner.AddTask("first", func(*TaskOutput) error { return nil })
	d.runner.AddTask("second", func(*TaskOutput) error { return errors.New("failed") })

	if err := d.run(t); err == nil || err.Error() != "failed" {
		t.Errorf("error = %v, want the second task's error", err)
	}
	if got := d.interrupts.Load(); got != 0 {
		t.Errorf("interrupt handler called %d times, want 0", got)
	}
}

// After Ctrl-C, a cleanup task runs although the task before it failed and the next ordinary task
// is skipped, and its context is not cancelled.
func TestTaskRunnerCleanupTaskRunsAfterCtrlC(t *testing.T) {
	d := newDisplayTest(t)
	var ran []string
	var cleanupCtxErr error
	d.runner.AddTask("collect", func(*TaskOutput) error {
		_, _ = io.WriteString(d.keys, ctrlC)
		<-d.ctx.Done()
		return d.ctx.Err()
	})
	d.runner.AddTask("download", func(*TaskOutput) error {
		ran = append(ran, "download")
		return nil
	})
	d.runner.AddCleanupTask("remove", func(ctx context.Context, _ *TaskOutput) error {
		ran = append(ran, "remove")
		cleanupCtxErr = ctx.Err()
		return nil
	})

	if err := d.run(t); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if len(ran) != 1 || ran[0] != "remove" {
		t.Errorf("tasks run after the failure = %v, want only the cleanup task", ran)
	}
	if cleanupCtxErr != nil {
		t.Errorf("cleanup task's context: %v", cleanupCtxErr)
	}
}

// Without a display, a failed task skips the ordinary tasks after it but not the cleanup tasks,
// and the runner returns the first failure, not a cleanup task's.
func TestTaskRunnerNonInteractiveRunsCleanupAfterFailure(t *testing.T) {
	isInteractiveMode = false
	t.Cleanup(func() { isInteractiveMode = true })

	runner := NewTaskRunner()
	var ran []string
	add := func(name string, err error) {
		runner.AddTask(name, func(*TaskOutput) error { ran = append(ran, name); return err })
	}
	addCleanup := func(name string, err error) {
		runner.AddCleanupTask(name, func(context.Context, *TaskOutput) error { ran = append(ran, name); return err })
	}
	add("first", nil)
	addCleanup("undo first", nil)
	add("second", errors.New("second failed"))
	add("third", nil)
	addCleanup("undo second", errors.New("undo failed"))

	err := runner.Run()

	if err == nil || err.Error() != "second failed" {
		t.Errorf("error = %v, want the second task's", err)
	}
	if got := strings.Join(ran, ","); got != "first,undo first,second,undo second" {
		t.Errorf("tasks run = %s", got)
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
