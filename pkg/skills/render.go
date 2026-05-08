/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"bytes"
	"fmt"
	"strings"
)

// subSkillsMarker is the literal token a root page places (typically on its
// own line) to request an auto-rendered sub-skills section. The renderer
// substitutes the entire block — `## Sub-skills` heading and bullet list —
// or drops the line entirely if the skill has no listable sub-skills.
const subSkillsMarker = "{{subskills}}"

// RenderRootPage substitutes the {{subskills}} marker in a root page's content
// with an auto-generated sub-skills section. The expansion includes the
// `## Sub-skills` heading and a bullet list — one bullet per sub-skill, with
// the full `metaplay skills get <skill>/<page>` address and the sub-skill's
// frontmatter description. The pseudo-page `main` is excluded.
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
		// where the section would have been, not two.
		s = strings.ReplaceAll(s, "\n\n"+subSkillsMarker+"\n\n", "\n\n")
		s = strings.ReplaceAll(s, "\n"+subSkillsMarker+"\n", "\n")
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
		entries = append(entries, fmt.Sprintf("- `metaplay skills get %s/%s` — %s", skill.ID, p, desc))
	}
	if len(entries) == 0 {
		return ""
	}
	return "## Sub-skills\n\n" + strings.Join(entries, "\n")
}
