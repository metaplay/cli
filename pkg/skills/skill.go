/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Skill is one Metaplay agent skill loaded from the embedded (or dev-disk)
// data tree. The directory name is treated as the canonical ID; the
// frontmatter `name` field should match.
type Skill struct {
	// ID is the directory name and the address users type
	// (e.g. "metaplay-develop").
	ID string

	// Frontmatter is the parsed YAML header of SKILL.md.
	Frontmatter *Frontmatter

	// Body is the markdown after the frontmatter, byte-for-byte.
	Body []byte

	// RawSKILL is the full SKILL.md exactly as it appears in the source
	// (frontmatter + body), used when serving `metaplay skills get <id>`.
	RawSKILL []byte

	// SubSkills maps the local sub-skill name (filename without `.md`) to
	// the parsed sub-skill.
	SubSkills map[string]*SubSkill
}

// SubSkill is one `.md` file inside a skill directory other than SKILL.md.
// Non-`main` sub-skills must carry a YAML frontmatter block with a
// `description` field — they are listed by description in `skills list` and
// in the auto-rendered "Sub-skills" section of the parent root page.
// `main.md` is the root body and is allowed to omit frontmatter.
type SubSkill struct {
	// Frontmatter is the parsed YAML header. Empty (no items) when the
	// file has no frontmatter block.
	Frontmatter *Frontmatter
	// Body is the markdown after the frontmatter. Equal to Raw when no
	// frontmatter block is present.
	Body []byte
	// Raw is the full file as it appears on disk (frontmatter + body),
	// returned by Resolve so callers see the file verbatim.
	Raw []byte
}

// SubSkillNames returns the sub-skill names sorted alphabetically.
func (s *Skill) SubSkillNames() []string {
	names := make([]string, 0, len(s.SubSkills))
	for k := range s.SubSkills {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// LoadAll walks rootFS as the skills data root: each top-level directory
// must contain a SKILL.md, and any other `.md` files in that directory are
// loaded as sub-skills.
//
// rootFS is the skill payload root — pass EmbeddedFS() to load the bundle
// shipped with this package, or any fs.FS for tests/custom payloads.
func LoadAll(rootFS fs.FS) ([]*Skill, error) {
	entries, err := fs.ReadDir(rootFS, ".")
	if err != nil {
		return nil, fmt.Errorf("read skills root: %w", err)
	}
	var skills []*Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skill, err := loadSkill(rootFS, e.Name())
		if err != nil {
			return nil, err
		}
		skills = append(skills, skill)
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].ID < skills[j].ID })
	return skills, nil
}

func loadSkill(rootFS fs.FS, id string) (*Skill, error) {
	skillPath := path.Join(id, "SKILL.md")
	raw, err := fs.ReadFile(rootFS, skillPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", skillPath, err)
	}
	fm, body, err := ParseFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", skillPath, err)
	}
	if fm.Name() == "" {
		return nil, fmt.Errorf("skill %s: frontmatter must include a non-empty name field", id)
	}
	if fm.Name() != id {
		return nil, fmt.Errorf("skill %s: frontmatter name %q does not match directory name", id, fm.Name())
	}

	skill := &Skill{
		ID:          id,
		Frontmatter: fm,
		Body:        body,
		RawSKILL:    raw,
		SubSkills:    map[string]*SubSkill{},
	}

	entries, err := fs.ReadDir(rootFS, id)
	if err != nil {
		return nil, fmt.Errorf("read skill dir %s: %w", id, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		if name == "SKILL.md" || !strings.HasSuffix(name, ".md") {
			continue
		}
		page := strings.TrimSuffix(name, ".md")
		raw, err := fs.ReadFile(rootFS, path.Join(id, name))
		if err != nil {
			return nil, fmt.Errorf("read sub-skill %s/%s: %w", id, name, err)
		}
		sub := &SubSkill{Raw: raw}
		if HasFrontmatter(raw) {
			subFM, subBody, err := ParseFrontmatter(raw)
			if err != nil {
				return nil, fmt.Errorf("parse %s/%s: %w", id, name, err)
			}
			sub.Frontmatter = subFM
			sub.Body = subBody
		} else {
			sub.Frontmatter = &Frontmatter{}
			sub.Body = raw
		}
		// `main` is the root body; description lives in the parent SKILL.md.
		// All other sub-skills are listed by description, and their `name`
		// must equal the standalone-skill identifier (`<parent>-<sub>`)
		// they would adopt if ever promoted to a top-level skill.
		if page != "main" {
			if sub.Frontmatter.Description() == "" {
				return nil, fmt.Errorf("sub-skill %s/%s: must have YAML frontmatter with a description field", id, name)
			}
			expectedName := id + "-" + page
			if sub.Frontmatter.Name() != expectedName {
				return nil, fmt.Errorf("sub-skill %s/%s: frontmatter name %q must equal %q", id, name, sub.Frontmatter.Name(), expectedName)
			}
		}
		skill.SubSkills[page] = sub
	}

	return skill, nil
}

// FindByID returns the skill with the given ID, or nil if absent.
func FindByID(skills []*Skill, id string) *Skill {
	for _, s := range skills {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// ErrSkillNotFound is returned by Resolve when the address names an unknown
// skill.
var ErrSkillNotFound = errors.New("skill not found")

// ErrSubSkillNotFound is returned by Resolve when the address names a known
// skill but an unknown sub-skill within it.
var ErrSubSkillNotFound = errors.New("sub-skill not found")

// Resolve looks up content by an address of the form `<skill>` or
// `<skill>/<sub-skill>`. The first form returns the skill's main payload —
// `main.md` if present, else SKILL.md as a fallback. The second form
// returns a sub-skill's bytes; the special name `SKILL.md` returns
// the raw wrapper file (intended for internal/debug use, not advertised).
func Resolve(skills []*Skill, address string) ([]byte, error) {
	skillID, page, hasPage := strings.Cut(address, "/")
	if skillID == "" {
		return nil, fmt.Errorf("empty skill address")
	}
	skill := FindByID(skills, skillID)
	if skill == nil {
		return nil, fmt.Errorf("%w: %s", ErrSkillNotFound, skillID)
	}
	if !hasPage {
		if main, ok := skill.SubSkills["main"]; ok {
			return main.Raw, nil
		}
		return skill.RawSKILL, nil
	}
	if page == "" {
		return nil, fmt.Errorf("empty page name in address %q", address)
	}
	if page == "SKILL.md" {
		return skill.RawSKILL, nil
	}
	sub, ok := skill.SubSkills[page]
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s", ErrSubSkillNotFound, skillID, page)
	}
	return sub.Raw, nil
}
