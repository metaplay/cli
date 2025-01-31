/*
 * Copyright Metaplay. All rights reserved.
 */
package tui

import (
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/metaplay/cli/pkg/styles"
)

// Item in our compact list.
type compactListItem struct {
	index       int
	name        string
	description string
}

func (item compactListItem) Title() string {
	return fmt.Sprintf("%s %s", item.name, styles.RenderMuted(item.description))
}

func (item compactListItem) FilterValue() string { return item.description }

// compactListDelegate implements a compact list delegate for the project selection.
type compactListDelegate struct{}

func (d compactListDelegate) Height() int                               { return 1 }
func (d compactListDelegate) Spacing() int                              { return 0 }
func (d compactListDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d compactListDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	item, ok := listItem.(compactListItem)
	if !ok {
		return
	}

	title := item.Title()

	// Render differently if selected
	if index == m.Index() {
		// Add selector and style with blue color
		styledTitle := "▸ " + title
		fmt.Fprint(w, lipgloss.NewStyle().Foreground(styles.ColorOrange).Render(styledTitle))
	} else {
		fmt.Fprint(w, "  "+title)
	}
}

// Model for the compact selection list.
type compactListModel struct {
	title    string
	model    list.Model
	selected *compactListItem
	quitting bool
	err      error
}

func newCompactListModel(title string, model list.Model) compactListModel {
	return compactListModel{
		title: title,
		model: model,
	}
}

func (m compactListModel) Init() tea.Cmd {
	return nil
}

func (m compactListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if item, ok := m.model.SelectedItem().(compactListItem); ok {
				m.selected = &item
				m.quitting = true
				return m, tea.Quit
			}
		}
	}

	var cmd tea.Cmd
	m.model, cmd = m.model.Update(msg)
	return m, cmd
}

func (m compactListModel) View() string {
	content := "\n" + styles.RenderTitle(m.title) + "\n\n"

	if !m.quitting {
		content += styles.ListStyle.Render(m.model.View())
	}

	return content
}

func chooseFromList(title string, items []list.Item) (int, error) {
	// Initialize list with custom delegate
	list := list.New(items, compactListDelegate{}, 80, 20) // Set default width and height that will be updated by WindowSizeMsg
	list.SetShowTitle(false)
	list.SetFilteringEnabled(false)
	list.SetShowStatusBar(false)
	list.SetShowHelp(false)

	// Create and run model
	model := newCompactListModel(title, list)
	program := tea.NewProgram(model)
	finalModel, err := program.Run()
	if err != nil {
		return -1, fmt.Errorf("failed to run project selection: %w", err)
	}

	// Check if a project was selected
	finalM := finalModel.(compactListModel)
	if finalM.err != nil {
		return -1, finalM.err
	}
	selectedItem := finalM.selected
	if selectedItem == nil {
		return -1, fmt.Errorf("user did not select any item")
	}
	return selectedItem.index, nil
}
