/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	skillspkg "github.com/metaplay/cli/internal/skills"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type skillsRemoveOpts struct {
	UsePositionalArgs

	argSkill string

	flagScope     string
	flagAgents    []string
	flagAllAgents bool

	resolvedScope  skillspkg.Scope
	resolvedAgents []skillspkg.AgentHost
}

func init() {
	o := skillsRemoveOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argSkill, "SKILL", "Specific skill name to remove (defaults to all metaplay-cli wrappers).")

	cmd := &cobra.Command{
		Use:   "remove [SKILL] [flags]",
		Short: "Remove Metaplay skill wrappers from the project (or user home)",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Delete wrappers previously written by 'metaplay skills install'.

			{Arguments}

			Only files carrying 'managed-by: metaplay-cli' in their
			frontmatter are removed; user-authored skill files in the same
			directory are always preserved. After deleting a SKILL.md, the
			parent skill directory is removed if it is empty.

			Default scope is 'project' and default agent is 'claude-code'.
			With no SKILL argument, every metaplay-cli wrapper found under
			the chosen agent dirs is cleaned up — useful for clearing out
			orphan wrappers from skills that have been removed from the
			canonical set.
		`),
		Example: renderExample(`
			# Remove all metaplay-cli wrappers from the current project (Claude Code).
			metaplay skills remove

			# Remove just one skill.
			metaplay skills remove metaplay-develop

			# Remove from every known AI tool's project dirs.
			metaplay skills remove --all-agents

			# Remove user-scope wrappers under your home directory.
			metaplay skills remove --scope user
		`),
	}

	flags := cmd.Flags()
	flags.StringVar(&o.flagScope, "scope", "project", "'project' (under metaplay-project.yaml) or 'user' (under your home directory)")
	flags.StringSliceVar(&o.flagAgents, "agent", []string{skillspkg.DefaultAgentID}, "Target AI tool ID (repeatable)")
	flags.BoolVar(&o.flagAllAgents, "all-agents", false, "Remove for every AI tool the CLI knows about")

	skillsCmd.AddCommand(cmd)
}

func (o *skillsRemoveOpts) Prepare(cmd *cobra.Command, args []string) error {
	switch o.flagScope {
	case "project":
		o.resolvedScope = skillspkg.ScopeProject
	case "user":
		o.resolvedScope = skillspkg.ScopeUser
	default:
		return clierrors.NewUsageErrorf("Invalid --scope %q", o.flagScope).
			WithSuggestion("Use --scope project or --scope user")
	}

	if o.flagAllAgents {
		o.resolvedAgents = append([]skillspkg.AgentHost(nil), skillspkg.AgentHosts...)
	} else {
		seen := map[string]bool{}
		for _, id := range o.flagAgents {
			if seen[id] {
				continue
			}
			seen[id] = true
			a := skillspkg.LookupAgent(id)
			if a == nil {
				return clierrors.NewUsageErrorf("Unknown --agent %q", id).
					WithDetails("Known agents: " + strings.Join(skillspkg.AgentIDs(), ", "))
			}
			o.resolvedAgents = append(o.resolvedAgents, *a)
		}
	}
	if len(o.resolvedAgents) == 0 {
		return clierrors.NewUsageError("At least one --agent is required").
			WithSuggestion("Specify --agent <id>, or --all-agents")
	}
	return nil
}

func (o *skillsRemoveOpts) Run(cmd *cobra.Command) error {
	rootDir, err := o.resolveRootDir()
	if err != nil {
		return err
	}

	var skillIDs []string
	if o.argSkill != "" {
		skillIDs = []string{o.argSkill}
	}

	actions, err := skillspkg.Remove(skillspkg.RemoveOptions{
		Agents:   o.resolvedAgents,
		RootDir:  rootDir,
		Scope:    o.resolvedScope,
		SkillIDs: skillIDs,
	})
	if err != nil {
		return clierrors.Wrap(err, "Remove failed")
	}

	o.reportRemoveActions(actions)
	return nil
}

func (o *skillsRemoveOpts) resolveRootDir() (string, error) {
	switch o.resolvedScope {
	case skillspkg.ScopeProject:
		project, err := tryResolveProject()
		if err != nil {
			return "", err
		}
		if project == nil {
			return "", clierrors.New("No metaplay-project.yaml found").
				WithSuggestion("Run from a Metaplay project directory, or use --scope user")
		}
		return project.RelativeDir, nil
	case skillspkg.ScopeUser:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", clierrors.Wrap(err, "Could not determine user home directory")
		}
		return home, nil
	}
	return "", clierrors.New("Unknown scope")
}

func (o *skillsRemoveOpts) reportRemoveActions(actions []skillspkg.RemoveAction) {
	var removed, skipped, errs int
	for _, a := range actions {
		var line string
		switch a.Status {
		case skillspkg.StatusRemoved:
			removed++
			line = styles.RenderSuccess("REMOVED ") + " " + a.Path
		case skillspkg.StatusRemoveSkippedUser:
			skipped++
			line = styles.RenderAttention("KEPT    ") + " " + a.Path + "  " + styles.RenderMuted("("+a.Reason+")")
		case skillspkg.StatusRemoveSkippedNotFound:
			skipped++
			suffix := ""
			if a.Reason != "" {
				suffix = "  " + styles.RenderMuted("("+a.Reason+")")
			}
			line = styles.RenderMuted("absent  ") + " " + a.Path + suffix
		case skillspkg.StatusRemoveSkippedError:
			errs++
			line = styles.RenderError("ERROR   ") + " " + a.Path + "  " + styles.RenderMuted("("+a.Reason+")")
		}
		log.Info().Msg(line)
	}
	log.Info().Msgf("%s %d removed, %d skipped, %d errors", styles.RenderMuted("Summary:"), removed, skipped, errs)
}
