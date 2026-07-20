/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"strings"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/skills"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
)

// tuiPrompter is the cobra-side adapter that satisfies skills.Prompter using
// the existing internal/tui dialogs. Built only when interactive mode is on
// — see newSkillsInstallPrompter / newSkillsRemovePrompter below.
type tuiPrompter struct {
	scopeTitle       string   // "Install scope" / "Remove scope"
	scopePreamble    []string // optional lines printed once above the dialog
	projectScopeDesc []string // multi-line description for the Project option
	userScopeDesc    []string // multi-line description for the User option
	targetTitle      string   // "Install target(s)" / "Remove target(s)"
	footer           string   // optional prompt footer for the multi-select
	recommendProject bool     // mark Project option as recommended in the scope dialog
}

// newSkillsRemovePrompter returns a Prompter for the remove command. Returns
// nil in non-interactive mode (CI, --verbose, no TTY) so the orchestrator
// falls back to its non-interactive defaults.
func newSkillsRemovePrompter(scopeTitle, targetTitle, footer string) skills.Prompter {
	if !tui.IsInteractiveMode() {
		return nil
	}
	return &tuiPrompter{
		scopeTitle:  scopeTitle,
		targetTitle: targetTitle,
		footer:      footer,
		scopePreamble: []string{
			"This will remove the Metaplay Agent skill files from your machine.",
			"Only files installed by " + styles.RenderTechnical("metaplay skills install") + " are affected.",
		},
		projectScopeDesc: []string{
			"Remove the skill files from the current project directory.",
		},
		userScopeDesc: []string{
			"Remove the skill files from your home folder (all-projects install).",
		},
	}
}

// newSkillsInstallPrompter is like newSkillsRemovePrompter but adds the
// install-specific scope explanation and marks the Project option as
// recommended.
func newSkillsInstallPrompter(scopeTitle, targetTitle, footer string) skills.Prompter {
	if !tui.IsInteractiveMode() {
		return nil
	}
	return &tuiPrompter{
		scopeTitle:  scopeTitle,
		targetTitle: targetTitle,
		footer:      footer,
		scopePreamble: []string{
			"This will install the Metaplay Agent skill files into your machine.",
		},
		projectScopeDesc: []string{
			"Skill files are added into the current project and shared with your",
			"team by committing them into your repository.",
		},
		userScopeDesc: []string{
			"Skill files are installed in your home folder. They are available",
			"in all your projects, but not shared with your team members.",
		},
		recommendProject: true,
	}
}

func (p *tuiPrompter) AskScope(currentDir, homeDir string) (skills.Scope, error) {
	type scopeOpt struct {
		id    string
		label string
		hint  string
		desc  []string
	}
	projectLabel := "Project"
	if p.recommendProject {
		projectLabel = "Project (recommended)"
	}
	items := []scopeOpt{
		{id: "project", label: projectLabel, hint: "— " + currentDir, desc: p.projectScopeDesc},
		{id: "user", label: "User", hint: "— " + homeDir, desc: p.userScopeDesc},
	}
	for _, line := range p.scopePreamble {
		log.Info().Msg(line)
	}
	chosen, err := tui.ChooseFromListDialogMultiline(p.scopeTitle, items, func(it *scopeOpt) (string, string, []string) {
		return it.label, it.hint, it.desc
	})
	if err != nil {
		return 0, err
	}
	log.Info().Msgf(" %s %s %s", styles.RenderSuccess("✓"), chosen.label, styles.RenderMuted(chosen.hint))
	if chosen.id == "user" {
		return skills.ScopeUser, nil
	}
	return skills.ScopeProject, nil
}

func (p *tuiPrompter) AskTargets(scope skills.Scope, groups []skills.AgentDirGroup, defaults []string) ([]skills.AgentDir, error) {
	selected, err := tui.ChooseMultipleFromListDialogWithDefaults(
		p.targetTitle,
		p.footer,
		groups,
		func(g *skills.AgentDirGroup) (string, string) {
			return g.Path + "/", "— " + strings.Join(g.Tools, ", ")
		},
		func(g *skills.AgentDirGroup) bool {
			return containsStr(defaults, g.Rep.ID)
		},
	)
	if err != nil {
		return nil, err
	}
	out := make([]skills.AgentDir, 0, len(selected))
	for _, g := range selected {
		out = append(out, g.Rep)
	}
	logSelectedSkillTargets(out)
	return out, nil
}

// logSelectedSkillTargets echoes the chosen target dirs so the multi-select
// dialog's chosen values aren't lost when the dialog body collapses.
func logSelectedSkillTargets(targets []skills.AgentDir) {
	names := make([]string, 0, len(targets))
	for _, t := range targets {
		names = append(names, t.DisplayName)
	}
	log.Info().Msgf(" %s %s", styles.RenderSuccess("✓"), strings.Join(names, ", "))
	log.Info().Msg("")
}
