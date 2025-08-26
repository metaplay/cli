/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package styles

import (
	"strings"
)

func RenderBright(str string) string    { return StyleBright.Render(str) }
func RenderTitle(str string) string     { return StyleTitle.Render(str) }
func RenderError(str string) string     { return StyleError.Render(str) }
func RenderWarning(str string) string   { return StyleWarning.Render(str) }
func RenderTechnical(str string) string { return StyleTitle.Render(str) }
func RenderAttention(str string) string { return StyleWarning.Render(str) }
func RenderSuccess(str string) string   { return StyleSuccess.Render(str) }
func RenderMuted(str string) string     { return StyleMuted.Render(str) }
func RenderPrompt(str string) string    { return StylePrompt.Render(str) }

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
	return StyleComment.Render(text)
}
