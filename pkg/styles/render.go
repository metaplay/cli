/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package styles

import (
	"strings"

	"charm.land/lipgloss/v2"
)

func render(style lipgloss.Style, str string) string {
	// lipgloss v2 styles always emit escape sequences, even bold with no color.
	if !colorEnabled {
		return str
	}
	return style.Render(str)
}

func RenderBright(str string) string    { return render(StyleBright, str) }
func RenderTitle(str string) string     { return render(StyleTitle, str) }
func RenderError(str string) string     { return render(StyleError, str) }
func RenderWarning(str string) string   { return render(StyleWarning, str) }
func RenderTechnical(str string) string { return render(StyleTitle, str) }
func RenderAttention(str string) string { return render(StyleWarning, str) }
func RenderSuccess(str string) string   { return render(StyleSuccess, str) }
func RenderMuted(str string) string     { return render(StyleMuted, str) }
func RenderPrompt(str string) string    { return render(StylePrompt, str) }

func RenderListTechnical(list []string) string {
	// Build comma-separated list of keys with technical styling
	elements := make([]string, 0, len(list))
	for _, str := range list {
		elements = append(elements, RenderTechnical(str))
	}
	return strings.Join(elements, ", ")
}

// RenderComment renders text in a comment style (darker green).
func RenderComment(text string) string {
	return render(StyleComment, text)
}
