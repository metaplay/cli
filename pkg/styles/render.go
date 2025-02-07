/*
 * Copyright Metaplay. All rights reserved.
 */
package styles

func RenderTitle(str string) string     { return StyleTitle.Render(str) }
func RenderError(str string) string     { return StyleError.Render(str) }
func RenderWarning(str string) string   { return StyleWarning.Render(str) }
func RenderTechnical(str string) string { return StyleTitle.Render(str) }
func RenderAttention(str string) string { return StyleWarning.Render(str) }
func RenderSuccess(str string) string   { return StyleSuccess.Render(str) }
func RenderMuted(str string) string     { return StyleMuted.Render(str) }
func RenderPrompt(str string) string    { return StylePrompt.Render(str) }
