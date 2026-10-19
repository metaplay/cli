/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// textColumn is one column of a text table: its heading, and how its cells are
// styled; a nil style leaves them plain.
type textColumn struct {
	heading string
	style   func(string) string
}

// renderTextTable lays out rows as aligned, indented columns under a heading
// line. Widths are measured on screen from the plain text, so non-ASCII names
// line up, and cells are styled after padding, since styling takes no room.
func renderTextTable(columns []textColumn, rows [][]string) []string {
	widths := make([]int, len(columns))
	for column, col := range columns {
		widths[column] = lipgloss.Width(col.heading)
		for _, row := range rows {
			widths[column] = max(widths[column], lipgloss.Width(row[column]))
		}
	}

	line := func(cells []string, styled bool) string {
		parts := make([]string, len(cells))
		for column, cell := range cells {
			if column < len(cells)-1 {
				cell += strings.Repeat(" ", widths[column]-lipgloss.Width(cell))
			}
			if style := columns[column].style; styled && style != nil {
				cell = style(cell)
			}
			parts[column] = cell
		}
		return strings.TrimRight("  "+strings.Join(parts, "  "), " ")
	}

	headings := make([]string, len(columns))
	for column, col := range columns {
		headings[column] = col.heading
	}
	lines := []string{line(headings, false)}
	for _, row := range rows {
		lines = append(lines, line(row, true))
	}
	return lines
}
