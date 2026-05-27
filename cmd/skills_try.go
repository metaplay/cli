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

type skillsTryOpts struct{}

func init() {
	o := skillsTryOpts{}

	cmd := &cobra.Command{
		Use:   "try",
		Short: "[preview] Try the Metaplay skills in your AI agent without installing them",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			PREVIEW: This command is in preview and subject to change!

			Try the Metaplay skills in your AI agent without installing them.

			To have your AI agent use the Metaplay skills on demand, include
			this blurb in your prompt:

			  Run 'metaplay skills try' before starting work.

			The agent then runs the command and follows the per-skill
			catalogue and load instructions printed to stdout.

			To wire the skills in permanently, run 'metaplay skills install'
			— the agent then picks them up without needing the blurb in
			every prompt.
		`),
	}

	skillsCmd.AddCommand(cmd)
}

func (o *skillsTryOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *skillsTryOpts) Run(cmd *cobra.Command) error {
	skills, err := skillspkg.LoadAll(skillspkg.EmbeddedFS())
	if err != nil {
		return clierrors.Wrap(err, "Failed to load embedded skills")
	}

	if len(skills) == 0 {
		log.Info().Msg("No skills are bundled with this CLI.")
		return nil
	}

	fmt.Println("For any task involving the Metaplay SDK, game server, or LiveOps, load the matching skill below before starting work.")
	fmt.Println()
	fmt.Println("Load a skill's main page first:")
	fmt.Println()
	fmt.Println("  metaplay skills get <skill>")
	fmt.Println()
	fmt.Println("If the main page references sub-skills relevant to the task, load those too:")
	fmt.Println()
	fmt.Println("  metaplay skills get <skill>/<sub-skill>")
	fmt.Println()
	fmt.Println("Available skills:")

	for _, s := range skills {
		fmt.Println()
		desc := strings.ReplaceAll(s.Frontmatter.Description(), "\n", " ")
		fmt.Printf("%s: %s\n", styles.RenderSuccess(s.ID), desc)
	}

	fmt.Println()
	fmt.Println("Match the current task to a skill description above, load the main page, then load any sub-skills it references that apply before doing the work.")
	return nil
}
