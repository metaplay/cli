/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"errors"
	"testing"
	"testing/fstest"
)

func TestLoadAll_LoadsEmbeddedSkills(t *testing.T) {
	skills, err := LoadAll(OpenFS())
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(skills) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(skills))
	}
	wantIDs := []string{"metaplay-develop", "metaplay-devops", "metaplay-docs"}
	for i, want := range wantIDs {
		if skills[i].ID != want {
			t.Errorf("skill[%d].ID = %q, want %q", i, skills[i].ID, want)
		}
		if skills[i].Frontmatter.Name() != want {
			t.Errorf("skill[%d].Frontmatter.Name() = %q, want %q", i, skills[i].Frontmatter.Name(), want)
		}
	}
}

func TestLoadAll_SubPagesLoaded(t *testing.T) {
	skills, err := LoadAll(OpenFS())
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	develop := FindByID(skills, "metaplay-develop")
	if develop == nil {
		t.Fatal("metaplay-develop not found")
	}
	wantPages := map[string]bool{
		"review-actions": true,
		"review-configs": true,
		"review-models":  true,
	}
	for page := range wantPages {
		if _, ok := develop.SubPages[page]; !ok {
			t.Errorf("missing sub-page %q", page)
		}
	}
	if len(develop.SubPages) != len(wantPages) {
		t.Errorf("sub-page count = %d, want %d (%v)", len(develop.SubPages), len(wantPages), develop.SubPages)
	}

	devops := FindByID(skills, "metaplay-devops")
	if devops == nil {
		t.Fatal("metaplay-devops not found")
	}
	if _, ok := devops.SubPages["incident-analysis"]; !ok {
		t.Errorf("missing incident-analysis sub-page")
	}

	docs := FindByID(skills, "metaplay-docs")
	if docs == nil {
		t.Fatal("metaplay-docs not found")
	}
	if len(docs.SubPages) != 0 {
		t.Errorf("metaplay-docs should have no sub-pages, got %v", docs.SubPages)
	}
}

func TestResolve_SkillRoot(t *testing.T) {
	skills, _ := LoadAll(OpenFS())
	got, err := Resolve(skills, "metaplay-docs")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) == 0 {
		t.Errorf("empty content")
	}
}

func TestResolve_SubPage(t *testing.T) {
	skills, _ := LoadAll(OpenFS())
	got, err := Resolve(skills, "metaplay-develop/review-models")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) == 0 {
		t.Errorf("empty content")
	}
}

func TestResolve_UnknownSkill(t *testing.T) {
	skills, _ := LoadAll(OpenFS())
	_, err := Resolve(skills, "nonexistent")
	if !errors.Is(err, ErrSkillNotFound) {
		t.Errorf("expected ErrSkillNotFound, got %v", err)
	}
}

func TestResolve_UnknownSubPage(t *testing.T) {
	skills, _ := LoadAll(OpenFS())
	_, err := Resolve(skills, "metaplay-develop/nonexistent")
	if !errors.Is(err, ErrSubPageNotFound) {
		t.Errorf("expected ErrSubPageNotFound, got %v", err)
	}
}

func TestLoadAll_RejectsNameMismatch(t *testing.T) {
	mock := fstest.MapFS{
		"badskill/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: not-the-dir\ndescription: x\n---\nbody\n"),
		},
	}
	_, err := LoadAll(mock)
	if err == nil {
		t.Errorf("expected error on name mismatch")
	}
}
