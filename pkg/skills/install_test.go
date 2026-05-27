/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper: a minimal in-memory skill for install tests.
func mkSkill(t *testing.T, id string) *Skill {
	t.Helper()
	raw := []byte("---\nname: " + id + "\ndescription: test skill\n---\nbody for " + id + "\n")
	fm, body, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("seed parse: %v", err)
	}
	return &Skill{
		ID:          id,
		Frontmatter: fm,
		Body:        body,
		RawSKILL:    raw,
		SubSkills:    map[string]*SubSkill{},
	}
}

func claudeTarget() AgentDir {
	return AgentDir{
		ID:          AgentDirClaudeID,
		DisplayName: "Claude Code",
		ProjectDir:  ".claude/skills",
		UserDir:     ".claude/skills",
	}
}

func standardTarget() AgentDir {
	return AgentDir{
		ID:          AgentDirStandardID,
		DisplayName: "Standard",
		ProjectDir:  ".agents/skills",
		UserDir:     ".agents/skills",
	}
}

func TestInstall_FreshWrite(t *testing.T) {
	root := t.TempDir()
	skill := mkSkill(t, "skill-a")
	res, err := Install(InstallOptions{
		Skills:  []*Skill{skill},
		Targets: []AgentDir{claudeTarget()},
		RootDir: root,
		Scope:   ScopeProject,
		Version: "1.0.0",
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(res) != 1 || res[0].Status != StatusWritten {
		t.Fatalf("expected 1 written action, got %+v", res)
	}
	expected := filepath.Join(root, ".claude/skills/skill-a/SKILL.md")
	bs, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if !strings.Contains(string(bs), "metaplay-cli-version: 1.0.0") {
		t.Errorf("missing version stamp; got:\n%s", bs)
	}
	if !strings.Contains(string(bs), "managed-by: metaplay-cli") {
		t.Errorf("missing managed-by; got:\n%s", bs)
	}
	if !strings.Contains(string(bs), "body for skill-a") {
		t.Errorf("missing body; got:\n%s", bs)
	}
}

func TestInstall_SkipsNewerOnDisk(t *testing.T) {
	root := t.TempDir()
	skill := mkSkill(t, "skill-a")
	target := filepath.Join(root, ".claude/skills/skill-a/SKILL.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	preexisting := []byte("---\nname: skill-a\ndescription: newer\nmetaplay-cli-version: 2.0.0\nmanaged-by: metaplay-cli\n---\nnew body\n")
	if err := os.WriteFile(target, preexisting, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Install(InstallOptions{
		Skills:  []*Skill{skill},
		Targets: []AgentDir{claudeTarget()},
		RootDir: root,
		Scope:   ScopeProject,
		Version: "1.0.0",
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res[0].Status != StatusSkippedNewer {
		t.Errorf("expected StatusSkippedNewer, got %v (reason=%s)", res[0].Status, res[0].Reason)
	}
	got, _ := os.ReadFile(target)
	if string(got) != string(preexisting) {
		t.Errorf("file should not have been overwritten; got:\n%s", got)
	}
}

func TestInstall_OverwritesOlderOnDisk(t *testing.T) {
	root := t.TempDir()
	skill := mkSkill(t, "skill-a")
	target := filepath.Join(root, ".claude/skills/skill-a/SKILL.md")
	_ = os.MkdirAll(filepath.Dir(target), 0o755)
	preexisting := []byte("---\nname: skill-a\ndescription: older\nmetaplay-cli-version: 0.9.0\nmanaged-by: metaplay-cli\n---\nold body\n")
	_ = os.WriteFile(target, preexisting, 0o644)
	res, err := Install(InstallOptions{
		Skills:  []*Skill{skill},
		Targets: []AgentDir{claudeTarget()},
		RootDir: root,
		Scope:   ScopeProject,
		Version: "1.0.0",
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res[0].Status != StatusWritten {
		t.Errorf("expected StatusWritten, got %v", res[0].Status)
	}
	got, _ := os.ReadFile(target)
	if !strings.Contains(string(got), "metaplay-cli-version: 1.0.0") {
		t.Errorf("expected new version stamp; got:\n%s", got)
	}
}

func TestInstall_PreservesUserAuthored(t *testing.T) {
	root := t.TempDir()
	skill := mkSkill(t, "skill-a")
	target := filepath.Join(root, ".claude/skills/skill-a/SKILL.md")
	_ = os.MkdirAll(filepath.Dir(target), 0o755)
	userFile := []byte("---\nname: skill-a\ndescription: hand-written by the user\n---\ncustom body\n")
	_ = os.WriteFile(target, userFile, 0o644)
	res, err := Install(InstallOptions{
		Skills:  []*Skill{skill},
		Targets: []AgentDir{claudeTarget()},
		RootDir: root,
		Scope:   ScopeProject,
		Version: "1.0.0",
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res[0].Status != StatusSkippedUser {
		t.Errorf("expected StatusSkippedUser, got %v", res[0].Status)
	}
	got, _ := os.ReadFile(target)
	if string(got) != string(userFile) {
		t.Errorf("user file modified; got:\n%s", got)
	}
}

func TestInstall_ForceBypassesGate(t *testing.T) {
	root := t.TempDir()
	skill := mkSkill(t, "skill-a")
	target := filepath.Join(root, ".claude/skills/skill-a/SKILL.md")
	_ = os.MkdirAll(filepath.Dir(target), 0o755)
	preexisting := []byte("---\nname: skill-a\nmetaplay-cli-version: 999.0.0\nmanaged-by: metaplay-cli\n---\nfuture body\n")
	_ = os.WriteFile(target, preexisting, 0o644)
	res, err := Install(InstallOptions{
		Skills:  []*Skill{skill},
		Targets: []AgentDir{claudeTarget()},
		RootDir: root,
		Scope:   ScopeProject,
		Version: "1.0.0",
		Force:   true,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res[0].Status != StatusWritten {
		t.Errorf("expected StatusWritten with --force, got %v", res[0].Status)
	}
}

func TestInstall_DevModeBypassesGate(t *testing.T) {
	root := t.TempDir()
	skill := mkSkill(t, "skill-a")
	target := filepath.Join(root, ".claude/skills/skill-a/SKILL.md")
	_ = os.MkdirAll(filepath.Dir(target), 0o755)
	preexisting := []byte("---\nname: skill-a\nmetaplay-cli-version: 1.0.0\nmanaged-by: metaplay-cli\n---\nold body\n")
	_ = os.WriteFile(target, preexisting, 0o644)
	res, err := Install(InstallOptions{
		Skills:  []*Skill{skill},
		Targets: []AgentDir{claudeTarget()},
		RootDir: root,
		Scope:   ScopeProject,
		Version: "dev",
		DevMode: true,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res[0].Status != StatusWritten {
		t.Errorf("expected StatusWritten in dev mode, got %v (reason=%s)", res[0].Status, res[0].Reason)
	}
}

func TestInstall_UnchangedWhenIdentical(t *testing.T) {
	root := t.TempDir()
	skill := mkSkill(t, "skill-a")

	// First write.
	_, err := Install(InstallOptions{
		Skills: []*Skill{skill}, Targets: []AgentDir{claudeTarget()},
		RootDir: root, Scope: ScopeProject, Version: "1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Second write — should be no-op.
	res, _ := Install(InstallOptions{
		Skills: []*Skill{skill}, Targets: []AgentDir{claudeTarget()},
		RootDir: root, Scope: ScopeProject, Version: "1.0.0",
	})
	if res[0].Status != StatusUnchanged {
		t.Errorf("expected StatusUnchanged, got %v (reason=%s)", res[0].Status, res[0].Reason)
	}
}

func TestInstall_DedupesSharedDirs(t *testing.T) {
	// Two AgentDir entries that resolve to the same path should write once
	// and report the second as shared.
	root := t.TempDir()
	skill := mkSkill(t, "skill-a")
	a := AgentDir{ID: "a", ProjectDir: ".shared/skills", UserDir: ".shared/skills"}
	b := AgentDir{ID: "b", ProjectDir: ".shared/skills", UserDir: ".other/skills"}
	res, err := Install(InstallOptions{
		Skills: []*Skill{skill}, Targets: []AgentDir{a, b},
		RootDir: root, Scope: ScopeProject, Version: "1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	var written, shared int
	for _, a := range res {
		switch a.Status {
		case StatusWritten:
			written++
		case StatusUnchanged:
			shared++
		}
	}
	if written != 1 || shared != 1 {
		t.Errorf("expected 1 written + 1 shared; got %d written, %d shared, actions=%+v", written, shared, res)
	}
}

func TestInstall_ScopeUserUsesUserDir(t *testing.T) {
	// AgentDir with distinct ProjectDir vs UserDir — verify user scope picks UserDir.
	root := t.TempDir()
	skill := mkSkill(t, "skill-a")
	target := AgentDir{ID: "x", ProjectDir: ".agents/skills", UserDir: ".elsewhere/skills"}
	res, err := Install(InstallOptions{
		Skills: []*Skill{skill}, Targets: []AgentDir{target},
		RootDir: root, Scope: ScopeUser, Version: "1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Status != StatusWritten {
		t.Fatalf("status = %v", res[0].Status)
	}
	expected := filepath.Join(root, ".elsewhere/skills/skill-a/SKILL.md")
	if _, err := os.Stat(expected); err != nil {
		t.Errorf("expected file %s: %v", expected, err)
	}
}

func TestInstall_RejectsEmptyOptions(t *testing.T) {
	if _, err := Install(InstallOptions{}); err == nil {
		t.Error("expected error for empty options")
	}
}
