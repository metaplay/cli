/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// subSkillsMarker is the literal token a root page places (typically on its
// own line) to request an auto-rendered sub-skills section. The renderer
// substitutes the entire block — `## Sub-skills` heading and bullet list —
// or drops the line entirely if the skill has no listable sub-skills.
const subSkillsMarker = "{{subskills}}"

// Both patterns capture the leading newline group so the replacement preserves
// the original line-ending style (LF vs CRLF). markerParagraph also collapses
// the surrounding blank lines so a marker that sat in its own paragraph leaves
// exactly one blank line behind, not two.
var (
	markerParagraph = regexp.MustCompile(`(\r?\n\r?\n)` + regexp.QuoteMeta(subSkillsMarker) + `\r?\n\r?\n`)
	markerLine      = regexp.MustCompile(`(\r?\n)` + regexp.QuoteMeta(subSkillsMarker) + `\r?\n`)
)

// RenderRootPage substitutes the {{subskills}} marker in a root page's content
// with an auto-generated sub-skills section. The expansion includes the
// `## Sub-skills` heading and a bullet list — one bullet per sub-skill, with
// the full `metaplay skills get <skill>-<sub-skill>` address and the
// sub-skill's frontmatter description. The `main` entry (the root body
// itself) is excluded.
//
// If the skill has no listable sub-skills, the marker (and the line it sits
// on) is removed entirely so the root page does not emit an empty heading.
//
// Files without the marker pass through unchanged.
func RenderRootPage(skill *Skill, content []byte) []byte {
	if !bytes.Contains(content, []byte(subSkillsMarker)) {
		return content
	}
	section := buildSubSkillsSection(skill)
	s := string(content)
	if section == "" {
		// Drop the marker. Collapse a marker that sits alone on a line
		// surrounded by blank lines so the result has one blank line
		// where the section would have been, not two. Patterns are
		// LF/CRLF-tolerant so Windows-authored content is handled too;
		// the leading newline group is preserved when collapsing a
		// paragraph, which keeps the original line-ending style.
		s = markerParagraph.ReplaceAllString(s, "$1")
		s = markerLine.ReplaceAllString(s, "$1")
		s = strings.ReplaceAll(s, subSkillsMarker, "")
	} else {
		s = strings.ReplaceAll(s, subSkillsMarker, section)
	}
	return []byte(s)
}

func buildSubSkillsSection(skill *Skill) string {
	var entries []string
	for _, p := range skill.SubSkillNames() {
		if p == "main" {
			continue
		}
		sub := skill.SubSkills[p]
		desc := strings.ReplaceAll(sub.Frontmatter.Description(), "\n", " ")
		if desc == "" {
			desc = "(no description)"
		}
		entries = append(entries, fmt.Sprintf("- `metaplay skills get %s-%s` — %s", skill.ID, p, desc))
	}
	if len(entries) == 0 {
		return ""
	}
	return "## Sub-skills\n\n" + strings.Join(entries, "\n")
}
