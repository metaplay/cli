/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"errors"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	skillspkg "github.com/metaplay/cli/internal/skills"
	"github.com/spf13/cobra"
)

type skillsGetOpts struct {
	UsePositionalArgs

	argName string
}

func init() {
	o := skillsGetOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argName, "NAME", "Skill or sub-page address (e.g., 'metaplay-develop' or 'metaplay-develop/review-models').")

	cmd := &cobra.Command{
		Use:   "get NAME",
		Short: "Print an embedded Metaplay skill or sub-page to stdout",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Print the embedded skill content addressed by NAME.

			{Arguments}

			NAME is either a skill name (returns its SKILL.md) or
			'<skill>/<page>' (returns a sub-page).

			Output goes to stdout with a single trailing newline so it can
			be piped or captured by an AI agent.
		`),
		Example: renderExample(`
			# Print the metaplay-develop SKILL.md.
			metaplay skills get metaplay-develop

			# Print the review-models sub-page.
			metaplay skills get metaplay-develop/review-models
		`),
	}

	skillsCmd.AddCommand(cmd)
}

func (o *skillsGetOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *skillsGetOpts) Run(cmd *cobra.Command) error {
	skills, err := skillspkg.LoadAll(skillspkg.OpenFS())
	if err != nil {
		return clierrors.Wrap(err, "Failed to load embedded skills")
	}

	content, err := skillspkg.Resolve(skills, o.argName)
	if err != nil {
		switch {
		case errors.Is(err, skillspkg.ErrSkillNotFound):
			ids := make([]string, 0, len(skills))
			for _, s := range skills {
				ids = append(ids, s.ID)
			}
			return clierrors.Wrap(err, "Unknown skill").
				WithDetails("Available skills: " + strings.Join(ids, ", ")).
				WithSuggestion("Run 'metaplay skills list' to see all skills")
		case errors.Is(err, skillspkg.ErrSubPageNotFound):
			return clierrors.Wrap(err, "Unknown sub-page").
				WithSuggestion("Run 'metaplay skills list --full' to see available sub-pages")
		default:
			return clierrors.Wrap(err, "Failed to resolve skill address")
		}
	}

	printSkillContent(content)
	return nil
}
