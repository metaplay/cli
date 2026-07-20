/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

// Prompter drives the interactive bits of the orchestration helpers
// (RunInstall / RunRemove). The library does not import any UI package; a
// caller wraps its own interactive primitives (e.g. github.com/metaplay/cli's
// internal/tui dialogs) to satisfy this interface.
//
// Pass a nil Prompter when no interactive input is acceptable — typical for
// CI / scripted use. The orchestrator then falls back to detection or
// defaults; if neither yields enough information, it returns an error rather
// than silently picking on the user's behalf.
type Prompter interface {
	// AskScope prompts the user to choose between project and user scope.
	// currentDir and homeDir are for display in the prompt only;
	// implementations should not derive any path from them.
	AskScope(currentDir, homeDir string) (Scope, error)

	// AskTargets prompts the user to choose one or more install/remove
	// targets. groups is the deduplicated registry view for scope (one entry
	// per unique scope-relative path); defaults lists the group IDs that
	// should start pre-checked. The returned AgentDir slice is the canonical
	// representative of each selected group.
	AskTargets(scope Scope, groups []AgentDirGroup, defaults []string) ([]AgentDir, error)
}
