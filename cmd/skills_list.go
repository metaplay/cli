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

			By default only top-level skills are listed. Pass --full to also
			list each skill's sub-skills after their parent in DFS order, with
			their full '<skill>/<sub-skill>' address (the same form
			'metaplay skills get' accepts) and the description from the
			sub-skill's own frontmatter.
		`),
	}

	flags := cmd.Flags()
	flags.BoolVar(&o.flagFull, "full", false, "Also list each skill's sub-skills")

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
		for _, p := range s.SubSkillNames() {
			if p == "main" {
				continue
			}
			subDesc := strings.ReplaceAll(s.SubSkills[p].Frontmatter.Description(), "\n", " ")
			if subDesc == "" {
				subDesc = styles.RenderMuted("(no description)")
			}
			fmt.Printf("  %s: %s\n", styles.RenderSuccess(s.ID+"/"+p), subDesc)
		}
	}
	return nil
}
