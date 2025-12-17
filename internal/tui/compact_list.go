/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package tui

import (
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
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
		m.model.SetWidth(msg.Width)
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

// min returns the smaller of x or y
func min(x, y int) int {
	if x < y {
		return x
	}
	return y
}

func chooseFromList(title string, items []list.Item) (int, error) {
	// Initialize list with custom delegate
	list := list.New(items, compactListDelegate{}, 0, min(2+len(items), 20))
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
		return -1, fmt.Errorf("selection canceled")
	}
	return selectedItem.index, nil
}

// Show a dialog to user to select an item from the provided list.
// The toItemFunc() is used to convert the items into a (name, description)
// tuple for display. The selected item in the list is returned (or error).
func ChooseFromListDialog[TItem any](title string, items []TItem, toItemFunc func(item *TItem) (string, string)) (*TItem, error) {
	// \todo Bit of a hack to render title first
	if len(items) == 0 {
		log.Info().Msg("")
		log.Info().Msg(styles.RenderTitle(title))
		log.Info().Msg("")
		return nil, fmt.Errorf("ChooseFromListDialog(): an empty list was provided")
	}

	// Convert items to list items.
	listItems := make([]list.Item, len(items))
	for ndx := range items {
		item := &items[ndx]
		name, description := toItemFunc(item)
		listItems[ndx] = compactListItem{
			index:       ndx,
			name:        name,
			description: description,
		}
	}

	// Let the user choose list items.
	chosen, err := chooseFromList(title, listItems)
	if err != nil {
		return nil, err
	}

	return &items[chosen], nil
}
