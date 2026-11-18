/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestTaskRunnerCtrlCStopsFurtherTasks checks that Ctrl-C marks the runner interrupted and that an
// interrupted runner starts no further tasks.
func TestTaskRunnerCtrlCStopsFurtherTasks(t *testing.T) {
	runner := NewTaskRunner()
	ran := []string{}
	runner.AddTask("first", func(*TaskOutput) error {
		// Ctrl-C arrives while the first task runs, and the task ignores it.
		_, cmd := runner.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		if cmd == nil {
			t.Error("Ctrl-C did not quit the display")
		}
		ran = append(ran, "first")
		return nil
	})
	runner.AddTask("second", func(*TaskOutput) error {
		ran = append(ran, "second")
		return nil
	})

	runner.executeTasks()

	if !runner.interrupted.Load() {
		t.Error("runner not marked interrupted after Ctrl-C")
	}
	if len(ran) != 1 || ran[0] != "first" {
		t.Errorf("tasks run = %v, want only the first", ran)
	}
}

func TestInterruptCallsHandler(t *testing.T) {
	called := 0
	SetInterruptHandler(func() { called++ })
	t.Cleanup(func() { SetInterruptHandler(nil) })

	interrupt()
	if called != 1 {
		t.Errorf("handler called %d times, want 1", called)
	}

	SetInterruptHandler(nil)
	interrupt() // No handler: must not panic
}
