/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// RemoveStatus categorises the outcome of removing a single wrapper.
type RemoveStatus int

const (
	// StatusRemoved means the wrapper was deleted from disk.
	StatusRemoved RemoveStatus = iota
	// StatusRemoveSkippedUser means the on-disk file lacked the
	// managed-by stamp and was left in place.
	StatusRemoveSkippedUser
	// StatusRemoveSkippedNotFound means there was no file at the
	// target path to begin with.
	StatusRemoveSkippedNotFound
	// StatusRemoveSkippedError means an I/O or parse failure prevented
	// the operation; see Reason for details.
	StatusRemoveSkippedError
)

// RemoveAction records what happened to one (skill, agent) target path.
type RemoveAction struct {
	SkillID string
	AgentID string
	Path    string
	Status  RemoveStatus
	Reason  string
}

// RemoveOptions describes one removal pass.
type RemoveOptions struct {
	// Agents lists the AgentHosts to consider. Same dedupe rules as Install.
	Agents []AgentHost
	// RootDir is the absolute base directory; the project root for
	// ScopeProject, the user home for ScopeUser.
	RootDir string
	// Scope selects ProjectDir vs UserDir.
	Scope Scope
	// SkillIDs optionally restricts the removal to specific skill names.
	// When empty, every managed-by wrapper found under the agent dir is
	// removed (covering orphans from skills no longer in the embedded set).
	SkillIDs []string
}

// Remove deletes wrappers carrying the managed-by:metaplay-cli stamp under
// each (agent, scope) directory. User-authored skill files are never
// touched. After removing a SKILL.md, the parent skill directory is also
// removed if empty.
func Remove(opts RemoveOptions) ([]RemoveAction, error) {
	if opts.RootDir == "" {
		return nil, errors.New("RootDir is required")
	}
	if len(opts.Agents) == 0 {
		return nil, errors.New("no agents specified")
	}

	// Group agents by base directory before touching the filesystem so two
	// agents that share a path scan it once and the report attributes the
	// removal to the first agent, with the rest marked as shared.
	type group struct {
		baseDir string
		agents  []string
	}
	var groups []group
	groupIdx := map[string]int{}
	var actions []RemoveAction

	for _, agent := range opts.Agents {
		dir := optsScopeDir(opts.Scope, agent)
		if dir == "" {
			actions = append(actions, RemoveAction{
				AgentID: agent.ID,
				Status:  StatusRemoveSkippedError,
				Reason:  fmt.Sprintf("agent %q has no %s directory", agent.ID, opts.Scope),
			})
			continue
		}
		baseDir := filepath.Join(opts.RootDir, dir)
		if idx, ok := groupIdx[baseDir]; ok {
			groups[idx].agents = append(groups[idx].agents, agent.ID)
			continue
		}
		groupIdx[baseDir] = len(groups)
		groups = append(groups, group{baseDir: baseDir, agents: []string{agent.ID}})
	}

	for _, g := range groups {
		candidates, err := candidateSkillDirs(g.baseDir, opts.SkillIDs)
		if err != nil {
			actions = append(actions, RemoveAction{
				AgentID: g.agents[0],
				Path:    g.baseDir,
				Status:  StatusRemoveSkippedError,
				Reason:  err.Error(),
			})
			continue
		}
		primary := g.agents[0]
		for _, skillID := range candidates {
			skillPath := filepath.Join(g.baseDir, skillID, "SKILL.md")
			actions = append(actions, removeOne(skillID, primary, skillPath))
			for _, agentID := range g.agents[1:] {
				actions = append(actions, RemoveAction{
					SkillID: skillID,
					AgentID: agentID,
					Path:    skillPath,
					Status:  StatusRemoveSkippedNotFound,
					Reason:  fmt.Sprintf("shared with agent %q", primary),
				})
			}
		}
	}

	sort.Slice(actions, func(i, j int) bool {
		if actions[i].AgentID != actions[j].AgentID {
			return actions[i].AgentID < actions[j].AgentID
		}
		return actions[i].SkillID < actions[j].SkillID
	})
	return actions, nil
}

// optsScopeDir is shared with InstallOptions through a free function so we
// can keep that method on InstallOptions and avoid coupling these two types.
func optsScopeDir(scope Scope, a AgentHost) string {
	switch scope {
	case ScopeProject:
		return a.ProjectDir
	case ScopeUser:
		return a.UserDir
	}
	return ""
}

// candidateSkillDirs returns the set of skill IDs to attempt removal for in
// the given base directory.
//
// When filter is non-empty, only those names are considered (and missing
// ones surface as StatusRemoveSkippedNotFound). When filter is empty, every
// subdirectory of baseDir is considered, which covers wrappers for skills
// that have since been removed from the embedded set.
func candidateSkillDirs(baseDir string, filter []string) ([]string, error) {
	if len(filter) > 0 {
		out := append([]string(nil), filter...)
		sort.Strings(out)
		return out, nil
	}
	if _, err := os.Stat(baseDir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func removeOne(skillID, agentID, skillPath string) RemoveAction {
	act := RemoveAction{
		SkillID: skillID,
		AgentID: agentID,
		Path:    skillPath,
	}
	raw, err := os.ReadFile(skillPath)
	if errors.Is(err, fs.ErrNotExist) {
		act.Status = StatusRemoveSkippedNotFound
		return act
	}
	if err != nil {
		act.Status = StatusRemoveSkippedError
		act.Reason = err.Error()
		return act
	}
	fm, _, perr := ParseFrontmatter(raw)
	if perr != nil {
		act.Status = StatusRemoveSkippedUser
		act.Reason = "no/invalid frontmatter; treating as user-authored"
		return act
	}
	if fm.ManagedBy() != ManagedByValue {
		act.Status = StatusRemoveSkippedUser
		act.Reason = "not managed by metaplay-cli"
		return act
	}
	if err := os.Remove(skillPath); err != nil {
		act.Status = StatusRemoveSkippedError
		act.Reason = err.Error()
		return act
	}
	// Best-effort: clean up the now-empty skill directory. Errors are
	// ignored because the SKILL.md removal already succeeded; a leftover
	// empty dir is harmless.
	dir := filepath.Dir(skillPath)
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		_ = os.Remove(dir)
	}
	act.Status = StatusRemoved
	return act
}
