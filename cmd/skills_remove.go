/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	skillspkg "github.com/metaplay/cli/internal/skills"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type skillsRemoveOpts struct {
	UsePositionalArgs

	argSkill string

	flagScope   string
	flagTargets []string

	resolvedScope   skillspkg.Scope
	resolvedTargets []skillspkg.AgentDir
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
	flags.StringSliceVar(&o.flagTargets, "target", nil, "Target dir(s). Repeatable. Project scope: 'standard' (.agents/skills), 'claude'. User scope: claude, cursor, copilot, codex, windsurf, gemini, junie, continue, cline, warp, goose, amp, opencode, augment, roo. Defaults to interactive prompt or detection.")

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

	seen := map[string]bool{}
	for _, id := range o.flagTargets {
		if seen[id] {
			continue
		}
		seen[id] = true
		t := skillspkg.LookupAgentDir(id)
		if t == nil {
			return clierrors.NewUsageErrorf("Unknown --target %q", id).
				WithDetails("Known targets: " + strings.Join(skillspkg.AgentDirIDs(), ", "))
		}
		o.resolvedTargets = append(o.resolvedTargets, *t)
	}
	return nil
}

func (o *skillsRemoveOpts) Run(cmd *cobra.Command) error {
	if err := o.resolveScope(); err != nil {
		return err
	}
	rootDir, err := o.resolveRootDir()
	if err != nil {
		return err
	}
	if err := o.resolveTargets(rootDir); err != nil {
		return err
	}
	if err := validateTargetsForScope(o.resolvedTargets, o.resolvedScope); err != nil {
		return err
	}

	var skillIDs []string
	if o.argSkill != "" {
		skillIDs = []string{o.argSkill}
	}

	actions, err := skillspkg.Remove(skillspkg.RemoveOptions{
		Targets:  o.resolvedTargets,
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

func (o *skillsRemoveOpts) resolveScope() error {
	if o.flagScope != "" {
		switch o.flagScope {
		case "project":
			o.resolvedScope = skillspkg.ScopeProject
		case "user":
			o.resolvedScope = skillspkg.ScopeUser
		}
		return nil
	}
	if !tui.IsInteractiveMode() {
		o.resolvedScope = skillspkg.ScopeProject
		return nil
	}
	cwd, _ := os.Getwd()
	if flagProjectConfigPath != "" {
		cwd = flagProjectConfigPath
	}
	home, _ := os.UserHomeDir()
	type scopeOpt struct {
		id    string
		label string
		hint  string
	}
	items := []scopeOpt{
		{id: "project", label: "Project (current directory)", hint: cwd},
		{id: "user", label: "User (home directory)", hint: home},
	}
	chosen, err := tui.ChooseFromListDialog("Remove scope", items, func(it *scopeOpt) (string, string) {
		return it.label, it.hint
	})
	if err != nil {
		return clierrors.Wrap(err, "Scope selection cancelled")
	}
	if chosen.id == "user" {
		o.resolvedScope = skillspkg.ScopeUser
	} else {
		o.resolvedScope = skillspkg.ScopeProject
	}
	log.Info().Msgf(" %s %s — %s", styles.RenderSuccess("✓"), chosen.label, chosen.hint)
	return nil
}

func (o *skillsRemoveOpts) resolveRootDir() (string, error) {
	switch o.resolvedScope {
	case skillspkg.ScopeProject:
		// Project scope = current working directory; -p/--project overrides.
		// See cmd/skills_install.go for rationale.
		if flagProjectConfigPath != "" {
			return flagProjectConfigPath, nil
		}
		cwd, err := os.Getwd()
		if err != nil {
			return "", clierrors.Wrap(err, "Could not determine current working directory")
		}
		return cwd, nil
	case skillspkg.ScopeUser:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", clierrors.Wrap(err, "Could not determine user home directory")
		}
		return home, nil
	}
	return "", clierrors.New("Unknown scope")
}

func (o *skillsRemoveOpts) resolveTargets(rootDir string) error {
	if len(o.resolvedTargets) > 0 {
		return nil
	}
	detected := detectExistingTargets(rootDir, o.resolvedScope)

	if !tui.IsInteractiveMode() {
		// Non-interactive: target every detected dir; fall back to the
		// scope-applicable default if none exists. Removing from a
		// non-existent dir is a cheap no-op so this is safe.
		if len(detected) > 0 {
			for _, id := range detected {
				if t := skillspkg.LookupAgentDir(id); t != nil {
					o.resolvedTargets = append(o.resolvedTargets, *t)
				}
			}
			return nil
		}
		d := skillspkg.LookupAgentDir(defaultTargetForScope(o.resolvedScope))
		o.resolvedTargets = append(o.resolvedTargets, *d)
		return nil
	}

	// Interactive multi-select. Order from the scope-filtered registry.
	// Existing dirs pre-checked; if none exist, the scope-applicable default.
	items := skillspkg.AgentDirsForScope(o.resolvedScope)
	defaultID := defaultTargetForScope(o.resolvedScope)
	selected, err := tui.ChooseMultipleFromListDialogWithDefaults(
		"Remove target(s)",
		items,
		func(it *skillspkg.AgentDir) (string, string) {
			hint := ""
			if containsStr(detected, it.ID) {
				hint = "(detected)"
			}
			return it.DisplayName, hint
		},
		func(it *skillspkg.AgentDir) bool {
			if len(detected) == 0 {
				return it.ID == defaultID
			}
			return containsStr(detected, it.ID)
		},
	)
	if err != nil {
		return clierrors.Wrap(err, "Target selection cancelled")
	}
	o.resolvedTargets = append(o.resolvedTargets, selected...)
	logSelectedTargets(o.resolvedTargets)
	return nil
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
