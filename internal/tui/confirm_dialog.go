/*
 * Copyright Metaplay. All rights reserved.
 */
package tui

import (
	"context"
	"fmt"

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

	return content
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
