/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package tui

import (
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/metaplay/cli/internal/interrupt"
)

// dialogModel is the model of a dialog that asks the user for an answer.
type dialogModel interface {
	tea.Model
	leftWithCtrlC() bool // The user left the dialog with Ctrl-C
}

// ctrlCExit is embedded in a dialog's model, which sets ctrlC when the user leaves with Ctrl-C.
type ctrlCExit struct{ ctrlC bool }

func (c ctrlCExit) leftWithCtrlC() bool { return c.ctrlC }

// runDialog shows a dialog and returns its model once the user has answered. what names the dialog
// in errors.
//
// The terminal sends no SIGINT for Ctrl-C while the dialog holds it in raw mode: the dialog gets a
// key press instead. runDialog returns interrupt.ErrInterrupted for it, and for SIGINT, so the
// command ends as it does when interrupted elsewhere: with "Canceled." and exit code 130.
func runDialog[M dialogModel](model M, what string, options ...tea.ProgramOption) (M, error) {
	final, err := tea.NewProgram(model, options...).Run()
	if errors.Is(err, tea.ErrInterrupted) {
		return model, interrupt.ErrInterrupted
	}
	if err != nil {
		return model, fmt.Errorf("failed to run %s: %w", what, err)
	}
	finalModel := final.(M)
	if finalModel.leftWithCtrlC() {
		return finalModel, interrupt.ErrInterrupted
	}
	return finalModel, nil
}
