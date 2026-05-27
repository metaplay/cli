/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"errors"
	"testing"
	"testing/fstest"
)

// fakeSkillFS returns an in-memory tree with three skills, each shaped like
// the bundled Metaplay set, so engine-level tests don't depend on any
// product-specific data.
func fakeSkillFS() fstest.MapFS {
	return fstest.MapFS{
		"alpha/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: alpha\ndescription: Alpha skill description.\n---\nbody\n"),
		},
		"alpha/main.md": &fstest.MapFile{
			Data: []byte("alpha main page\n"),
		},
		"alpha/extra.md": &fstest.MapFile{
			Data: []byte("---\nname: alpha-extra\ndescription: Alpha extra sub-skill description.\n---\nalpha extra page\n"),
		},
		"beta/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: beta\ndescription: Beta skill description.\n---\nbody\n"),
		},
		"beta/main.md": &fstest.MapFile{
			Data: []byte("beta main page\n"),
		},
		"gamma/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: gamma\ndescription: Gamma skill description.\n---\nbody\n"),
		},
	}
}

func TestLoadAll_LoadsAllSkills(t *testing.T) {
	loaded, err := LoadAll(fakeSkillFS())
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(loaded))
	}
	wantIDs := []string{"alpha", "beta", "gamma"}
	for i, want := range wantIDs {
		if loaded[i].ID != want {
			t.Errorf("skill[%d].ID = %q, want %q", i, loaded[i].ID, want)
		}
		if loaded[i].Frontmatter.Name() != want {
			t.Errorf("skill[%d].Frontmatter.Name() = %q, want %q", i, loaded[i].Frontmatter.Name(), want)
		}
	}
}

func TestLoadAll_SubSkillsLoaded(t *testing.T) {
	loaded, err := LoadAll(fakeSkillFS())
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	alpha := FindByID(loaded, "alpha")
	if alpha == nil {
		t.Fatal("alpha not found")
	}
	wantSubSkills := map[string]bool{"main": true, "extra": true}
	for subSkillID := range wantSubSkills {
		if _, ok := alpha.SubSkills[subSkillID]; !ok {
			t.Errorf("missing sub-skill %q", subSkillID)
		}
	}
	if len(alpha.SubSkills) != len(wantSubSkills) {
		t.Errorf("sub-skill count = %d, want %d (%v)", len(alpha.SubSkills), len(wantSubSkills), alpha.SubSkills)
	}

	gamma := FindByID(loaded, "gamma")
	if gamma == nil {
		t.Fatal("gamma not found")
	}
	if len(gamma.SubSkills) != 0 {
		t.Errorf("gamma should have no sub-skills, got %v", gamma.SubSkills)
	}
}

func TestResolve_SkillRoot(t *testing.T) {
	loaded, _ := LoadAll(fakeSkillFS())
	got, err := Resolve(loaded, "alpha")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) == 0 {
		t.Errorf("empty content")
	}
}

func TestResolve_SubSkill(t *testing.T) {
	loaded, _ := LoadAll(fakeSkillFS())
	got, err := Resolve(loaded, "alpha/extra")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) == 0 {
		t.Errorf("empty content")
	}
}

func TestResolve_UnknownSkill(t *testing.T) {
	loaded, _ := LoadAll(fakeSkillFS())
	_, err := Resolve(loaded, "nonexistent")
	if !errors.Is(err, ErrSkillNotFound) {
		t.Errorf("expected ErrSkillNotFound, got %v", err)
	}
}

func TestResolve_UnknownSubSkill(t *testing.T) {
	loaded, _ := LoadAll(fakeSkillFS())
	_, err := Resolve(loaded, "alpha/nonexistent")
	if !errors.Is(err, ErrSubSkillNotFound) {
		t.Errorf("expected ErrSubSkillNotFound, got %v", err)
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

func TestLoadAll_RejectsSKILLWithoutName(t *testing.T) {
	mock := fstest.MapFS{
		"nameless/SKILL.md": &fstest.MapFile{
			Data: []byte("---\ndescription: missing the name field.\n---\nbody\n"),
		},
	}
	_, err := LoadAll(mock)
	if err == nil {
		t.Errorf("expected error when SKILL.md has no name field")
	}
}

func TestLoadAll_RejectsSubSkillWithoutName(t *testing.T) {
	mock := fstest.MapFS{
		"alpha/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: alpha\ndescription: Alpha skill description.\n---\nbody\n"),
		},
		"alpha/extra.md": &fstest.MapFile{
			Data: []byte("---\ndescription: Sub-skill with description but no name.\n---\nbody\n"),
		},
	}
	_, err := LoadAll(mock)
	if err == nil {
		t.Errorf("expected error when sub-skill frontmatter has no name field")
	}
}

func TestLoadAll_RejectsSubSkillWithWrongName(t *testing.T) {
	mock := fstest.MapFS{
		"alpha/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: alpha\ndescription: Alpha skill description.\n---\nbody\n"),
		},
		"alpha/extra.md": &fstest.MapFile{
			Data: []byte("---\nname: not-alpha-extra\ndescription: Sub-skill with mismatched name.\n---\nbody\n"),
		},
	}
	_, err := LoadAll(mock)
	if err == nil {
		t.Errorf("expected error when sub-skill name does not equal <parent>-<id>")
	}
}
