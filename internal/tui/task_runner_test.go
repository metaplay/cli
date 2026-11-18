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

// resetInterruptState gives the test a fresh interrupt state and records the errors the runner
// shows. It restores the package's state afterwards.
func resetInterruptState(t *testing.T) *[]string {
	t.Helper()
	interrupted.Store(false)
	previousDisplay := errorDisplay
	shown := &[]string{}
	errorDisplay = func(err error) { *shown = append(*shown, err.Error()) }
	t.Cleanup(func() {
		interrupted.Store(false)
		errorDisplay = previousDisplay
		SetInterruptHandler(nil)
	})
	return shown
}

// displayTest runs a task runner's display, which reads key presses from keys and draws nowhere.
// Its interrupt handler cancels ctx, as main's cancels the command's context.
type displayTest struct {
	runner     *TaskRunner
	keys       io.Writer
	input      io.Reader
	ctx        context.Context
	interrupts atomic.Int32
	shown      *[]string // Errors the runner showed
}

func newDisplayTest(t *testing.T) *displayTest {
	t.Helper()
	shown := resetInterruptState(t)
	input, keys := io.Pipe()
	t.Cleanup(func() { _ = keys.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d := &displayTest{runner: NewTaskRunner(), keys: keys, input: input, ctx: ctx, shown: shown}
	SetInterruptHandler(func() {
		d.interrupts.Add(1)
		cancel()
	})
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

// pressCtrlC sends Ctrl-C to the display and waits until the command is cancelled, as it is once
// the display has closed.
func (d *displayTest) pressCtrlC() {
	_, _ = io.WriteString(d.keys, "\x03")
	<-d.ctx.Done()
}

// recorder returns task functions that record their names in the order they run.
type recorder struct{ ran []string }

func (r *recorder) task(name string, err error) TaskRunFunc {
	return func(*TaskOutput) error {
		r.ran = append(r.ran, name)
		return err
	}
}

func (r *recorder) cleanup(name string, err error) func(context.Context, *TaskOutput) error {
	return func(ctx context.Context, _ *TaskOutput) error {
		r.ran = append(r.ran, name)
		if ctx.Err() != nil {
			return errors.New(name + ": context already done")
		}
		return err
	}
}

func (r *recorder) order() string { return strings.Join(r.ran, ",") }

// Ctrl-C cancels the command's context, so the running task stops. The runner starts no further
// ordinary task, still runs the cleanup task, and returns the running task's error.
func TestTaskRunnerCtrlCCancelsRunningTaskAndSkipsTheRest(t *testing.T) {
	d := newDisplayTest(t)
	r := &recorder{}
	d.runner.AddTask("collect", func(*TaskOutput) error {
		r.ran = append(r.ran, "collect")
		d.pressCtrlC()
		return d.ctx.Err()
	})
	d.runner.AddTask("download", r.task("download", nil))
	d.runner.AddCleanupTask("remove", r.cleanup("remove", nil))

	err := d.run(t)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if got := d.interrupts.Load(); got != 1 {
		t.Errorf("interrupt handler called %d times, want 1", got)
	}
	if got := r.order(); got != "collect,remove" {
		t.Errorf("tasks run = %s, want collect,remove", got)
	}
	if len(*d.shown) != 0 {
		t.Errorf("errors shown = %v, want none: the failure was caused by the interrupt", *d.shown)
	}
}

// A task that ignores the context runs to its end after Ctrl-C, but the next ordinary task does not
// start, and the runner returns context.Canceled although no task failed.
func TestTaskRunnerCtrlCSkipsTasksAfterOneIgnoringContext(t *testing.T) {
	d := newDisplayTest(t)
	r := &recorder{}
	d.runner.AddTask("uninstall", func(*TaskOutput) error {
		r.ran = append(r.ran, "uninstall")
		d.pressCtrlC()
		return nil // Finishes anyway, as Helm does
	})
	d.runner.AddTask("uninstall another", r.task("uninstall another", nil))

	err := d.run(t)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if got := r.order(); got != "uninstall" {
		t.Errorf("tasks run = %s, want uninstall", got)
	}
}

// A real failure after Ctrl-C, which the command reports only as "Canceled.", is shown by the
// runner, and so is a cleanup task's failure.
func TestTaskRunnerShowsRealFailuresAfterCtrlC(t *testing.T) {
	d := newDisplayTest(t)
	r := &recorder{}
	d.runner.AddTask("deploy", func(*TaskOutput) error {
		r.ran = append(r.ran, "deploy")
		d.pressCtrlC()
		return errors.New("helm: release failed") // Not caused by the cancellation
	})
	d.runner.AddCleanupTask("re-enable", r.cleanup("re-enable", errors.New("curl failed")))

	if err := d.run(t); err == nil || err.Error() != "helm: release failed" {
		t.Errorf("error = %v, want the deploy's", err)
	}
	want := "deploy: helm: release failed|re-enable: curl failed"
	if got := strings.Join(*d.shown, "|"); got != want {
		t.Errorf("errors shown = %q, want %q", got, want)
	}
}

// SIGINT while the display is on screen, which Bubble Tea turns into an InterruptMsg, is handled as
// the Ctrl-C key press: the runner waits for the running task instead of returning ErrInterrupted.
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

// Without Ctrl-C, the command is not interrupted, and the failure is left to the command to show.
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
	if len(*d.shown) != 0 {
		t.Errorf("errors shown = %v, want none", *d.shown)
	}
}

// nonInteractive runs the test without a display, restoring the mode afterwards.
func nonInteractive(t *testing.T) {
	t.Helper()
	previous := isInteractiveMode
	isInteractiveMode = false
	t.Cleanup(func() { isInteractiveMode = previous })
}

// After an ordinary task fails, only cleanup tasks run. The runner returns the first ordinary
// failure, and shows the cleanup task's failure, which that error does not cover.
func TestTaskRunnerRunsOnlyCleanupAfterFailure(t *testing.T) {
	nonInteractive(t)
	shown := resetInterruptState(t)
	r := &recorder{}
	runner := NewTaskRunner()
	runner.AddTask("disable", r.task("disable", nil))
	runner.AddTask("collect", r.task("collect", errors.New("collect failed")))
	runner.AddCleanupTask("re-enable", r.cleanup("re-enable", errors.New("curl failed")))
	runner.AddTask("download", r.task("download", nil))
	runner.AddCleanupTask("remove", r.cleanup("remove", nil))

	err := runner.Run()

	if err == nil || err.Error() != "collect failed" {
		t.Errorf("error = %v, want the collection's", err)
	}
	if got := r.order(); got != "disable,collect,re-enable,remove" {
		t.Errorf("tasks run = %s", got)
	}
	if got := strings.Join(*shown, "|"); got != "re-enable: curl failed" {
		t.Errorf("errors shown = %q", got)
	}
}

// A cleanup task's failure does not stop the ordinary tasks after it, and is the runner's error
// when no ordinary task failed.
func TestTaskRunnerCleanupFailureDoesNotSkipTasks(t *testing.T) {
	nonInteractive(t)
	shown := resetInterruptState(t)
	r := &recorder{}
	runner := NewTaskRunner()
	runner.AddTask("collect", r.task("collect", nil))
	runner.AddCleanupTask("re-enable", r.cleanup("re-enable", errors.New("curl failed")))
	runner.AddTask("download", r.task("download", nil))
	runner.AddCleanupTask("remove", r.cleanup("remove", nil))

	err := runner.Run()

	if err == nil || err.Error() != "curl failed" {
		t.Errorf("error = %v, want the cleanup task's", err)
	}
	if got := r.order(); got != "collect,re-enable,download,remove" {
		t.Errorf("tasks run = %s", got)
	}
	if len(*shown) != 0 {
		t.Errorf("errors shown = %v, want none: the command shows its error", *shown)
	}
}

// An interrupt without a display, from SIGINT, stops the runner the same way, and a cleanup task
// does not run when no task before it started.
func TestTaskRunnerInterruptWithoutDisplay(t *testing.T) {
	nonInteractive(t)
	resetInterruptState(t)
	r := &recorder{}
	runner := NewTaskRunner()
	runner.AddCleanupTask("nothing to undo", r.cleanup("nothing to undo", nil))
	runner.AddTask("first", func(*TaskOutput) error {
		r.ran = append(r.ran, "first")
		Interrupt()
		return nil
	})
	runner.AddTask("second", r.task("second", nil))
	runner.AddCleanupTask("undo first", r.cleanup("undo first", nil))

	if err := runner.Run(); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if got := r.order(); got != "first,undo first" {
		t.Errorf("tasks run = %s, want first,undo first", got)
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

// Once the display has closed after an interrupt, a task's output lines are logged, so a task that
// still runs, such as a Helm install, shows its progress.
func TestTaskOutputLoggedOnceDisplayClosed(t *testing.T) {
	resetInterruptState(t)
	runner := NewTaskRunner()
	runner.AddTask("install", func(*TaskOutput) error { return nil })
	output := &runner.tasks[0].output

	if output.logsLines() {
		t.Error("lines logged while the display is on screen")
	}
	runner.progress.closeDisplay()
	if !output.logsLines() {
		t.Error("lines not logged after the display closed")
	}
}
