/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	skillspkg "github.com/metaplay/cli/pkg/skills"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type skillsListOpts struct {
	flagFull bool
}

func init() {
	o := skillsListOpts{}

	cmd := &cobra.Command{
		Use:   "list [flags]",
		Short: "[preview] List the Metaplay skills bundled with this CLI",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			PREVIEW: This command is in preview and subject to change!

			List every skill embedded in the CLI binary. Each skill's name and
			description come from its SKILL.md frontmatter.

			By default sub-pages are shown after their parent skill in DFS
			order, with their full '<skill>/<page>' address (the same form
			'metaplay skills get' accepts) and the lead paragraph of the
			sub-page as its description. Pass --full=false to list only the
			top-level skills.
		`),
	}

	flags := cmd.Flags()
	flags.BoolVar(&o.flagFull, "full", true, "Also list each skill's sub-pages")

	skillsCmd.AddCommand(cmd)
}

func (o *skillsListOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *skillsListOpts) Run(cmd *cobra.Command) error {
	skills, err := skillspkg.LoadAll(skillspkg.EmbeddedFS())
	if err != nil {
		return clierrors.Wrap(err, "Failed to load embedded skills")
	}

	if len(skills) == 0 {
		log.Info().Msg("No skills are bundled with this CLI.")
		return nil
	}

	for _, s := range skills {
		fmt.Println()
		desc := strings.ReplaceAll(s.Frontmatter.Description(), "\n", " ")
		fmt.Printf("%s: %s\n", styles.RenderSuccess(s.ID), desc)
		if !o.flagFull {
			continue
		}
		for _, p := range s.SubPageNames() {
			subDesc := subPageDescription(s.SubPages[p])
			if subDesc == "" {
				subDesc = styles.RenderMuted("(no description)")
			}
			fmt.Printf("  %s: %s\n", styles.RenderSuccess(s.ID+"/"+p), subDesc)
		}
	}
	return nil
}

// subPageDescription extracts the lead paragraph from a markdown sub-page,
// skipping leading blank lines and a single H1 title. Returns "" if the page
// has no body paragraph before the next blank line. Multi-line paragraphs
// are flattened to one space-separated line for tabular display.
func subPageDescription(content []byte) string {
	lines := strings.Split(string(content), "\n")
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "# ") {
		i++
	}
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	var paragraph []string
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			break
		}
		paragraph = append(paragraph, line)
		i++
	}
	return strings.Join(paragraph, " ")
}
