/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package tui

import (
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
)

// Cached style for selected list item
var selectedStyle = lipgloss.NewStyle().Foreground(styles.ColorOrange)

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
		styledTitle := "▸ " + title
		fmt.Fprint(w, selectedStyle.Render(styledTitle))
	} else {
		fmt.Fprint(w, "  "+title)
	}
}

// Model for the compact selection list.
type compactListModel struct {
	title    string
	subtitle string
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
	case tea.KeyPressMsg:
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

func (m compactListModel) View() tea.View {
	content := "\n" + styles.RenderTitle(m.title) + "\n\n"

	if !m.quitting {
		if m.subtitle != "" {
			content += "  " + styles.RenderMuted(m.subtitle) + "\n"
		}
		content += styles.ListStyle.Render(m.model.View())
	}

	return tea.NewView(content)
}

// multiSelectDelegate renders list items with checkbox indicators.
type multiSelectDelegate struct {
	checked map[int]bool
}

func (d multiSelectDelegate) Height() int                               { return 1 }
func (d multiSelectDelegate) Spacing() int                              { return 0 }
func (d multiSelectDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d multiSelectDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	item, ok := listItem.(compactListItem)
	if !ok {
		return
	}

	title := item.Title()
	checkbox := "[ ] "
	if d.checked[item.index] {
		checkbox = "[x] "
	}

	if index == m.Index() {
		styledTitle := "▸ " + checkbox + title
		fmt.Fprint(w, lipgloss.NewStyle().Foreground(styles.ColorOrange).Render(styledTitle))
	} else {
		fmt.Fprint(w, "  "+checkbox+title)
	}
}

// multiSelectModel for the multi-select list.
type multiSelectModel struct {
	title    string
	footer   string
	model    list.Model
	checked  map[int]bool
	done     bool
	quitting bool
	err      error
}

func newMultiSelectModel(title string, footer string, model list.Model, checked map[int]bool) multiSelectModel {
	return multiSelectModel{
		title:   title,
		footer:  footer,
		model:   model,
		checked: checked,
	}
}

func (m multiSelectModel) Init() tea.Cmd {
	return nil
}

func (m multiSelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.model.SetWidth(msg.Width)
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "space":
			// Toggle selection of the current item. (bubbletea v2 reports
			// space as "space" rather than " ".)
			if item, ok := m.model.SelectedItem().(compactListItem); ok {
				if m.checked[item.index] {
					delete(m.checked, item.index)
				} else {
					m.checked[item.index] = true
				}
			}
			return m, nil
		case "enter":
			m.done = true
			m.quitting = true
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.model, cmd = m.model.Update(msg)
	return m, cmd
}

func (m multiSelectModel) View() tea.View {
	content := "\n" + styles.RenderTitle(m.title) + "\n\n"

	if !m.quitting {
		content += styles.ListStyle.Render(m.model.View())
		if m.footer != "" {
			// The list output trails with whitespace and no final newline,
			// so trim and control spacing explicitly: one blank line above
			// the footer, footer line itself, then the help line.
			content = strings.TrimRight(content, " \t\n") + "\n\n"
			content += styles.RenderMuted("  "+m.footer) + "\n"
		}
		content += styles.RenderMuted("  space to toggle, enter to confirm")
	}

	return tea.NewView(content)
}

func chooseFromList(title string, items []list.Item) (int, error) {
	return chooseFromListWithSubtitle(title, "", items)
}

func chooseFromListWithSubtitle(title string, subtitle string, items []list.Item) (int, error) {
	// Initialize list with custom delegate
	list := list.New(items, compactListDelegate{}, 0, min(2+len(items), 20))
	list.SetShowTitle(false)
	list.SetFilteringEnabled(false)
	list.SetShowStatusBar(false)
	list.SetShowHelp(false)

	// Create and run model
	model := newCompactListModel(title, list)
	model.subtitle = subtitle
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

// ChooseFromListDialogWithHeader is like ChooseFromListDialog but displays a header row above the list items.
func ChooseFromListDialogWithHeader[TItem any](title string, header string, items []TItem, toItemFunc func(item *TItem) (string, string)) (*TItem, error) {
	if len(items) == 0 {
		log.Info().Msg("")
		log.Info().Msg(styles.RenderTitle(title))
		log.Info().Msg("")
		return nil, fmt.Errorf("ChooseFromListDialogWithHeader(): an empty list was provided")
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
	chosen, err := chooseFromListWithSubtitle(title, header, listItems)
	if err != nil {
		return nil, err
	}

	return &items[chosen], nil
}

// ChooseMultipleFromListDialog shows a dialog to select multiple items from a list using checkboxes.
// The toItemFunc() is used to convert the items into a (name, description) tuple for display.
// All items are pre-selected by default. Returns the selected items (or error if none chosen).
func ChooseMultipleFromListDialog[TItem any](title string, items []TItem, toItemFunc func(item *TItem) (string, string)) ([]TItem, error) {
	return ChooseMultipleFromListDialogWithDefaults(title, "", items, toItemFunc, func(*TItem) bool { return true })
}

// ChooseMultipleFromListDialogWithDefaults is like ChooseMultipleFromListDialog
// but lets the caller specify which items start checked via the
// defaultChecked predicate, invoked once per item before showing the dialog.
// An optional footer is rendered (muted) below the list, above the
// "space to toggle, enter to confirm" help line. Pass "" to omit it.
func ChooseMultipleFromListDialogWithDefaults[TItem any](
	title string,
	footer string,
	items []TItem,
	toItemFunc func(item *TItem) (string, string),
	defaultChecked func(item *TItem) bool,
) ([]TItem, error) {
	if len(items) == 0 {
		log.Info().Msg("")
		log.Info().Msg(styles.RenderTitle(title))
		log.Info().Msg("")
		return nil, fmt.Errorf("ChooseMultipleFromListDialogWithDefaults(): an empty list was provided")
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

	// Initialise the checked map per the predicate.
	checked := make(map[int]bool, len(items))
	for ndx := range items {
		if defaultChecked(&items[ndx]) {
			checked[ndx] = true
		}
	}
	delegate := &multiSelectDelegate{checked: checked}
	l := list.New(listItems, delegate, 0, min(2+len(listItems), 20))
	l.SetShowTitle(false)
	l.SetFilteringEnabled(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)

	// Create and run model.
	model := newMultiSelectModel(title, footer, l, checked)
	program := tea.NewProgram(model)
	finalModel, err := program.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to run multi-select dialog: %w", err)
	}

	finalM := finalModel.(multiSelectModel)
	if finalM.err != nil {
		return nil, finalM.err
	}
	if !finalM.done {
		return nil, fmt.Errorf("selection canceled")
	}

	// Collect selected items in order.
	var selected []TItem
	for ndx := range items {
		if finalM.checked[ndx] {
			selected = append(selected, items[ndx])
		}
	}

	if len(selected) == 0 {
		return nil, fmt.Errorf("no items selected")
	}

	return selected, nil
}

// compactListMultilineItem carries a name, optional muted hint on the same
// row as the name, and description lines rendered under the name. Used by
// ChooseFromListDialogMultiline.
type compactListMultilineItem struct {
	index            int
	name             string
	hint             string
	descriptionLines []string
}

func (item compactListMultilineItem) FilterValue() string { return item.name }

// compactMultilineDelegate renders an item as: selected marker + name on the
// first line, then each description line under it. The list expects
// every item to have the same height, so the caller pads descriptionLines to
// a common length.
type compactMultilineDelegate struct {
	rowsPerItem int
}

func (d compactMultilineDelegate) Height() int                               { return d.rowsPerItem }
func (d compactMultilineDelegate) Spacing() int                              { return 1 }
func (d compactMultilineDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d compactMultilineDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	item, ok := listItem.(compactListMultilineItem)
	if !ok {
		return
	}
	selected := index == m.Index()
	hint := ""
	if item.hint != "" {
		hint = " " + styles.RenderMuted(item.hint)
	}
	var lines []string
	if selected {
		lines = append(lines, selectedStyle.Render("▸ "+item.name)+hint)
	} else {
		lines = append(lines, "  "+item.name+hint)
	}
	for _, dl := range item.descriptionLines {
		if dl == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, "  "+styles.RenderMuted(dl))
	}
	fmt.Fprint(w, strings.Join(lines, "\n"))
}

// ChooseFromListDialogMultiline is like ChooseFromListDialog but renders a
// multi-line description under each selectable item. toItemFunc returns
// (name, hint, descriptionLines): hint is rendered on the same row as
// the name (e.g. a path), descriptionLines render indented underneath. All
// items are padded to the same number of rows.
func ChooseFromListDialogMultiline[TItem any](
	title string,
	items []TItem,
	toItemFunc func(item *TItem) (string, string, []string),
) (*TItem, error) {
	if len(items) == 0 {
		log.Info().Msg("")
		log.Info().Msg(styles.RenderTitle(title))
		log.Info().Msg("")
		return nil, fmt.Errorf("ChooseFromListDialogMultiline(): an empty list was provided")
	}

	// Build items; pad description rows so every slot has the same height.
	listItems := make([]list.Item, len(items))
	maxDescLines := 0
	type prepped struct {
		name string
		hint string
		desc []string
	}
	prep := make([]prepped, len(items))
	for ndx := range items {
		name, hint, desc := toItemFunc(&items[ndx])
		prep[ndx] = prepped{name: name, hint: hint, desc: desc}
		if len(desc) > maxDescLines {
			maxDescLines = len(desc)
		}
	}
	for ndx, p := range prep {
		desc := p.desc
		for len(desc) < maxDescLines {
			desc = append(desc, "")
		}
		listItems[ndx] = compactListMultilineItem{
			index:            ndx,
			name:             p.name,
			hint:             p.hint,
			descriptionLines: desc,
		}
	}

	rowsPerItem := 1 + maxDescLines
	delegate := compactMultilineDelegate{rowsPerItem: rowsPerItem}
	// Bubble list height: rowsPerItem*N + spacing*(N-1) + slack.
	listHeight := rowsPerItem*len(items) + delegate.Spacing()*(len(items)-1) + 2
	if listHeight > 30 {
		listHeight = 30
	}
	l := list.New(listItems, delegate, 0, listHeight)
	l.SetShowTitle(false)
	l.SetFilteringEnabled(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)

	model := newCompactListMultilineModel(title, l)
	program := tea.NewProgram(model)
	finalModel, err := program.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to run selection dialog: %w", err)
	}
	finalM := finalModel.(compactListMultilineModel)
	if finalM.err != nil {
		return nil, finalM.err
	}
	if finalM.selectedIndex < 0 {
		return nil, fmt.Errorf("selection canceled")
	}
	return &items[finalM.selectedIndex], nil
}

// compactListMultilineModel drives ChooseFromListDialogMultiline. Mirrors
// compactListModel but tracks the selected index by value (the multi-line
// item type doesn't reuse compactListItem).
type compactListMultilineModel struct {
	title         string
	model         list.Model
	selectedIndex int
	quitting      bool
	err           error
}

func newCompactListMultilineModel(title string, model list.Model) compactListMultilineModel {
	return compactListMultilineModel{title: title, model: model, selectedIndex: -1}
}

func (m compactListMultilineModel) Init() tea.Cmd { return nil }

func (m compactListMultilineModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.model.SetWidth(msg.Width)
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if item, ok := m.model.SelectedItem().(compactListMultilineItem); ok {
				m.selectedIndex = item.index
				m.quitting = true
				return m, tea.Quit
			}
		}
	}
	var cmd tea.Cmd
	m.model, cmd = m.model.Update(msg)
	return m, cmd
}

func (m compactListMultilineModel) View() tea.View {
	content := "\n" + styles.RenderTitle(m.title) + "\n\n"
	if !m.quitting {
		content += styles.ListStyle.Render(m.model.View())
	}
	return tea.NewView(content)
}
