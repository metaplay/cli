/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakePrompter is a Prompter that returns canned responses, letting tests
// drive the interactive code paths without a TTY.
type fakePrompter struct {
	scope          Scope
	scopeErr       error
	scopeCallCount int

	chosenIDs       []string
	targetsErr      error
	targetsCallCount int

	// Captured arguments for assertions.
	lastDefaults []string
	lastGroups   []AgentDirGroup
	lastScope    Scope
}

func (p *fakePrompter) AskScope(currentDir, homeDir string) (Scope, error) {
	p.scopeCallCount++
	if p.scopeErr != nil {
		return 0, p.scopeErr
	}
	return p.scope, nil
}

func (p *fakePrompter) AskTargets(scope Scope, groups []AgentDirGroup, defaults []string) ([]AgentDir, error) {
	p.targetsCallCount++
	p.lastScope = scope
	p.lastGroups = groups
	p.lastDefaults = defaults
	if p.targetsErr != nil {
		return nil, p.targetsErr
	}
	out := make([]AgentDir, 0, len(p.chosenIDs))
	for _, id := range p.chosenIDs {
		if t := LookupAgentDir(id); t != nil {
			out = append(out, *t)
		}
	}
	return out, nil
}

func TestRunInstall_ProjectScopeExplicitTarget(t *testing.T) {
	tmp := t.TempDir()
	scope := ScopeProject
	res, err := RunInstall(InstallRequest{
		Skills:     []*Skill{mkSkill(t, "skill-a")},
		Scope:      &scope,
		ProjectDir: tmp,
		TargetIDs:  []string{AgentDirClaudeID},
		Version:    "1.0.0",
	})
	if err != nil {
		t.Fatalf("RunInstall: %v", err)
	}
	if res.Scope != ScopeProject {
		t.Errorf("Scope = %v, want ScopeProject", res.Scope)
	}
	if res.RootDir != tmp {
		t.Errorf("RootDir = %q, want %q", res.RootDir, tmp)
	}
	if len(res.Targets) != 1 || res.Targets[0].ID != AgentDirClaudeID {
		t.Errorf("Targets = %+v", res.Targets)
	}
	expected := filepath.Join(tmp, ".claude/skills/skill-a/SKILL.md")
	if _, err := os.Stat(expected); err != nil {
		t.Errorf("expected file %s: %v", expected, err)
	}
}

func TestRunInstall_UserScopeWithTempHome(t *testing.T) {
	// This is the headline test: drive a user-scope install entirely under a
	// tempdir, with no real $HOME involvement.
	fakeHome := t.TempDir()
	scope := ScopeUser
	res, err := RunInstall(InstallRequest{
		Skills:    []*Skill{mkSkill(t, "skill-a")},
		Scope:     &scope,
		UserDir:   fakeHome,
		TargetIDs: []string{"cursor", AgentDirClaudeID},
		Version:   "1.0.0",
	})
	if err != nil {
		t.Fatalf("RunInstall: %v", err)
	}
	if res.RootDir != fakeHome {
		t.Errorf("RootDir = %q, want %q", res.RootDir, fakeHome)
	}
	for _, rel := range []string{".cursor/skills/skill-a/SKILL.md", ".claude/skills/skill-a/SKILL.md"} {
		path := filepath.Join(fakeHome, rel)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file %s: %v", path, err)
		}
	}
}

func TestRunInstall_InteractiveUserScope(t *testing.T) {
	// Prompter drives both scope and target selection. Tests that the
	// orchestrator hands the right scope and defaults to AskTargets.
	fakeHome := t.TempDir()
	prompter := &fakePrompter{
		scope:     ScopeUser,
		chosenIDs: []string{AgentDirStandardID},
	}
	res, err := RunInstall(InstallRequest{
		Skills:     []*Skill{mkSkill(t, "skill-a")},
		ProjectDir: t.TempDir(),
		UserDir:    fakeHome,
		Version:    "1.0.0",
		Prompter:   prompter,
	})
	if err != nil {
		t.Fatalf("RunInstall: %v", err)
	}
	if prompter.scopeCallCount != 1 {
		t.Errorf("AskScope calls = %d, want 1", prompter.scopeCallCount)
	}
	if prompter.targetsCallCount != 1 {
		t.Errorf("AskTargets calls = %d, want 1", prompter.targetsCallCount)
	}
	if prompter.lastScope != ScopeUser {
		t.Errorf("AskTargets received scope = %v, want ScopeUser", prompter.lastScope)
	}
	// Default selection should be DefaultAgentDirID since no dirs exist yet.
	if len(prompter.lastDefaults) != 1 || prompter.lastDefaults[0] != DefaultAgentDirID {
		t.Errorf("AskTargets defaults = %v, want [%s]", prompter.lastDefaults, DefaultAgentDirID)
	}
	if res.Scope != ScopeUser {
		t.Errorf("res.Scope = %v, want ScopeUser", res.Scope)
	}
}

func TestRunInstall_DefaultsToDetectedTargets(t *testing.T) {
	tmp := t.TempDir()
	// Pre-create .claude/skills/ to simulate "Claude already detected here".
	if err := os.MkdirAll(filepath.Join(tmp, ".claude/skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	scope := ScopeProject
	// No Prompter, no TargetIDs — should detect .claude/skills/.
	res, err := RunInstall(InstallRequest{
		Skills:     []*Skill{mkSkill(t, "skill-a")},
		Scope:      &scope,
		ProjectDir: tmp,
		Version:    "1.0.0",
	})
	if err != nil {
		t.Fatalf("RunInstall: %v", err)
	}
	if len(res.Targets) != 1 || res.Targets[0].ID != AgentDirClaudeID {
		t.Errorf("expected detected claude target, got %+v", res.Targets)
	}
}

func TestRunInstall_DefaultsToStandardWhenNoneDetected(t *testing.T) {
	tmp := t.TempDir()
	scope := ScopeProject
	res, err := RunInstall(InstallRequest{
		Skills:     []*Skill{mkSkill(t, "skill-a")},
		Scope:      &scope,
		ProjectDir: tmp,
		Version:    "1.0.0",
	})
	if err != nil {
		t.Fatalf("RunInstall: %v", err)
	}
	if len(res.Targets) != 1 || res.Targets[0].ID != DefaultAgentDirID {
		t.Errorf("expected default target [%s], got %+v", DefaultAgentDirID, res.Targets)
	}
}

func TestRunInstall_PromptDefaultsToDetected(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".claude/skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	prompter := &fakePrompter{
		scope:     ScopeProject,
		chosenIDs: []string{AgentDirClaudeID},
	}
	scope := ScopeProject
	_, err := RunInstall(InstallRequest{
		Skills:     []*Skill{mkSkill(t, "skill-a")},
		Scope:      &scope,
		ProjectDir: tmp,
		Version:    "1.0.0",
		Prompter:   prompter,
	})
	if err != nil {
		t.Fatalf("RunInstall: %v", err)
	}
	if len(prompter.lastDefaults) != 1 || prompter.lastDefaults[0] != AgentDirClaudeID {
		t.Errorf("AskTargets defaults = %v, want [claude] from detection", prompter.lastDefaults)
	}
}

func TestRunInstall_RejectsUnknownTarget(t *testing.T) {
	scope := ScopeProject
	_, err := RunInstall(InstallRequest{
		Skills:     []*Skill{mkSkill(t, "skill-a")},
		Scope:      &scope,
		ProjectDir: t.TempDir(),
		TargetIDs:  []string{"nope"},
		Version:    "1.0.0",
	})
	if err == nil {
		t.Fatal("expected error for unknown target")
	}
	if !strings.Contains(err.Error(), "unknown target") {
		t.Errorf("error = %v, want contains 'unknown target'", err)
	}
}

func TestRunInstall_RejectsTargetWithoutScopeDir(t *testing.T) {
	// "cursor" is user-scope-only (no ProjectDir). Picking it at project
	// scope should fail validation.
	scope := ScopeProject
	_, err := RunInstall(InstallRequest{
		Skills:     []*Skill{mkSkill(t, "skill-a")},
		Scope:      &scope,
		ProjectDir: t.TempDir(),
		TargetIDs:  []string{"cursor"},
		Version:    "1.0.0",
	})
	if err == nil {
		t.Fatal("expected error for cursor at project scope")
	}
}

func TestRunInstall_RequiresProjectDirForProjectScope(t *testing.T) {
	scope := ScopeProject
	_, err := RunInstall(InstallRequest{
		Skills:    []*Skill{mkSkill(t, "skill-a")},
		Scope:     &scope,
		TargetIDs: []string{AgentDirClaudeID},
		Version:   "1.0.0",
	})
	if err == nil {
		t.Fatal("expected error for missing ProjectDir")
	}
	if !strings.Contains(err.Error(), "ProjectDir") {
		t.Errorf("error = %v, want contains 'ProjectDir'", err)
	}
}

func TestRunInstall_RequiresUserDirForUserScope(t *testing.T) {
	scope := ScopeUser
	_, err := RunInstall(InstallRequest{
		Skills:    []*Skill{mkSkill(t, "skill-a")},
		Scope:     &scope,
		TargetIDs: []string{"cursor"},
		Version:   "1.0.0",
	})
	if err == nil {
		t.Fatal("expected error for missing UserDir")
	}
	if !strings.Contains(err.Error(), "UserDir") {
		t.Errorf("error = %v, want contains 'UserDir'", err)
	}
}

func TestRunInstall_RequiresVersion(t *testing.T) {
	scope := ScopeProject
	_, err := RunInstall(InstallRequest{
		Skills:     []*Skill{mkSkill(t, "skill-a")},
		Scope:      &scope,
		ProjectDir: t.TempDir(),
		TargetIDs:  []string{AgentDirClaudeID},
	})
	if err == nil {
		t.Fatal("expected error for missing Version")
	}
}

func TestRunInstall_PrompterErrorPropagates(t *testing.T) {
	prompter := &fakePrompter{scopeErr: errors.New("user cancelled")}
	_, err := RunInstall(InstallRequest{
		Skills:     []*Skill{mkSkill(t, "skill-a")},
		ProjectDir: t.TempDir(),
		UserDir:    t.TempDir(),
		Version:    "1.0.0",
		Prompter:   prompter,
	})
	if err == nil || !strings.Contains(err.Error(), "user cancelled") {
		t.Errorf("error = %v, want contains 'user cancelled'", err)
	}
}

func TestRunInstall_NonInteractiveDefaultsToProjectScope(t *testing.T) {
	tmp := t.TempDir()
	res, err := RunInstall(InstallRequest{
		Skills:     []*Skill{mkSkill(t, "skill-a")},
		ProjectDir: tmp,
		TargetIDs:  []string{AgentDirClaudeID},
		Version:    "1.0.0",
	})
	if err != nil {
		t.Fatalf("RunInstall: %v", err)
	}
	if res.Scope != ScopeProject {
		t.Errorf("Scope = %v, want ScopeProject", res.Scope)
	}
}

func TestRunInstall_VersionGateAcrossRuns(t *testing.T) {
	// End-to-end: install at v1, then re-run at v0.5 (older) — second run
	// should be a no-op due to the version gate.
	tmp := t.TempDir()
	scope := ScopeProject
	skills := []*Skill{mkSkill(t, "skill-a")}

	_, err := RunInstall(InstallRequest{
		Skills: skills, Scope: &scope, ProjectDir: tmp,
		TargetIDs: []string{AgentDirClaudeID}, Version: "1.0.0",
	})
	if err != nil {
		t.Fatalf("first install: %v", err)
	}

	res, err := RunInstall(InstallRequest{
		Skills: skills, Scope: &scope, ProjectDir: tmp,
		TargetIDs: []string{AgentDirClaudeID}, Version: "0.5.0",
	})
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Status != StatusSkippedNewer {
		t.Errorf("expected StatusSkippedNewer on older re-run; got %+v", res.Actions)
	}

	// Force should override.
	res, err = RunInstall(InstallRequest{
		Skills: skills, Scope: &scope, ProjectDir: tmp,
		TargetIDs: []string{AgentDirClaudeID}, Version: "0.5.0", Force: true,
	})
	if err != nil {
		t.Fatalf("force install: %v", err)
	}
	if res.Actions[0].Status != StatusWritten {
		t.Errorf("expected StatusWritten with --force; got %+v", res.Actions)
	}
}
