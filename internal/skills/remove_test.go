/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func writeWrapper(t *testing.T, root string, agentDir, skillID string, raw []byte) string {
	t.Helper()
	dir := filepath.Join(root, agentDir, skillID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func managedWrapper(name, version string) []byte {
	return []byte("---\nname: " + name + "\nmetaplay-cli-version: " + version + "\nmanaged-by: metaplay-cli\n---\nbody\n")
}

func TestRemove_DeletesManagedWrapper(t *testing.T) {
	root := t.TempDir()
	path := writeWrapper(t, root, ".claude/skills", "skill-a", managedWrapper("skill-a", "1.0.0"))

	res, err := Remove(RemoveOptions{
		Targets: []AgentDir{claudeTarget()},
		RootDir: root,
		Scope:   ScopeProject,
	})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(res) != 1 || res[0].Status != StatusRemoved {
		t.Fatalf("expected one StatusRemoved, got %+v", res)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists: %v", err)
	}
}

func TestRemove_PreservesUserAuthored(t *testing.T) {
	root := t.TempDir()
	user := []byte("---\nname: skill-a\ndescription: hand-written\n---\nuser body\n")
	path := writeWrapper(t, root, ".claude/skills", "skill-a", user)

	res, err := Remove(RemoveOptions{
		Targets: []AgentDir{claudeTarget()},
		RootDir: root,
		Scope:   ScopeProject,
	})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if res[0].Status != StatusRemoveSkippedUser {
		t.Errorf("expected StatusRemoveSkippedUser, got %v (reason=%s)", res[0].Status, res[0].Reason)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("user file should still exist: %v", err)
	}
}

func TestRemove_AbsentSkill(t *testing.T) {
	root := t.TempDir()
	res, err := Remove(RemoveOptions{
		Targets:  []AgentDir{claudeTarget()},
		RootDir:  root,
		Scope:    ScopeProject,
		SkillIDs: []string{"missing-skill"},
	})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if res[0].Status != StatusRemoveSkippedNotFound {
		t.Errorf("expected StatusRemoveSkippedNotFound, got %v", res[0].Status)
	}
}

func TestRemove_FilterByID(t *testing.T) {
	root := t.TempDir()
	pathA := writeWrapper(t, root, ".claude/skills", "skill-a", managedWrapper("skill-a", "1.0.0"))
	pathB := writeWrapper(t, root, ".claude/skills", "skill-b", managedWrapper("skill-b", "1.0.0"))

	res, err := Remove(RemoveOptions{
		Targets:  []AgentDir{claudeTarget()},
		RootDir:  root,
		Scope:    ScopeProject,
		SkillIDs: []string{"skill-a"},
	})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected one action, got %d: %+v", len(res), res)
	}
	if res[0].SkillID != "skill-a" || res[0].Status != StatusRemoved {
		t.Errorf("expected skill-a removed, got %+v", res[0])
	}
	if _, err := os.Stat(pathA); !os.IsNotExist(err) {
		t.Errorf("skill-a still exists: %v", err)
	}
	if _, err := os.Stat(pathB); err != nil {
		t.Errorf("skill-b should still exist: %v", err)
	}
}

func TestRemove_RemovesEmptyParentDir(t *testing.T) {
	root := t.TempDir()
	path := writeWrapper(t, root, ".claude/skills", "skill-a", managedWrapper("skill-a", "1.0.0"))
	skillDir := filepath.Dir(path)

	if _, err := Remove(RemoveOptions{
		Targets: []AgentDir{claudeTarget()},
		RootDir: root,
		Scope:   ScopeProject,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Errorf("expected skill dir cleaned up, still exists: %v", err)
	}
}

func TestRemove_KeepsParentIfNonEmpty(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude/skills/skill-a")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), managedWrapper("skill-a", "1.0.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Drop a sibling file that should keep the dir alive.
	sibling := filepath.Join(skillDir, "user-notes.md")
	if err := os.WriteFile(sibling, []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(RemoveOptions{
		Targets: []AgentDir{claudeTarget()}, RootDir: root, Scope: ScopeProject,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skillDir); err != nil {
		t.Errorf("parent dir should remain (sibling present): %v", err)
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Errorf("sibling file should remain: %v", err)
	}
}

func TestRemove_DiscoversOrphanWhenNoFilter(t *testing.T) {
	// No SkillIDs filter and the canonical embedded set never includes
	// "old-skill" — Remove should still find and clean it up.
	root := t.TempDir()
	writeWrapper(t, root, ".claude/skills", "old-skill", managedWrapper("old-skill", "1.0.0"))

	res, err := Remove(RemoveOptions{
		Targets: []AgentDir{claudeTarget()},
		RootDir: root,
		Scope:   ScopeProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].SkillID != "old-skill" || res[0].Status != StatusRemoved {
		t.Errorf("orphan not handled correctly: %+v", res)
	}
}

func TestRemove_DedupesSharedDirs(t *testing.T) {
	root := t.TempDir()
	a := AgentDir{ID: "a", ProjectDir: ".shared/skills"}
	b := AgentDir{ID: "b", ProjectDir: ".shared/skills"}
	writeWrapper(t, root, ".shared/skills", "skill-a", managedWrapper("skill-a", "1.0.0"))

	res, err := Remove(RemoveOptions{
		Targets: []AgentDir{a, b}, RootDir: root, Scope: ScopeProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	var removed, dedup int
	for _, a := range res {
		switch a.Status {
		case StatusRemoved:
			removed++
		case StatusRemoveSkippedNotFound:
			dedup++
		}
	}
	if removed != 1 || dedup != 1 {
		t.Errorf("expected 1 removed + 1 deduped, got removed=%d dedup=%d (%+v)", removed, dedup, res)
	}
}
