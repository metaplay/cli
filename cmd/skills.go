/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// skillsCmd is the parent for `metaplay skills ...` subcommands. The actual
// behaviour lives in skills_list.go, skills_get.go, skills_install.go.
var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Inspect and install Metaplay agent skills",
	Long: renderLong(nil, `
		Manage the Metaplay agent skills bundled with this CLI.

		Skills are markdown documents (with YAML frontmatter) that AI coding
		agents like Claude Code load to learn how to work with the Metaplay
		SDK. Each skill ships embedded in the CLI binary; only thin wrapper
		files are written into your project (or user home) when you run
		'metaplay skills install'.

		Sub-pages of a skill are not written to disk — they stay in the CLI
		and are fetched on demand via 'metaplay skills get <skill>/<page>'.
	`),
	Example: renderExample(`
		# List the skills shipped with this CLI.
		metaplay skills list

		# Print a skill's main markdown.
		metaplay skills get metaplay-develop

		# Print a skill sub-page.
		metaplay skills get metaplay-develop/review-models

		# Install the wrappers into the current project for Claude Code.
		metaplay skills install
	`),
}

func init() {
	skillsCmd.GroupID = "project"
	rootCmd.AddCommand(skillsCmd)
}

// printSkillContent writes raw skill content directly to stdout, bypassing
// the logger so --verbose does not prefix it with timestamps and so
// pipelines see the exact bytes. Ensures a single trailing newline.
func printSkillContent(content []byte) {
	fmt.Print(string(content))
	if len(content) == 0 || content[len(content)-1] != '\n' {
		fmt.Println()
	}
}
