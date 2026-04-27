/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Scope selects which AgentHost directory to install into.
type Scope int

const (
	// ScopeProject targets the project's working directory (AgentHost.ProjectDir).
	ScopeProject Scope = iota
	// ScopeUser targets the user's home directory (AgentHost.UserDir).
	ScopeUser
)

// String returns the lowercase scope name.
func (s Scope) String() string {
	switch s {
	case ScopeProject:
		return "project"
	case ScopeUser:
		return "user"
	}
	return fmt.Sprintf("scope(%d)", int(s))
}

// ManagedByValue is the literal value written to the `managed-by` field of
// every wrapper. Wrappers without this exact value are treated as user files.
const ManagedByValue = "metaplay-cli"

// CLIVersionField and ManagedByField name the frontmatter fields injected
// into wrappers at install time.
const (
	CLIVersionField = "metaplay-cli-version"
	ManagedByField  = "managed-by"
)

// InstallStatus categorises the outcome of installing a single wrapper.
type InstallStatus int

const (
	// StatusWritten means the wrapper was created or overwritten.
	StatusWritten InstallStatus = iota
	// StatusUnchanged means a wrapper with identical content already existed.
	StatusUnchanged
	// StatusSkippedNewer means the on-disk wrapper has a newer version stamp.
	StatusSkippedNewer
	// StatusSkippedUser means the on-disk file is user-authored (no managed-by).
	StatusSkippedUser
	// StatusSkippedError means the on-disk version stamp could not be parsed.
	StatusSkippedError
)

// InstallAction records what happened to one (skill, agent) target path.
type InstallAction struct {
	SkillID  string
	AgentID  string
	Path     string
	Status   InstallStatus
	Reason   string // free text, populated for any Skipped status
	BytesOut int    // length of the wrapper content (for Written/Unchanged)
}

// InstallOptions describes a single install pass.
type InstallOptions struct {
	// Skills is the canonical embedded set, e.g. from LoadAll(OpenFS()).
	Skills []*Skill
	// Agents lists the AgentHost rows to install for. Duplicates and same-
	// path agents are deduped automatically.
	Agents []AgentHost
	// RootDir is the absolute base directory to install into; for
	// ScopeProject this is the project root, for ScopeUser the user home.
	RootDir string
	// Scope selects ProjectDir vs UserDir on each AgentHost.
	Scope Scope
	// Version is the value to stamp into the wrapper's
	// `metaplay-cli-version` field. Should be the current CLI's
	// version.AppVersion (or DevVersion in dev builds).
	Version string
	// Force bypasses the version gate (still respects ManagedBy: only writes
	// over wrappers we ourselves manage).
	Force bool
	// DevMode bypasses the version gate just like Force; logically separate
	// so callers can present different messaging.
	DevMode bool
}

// Install writes each (skill × agent) wrapper, gated by version stamps.
// Returns one InstallAction per unique target path and a non-nil error only
// for catastrophic failures (e.g. could not write at all). Per-target
// problems are reflected in the returned actions.
func Install(opts InstallOptions) ([]InstallAction, error) {
	if opts.RootDir == "" {
		return nil, errors.New("RootDir is required")
	}
	if opts.Version == "" {
		return nil, errors.New("Version is required")
	}
	if len(opts.Skills) == 0 {
		return nil, errors.New("no skills to install")
	}
	if len(opts.Agents) == 0 {
		return nil, errors.New("no agents specified")
	}

	// Dedupe (skill, target-path) tuples. Multiple AgentHosts can share
	// the same directory (e.g. several point at .agents/skills); writing
	// the same content twice is wasteful and produces noisy output.
	type key struct{ skill, path string }
	seen := map[key]string{} // first AgentID claimed for this target
	var actions []InstallAction

	for _, agent := range opts.Agents {
		dir := opts.scopeDir(agent)
		if dir == "" {
			actions = append(actions, InstallAction{
				AgentID: agent.ID,
				Status:  StatusSkippedError,
				Reason:  fmt.Sprintf("agent %q has no %s directory", agent.ID, opts.Scope),
			})
			continue
		}
		for _, skill := range opts.Skills {
			targetPath := filepath.Join(opts.RootDir, dir, skill.ID, "SKILL.md")
			k := key{skill: skill.ID, path: targetPath}
			if claimedBy, ok := seen[k]; ok {
				actions = append(actions, InstallAction{
					SkillID: skill.ID,
					AgentID: agent.ID,
					Path:    targetPath,
					Status:  StatusUnchanged,
					Reason:  fmt.Sprintf("shared with agent %q", claimedBy),
				})
				continue
			}
			seen[k] = agent.ID
			act := installOne(skill, agent.ID, targetPath, opts)
			actions = append(actions, act)
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

func (o InstallOptions) scopeDir(a AgentHost) string {
	switch o.Scope {
	case ScopeProject:
		return a.ProjectDir
	case ScopeUser:
		return a.UserDir
	}
	return ""
}

func installOne(skill *Skill, agentID, targetPath string, opts InstallOptions) InstallAction {
	act := InstallAction{
		SkillID: skill.ID,
		AgentID: agentID,
		Path:    targetPath,
	}
	wrapper, err := renderWrapper(skill, opts.Version)
	if err != nil {
		act.Status = StatusSkippedError
		act.Reason = fmt.Sprintf("render wrapper: %v", err)
		return act
	}

	existing, existedRaw, err := readExisting(targetPath)
	if err != nil {
		act.Status = StatusSkippedError
		act.Reason = fmt.Sprintf("read existing: %v", err)
		return act
	}

	allowOverwrite, skipReason := decide(existing, opts)
	if !allowOverwrite {
		// Determine whether this is "user file" vs "newer version" vs "error".
		switch skipReason.kind {
		case skipUserFile:
			act.Status = StatusSkippedUser
		case skipNewer:
			act.Status = StatusSkippedNewer
		case skipMalformed:
			act.Status = StatusSkippedError
		}
		act.Reason = skipReason.detail
		return act
	}

	// Even if we are allowed to overwrite, skip the write when bytes match.
	if existedRaw != nil && bytes.Equal(existedRaw, wrapper) {
		act.Status = StatusUnchanged
		act.BytesOut = len(wrapper)
		return act
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		act.Status = StatusSkippedError
		act.Reason = fmt.Sprintf("mkdir: %v", err)
		return act
	}
	if err := writeAtomically(targetPath, wrapper); err != nil {
		act.Status = StatusSkippedError
		act.Reason = fmt.Sprintf("write: %v", err)
		return act
	}
	act.Status = StatusWritten
	act.BytesOut = len(wrapper)
	return act
}

// renderWrapper takes the canonical SKILL.md and injects the install-time
// frontmatter fields. The original Skill is not mutated; we re-parse from
// RawSKILL each time.
func renderWrapper(skill *Skill, version string) ([]byte, error) {
	fm, body, err := ParseFrontmatter(skill.RawSKILL)
	if err != nil {
		return nil, fmt.Errorf("parse embedded SKILL.md: %w", err)
	}
	fm.Set(CLIVersionField, version)
	fm.Set(ManagedByField, ManagedByValue)
	return fm.MarshalDocument(body)
}

// existingState carries the parsed view of an on-disk wrapper, or nil if
// the file does not exist. existingRaw is non-nil iff the file existed and
// was readable.
type existingState struct {
	exists      bool
	parsed      *Frontmatter
	parseFailed bool
}

func readExisting(targetPath string) (existingState, []byte, error) {
	raw, err := os.ReadFile(targetPath)
	if errors.Is(err, fs.ErrNotExist) {
		return existingState{exists: false}, nil, nil
	}
	if err != nil {
		return existingState{}, nil, err
	}
	fm, _, perr := ParseFrontmatter(raw)
	if perr != nil {
		return existingState{exists: true, parseFailed: true}, raw, nil
	}
	return existingState{exists: true, parsed: fm}, raw, nil
}

type skipKind int

const (
	skipNone skipKind = iota
	skipUserFile
	skipNewer
	skipMalformed
)

type skipInfo struct {
	kind   skipKind
	detail string
}

func decide(state existingState, opts InstallOptions) (bool, skipInfo) {
	if !state.exists {
		return true, skipInfo{}
	}
	// Existing file but unparseable as frontmatter — treat as user file.
	if state.parseFailed || state.parsed == nil {
		return false, skipInfo{kind: skipUserFile, detail: "existing file has no/invalid frontmatter; treating as user-authored"}
	}
	if state.parsed.ManagedBy() != ManagedByValue {
		return false, skipInfo{kind: skipUserFile, detail: "existing file is not managed by metaplay-cli"}
	}
	// We own this file. Force/dev unconditionally write.
	if opts.Force || opts.DevMode {
		return true, skipInfo{}
	}
	existingVersion := state.parsed.CLIVersion()
	if existingVersion == "" {
		// Managed file but missing stamp — overwrite to fix.
		return true, skipInfo{}
	}
	cmp, err := CompareVersions(opts.Version, existingVersion)
	if err != nil {
		return false, skipInfo{
			kind:   skipMalformed,
			detail: fmt.Sprintf("on-disk version %q could not be parsed: %v", existingVersion, err),
		}
	}
	if cmp < 0 {
		return false, skipInfo{
			kind:   skipNewer,
			detail: fmt.Sprintf("on-disk version %s is newer than current %s", existingVersion, opts.Version),
		}
	}
	return true, skipInfo{}
}

// writeAtomically writes data to path via a temp file in the same directory
// followed by an os.Rename, so concurrent readers never see a partial file.
func writeAtomically(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".skill-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
