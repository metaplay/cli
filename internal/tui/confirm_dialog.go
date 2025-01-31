/*
 * Copyright Metaplay. All rights reserved.
 */
package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/metaplay/cli/pkg/styles"
)

// Model for the confirmation dialog
type confirmDialog struct {
	ctx      context.Context
	title    string
	body     string
	question string
	choice   bool
	quitting bool
}

func newConfirmDialog(ctx context.Context, title string, body string, question string) confirmDialog {
	return confirmDialog{
		ctx:      ctx,
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
	case tea.KeyMsg:
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

func (m confirmDialog) View() string {
	// Render content
	content := "\n" + styles.RenderTitle(m.title) + "\n\n"
	content += m.body + "\n\n"

	// Show question until answered
	if !m.quitting {
		content += m.question + styles.RenderPrompt(" [Y/n]") + "\n"
	}

	return content
}
