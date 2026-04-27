/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"

	clierrors "github.com/metaplay/cli/internal/errors"
	skillspkg "github.com/metaplay/cli/internal/skills"
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
		Short: "List the Metaplay skills bundled with this CLI",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			List every skill embedded in the CLI binary. Each skill's name and
			description come from its SKILL.md frontmatter. Use --full to also
			print the sub-pages addressable via 'metaplay skills get <skill>/<page>'.
		`),
	}

	flags := cmd.Flags()
	flags.BoolVar(&o.flagFull, "full", false, "Also list each skill's sub-pages")

	skillsCmd.AddCommand(cmd)
}

func (o *skillsListOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *skillsListOpts) Run(cmd *cobra.Command) error {
	skills, err := skillspkg.LoadAll(skillspkg.OpenFS())
	if err != nil {
		return clierrors.Wrap(err, "Failed to load embedded skills")
	}

	if len(skills) == 0 {
		log.Info().Msg("No skills are bundled with this CLI.")
		return nil
	}

	for i, s := range skills {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("%s\n", styles.RenderSuccess(s.ID))
		fmt.Printf("  %s\n", truncateForList(s.Frontmatter.Description(), 200))
		if o.flagFull {
			pages := s.SubPageNames()
			if len(pages) == 0 {
				fmt.Printf("  %s\n", styles.RenderMuted("(no sub-pages)"))
			} else {
				fmt.Printf("  %s\n", styles.RenderMuted("sub-pages:"))
				for _, p := range pages {
					fmt.Printf("    %s\n", p)
				}
			}
		}
	}
	return nil
}
