/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"strings"
	"testing"
	"testing/fstest"
)

func mkSubSkill(t *testing.T, frontmatterDescription string, body string) *SubSkill {
	t.Helper()
	if frontmatterDescription == "" {
		raw := []byte(body)
		return &SubSkill{Frontmatter: &Frontmatter{}, Body: raw, Raw: raw}
	}
	raw := []byte("---\ndescription: " + frontmatterDescription + "\n---\n" + body)
	fm, b, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("seed parse: %v", err)
	}
	return &SubSkill{Frontmatter: fm, Body: b, Raw: raw}
}

func TestRenderRootPage_PassThroughWithoutMarker(t *testing.T) {
	skill := &Skill{
		ID: "alpha",
		SubSkills: map[string]*SubSkill{
			"extra": mkSubSkill(t, "extra description", "body\n"),
		},
	}
	in := []byte("# Alpha\n\nNo marker here.\n")
	got := RenderRootPage(skill, in)
	if string(got) != string(in) {
		t.Errorf("expected pass-through, got %q", string(got))
	}
}

func TestRenderRootPage_ExpandsWithSubSkills(t *testing.T) {
	skill := &Skill{
		ID: "alpha",
		SubSkills: map[string]*SubSkill{
			"main":  mkSubSkill(t, "", "alpha root\n"),
			"one":   mkSubSkill(t, "First sub-skill.", "body\n"),
			"two":   mkSubSkill(t, "Second sub-skill.", "body\n"),
			"three": mkSubSkill(t, "Third sub-skill.", "body\n"),
		},
	}
	in := []byte("# Alpha\n\nIntro.\n\n{{subskills}}\n\nOutro.\n")
	got := string(RenderRootPage(skill, in))

	if strings.Contains(got, "{{subskills}}") {
		t.Errorf("marker not substituted: %q", got)
	}
	if !strings.Contains(got, "## Sub-skills") {
		t.Errorf("missing section header in output: %q", got)
	}
	wantBullets := []string{
		"- `metaplay skills get alpha-one` — First sub-skill.",
		"- `metaplay skills get alpha-three` — Third sub-skill.",
		"- `metaplay skills get alpha-two` — Second sub-skill.",
	}
	for _, b := range wantBullets {
		if !strings.Contains(got, b) {
			t.Errorf("missing bullet %q in output: %q", b, got)
		}
	}
	if strings.Contains(got, "alpha-main") {
		t.Errorf("main should be excluded from sub-skills list, got %q", got)
	}
	if i := strings.Index(got, "alpha-one"); i < 0 || strings.Index(got, "alpha-three") < i {
		t.Errorf("sub-skills should be sorted alphabetically: %q", got)
	}
}

func TestRenderRootPage_DropsMarkerWhenEmpty(t *testing.T) {
	skill := &Skill{
		ID: "alpha",
		SubSkills: map[string]*SubSkill{
			"main": mkSubSkill(t, "", "alpha root\n"),
		},
	}
	in := []byte("# Alpha\n\nIntro.\n\n{{subskills}}\n\nOutro.\n")
	got := string(RenderRootPage(skill, in))

	if strings.Contains(got, "{{subskills}}") {
		t.Errorf("marker should be removed when empty: %q", got)
	}
	if strings.Contains(got, "## Sub-skills") {
		t.Errorf("empty list should not emit a heading: %q", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("dropped marker left a triple newline: %q", got)
	}
	if !strings.Contains(got, "Intro.\n\nOutro.\n") {
		t.Errorf("expected intro/outro separated by single blank line, got %q", got)
	}
}

func TestRenderRootPage_DropsMarkerWhenEmptyCRLF(t *testing.T) {
	skill := &Skill{
		ID: "alpha",
		SubSkills: map[string]*SubSkill{
			"main": mkSubSkill(t, "", "alpha root\r\n"),
		},
	}
	in := []byte("# Alpha\r\n\r\nIntro.\r\n\r\n{{subskills}}\r\n\r\nOutro.\r\n")
	got := string(RenderRootPage(skill, in))

	if strings.Contains(got, "{{subskills}}") {
		t.Errorf("marker should be removed when empty: %q", got)
	}
	if strings.Contains(got, "## Sub-skills") {
		t.Errorf("empty list should not emit a heading: %q", got)
	}
	if !strings.Contains(got, "Intro.\r\n\r\nOutro.\r\n") {
		t.Errorf("expected intro/outro separated by single CRLF blank line, got %q", got)
	}
}

func TestRenderRootPage_FallbackForMissingDescription(t *testing.T) {
	skill := &Skill{
		ID: "alpha",
		SubSkills: map[string]*SubSkill{
			"orphan": {Frontmatter: &Frontmatter{}, Body: []byte("body"), Raw: []byte("body")},
		},
	}
	in := []byte("{{subskills}}\n")
	got := string(RenderRootPage(skill, in))
	if !strings.Contains(got, "(no description)") {
		t.Errorf("expected fallback placeholder, got %q", got)
	}
}

func TestLoadAll_RejectsSubSkillWithoutDescription(t *testing.T) {
	mock := fstest.MapFS{
		"alpha/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: alpha\ndescription: Alpha skill description.\n---\nbody\n"),
		},
		"alpha/orphan.md": &fstest.MapFile{
			Data: []byte("no frontmatter here\n"),
		},
	}
	_, err := LoadAll(mock)
	if err == nil {
		t.Errorf("expected error for non-main sub-skill without description")
	}
}
