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
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/interrupt"
)

// unreported returns the failures that the runner's error reports besides the command's error.
func unreported(err error) string {
	tasksErr, ok := errors.AsType[*TasksError](err)
	if !ok {
		return ""
	}
	var messages []string
	for _, failure := range tasksErr.Unreported {
		messages = append(messages, failure.Error())
	}
	return strings.Join(messages, "|")
}

// displayTest runs a task runner's display, which reads key presses from keys and draws nowhere.
type displayTest struct {
	runner  *TaskRunner
	keys    io.Writer
	input   io.Reader
	ctx     context.Context // The command's context, which an interrupt cancels
	notices atomic.Int32    // Times the user was told about the interrupt
}

func newDisplayTest(t *testing.T) *displayTest {
	t.Helper()
	input, keys := io.Pipe()
	t.Cleanup(func() { _ = keys.Close() })

	d := &displayTest{runner: NewTaskRunner(), keys: keys, input: input}
	d.ctx = interrupt.WithCancel(t.Context(), func() { d.notices.Add(1) })
	return d
}

// run runs the tasks with the display, failing the test if it does not return within 10 seconds.
func (d *displayTest) run(t *testing.T) error {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		result <- d.runner.runInteractive(d.ctx, tea.WithInput(d.input), tea.WithOutput(io.Discard))
	}()
	select {
	case err := <-result:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("task runner did not return")
		return nil
	}
}

// pressCtrlC sends Ctrl-C to the display and waits until the command is cancelled.
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

func (r *recorder) cleanup(name string, err error) CleanupTaskRunFunc {
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
	if cause := context.Cause(d.ctx); !errors.Is(cause, interrupt.ErrInterrupted) {
		t.Errorf("cause of the cancellation = %v, want ErrInterrupted", cause)
	}
	if got := d.notices.Load(); got != 1 {
		t.Errorf("interrupt notice shown %d times, want 1", got)
	}
	if got := r.order(); got != "collect,remove" {
		t.Errorf("tasks run = %s, want collect,remove", got)
	}
	if got := unreported(err); got != "" {
		t.Errorf("unreported failures = %q, want none: the failure was caused by the interrupt", got)
	}
	if got := strings.Join(d.runner.notStarted, ","); got != "download" {
		t.Errorf("not started because of the interrupt = %q, want download", got)
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

// A real failure after Ctrl-C, which the command reports only as "Canceled.", is reported as
// unreported by the command's error, and so is a cleanup task's failure.
func TestTaskRunnerShowsRealFailuresAfterCtrlC(t *testing.T) {
	d := newDisplayTest(t)
	r := &recorder{}
	d.runner.AddTask("deploy", func(*TaskOutput) error {
		r.ran = append(r.ran, "deploy")
		d.pressCtrlC()
		return errors.New("helm: release failed") // Not caused by the cancellation
	})
	d.runner.AddCleanupTask("re-enable", r.cleanup("re-enable", errors.New("curl failed")))

	err := d.run(t)

	if err == nil || err.Error() != "helm: release failed" {
		t.Errorf("error = %v, want the deploy's", err)
	}
	want := "deploy: helm: release failed|re-enable: curl failed"
	if got := unreported(err); got != want {
		t.Errorf("unreported failures = %q, want %q", got, want)
	}
}

// An interrupt from a signal while the display is on screen, which cancels the context without a
// key press, is handled as the Ctrl-C key press: the runner waits for the running task.
func TestTaskRunnerSignalWaitsForTasks(t *testing.T) {
	d := newDisplayTest(t)
	finished := false
	d.runner.AddTask("first", func(*TaskOutput) error {
		interrupt.Trigger(d.ctx)          // As main does on SIGINT and SIGTERM
		time.Sleep(50 * time.Millisecond) // Cleanup that takes a while
		finished = true
		return d.ctx.Err()
	})
	d.runner.AddTask("second", func(*TaskOutput) error { return nil })

	err := d.run(t)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if !finished {
		t.Error("runner returned before the task finished")
	}
	if got := d.notices.Load(); got != 1 {
		t.Errorf("interrupt notice shown %d times, want 1", got)
	}
}

// A cancellation that is not an interrupt, such as a deadline, stops the runner the same way, and
// the runner returns the context's error.
func TestTaskRunnerDeadlineStopsTasks(t *testing.T) {
	d := newDisplayTest(t)
	ctx, cancel := context.WithTimeout(d.ctx, 10*time.Millisecond)
	defer cancel()
	d.ctx = ctx
	r := &recorder{}
	d.runner.AddTask("first", func(*TaskOutput) error {
		r.ran = append(r.ran, "first")
		<-ctx.Done()
		return nil
	})
	d.runner.AddTask("second", r.task("second", nil))

	err := d.run(t)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
	if got := r.order(); got != "first" {
		t.Errorf("tasks run = %s, want first", got)
	}
	if got := d.notices.Load(); got != 0 {
		t.Errorf("interrupt notice shown %d times, want 0", got)
	}
}

// A Ctrl-C key press during the last task stops the command too, although the runner returns no
// error because every task ran: the command's context is cancelled.
func TestTaskRunnerCtrlCDuringLastTaskStopsCommand(t *testing.T) {
	d := newDisplayTest(t)
	d.runner.AddTask("last", func(*TaskOutput) error {
		d.pressCtrlC()
		return nil
	})

	if err := d.run(t); err != nil {
		t.Errorf("error = %v, want nil: every task ran", err)
	}
	if d.ctx.Err() == nil {
		t.Error("the command's context was not cancelled")
	}
}

// A cleanup task's context keeps the values of the command's context, and is not cancelled with it.
func TestTaskRunnerCleanupContext(t *testing.T) {
	nonInteractive(t)
	type key struct{}
	ctx := context.WithValue(interrupt.WithCancel(t.Context(), nil), key{}, "value")
	runner := NewTaskRunner()
	runner.AddTask("first", func(*TaskOutput) error {
		interrupt.Trigger(ctx)
		return nil
	})
	var value any
	var cleanupErr error
	hasDeadline := false
	runner.AddCleanupTask("undo", func(ctx context.Context, _ *TaskOutput) error {
		value = ctx.Value(key{})
		cleanupErr = ctx.Err()
		_, hasDeadline = ctx.Deadline()
		return nil
	})

	if err := runner.Run(ctx); err != nil {
		t.Errorf("error = %v, want nil: every task ran", err)
	}
	if value != "value" {
		t.Errorf("value in the cleanup task's context = %v, want the command's", value)
	}
	if cleanupErr != nil || !hasDeadline {
		t.Errorf("cleanup task's context: err = %v, has deadline = %v, want not cancelled and bounded", cleanupErr, hasDeadline)
	}
}

// Without Ctrl-C, the command is not interrupted, and the failure is left to the command to show.
func TestTaskRunnerWithoutCtrlCDoesNotInterrupt(t *testing.T) {
	d := newDisplayTest(t)
	d.runner.AddTask("first", func(*TaskOutput) error { return nil })
	d.runner.AddTask("second", func(*TaskOutput) error { return errors.New("failed") })

	err := d.run(t)

	if err == nil || err.Error() != "failed" {
		t.Errorf("error = %v, want the second task's error", err)
	}
	if d.ctx.Err() != nil {
		t.Errorf("the command's context was cancelled: %v", d.ctx.Err())
	}
	if got := unreported(err); got != "" {
		t.Errorf("unreported failures = %q, want none", got)
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
// failure, and reports the cleanup task's failure, which that error does not cover.
func TestTaskRunnerRunsOnlyCleanupAfterFailure(t *testing.T) {
	nonInteractive(t)
	r := &recorder{}
	runner := NewTaskRunner()
	runner.AddTask("disable", r.task("disable", nil))
	runner.AddTask("collect", r.task("collect", errors.New("collect failed")))
	runner.AddCleanupTask("re-enable", r.cleanup("re-enable", errors.New("curl failed")))
	runner.AddTask("download", r.task("download", nil))
	runner.AddCleanupTask("remove", r.cleanup("remove", nil))

	err := runner.Run(context.Background())

	if err == nil || err.Error() != "collect failed" {
		t.Errorf("error = %v, want the collection's", err)
	}
	if got := r.order(); got != "disable,collect,re-enable,remove" {
		t.Errorf("tasks run = %s", got)
	}
	if got := unreported(err); got != "re-enable: curl failed" {
		t.Errorf("unreported failures = %q", got)
	}
}

// A cleanup task's failure does not stop the ordinary tasks after it, and is the runner's error
// when no ordinary task failed.
func TestTaskRunnerCleanupFailureDoesNotSkipTasks(t *testing.T) {
	nonInteractive(t)
	r := &recorder{}
	runner := NewTaskRunner()
	runner.AddTask("collect", r.task("collect", nil))
	runner.AddCleanupTask("re-enable", r.cleanup("re-enable", errors.New("curl failed")))
	runner.AddTask("download", r.task("download", nil))
	runner.AddCleanupTask("remove", r.cleanup("remove", nil))

	err := runner.Run(context.Background())

	if err == nil || err.Error() != "curl failed" {
		t.Errorf("error = %v, want the cleanup task's", err)
	}
	if got := r.order(); got != "collect,re-enable,download,remove" {
		t.Errorf("tasks run = %s", got)
	}
	if got := unreported(err); got != "" {
		t.Errorf("unreported failures = %q, want none: the command shows its error", got)
	}
}

// An interrupt without a display, from SIGINT, stops the runner the same way, and a cleanup task
// does not run when no task before it started.
func TestTaskRunnerInterruptWithoutDisplay(t *testing.T) {
	nonInteractive(t)
	ctx := interrupt.WithCancel(t.Context(), nil)
	r := &recorder{}
	runner := NewTaskRunner()
	runner.AddCleanupTask("nothing to undo", r.cleanup("nothing to undo", nil))
	runner.AddTask("first", func(*TaskOutput) error {
		r.ran = append(r.ran, "first")
		interrupt.Trigger(ctx)
		return nil
	})
	runner.AddTask("second", r.task("second", nil))
	runner.AddCleanupTask("undo first", r.cleanup("undo first", nil))

	if err := runner.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if got := r.order(); got != "first,undo first" {
		t.Errorf("tasks run = %s, want first,undo first", got)
	}
}

// Once the display has closed after an interrupt, a task's output lines are logged, so a task that
// still runs, such as a Helm install, shows its progress.
func TestTaskOutputLoggedOnceDisplayClosed(t *testing.T) {
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

// Tasks skipped because an earlier task failed are not blamed on an interrupt that came later, and
// the failure is the runner's error.
func TestTaskRunnerInterruptAfterFailure(t *testing.T) {
	nonInteractive(t)
	ctx := interrupt.WithCancel(t.Context(), nil)
	r := &recorder{}
	runner := NewTaskRunner()
	runner.AddTask("collect", r.task("collect", errors.New("collect failed")))
	runner.AddCleanupTask("re-enable", func(context.Context, *TaskOutput) error {
		interrupt.Trigger(ctx)
		return nil
	})
	runner.AddTask("download", r.task("download", nil))

	if err := runner.Run(ctx); err == nil || err.Error() != "collect failed" {
		t.Errorf("error = %v, want the collection's", err)
	}
	if len(runner.notStarted) != 0 {
		t.Errorf("not started because of the interrupt = %v, want none", runner.notStarted)
	}
}

// A CLIError a cleanup task returns is reported as it is: the command's error display shows only a
// CLIError's own message, so a title wrapped around it would be lost anyway.
func TestTaskRunnerShowsCLIErrorAsItIs(t *testing.T) {
	nonInteractive(t)
	cleanupErr := clierrors.New("Failed to remove the dump")
	runner := NewTaskRunner()
	runner.AddTask("collect", func(*TaskOutput) error { return errors.New("collect failed") })
	runner.AddCleanupTask("remove", func(context.Context, *TaskOutput) error { return cleanupErr })

	err := runner.Run(context.Background())

	tasksErr, ok := errors.AsType[*TasksError](err)
	if !ok || len(tasksErr.Unreported) != 1 || tasksErr.Unreported[0] != cleanupErr { //nolint:errorlint // The exact error, not a wrapper
		t.Errorf("error = %#v, want the CLIError itself as the only unreported failure", err)
	}
}
