/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/metaplay/cli/internal/interrupt"
)

// answerDialog runs a dialog whose user types keys.
func answerDialog[M dialogModel](t *testing.T, model M, keys string) (M, error) {
	t.Helper()
	return runDialog(model, "test dialog", tea.WithInput(strings.NewReader(keys)), tea.WithOutput(io.Discard), tea.WithoutSignalHandler())
}

func newList() list.Model { return list.New(nil, list.NewDefaultDelegate(), 0, 0) }

// Ctrl-C in any dialog is an interrupt, which the command reports as "Canceled." with exit code
// 130. It counts as a cancellation for errors.Is.
func TestDialogsCtrlCIsInterrupt(t *testing.T) {
	check := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, interrupt.ErrInterrupted) || !errors.Is(err, context.Canceled) {
			t.Errorf("%s: error = %v, want ErrInterrupted", name, err)
		}
	}

	_, err := answerDialog(t, newConfirmDialog(context.Background(), "", "", "Sure?", true), "\x03")
	check("confirm dialog", err)
	_, err = answerDialog(t, newCompactListModel("Choose", newList()), "\x03")
	check("list", err)
	_, err = answerDialog(t, newMultiSelectModel("Choose", "", newList(), map[int]bool{}), "\x03")
	check("multi-select", err)
	_, err = answerDialog(t, newCompactListMultilineModel("Choose", newList()), "\x03")
	check("multi-line list", err)
}

// 'q' leaves a dialog without an answer, which is not an interrupt.
func TestDialogQuitIsNotInterrupt(t *testing.T) {
	confirm, err := answerDialog(t, newConfirmDialog(context.Background(), "", "", "Sure?", true), "q")
	if err != nil || !confirm.canceled {
		t.Errorf("confirm dialog: error = %v, canceled = %v, want no error and canceled", err, confirm.canceled)
	}

	chosen, err := answerDialog(t, newCompactListModel("Choose", newList()), "q")
	if err != nil || chosen.selected != nil {
		t.Errorf("list: error = %v, selected = %v, want no error and nothing selected", err, chosen.selected)
	}
}

// A 'y' key press is returned as the answer yes.
func TestConfirmDialogAnswer(t *testing.T) {
	confirm, err := answerDialog(t, newConfirmDialog(context.Background(), "", "", "Sure?", false), "y")
	if err != nil || !confirm.choice {
		t.Errorf("error = %v, choice = %v, want yes", err, confirm.choice)
	}
}
