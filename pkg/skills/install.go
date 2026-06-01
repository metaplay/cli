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

// Scope selects which AgentDir directory to install into.
type Scope int

const (
	// ScopeProject targets the project's working directory (AgentDir.ProjectDir).
	ScopeProject Scope = iota
	// ScopeUser targets the user's home directory (AgentDir.UserDir).
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
	// StatusSkippedShared means another target in this same Install call
	// already wrote (or would have written) the wrapper at this path. Only
	// occurs when two AgentDirs map to the same scope-relative directory.
	StatusSkippedShared
)

// InstallAction records what happened to one (skill, target) pair.
type InstallAction struct {
	SkillID  string
	TargetID string
	Path     string
	Status   InstallStatus
	Reason   string // free text, populated for any Skipped status
	BytesOut int    // length of the wrapper content (for Written/Unchanged)
}

// InstallOptions describes a single install pass.
type InstallOptions struct {
	// Skills is the canonical embedded set, e.g. from LoadAll(EmbeddedFS()).
	Skills []*Skill
	// Targets lists the AgentDir rows to install into.
	Targets []AgentDir
	// RootDir is the absolute base directory to install into; for
	// ScopeProject this is the project root, for ScopeUser the user home.
	RootDir string
	// Scope selects ProjectDir vs UserDir on each AgentDir.
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

// Install writes each (skill × target) wrapper, gated by version stamps.
// Returns one InstallAction per unique target path and a non-nil error only
// for catastrophic failures (e.g. could not write at all). Per-target
// problems are reflected in the returned actions.
func Install(opts InstallOptions) ([]InstallAction, error) {
	if opts.RootDir == "" {
		return nil, errors.New("RootDir is required")
	}
	if opts.Version == "" {
		return nil, errors.New("version is required")
	}
	if len(opts.Skills) == 0 {
		return nil, errors.New("no skills to install")
	}
	if len(opts.Targets) == 0 {
		return nil, errors.New("no targets specified")
	}

	// Dedupe (skill, target-path) tuples. Two AgentDirs that share a path
	// (not the case for the bundled set, but defensive) only get written once.
	type key struct{ skill, path string }
	seen := map[key]string{} // first TargetID claimed for this (skill, path)
	var actions []InstallAction

	for _, target := range opts.Targets {
		dir := opts.scopeDir(target)
		if dir == "" {
			actions = append(actions, InstallAction{
				TargetID: target.ID,
				Status:   StatusSkippedError,
				Reason:   fmt.Sprintf("target %q has no %s directory", target.ID, opts.Scope),
			})
			continue
		}
		for _, skill := range opts.Skills {
			targetPath := filepath.Join(opts.RootDir, dir, skill.ID, "SKILL.md")
			k := key{skill: skill.ID, path: targetPath}
			if claimedBy, ok := seen[k]; ok {
				actions = append(actions, InstallAction{
					SkillID:  skill.ID,
					TargetID: target.ID,
					Path:     targetPath,
					Status:   StatusSkippedShared,
					Reason:   fmt.Sprintf("shared with target %q", claimedBy),
				})
				continue
			}
			seen[k] = target.ID
			act := installOne(skill, target.ID, targetPath, opts)
			actions = append(actions, act)
		}
	}

	sort.Slice(actions, func(i, j int) bool {
		if actions[i].TargetID != actions[j].TargetID {
			return actions[i].TargetID < actions[j].TargetID
		}
		return actions[i].SkillID < actions[j].SkillID
	})
	return actions, nil
}

func (o InstallOptions) scopeDir(t AgentDir) string {
	switch o.Scope {
	case ScopeProject:
		return t.ProjectDir
	case ScopeUser:
		return t.UserDir
	}
	return ""
}

func installOne(skill *Skill, targetID, targetPath string, opts InstallOptions) InstallAction {
	act := InstallAction{
		SkillID:  skill.ID,
		TargetID: targetID,
		Path:     targetPath,
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
