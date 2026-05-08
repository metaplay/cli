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
		Short: "List the Metaplay skills bundled with this CLI",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			List every skill embedded in the CLI binary. Each skill's name and
			description come from its SKILL.md frontmatter.

			By default sub-pages are shown after their parent skill (DFS order).
			Pass --full=false to list only the top-level skills.
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
		fmt.Printf("%s\n", styles.RenderSuccess(s.ID))
		fmt.Printf("  %s\n", strings.ReplaceAll(s.Frontmatter.Description(), "\n", " "))
		if !o.flagFull {
			continue
		}
		pages := s.SubPageNames()
		if len(pages) == 0 {
			continue
		}
		fmt.Printf("  %s\n", styles.RenderMuted("sub-pages:"))
		for _, p := range pages {
			fmt.Printf("    %s\n", p)
		}
	}
	return nil
}
