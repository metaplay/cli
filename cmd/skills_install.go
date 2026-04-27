/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	skillspkg "github.com/metaplay/cli/internal/skills"
	"github.com/metaplay/cli/internal/version"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type skillsInstallOpts struct {
	flagScope     string
	flagAgents    []string
	flagAllAgents bool
	flagForce     bool

	resolvedScope skillspkg.Scope
	resolvedAgents []skillspkg.AgentHost
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

			Default scope is 'project' (write under the current
			metaplay-project.yaml). Default agent is 'claude-code'. Pass
			--agent <id> (repeatable) to target specific tools, or
			--all-agents to install for every known AI coding tool.
		`),
		Example: renderExample(`
			# Install for Claude Code in the current project (default).
			metaplay skills install

			# Install for Cursor as well.
			metaplay skills install --agent claude-code --agent cursor

			# Install user-scope wrappers under your home directory.
			metaplay skills install --scope user

			# Install for every known AI coding tool.
			metaplay skills install --all-agents

			# Force-overwrite even wrappers stamped with a newer version.
			metaplay skills install --force
		`),
	}

	flags := cmd.Flags()
	flags.StringVar(&o.flagScope, "scope", "project", "'project' (under metaplay-project.yaml) or 'user' (under your home directory)")
	flags.StringSliceVar(&o.flagAgents, "agent", []string{skillspkg.DefaultAgentID}, "Target AI tool ID (repeatable). See 'metaplay skills install --help' for the full list.")
	flags.BoolVar(&o.flagAllAgents, "all-agents", false, "Install for every AI tool the CLI knows about")
	flags.BoolVar(&o.flagForce, "force", false, "Overwrite even when the on-disk wrapper has a newer version stamp")

	skillsCmd.AddCommand(cmd)
}

func (o *skillsInstallOpts) Prepare(cmd *cobra.Command, args []string) error {
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

func (o *skillsInstallOpts) Run(cmd *cobra.Command) error {
	rootDir, err := o.resolveRootDir()
	if err != nil {
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
		Agents:  o.resolvedAgents,
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
