/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/metaplay/cli/pkg/styles"
)

// Model for the confirmation dialog
type confirmDialog struct {
	title      string
	body       string
	question   string
	defaultYes bool
	choice     bool
	canceled   bool
	quitting   bool
}

func newConfirmDialog(_ context.Context, title string, body string, question string, defaultYes bool) confirmDialog {
	return confirmDialog{
		title:      title,
		body:       body,
		question:   question,
		defaultYes: defaultYes,
	}
}

func (m confirmDialog) Init() tea.Cmd {
	return nil
}

func (m confirmDialog) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "y", "Y":
			m.choice = true
			m.quitting = true
			return m, tea.Quit
		case "n", "N":
			m.choice = false
			m.quitting = true
			return m, tea.Quit
		case "q", "ctrl+c":
			m.canceled = true
			m.quitting = true
			return m, tea.Quit
		case "enter":
			m.choice = m.defaultYes
			m.quitting = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m confirmDialog) View() tea.View {
	// Render content
	content := ""
	if m.title != "" {
		content += "\n" + styles.RenderTitle(m.title) + "\n"
	}
	if m.body != "" {
		content += "\n" + m.body + "\n\n"
	}

	// Show question until answered
	if !m.quitting {
		choices := " [y/N]"
		if m.defaultYes {
			choices = " [Y/n]"
		}
		content += m.question + styles.RenderPrompt(choices) + "\n"
	}

	return tea.NewView(content)
}

// Show the user a confirm dialog and wait for a yes/no answer. Defaults to 'yes'.
func DoConfirmDialog(ctx context.Context, title string, body string, question string) (bool, error) {
	return runConfirmDialog(ctx, title, body, question, true)
}

// Show the user a one-line confirm question and wait for a yes/no answer. Defaults to 'yes'.
func DoConfirmQuestion(ctx context.Context, question string) (bool, error) {
	return runConfirmDialog(ctx, "", "", question, true)
}

// Show the user a one-line confirm question and wait for a yes/no answer. Defaults to 'no'.
func DoConfirmQuestionDefaultNo(ctx context.Context, question string) (bool, error) {
	return runConfirmDialog(ctx, "", "", question, false)
}

func runConfirmDialog(ctx context.Context, title string, body string, question string, defaultYes bool) (bool, error) {
	if err := requireInteractiveMode("confirmation dialog"); err != nil {
		return false, err
	}

	p := tea.NewProgram(newConfirmDialog(ctx, title, body, question, defaultYes))
	m, err := p.Run()
	if err != nil {
		return false, fmt.Errorf("failed to run confirmation dialog: %w", err)
	}

	finalM := m.(confirmDialog)
	if finalM.canceled {
		return false, fmt.Errorf("confirmation canceled")
	}

	return finalM.choice, nil
}
