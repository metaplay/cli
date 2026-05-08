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

	// SubPages maps page-name (filename without `.md`) to file contents.
	SubPages map[string][]byte
}

// SubPageNames returns the sub-page names sorted alphabetically.
func (s *Skill) SubPageNames() []string {
	names := make([]string, 0, len(s.SubPages))
	for k := range s.SubPages {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// LoadAll walks rootFS as the skills data root: each top-level directory
// must contain a SKILL.md, and any other `.md` files in that directory are
// loaded as sub-pages.
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
	if name := fm.Name(); name != "" && name != id {
		return nil, fmt.Errorf("skill %s: frontmatter name %q does not match directory name", id, name)
	}

	skill := &Skill{
		ID:          id,
		Frontmatter: fm,
		Body:        body,
		RawSKILL:    raw,
		SubPages:    map[string][]byte{},
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
		contents, err := fs.ReadFile(rootFS, path.Join(id, name))
		if err != nil {
			return nil, fmt.Errorf("read sub-page %s/%s: %w", id, name, err)
		}
		skill.SubPages[page] = contents
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

// ErrSubPageNotFound is returned by Resolve when the address names a known
// skill but an unknown sub-page within it.
var ErrSubPageNotFound = errors.New("sub-page not found")

// Resolve looks up content by an address of the form `<skill>` or
// `<skill>/<page>`. The first form returns the skill's main payload —
// `main.md` if present, else SKILL.md as a fallback. The second form
// returns a sub-page's bytes; the special page name `SKILL.md` returns
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
		if main, ok := skill.SubPages["main"]; ok {
			return main, nil
		}
		return skill.RawSKILL, nil
	}
	if page == "" {
		return nil, fmt.Errorf("empty page name in address %q", address)
	}
	if page == "SKILL.md" {
		return skill.RawSKILL, nil
	}
	contents, ok := skill.SubPages[page]
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s", ErrSubPageNotFound, skillID, page)
	}
	return contents, nil
}
