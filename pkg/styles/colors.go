/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package styles

import (
	"image/color"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/jwalton/go-supportscolor"
	"github.com/mattn/go-isatty"
)

var (
	// Define colors based on platform
	ColorNeutral      color.Color
	ColorBright       color.Color
	ColorOrange       color.Color
	ColorGreen        color.Color
	ColorCommentGreen color.Color
	ColorBlue         color.Color
	ColorRed          color.Color
	ColorYellow       color.Color

	// Styles using the colors
	StyleBright    lipgloss.Style
	StyleTitle     lipgloss.Style
	StyleSuccess   lipgloss.Style
	StyleError     lipgloss.Style
	StyleWarning   lipgloss.Style
	StyleTechnical lipgloss.Style
	StyleMuted     lipgloss.Style
	StylePrompt    lipgloss.Style
	StyleComment   lipgloss.Style

	ListStyle = lipgloss.NewStyle()

	colorEnabled bool
)

// DetectColorSupport returns whether stdout is a terminal that should show colors.
func DetectColorSupport() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	fd := os.Stdout.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

type colorLevel int

const (
	colorLevel16 colorLevel = iota
	colorLevel256
	colorLevel16m
)

// detectColorLevel returns the palette depth for stdout.
func detectColorLevel() colorLevel {
	support := supportscolor.SupportsColor(os.Stdout.Fd())
	switch {
	case support.Has16m:
		return colorLevel16m
	case support.Has256:
		return colorLevel256
	case support.SupportsColor:
		return colorLevel16
	}

	// supportscolor reports no color for a pipe. Pick a palette anyway, so that
	// --color yes still produces colors when piped.
	if colorTerm := os.Getenv("COLORTERM"); colorTerm == "truecolor" || colorTerm == "24bit" {
		return colorLevel16m
	}
	return colorLevel256
}

// SetColorEnabled controls whether the Render*() helpers emit escape sequences.
func SetColorEnabled(enabled bool) {
	colorEnabled = enabled
}

// ColorEnabled returns whether the Render*() helpers emit escape sequences.
func ColorEnabled() bool {
	return colorEnabled
}

func init() {
	colorEnabled = DetectColorSupport()

	// Use appropriate colors based on terminal capabilities
	switch detectColorLevel() {
	case colorLevel16m:
		// Terminal supports true color (24-bit)
		ColorNeutral = lipgloss.Color("#737373")
		ColorBright = lipgloss.Color("#e0e0e0") // Light gray
		ColorOrange = lipgloss.Color("#ff7a00")
		ColorGreen = lipgloss.Color("#28a745")        // Metaplay green: lipgloss.Color("#3f6730")
		ColorCommentGreen = lipgloss.Color("#6A9955") // VSCode comment green
		ColorBlue = lipgloss.Color("#2d90dc")
		ColorRed = lipgloss.Color("#ef4444")
		ColorYellow = lipgloss.Color("#ffff55")
	case colorLevel256:
		// Terminal supports 256 colors (8-bit)
		ColorNeutral = lipgloss.Color("240")     // Gray
		ColorBright = lipgloss.Color("252")      // Light gray
		ColorOrange = lipgloss.Color("208")      // Orange
		ColorGreen = lipgloss.Color("34")        // Green
		ColorCommentGreen = lipgloss.Color("71") // Closest 256-color match to VSCode comment green
		ColorBlue = lipgloss.Color("33")         // Blue
		ColorRed = lipgloss.Color("196")         // Red
		ColorYellow = lipgloss.Color("226")      // Yellow
	default:
		// Terminal only supports basic 16 colors. lipgloss v2 parses color names like
		// "yellow" as no color, so use ANSI numbers.
		ColorNeutral = lipgloss.Color("8")      // Dark gray
		ColorBright = lipgloss.Color("15")      // Keep as white for basic terminals
		ColorOrange = lipgloss.Color("3")       // Basic terminals don't have orange
		ColorGreen = lipgloss.Color("2")        // Green
		ColorCommentGreen = lipgloss.Color("2") // Same as green for basic terminals
		ColorBlue = lipgloss.Color("4")         // Blue
		ColorRed = lipgloss.Color("1")          // Red
		ColorYellow = lipgloss.Color("3")       // Yellow
	}

	// Initialize styles with the appropriate colors
	// Explicitly set background to "default" to ensure proper rendering in macOS Terminal
	StyleTitle = lipgloss.NewStyle().Foreground(ColorBlue).Bold(true)
	StyleBright = lipgloss.NewStyle().Foreground(ColorBright).Bold(true)
	StyleSuccess = lipgloss.NewStyle().Foreground(ColorGreen)
	StyleComment = lipgloss.NewStyle().Foreground(ColorCommentGreen)
	StyleError = lipgloss.NewStyle().Foreground(ColorRed)
	StyleWarning = lipgloss.NewStyle().Foreground(ColorYellow)
	StyleTechnical = lipgloss.NewStyle().Foreground(ColorBlue)
	StyleMuted = lipgloss.NewStyle().Foreground(ColorNeutral)
	StylePrompt = lipgloss.NewStyle().Foreground(ColorOrange).Bold(true)
}
