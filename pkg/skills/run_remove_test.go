/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunRemove_RemovesManagedWrapper(t *testing.T) {
	tmp := t.TempDir()
	scope := ScopeProject
	skills := []*Skill{mkSkill(t, "skill-a")}

	if _, err := RunInstall(InstallRequest{
		Skills: skills, Scope: &scope, ProjectDir: tmp,
		TargetIDs: []string{AgentDirClaudeID}, Version: "1.0.0",
	}); err != nil {
		t.Fatalf("seed install: %v", err)
	}
	target := filepath.Join(tmp, ".claude/skills/skill-a/SKILL.md")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("seed file missing: %v", err)
	}

	res, err := RunRemove(RemoveRequest{
		Scope: &scope, ProjectDir: tmp,
		TargetIDs: []string{AgentDirClaudeID},
	})
	if err != nil {
		t.Fatalf("RunRemove: %v", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Status != StatusRemoved {
		t.Errorf("expected one StatusRemoved, got %+v", res.Actions)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("file should be gone, stat err = %v", err)
	}
	// Empty parent dir cleaned up.
	if _, err := os.Stat(filepath.Dir(target)); !os.IsNotExist(err) {
		t.Errorf("parent skill dir should be gone, stat err = %v", err)
	}
}

func TestRunRemove_PreservesUserAuthored(t *testing.T) {
	tmp := t.TempDir()
	scope := ScopeProject
	target := filepath.Join(tmp, ".claude/skills/skill-a/SKILL.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	userFile := []byte("---\nname: skill-a\ndescription: hand-written\n---\nbody\n")
	if err := os.WriteFile(target, userFile, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := RunRemove(RemoveRequest{
		Scope: &scope, ProjectDir: tmp,
		TargetIDs: []string{AgentDirClaudeID},
	})
	if err != nil {
		t.Fatalf("RunRemove: %v", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Status != StatusRemoveSkippedUser {
		t.Errorf("expected StatusRemoveSkippedUser, got %+v", res.Actions)
	}
	got, _ := os.ReadFile(target)
	if string(got) != string(userFile) {
		t.Errorf("user file modified")
	}
}

func TestRunRemove_UserScopeWithTempHome(t *testing.T) {
	// Mirror of TestRunInstall_UserScopeWithTempHome — exercise the user
	// scope code path under a tempdir.
	fakeHome := t.TempDir()
	scope := ScopeUser
	skills := []*Skill{mkSkill(t, "skill-a")}

	if _, err := RunInstall(InstallRequest{
		Skills: skills, Scope: &scope, UserDir: fakeHome,
		TargetIDs: []string{"cursor"}, Version: "1.0.0",
	}); err != nil {
		t.Fatalf("seed install: %v", err)
	}
	target := filepath.Join(fakeHome, ".cursor/skills/skill-a/SKILL.md")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("seed file missing: %v", err)
	}

	res, err := RunRemove(RemoveRequest{
		Scope: &scope, UserDir: fakeHome,
		TargetIDs: []string{"cursor"},
	})
	if err != nil {
		t.Fatalf("RunRemove: %v", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Status != StatusRemoved {
		t.Errorf("expected StatusRemoved, got %+v", res.Actions)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("file should be gone")
	}
}

func TestRunRemove_NonInteractiveDetectionFallback(t *testing.T) {
	// With no TargetIDs and no Prompter, the orchestrator should default to
	// detected dirs. Pre-seed an install at standard, then remove with no
	// explicit target.
	tmp := t.TempDir()
	scope := ScopeProject
	skills := []*Skill{mkSkill(t, "skill-a")}
	if _, err := RunInstall(InstallRequest{
		Skills: skills, Scope: &scope, ProjectDir: tmp,
		TargetIDs: []string{AgentDirStandardID}, Version: "1.0.0",
	}); err != nil {
		t.Fatalf("seed install: %v", err)
	}

	res, err := RunRemove(RemoveRequest{
		Scope: &scope, ProjectDir: tmp,
	})
	if err != nil {
		t.Fatalf("RunRemove: %v", err)
	}
	var removed int
	for _, a := range res.Actions {
		if a.Status == StatusRemoved {
			removed++
		}
	}
	if removed != 1 {
		t.Errorf("expected 1 removal via detection, got %d (actions=%+v)", removed, res.Actions)
	}
}

func TestRunRemove_SpecificSkillID(t *testing.T) {
	tmp := t.TempDir()
	scope := ScopeProject
	skills := []*Skill{mkSkill(t, "skill-a"), mkSkill(t, "skill-b")}
	if _, err := RunInstall(InstallRequest{
		Skills: skills, Scope: &scope, ProjectDir: tmp,
		TargetIDs: []string{AgentDirClaudeID}, Version: "1.0.0",
	}); err != nil {
		t.Fatalf("seed install: %v", err)
	}

	// Remove only skill-a.
	_, err := RunRemove(RemoveRequest{
		Scope: &scope, ProjectDir: tmp,
		TargetIDs: []string{AgentDirClaudeID},
		SkillIDs:  []string{"skill-a"},
	})
	if err != nil {
		t.Fatalf("RunRemove: %v", err)
	}
	pathA := filepath.Join(tmp, ".claude/skills/skill-a/SKILL.md")
	pathB := filepath.Join(tmp, ".claude/skills/skill-b/SKILL.md")
	if _, err := os.Stat(pathA); !os.IsNotExist(err) {
		t.Errorf("skill-a should be gone, err = %v", err)
	}
	if _, err := os.Stat(pathB); err != nil {
		t.Errorf("skill-b should remain, err = %v", err)
	}
}
