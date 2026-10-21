/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DetectExistingTargets returns the IDs of AgentDirs whose scope-relative
// directory already exists under rootDir. Multiple AgentDirs that share a
// path (e.g. standard / cline / warp at user scope all map to .agents/skills)
// collapse to the FIRST registry entry pointing at that path; the rest are
// omitted so a UI built on top doesn't show duplicate-checked rows.
//
// Use this to seed the default selection of an interactive picker, or as the
// non-interactive default when the caller has not specified targets.
func DetectExistingTargets(rootDir string, scope Scope) []string {
	var ids []string
	seen := map[string]bool{}
	for _, t := range AgentDirs {
		rel := scopeRelDir(scope, t)
		if rel == "" || seen[rel] {
			continue
		}
		path := filepath.Join(rootDir, rel)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			ids = append(ids, t.ID)
			seen[rel] = true
		}
	}
	return ids
}

// ValidateTargetsForScope ensures every target in the supplied slice has a
// non-empty scope-relative directory. It returns a descriptive error
// (suitable for surfacing to the user) when a target does not apply to the
// active scope — e.g. the per-harness home dirs at project scope.
func ValidateTargetsForScope(targets []AgentDir, scope Scope) error {
	scopeName := "project"
	if scope == ScopeUser {
		scopeName = "user"
	}
	valid := AgentDirIDsForScope(scope)
	for _, t := range targets {
		if scopeRelDir(scope, t) == "" {
			return fmt.Errorf("target %q has no %s-scope directory; valid %s targets: %s",
				t.ID, scopeName, scopeName, strings.Join(valid, ", "))
		}
	}
	return nil
}

// scopeRelDir returns AgentDir.ProjectDir or AgentDir.UserDir depending on
// scope. Centralised so detect / validate / install / remove all agree.
func scopeRelDir(scope Scope, t AgentDir) string {
	switch scope {
	case ScopeProject:
		return t.ProjectDir
	case ScopeUser:
		return t.UserDir
	}
	return ""
}
