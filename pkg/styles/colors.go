/*
 * Copyright Metaplay. All rights reserved.
 */
package styles

import "github.com/charmbracelet/lipgloss"

var (
	ColorNeutral = lipgloss.Color("#737373")
	ColorOrange  = lipgloss.Color("#ff7a00")
	ColorGreen   = lipgloss.Color("#28a745") // Metaplay green: lipgloss.Color("#3f6730")
	ColorBlue    = lipgloss.Color("#2d90dc")
	ColorRed     = lipgloss.Color("#ef4444")
	ColorYellow  = lipgloss.Color("#ffff55")

	StyleTitle     = lipgloss.NewStyle().Foreground(ColorBlue).Bold(true)
	StyleSuccess   = lipgloss.NewStyle().Foreground(ColorGreen)
	StyleError     = lipgloss.NewStyle().Foreground(ColorRed)
	StyleWarning   = lipgloss.NewStyle().Foreground(ColorYellow)
	StyleTechnical = lipgloss.NewStyle().Foreground(ColorBlue)
	StyleMuted     = lipgloss.NewStyle().Foreground(ColorNeutral)
	StylePrompt    = lipgloss.NewStyle().Foreground(ColorOrange).Bold(true)

	ListStyle = lipgloss.NewStyle().
		// Border(lipgloss.RoundedBorder()).
		// BorderForeground(lipgloss.Color("39")).
		// Padding(1).
		Width(70)
)
