/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	skillspkg "github.com/metaplay/cli/internal/skills"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/internal/version"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type skillsInstallOpts struct {
	flagScope   string
	flagTargets []string
	flagForce   bool

	resolvedScope   skillspkg.Scope
	resolvedTargets []skillspkg.AgentDir
}

func init() {
	o := skillsInstallOpts{}

	cmd := &cobra.Command{
		Use:   "install [flags]",
		Short: "Install Metaplay skill wrappers into your project (or user home)",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Install thin wrapper SKILL.md files for the embedded skills.

			Project scope writes under the current directory. We do NOT
			walk up to a metaplay-project.yaml or git root, since the user
			may run their harness from a subdirectory. Pass -p/--project
			to target a different directory explicitly.

			Each wrapper carries the CLI version that wrote it in its
			frontmatter. Subsequent installs only overwrite a wrapper if the
			current CLI is the same version or newer — running an older CLI
			against a project initialised by a newer CLI does not revert
			wrappers. User-authored skill files (those without
			'managed-by: metaplay-cli') are never touched.

			Targets:

			  - 'standard' (.agents/skills/) — the cross-agent shared dir.
			    At project scope, read by Cursor, Codex, GitHub Copilot /
			    VS Code, Windsurf, Gemini CLI, OpenCode, Cline, Amp, Warp,
			    and others that follow the open Agent Skills convention.
			    At user scope, ~/.agents/skills/ is read by Codex, Cursor,
			    Copilot, Windsurf, Gemini, Cline, and Warp.
			  - 'claude' (.claude/skills/) — read by Claude Code.

			User scope additionally offers per-harness home directories
			mirroring the vercel-labs/skills convention: cursor, copilot,
			codex, windsurf, gemini, junie, continue, cline, warp, goose,
			amp, opencode, augment, roo. Pick these for tools that don't
			read ~/.agents/skills/ (e.g. Junie, Continue, Roo) or when you
			only want to install for one specific tool.

			Project-scope note: for tools that read from neither
			.agents/skills/ nor .claude/skills/ (e.g. JetBrains Junie,
			Continue, Roo Code), either copy/symlink the installed dir
			into the tool's expected location, or run the user-scope
			install and tick the tool directly.

			In interactive mode, you'll be prompted first for the install
			scope (project or user) and then for the target dir(s); the
			default selection is based on which directories already exist.
		`),
		Example: renderExample(`
			# Interactive prompts for scope and target.
			metaplay skills install

			# Install for Claude Code in the current project.
			metaplay skills install --scope project --target claude

			# Install both standard and Claude Code dirs at project scope.
			metaplay skills install --target standard --target claude

			# User-scope install for two specific tools.
			metaplay skills install --scope user --target cursor --target junie

			# Force-overwrite even wrappers stamped with a newer version.
			metaplay skills install --force
		`),
	}

	flags := cmd.Flags()
	flags.StringVar(&o.flagScope, "scope", "", "'project' (current directory; or --project path) or 'user' (your home directory). Defaults to interactive prompt or 'project'.")
	flags.StringSliceVar(&o.flagTargets, "target", nil, "Target dir(s). Repeatable. Project scope: standard, claude. User scope: standard, claude, cursor, copilot, codex, windsurf, gemini, junie, continue, cline, warp, goose, amp, opencode, augment, roo. Defaults to interactive prompt or detection.")
	flags.BoolVar(&o.flagForce, "force", false, "Overwrite even when the on-disk wrapper has a newer version stamp")

	skillsCmd.AddCommand(cmd)
}

func (o *skillsInstallOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Validate --scope if explicitly given. Empty means "ask interactively
	// or fall back to project", resolved later in Run().
	switch o.flagScope {
	case "", "project", "user":
		// OK
	default:
		return clierrors.NewUsageErrorf("Invalid --scope %q", o.flagScope).
			WithSuggestion("Use --scope project or --scope user")
	}

	// Validate --target IDs if explicitly given.
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

func (o *skillsInstallOpts) Run(cmd *cobra.Command) error {
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

	skills, err := skillspkg.LoadAll(skillspkg.OpenFS())
	if err != nil {
		return clierrors.Wrap(err, "Failed to load embedded skills")
	}

	devMode := version.IsDevBuild()
	if devMode {
		log.Info().Msg(styles.RenderMuted("Dev build: bypassing version gate (write always)"))
	}

	actions, err := skillspkg.Install(skillspkg.InstallOptions{
		Skills:  skills,
		Targets: o.resolvedTargets,
		RootDir: rootDir,
		Scope:   o.resolvedScope,
		Version: version.AppVersion,
		Force:   o.flagForce,
		DevMode: devMode,
	})
	if err != nil {
		return clierrors.Wrap(err, "Install failed")
	}

	o.reportActions(actions)
	if o.resolvedScope == skillspkg.ScopeProject {
		log.Info().Msg("")
		log.Info().Msgf("%s", styles.RenderPrompt("You should commit the modified files to your version control."))
	}
	return nil
}

func (o *skillsInstallOpts) resolveScope() error {
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
		{id: "project", label: "Project", hint: "— " + cwd},
		{id: "user", label: "User", hint: "— " + home},
	}
	chosen, err := tui.ChooseFromListDialog("Install scope", items, func(it *scopeOpt) (string, string) {
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
	log.Info().Msgf(" %s %s %s", styles.RenderSuccess("✓"), chosen.label, chosen.hint)
	return nil
}

func (o *skillsInstallOpts) resolveRootDir() (string, error) {
	switch o.resolvedScope {
	case skillspkg.ScopeProject:
		// Project scope = current working directory. We deliberately do
		// NOT walk up to a metaplay-project.yaml or git root, since the
		// user may run their harness from a subdirectory and expect skills
		// to land there. The global -p/--project flag is honored as an
		// explicit override.
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

func (o *skillsInstallOpts) resolveTargets(rootDir string) error {
	if len(o.resolvedTargets) > 0 {
		return nil // explicitly set via --target
	}
	detected := detectExistingTargets(rootDir, o.resolvedScope)

	if !tui.IsInteractiveMode() {
		// Non-interactive: install into every detected dir, or fall back to
		// the default (standard) if none exist.
		if len(detected) > 0 {
			for _, id := range detected {
				if t := skillspkg.LookupAgentDir(id); t != nil {
					o.resolvedTargets = append(o.resolvedTargets, *t)
				}
			}
			return nil
		}
		d := skillspkg.LookupAgentDir(skillspkg.DefaultAgentDirID)
		o.resolvedTargets = append(o.resolvedTargets, *d)
		return nil
	}

	// Interactive multi-select. One row per unique path (so .agents/skills/
	// isn't shown three times for Standard/Cline/Warp). Existing dirs are
	// pre-checked; if none exist, the default (standard) is.
	groups := skillspkg.GroupAgentDirsForScope(o.resolvedScope)
	footer := ""
	if o.resolvedScope == skillspkg.ScopeProject {
		footer = "Other tools can pick these up by copying or symlinking " +
			"the installed dir into their own location."
	}
	selected, err := tui.ChooseMultipleFromListDialogWithDefaults(
		"Install target(s)",
		footer,
		groups,
		func(g *skillspkg.AgentDirGroup) (string, string) {
			return targetItemName(g), targetItemHint(g)
		},
		func(g *skillspkg.AgentDirGroup) bool {
			if len(detected) == 0 {
				return g.Rep.ID == skillspkg.DefaultAgentDirID
			}
			return containsStr(detected, g.Rep.ID)
		},
	)
	if err != nil {
		return clierrors.Wrap(err, "Target selection cancelled")
	}
	for _, g := range selected {
		o.resolvedTargets = append(o.resolvedTargets, g.Rep)
	}
	logSelectedTargets(o.resolvedTargets)
	return nil
}

// targetItemName is the un-muted left-hand text for each group in the
// multi-select: the scope-relative path. The path is the primary
// identifier since it's what gets written to disk.
func targetItemName(g *skillspkg.AgentDirGroup) string {
	return g.Path + "/"
}

// targetItemHint is the muted right-hand text for each group: "— <tool
// list>", listing every harness that reads the path. "(detected)" is
// omitted since the pre-checked checkbox already conveys it.
func targetItemHint(g *skillspkg.AgentDirGroup) string {
	return "— " + strings.Join(g.Tools, ", ")
}

// logSelectedTargets prints a one-line confirmation of which target dirs
// the user picked, so the multi-select dialog's chosen values aren't lost
// when the dialog body collapses after quitting. The trailing blank line
// separates the echo from the install/remove output that follows.
func logSelectedTargets(targets []skillspkg.AgentDir) {
	names := make([]string, 0, len(targets))
	for _, t := range targets {
		names = append(names, t.DisplayName)
	}
	log.Info().Msgf(" %s %s", styles.RenderSuccess("✓"), strings.Join(names, ", "))
	log.Info().Msg("")
}

// validateTargetsForScope ensures every chosen target has a non-empty
// directory at the active scope. Caller is expected to have already
// resolved both the scope and the targets.
func validateTargetsForScope(targets []skillspkg.AgentDir, scope skillspkg.Scope) error {
	scopeName := "project"
	if scope == skillspkg.ScopeUser {
		scopeName = "user"
	}
	valid := skillspkg.AgentDirIDsForScope(scope)
	for _, t := range targets {
		var rel string
		switch scope {
		case skillspkg.ScopeProject:
			rel = t.ProjectDir
		case skillspkg.ScopeUser:
			rel = t.UserDir
		}
		if rel == "" {
			return clierrors.NewUsageErrorf("Target %q has no %s directory", t.ID, scopeName).
				WithDetails(fmt.Sprintf("Valid %s targets: %s", scopeName, strings.Join(valid, ", ")))
		}
	}
	return nil
}

// detectExistingTargets returns the IDs of AgentDirs whose scope-relative
// directory already exists at rootDir. Multiple targets that share a path
// (e.g. standard, cline, warp at user scope all map to .agents/skills) are
// collapsed to the FIRST entry that points at that path — the others stay
// unselected so the multi-select doesn't duplicate-check redundant items.
func detectExistingTargets(rootDir string, scope skillspkg.Scope) []string {
	var ids []string
	seen := map[string]bool{}
	for _, t := range skillspkg.AgentDirs {
		var rel string
		switch scope {
		case skillspkg.ScopeProject:
			rel = t.ProjectDir
		case skillspkg.ScopeUser:
			rel = t.UserDir
		}
		if rel == "" || seen[rel] {
			continue
		}
		path := filepath.Join(rootDir, rel)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			ids = append(ids, t.ID)
			seen[rel] = true
		}
	}
	return ids
}

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func (o *skillsInstallOpts) reportActions(actions []skillspkg.InstallAction) {
	var written, unchanged, skipped int
	for _, a := range actions {
		var line string
		switch a.Status {
		case skillspkg.StatusWritten:
			written++
			line = styles.RenderSuccess("WROTE   ") + " " + a.Path
		case skillspkg.StatusUnchanged:
			unchanged++
			suffix := ""
			if a.Reason != "" {
				suffix = "  " + styles.RenderMuted("("+a.Reason+")")
			}
			line = styles.RenderMuted("unchanged") + " " + a.Path + suffix
		case skillspkg.StatusSkippedNewer:
			skipped++
			line = styles.RenderAttention("SKIP    ") + " " + a.Path + "  " + styles.RenderMuted("("+a.Reason+")")
		case skillspkg.StatusSkippedUser:
			skipped++
			line = styles.RenderAttention("SKIP    ") + " " + a.Path + "  " + styles.RenderMuted("("+a.Reason+")")
		case skillspkg.StatusSkippedError:
			skipped++
			line = styles.RenderError("ERROR   ") + " " + a.Path + "  " + styles.RenderMuted("("+a.Reason+")")
		}
		log.Info().Msg(line)
	}
	log.Info().Msgf("%s %d written, %d unchanged, %d skipped", styles.RenderMuted("Summary:"), written, unchanged, skipped)
}

