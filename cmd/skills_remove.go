/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	skillspkg "github.com/metaplay/cli/pkg/skills"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type skillsRemoveOpts struct {
	UsePositionalArgs

	argSkill string

	flagScope   string
	flagTargets []string
}

func init() {
	o := skillsRemoveOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argSkill, "SKILL", "Specific skill name to remove (defaults to all metaplay-cli wrappers).")

	cmd := &cobra.Command{
		Use:   "remove [SKILL] [flags]",
		Short: "[preview] Remove Metaplay skill wrappers from the project (or user home)",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			PREVIEW: This command is in preview and subject to change!

			Delete wrappers previously written by 'metaplay skills install'.

			{Arguments}

			Only files carrying 'managed-by: metaplay-cli' in their
			frontmatter are removed; user-authored skill files in the same
			directory are always preserved. After deleting a SKILL.md, the
			parent skill directory is removed if it is empty.

			With no SKILL argument, every metaplay-cli wrapper found under
			the chosen target dirs is cleaned up — useful for clearing out
			orphan wrappers from skills that have been removed from the
			canonical set.

			In interactive mode, you'll be prompted for the scope (project
			or user) and the target dir(s); the default selection is based
			on which directories already exist.
		`),
		Example: renderExample(`
			# Remove all metaplay-cli wrappers from the current project (interactive prompts).
			metaplay skills remove

			# Remove just one skill.
			metaplay skills remove metaplay-develop

			# Remove from both standard and Claude Code dirs.
			metaplay skills remove --target standard --target claude

			# Remove user-scope wrappers under your home directory.
			metaplay skills remove --scope user
		`),
	}

	flags := cmd.Flags()
	flags.StringVar(&o.flagScope, "scope", "", "'project' (current directory; or --project path) or 'user' (your home directory). Defaults to interactive prompt or 'project'.")
	flags.StringSliceVar(&o.flagTargets, "target", nil, "Target dir(s). Repeatable. Project scope: standard, claude. User scope: standard, claude, cursor, copilot, codex, windsurf, gemini, junie, continue, cline, warp, goose, amp, opencode, augment, roo. Defaults to interactive prompt or detection.")

	skillsCmd.AddCommand(cmd)
}

func (o *skillsRemoveOpts) Prepare(cmd *cobra.Command, args []string) error {
	switch o.flagScope {
	case "", "project", "user":
		// OK
	default:
		return clierrors.NewUsageErrorf("Invalid --scope %q", o.flagScope).
			WithSuggestion("Use --scope project or --scope user")
	}

	for _, id := range o.flagTargets {
		if skillspkg.LookupAgentDir(id) == nil {
			return clierrors.NewUsageErrorf("Unknown --target %q", id).
				WithDetails("Known targets: " + strings.Join(skillspkg.AgentDirIDs(), ", "))
		}
	}
	return nil
}

func (o *skillsRemoveOpts) Run(cmd *cobra.Command) error {
	projectDir, err := projectDirOrCwd()
	if err != nil {
		return err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return clierrors.Wrap(err, "Could not determine user home directory")
	}

	var skillIDs []string
	if o.argSkill != "" {
		skillIDs = []string{o.argSkill}
	}

	result, err := skillspkg.RunRemove(skillspkg.RemoveRequest{
		Scope:      flagScopeToScope(o.flagScope),
		ProjectDir: projectDir,
		UserDir:    homeDir,
		TargetIDs:  o.flagTargets,
		SkillIDs:   skillIDs,
		Prompter:   newSkillsRemovePrompter("Remove scope", "Remove target(s)", ""),
	})
	if err != nil {
		return clierrors.Wrap(err, "Remove failed")
	}

	reportRemoveActions(result.Actions)
	if result.Scope == skillspkg.ScopeProject {
		log.Info().Msg("")
		log.Info().Msgf("%s", styles.RenderPrompt("You should commit the modified files to your version control."))
	}
	return nil
}

func reportRemoveActions(actions []skillspkg.RemoveAction) {
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
