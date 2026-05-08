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

func TestEmbedded_SubPagesLoaded(t *testing.T) {
	loaded, err := LoadAll(EmbeddedFS())
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	develop := FindByID(loaded, "metaplay-develop")
	if develop == nil {
		t.Fatal("metaplay-develop not found")
	}
	wantPages := map[string]bool{
		"main":              true,
		"review-actions":    true,
		"review-configs":    true,
		"review-models":     true,
		"incident-analysis": true,
	}
	for page := range wantPages {
		if _, ok := develop.SubPages[page]; !ok {
			t.Errorf("missing sub-page %q", page)
		}
	}
	if len(develop.SubPages) != len(wantPages) {
		t.Errorf("sub-page count = %d, want %d (%v)", len(develop.SubPages), len(wantPages), develop.SubPages)
	}

	docs := FindByID(loaded, "metaplay-docs")
	if docs == nil {
		t.Fatal("metaplay-docs not found")
	}
	if _, ok := docs.SubPages["main"]; !ok {
		t.Errorf("metaplay-docs missing sub-page \"main\"")
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

func TestEmbedded_ResolveSubPage(t *testing.T) {
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

func TestEmbedded_ResolveUnknownSubPage(t *testing.T) {
	loaded, _ := LoadAll(EmbeddedFS())
	_, err := Resolve(loaded, "metaplay-develop/nonexistent")
	if !errors.Is(err, ErrSubPageNotFound) {
		t.Errorf("expected ErrSubPageNotFound, got %v", err)
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
	}
}
