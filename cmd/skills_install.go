/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
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

			Each wrapper carries the CLI version that wrote it in its
			frontmatter. Subsequent installs only overwrite a wrapper if the
			current CLI is the same version or newer — running an older CLI
			against a project initialised by a newer CLI does not revert
			wrappers. User-authored skill files (those without
			'managed-by: metaplay-cli') are never touched.

			Two target directories are supported:

			  - 'standard' (.agents/skills) — read by Cursor, Codex, GitHub
			    Copilot / VS Code, Windsurf, Gemini CLI, OpenCode, Cline,
			    Amp, Warp, Antigravity, and others that follow the open
			    Agent Skills convention.
			  - 'claude' (.claude/skills) — read by Claude Code.

			Note: For tools that read from neither (e.g. JetBrains Junie,
			Continue, Pi, Roo Code), copy or symlink the installed dir into
			the tool's expected location manually. We don't manage symlinks
			here because they're not portable across operating systems.

			In interactive mode, you'll be prompted first for the install
			scope (project or user) and then for the target dir(s); the
			default selection is based on which directories already exist.
		`),
		Example: renderExample(`
			# Interactive prompts for scope and target.
			metaplay skills install

			# Install for Claude Code in the current project.
			metaplay skills install --scope project --target claude

			# Install both standard and Claude Code dirs.
			metaplay skills install --target standard --target claude

			# Install user-scope wrappers under your home directory.
			metaplay skills install --scope user

			# Force-overwrite even wrappers stamped with a newer version.
			metaplay skills install --force
		`),
	}

	flags := cmd.Flags()
	flags.StringVar(&o.flagScope, "scope", "", "'project' (under metaplay-project.yaml) or 'user' (under your home directory). Defaults to interactive prompt or 'project'.")
	flags.StringSliceVar(&o.flagTargets, "target", nil, "Target dir: 'standard' (.agents/skills) and/or 'claude' (.claude/skills). Repeatable. Defaults to interactive prompt or detection.")
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
	o.printManualCopyNote()
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
	type scopeOpt struct {
		id    string
		label string
		hint  string
	}
	items := []scopeOpt{
		{id: "project", label: "Project", hint: "Under metaplay-project.yaml"},
		{id: "user", label: "User", hint: "Under your home directory"},
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
	return nil
}

func (o *skillsInstallOpts) resolveRootDir() (string, error) {
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

func (o *skillsInstallOpts) resolveTargets(rootDir string) error {
	if len(o.resolvedTargets) > 0 {
		return nil // explicitly set via --target
	}
	detected := detectExistingTargets(rootDir, o.resolvedScope)

	if !tui.IsInteractiveMode() {
		// Non-interactive: install into every detected dir, or fall back to
		// the default (Claude Code) if neither exists.
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

	// Interactive multi-select. Order is fixed (standard first), and
	// directories that already exist are pre-checked.
	items := orderedTargetItems()
	selected, err := tui.ChooseMultipleFromListDialogWithDefaults(
		"Install target(s)",
		items,
		func(it *skillspkg.AgentDir) (string, string) {
			hint := ""
			if containsStr(detected, it.ID) {
				hint = "(detected)"
			}
			return it.DisplayName, hint
		},
		func(it *skillspkg.AgentDir) bool {
			return containsStr(detected, it.ID)
		},
	)
	if err != nil {
		return clierrors.Wrap(err, "Target selection cancelled")
	}
	o.resolvedTargets = append(o.resolvedTargets, selected...)
	return nil
}

// orderedTargetItems returns the supported AgentDirs in the canonical
// display order (standard first, then claude).
func orderedTargetItems() []skillspkg.AgentDir {
	standard := skillspkg.LookupAgentDir(skillspkg.AgentDirStandardID)
	claude := skillspkg.LookupAgentDir(skillspkg.AgentDirClaudeID)
	return []skillspkg.AgentDir{*standard, *claude}
}

// detectExistingTargets returns the IDs of AgentDirs whose scope-relative
// directory already exists at rootDir.
func detectExistingTargets(rootDir string, scope skillspkg.Scope) []string {
	var ids []string
	for _, t := range skillspkg.AgentDirs {
		var rel string
		switch scope {
		case skillspkg.ScopeProject:
			rel = t.ProjectDir
		case skillspkg.ScopeUser:
			rel = t.UserDir
		}
		if rel == "" {
			continue
		}
		path := filepath.Join(rootDir, rel)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			ids = append(ids, t.ID)
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

// printManualCopyNote reminds users of tools that read from neither
// .agents/skills nor .claude/skills that they can pick up the installed
// content by copying or symlinking the directory themselves.
func (o *skillsInstallOpts) printManualCopyNote() {
	log.Info().Msg("")
	log.Info().Msg(styles.RenderMuted(
		"Note: AI tools that read from neither .agents/skills nor .claude/skills " +
			"(e.g. JetBrains Junie, Pi, Continue, Roo Code) can pick up these skills " +
			"by copying or symlinking the installed dir into the tool's own location.",
	))
}
