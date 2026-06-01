/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"os"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/metaplay/cli/internal/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// skillsCmd is the parent for `metaplay skills ...` subcommands. The actual
// behaviour lives in skills_list.go, skills_get.go, skills_install.go.
var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "[preview] Inspect and install Metaplay agent skills",
	Long: renderLong(nil, `
		PREVIEW: The 'skills' command family is in preview and subject to change!

		Manage the Metaplay agent skills bundled with this CLI.

		Skills are markdown documents (with YAML frontmatter) that AI coding
		agents like Claude Code load to learn how to work with the Metaplay
		SDK. Each skill ships embedded in the CLI binary; only thin wrapper
		files are written into your project (or user home) when you run
		'metaplay skills install'.

		Sub-skills of a skill are not written to disk — they stay in the CLI
		and are fetched on demand via 'metaplay skills get <skill>-<sub-skill>'.
	`),
	Example: renderExample(`
		# List the skills shipped with this CLI.
		metaplay skills list

		# Print agent-facing guidance for trying skills without installing.
		metaplay skills try

		# Print a skill's main markdown.
		metaplay skills get metaplay-develop

		# Print a sub-skill.
		metaplay skills get metaplay-develop-code-review

		# Install the wrappers into the current project for Claude Code.
		metaplay skills install
	`),
}

func init() {
	skillsCmd.GroupID = "project"
	rootCmd.AddCommand(skillsCmd)
}

// printSkillContent writes skill content to stdout. In interactive terminals
// it pretty-prints the markdown via glamour for readability; everywhere else
// (pipes, redirects, CI, --verbose) it writes the exact bytes so AI agents
// and shell pipelines see the raw markdown. Ensures a single trailing newline
// in the raw path; glamour produces its own trailing whitespace.
func printSkillContent(content []byte) {
	if tui.IsInteractiveMode() {
		if rendered, ok := renderMarkdownForTerminal(content); ok {
			fmt.Print(rendered)
			return
		}
	}
	fmt.Print(string(content))
	if len(content) == 0 || content[len(content)-1] != '\n' {
		fmt.Println()
	}
}

// renderMarkdownForTerminal renders markdown to ANSI using glamour, sized to
// the current stdout terminal width. Returns the rendered string and true on
// success; on any error the caller should fall back to raw output so the
// command never breaks.
func renderMarkdownForTerminal(content []byte) (string, bool) {
	width := 100
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 && w < width {
		width = w
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(skillsStyleConfig()),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", false
	}
	out, err := r.Render(string(content))
	if err != nil {
		return "", false
	}
	// glamour v2 is "pure" and always emits truecolor; downsample to the
	// terminal's actual color profile via lipgloss before printing.
	return lipgloss.Sprint(out), true
}

// skillsStyleConfig picks glamour's dark or light style based on the detected
// terminal background, then blanks the inline-code Prefix/Suffix so spans like
// `file:line` don't render with visible padding around them. The returned value
// is a copy; the package-global style is not mutated.
func skillsStyleConfig() ansi.StyleConfig {
	var s ansi.StyleConfig
	if lipgloss.HasDarkBackground(os.Stdin, os.Stdout) {
		s = styles.DarkStyleConfig
	} else {
		s = styles.LightStyleConfig
	}
	s.Code.Prefix = ""
	s.Code.Suffix = ""
	return s
}
