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
// — see newSkillsPrompter below.
type tuiPrompter struct {
	scopeTitle  string // "Install scope" / "Remove scope"
	targetTitle string // "Install target(s)" / "Remove target(s)"
	footer      string // optional prompt footer for the multi-select
}

// newSkillsPrompter returns a Prompter for cobra-driven runs. When the
// process is running non-interactively (CI, --verbose, no TTY) it returns
// nil so the orchestrator falls back to its non-interactive defaults.
func newSkillsPrompter(scopeTitle, targetTitle, footer string) skills.Prompter {
	if !tui.IsInteractiveMode() {
		return nil
	}
	return &tuiPrompter{
		scopeTitle:  scopeTitle,
		targetTitle: targetTitle,
		footer:      footer,
	}
}

func (p *tuiPrompter) AskScope(currentDir, homeDir string) (skills.Scope, error) {
	type scopeOpt struct {
		id    string
		label string
		hint  string
	}
	items := []scopeOpt{
		{id: "project", label: "Project", hint: "— " + currentDir},
		{id: "user", label: "User", hint: "— " + homeDir},
	}
	chosen, err := tui.ChooseFromListDialog(p.scopeTitle, items, func(it *scopeOpt) (string, string) {
		return it.label, it.hint
	})
	if err != nil {
		return 0, err
	}
	log.Info().Msgf(" %s %s %s", styles.RenderSuccess("✓"), chosen.label, chosen.hint)
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
