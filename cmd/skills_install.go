/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/version"
	skillspkg "github.com/metaplay/cli/pkg/skills"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type skillsInstallOpts struct {
	flagScope   string
	flagTargets []string
	flagForce   bool
}

func init() {
	o := skillsInstallOpts{}

	cmd := &cobra.Command{
		Use:   "install [flags]",
		Short: "[preview] Install Metaplay skill wrappers into your project (or user home)",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			PREVIEW: This command is in preview and subject to change!

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

func (o *skillsInstallOpts) Run(cmd *cobra.Command) error {
	projectDir, err := projectDirOrCwd()
	if err != nil {
		return err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return clierrors.Wrap(err, "Could not determine user home directory")
	}

	skillsList, err := skillspkg.LoadAll(skillspkg.EmbeddedFS())
	if err != nil {
		return clierrors.Wrap(err, "Failed to load embedded skills")
	}

	devMode := version.IsDevBuild()
	if devMode {
		log.Info().Msg(styles.RenderMuted("Dev build: bypassing version gate (write always)"))
	}

	footer := "Other tools can pick these up by copying or symlinking " +
		"the installed dir into their own location."

	result, err := skillspkg.RunInstall(skillspkg.InstallRequest{
		Skills:     skillsList,
		Scope:      flagScopeToScope(o.flagScope),
		ProjectDir: projectDir,
		UserDir:    homeDir,
		TargetIDs:  o.flagTargets,
		Version:    version.AppVersion,
		Force:      o.flagForce,
		DevMode:    devMode,
		Prompter:   newSkillsInstallPrompter("Install scope", "Install target(s)", footer),
	})
	if err != nil {
		return clierrors.Wrap(err, "Install failed")
	}

	reportInstallActions(result.Actions)
	if result.Scope == skillspkg.ScopeProject {
		log.Info().Msg("")
		log.Info().Msgf("%s", styles.RenderPrompt("You should commit the modified files to your version control."))
	}
	return nil
}

// projectDirOrCwd returns the -p/--project flag value when set, else the
// current working directory. We deliberately do NOT walk up to a
// metaplay-project.yaml or git root, since the user may run their harness
// from a subdirectory and expect skills to land there.
func projectDirOrCwd() (string, error) {
	if flagProjectConfigPath != "" {
		return flagProjectConfigPath, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", clierrors.Wrap(err, "Could not determine current working directory")
	}
	return cwd, nil
}

// flagScopeToScope translates the cobra string flag into the optional Scope
// pointer expected by skills.InstallRequest / RemoveRequest. Empty string
// returns nil so the orchestrator can prompt or default.
func flagScopeToScope(flag string) *skillspkg.Scope {
	switch flag {
	case "project":
		s := skillspkg.ScopeProject
		return &s
	case "user":
		s := skillspkg.ScopeUser
		return &s
	}
	return nil
}

// containsStr is shared with skills_prompter.go (defined here for now to
// avoid an extra file). Linear scan; targets are tiny.
func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func reportInstallActions(actions []skillspkg.InstallAction) {
	var written, unchanged, skipped int
	for _, a := range actions {
		var line string
		switch a.Status {
		case skillspkg.StatusWritten:
			written++
			line = styles.RenderSuccess("WROTE   ") + " " + a.Path
		case skillspkg.StatusUnchanged:
			unchanged++
			line = styles.RenderMuted("unchanged") + " " + a.Path
		case skillspkg.StatusSkippedShared:
			unchanged++
			line = styles.RenderMuted("shared   ") + " " + a.Path + "  " + styles.RenderMuted("("+a.Reason+")")
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
