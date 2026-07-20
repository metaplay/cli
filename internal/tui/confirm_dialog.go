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
	title    string
	body     string
	question string
	choice   bool
	quitting bool
}

func newConfirmDialog(_ context.Context, title string, body string, question string) confirmDialog {
	return confirmDialog{
		title:    title,
		body:     body,
		question: question,
	}
}

func (m confirmDialog) Init() tea.Cmd {
	return nil
}

func (m confirmDialog) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "y", "Y", "enter":
			m.choice = true
			m.quitting = true
			return m, tea.Quit
		case "n", "N", "q", "ctrl+c":
			m.choice = false
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
		content += m.question + styles.RenderPrompt(" [Y/n]") + "\n"
	}

	return tea.NewView(content)
}

// Show the user a confirm dialog and wait for a yes/no answer.
func DoConfirmDialog(ctx context.Context, title string, body string, question string) (bool, error) {
	p := tea.NewProgram(newConfirmDialog(ctx, title, body, question))
	m, err := p.Run()
	if err != nil {
		return false, fmt.Errorf("failed to run confirmation dialog: %v", err)
	}

	return m.(confirmDialog).choice, nil
}

// Show the user a one-line confirm question and wait for a yes/no answer.
func DoConfirmQuestion(ctx context.Context, question string) (bool, error) {
	return DoConfirmDialog(ctx, "", "", question)
}
