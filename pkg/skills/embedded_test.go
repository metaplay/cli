/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"errors"
	"testing"
)

// These tests verify that the skill payload bundled with this package
// loads cleanly and meets the constraints AI coding harnesses impose
// (e.g. description length). The engine itself is tested separately against
// synthetic fstest.MapFS data in skill_test.go.

func TestEmbedded_LoadsBundledSkills(t *testing.T) {
	loaded, err := LoadAll(EmbeddedFS())
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(loaded))
	}
	wantIDs := []string{"metaplay-develop", "metaplay-docs", "metaplay-troubleshoot"}
	for i, want := range wantIDs {
		if loaded[i].ID != want {
			t.Errorf("skill[%d].ID = %q, want %q", i, loaded[i].ID, want)
		}
		if loaded[i].Frontmatter.Name() != want {
			t.Errorf("skill[%d].Frontmatter.Name() = %q, want %q", i, loaded[i].Frontmatter.Name(), want)
		}
	}
}

func TestEmbedded_SubSkillsLoaded(t *testing.T) {
	loaded, err := LoadAll(EmbeddedFS())
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	develop := FindByID(loaded, "metaplay-develop")
	if develop == nil {
		t.Fatal("metaplay-develop not found")
	}
	wantSubSkills := map[string]bool{
		"main":              true,
		"review-actions":    true,
		"review-configs":    true,
		"review-models":     true,
		"incident-analysis": true,
		"update-sdk":        true,
	}
	for subSkillID := range wantSubSkills {
		if _, ok := develop.SubSkills[subSkillID]; !ok {
			t.Errorf("missing sub-skill %q", subSkillID)
		}
	}
	if len(develop.SubSkills) != len(wantSubSkills) {
		t.Errorf("sub-skill count = %d, want %d (%v)", len(develop.SubSkills), len(wantSubSkills), develop.SubSkills)
	}

	docs := FindByID(loaded, "metaplay-docs")
	if docs == nil {
		t.Fatal("metaplay-docs not found")
	}
	if _, ok := docs.SubSkills["main"]; !ok {
		t.Errorf("metaplay-docs missing sub-skill \"main\"")
	}
}

func TestEmbedded_ResolveSkillRoot(t *testing.T) {
	loaded, _ := LoadAll(EmbeddedFS())
	got, err := Resolve(loaded, "metaplay-docs")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) == 0 {
		t.Errorf("empty content")
	}
}

func TestEmbedded_ResolveSubSkill(t *testing.T) {
	loaded, _ := LoadAll(EmbeddedFS())
	got, err := Resolve(loaded, "metaplay-develop/review-models")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) == 0 {
		t.Errorf("empty content")
	}
}

func TestEmbedded_ResolveUnknownSkill(t *testing.T) {
	loaded, _ := LoadAll(EmbeddedFS())
	_, err := Resolve(loaded, "nonexistent")
	if !errors.Is(err, ErrSkillNotFound) {
		t.Errorf("expected ErrSkillNotFound, got %v", err)
	}
}

func TestEmbedded_ResolveUnknownSubSkill(t *testing.T) {
	loaded, _ := LoadAll(EmbeddedFS())
	_, err := Resolve(loaded, "metaplay-develop/nonexistent")
	if !errors.Is(err, ErrSubSkillNotFound) {
		t.Errorf("expected ErrSubSkillNotFound, got %v", err)
	}
}

func TestEmbedded_DescriptionUnderLimit(t *testing.T) {
	loaded, err := LoadAll(EmbeddedFS())
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	for _, s := range loaded {
		desc := s.Frontmatter.Description()
		if len(desc) > MaxDescriptionLength {
			t.Errorf("skill %q: description is %d chars, exceeds %d-char limit (Codex CLI rejects, Claude Code warns)",
				s.ID, len(desc), MaxDescriptionLength)
		}
		for subSkillID, sub := range s.SubSkills {
			if subSkillID == "main" {
				continue
			}
			d := sub.Frontmatter.Description()
			if len(d) > MaxDescriptionLength {
				t.Errorf("sub-skill %s/%s: description is %d chars, exceeds %d-char limit (matters if ever promoted to a standalone skill)",
					s.ID, subSkillID, len(d), MaxDescriptionLength)
			}
		}
	}
}
