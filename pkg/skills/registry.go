/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

// AgentDir describes one filesystem location that AI coding tools read skill
// markdown from. The CLI offers two project-scope dirs (a cross-agent
// "standard" .agents/skills and Claude Code's .claude/skills) plus per-harness
// home dirs at user scope, mirroring the vercel-labs/skills npx-skills tool.
//
// Entries with an empty ProjectDir are user-scope only; entries with an empty
// UserDir are project-scope only. The UI filters AgentDirs by scope before
// presenting them.
type AgentDir struct {
	// ID is the kebab-case identifier accepted by --target.
	ID string
	// DisplayName is shown in interactive prompts and tabular output.
	DisplayName string
	// ProjectDir is relative to the project root when --scope=project.
	// Empty means this entry is not offered at project scope.
	ProjectDir string
	// UserDir is relative to the user's home directory when --scope=user.
	// Empty means this entry is not offered at user scope.
	UserDir string
}

const (
	// AgentDirStandardID is the cross-agent shared dir used at project scope
	// by most modern AI coding harnesses (Cursor, Codex, Copilot, Windsurf,
	// Gemini, OpenCode, Cline, Amp, Warp, ...).
	AgentDirStandardID = "standard"
	// AgentDirClaudeID is Claude Code's dir, used at both scopes.
	AgentDirClaudeID = "claude"
)

// DefaultAgentDirID is the dir picked when no --target flag is given and no
// existing on-disk directory is detected. Standard is the safer default
// because it covers more agent harnesses than the Claude-Code-specific dir.
const DefaultAgentDirID = AgentDirStandardID

// AgentDirs enumerates the supported install targets.
//
// Paths are sourced from vercel-labs/skills (https://github.com/vercel-labs/skills)
// — the canonical npx-skills tool — keep in periodic sync as new tools are
// added there. We curate a subset of the most-used 15.
var AgentDirs = []AgentDir{
	// Cross-agent shared dir. At project scope every agent except Claude Code
	// reads from .agents/skills. At user scope ~/.agents/skills is read by
	// Codex, Cursor, Copilot, Windsurf, Gemini, Cline, and Warp — installing
	// here covers all of them in one go (rather than ticking each).
	{
		ID:          AgentDirStandardID,
		DisplayName: "Standard",
		ProjectDir:  ".agents/skills",
		UserDir:     ".agents/skills",
	},
	// Claude Code — both scopes.
	{
		ID:          AgentDirClaudeID,
		DisplayName: "Claude Code",
		ProjectDir:  ".claude/skills",
		UserDir:     ".claude/skills",
	},
	// User-scope-only per-harness entries, ordered roughly by mindshare.
	{
		ID:          "cursor",
		DisplayName: "Cursor",
		UserDir:     ".cursor/skills",
	},
	{
		ID:          "copilot",
		DisplayName: "GitHub Copilot / VS Code",
		UserDir:     ".copilot/skills",
	},
	{
		ID:          "codex",
		DisplayName: "OpenAI Codex",
		UserDir:     ".codex/skills",
	},
	{
		ID:          "windsurf",
		DisplayName: "Windsurf (Codeium)",
		UserDir:     ".codeium/windsurf/skills",
	},
	{
		ID:          "gemini",
		DisplayName: "Gemini CLI",
		UserDir:     ".gemini/skills",
	},
	{
		ID:          "junie",
		DisplayName: "JetBrains Junie",
		UserDir:     ".junie/skills",
	},
	{
		ID:          "continue",
		DisplayName: "Continue",
		UserDir:     ".continue/skills",
	},
	{
		ID:          "cline",
		DisplayName: "Cline",
		UserDir:     ".agents/skills", // shared with warp per vercel-labs
	},
	{
		ID:          "warp",
		DisplayName: "Warp",
		UserDir:     ".agents/skills", // shared with cline per vercel-labs
	},
	{
		ID:          "goose",
		DisplayName: "Goose",
		UserDir:     ".config/goose/skills",
	},
	{
		ID:          "amp",
		DisplayName: "Amp",
		UserDir:     ".config/agents/skills",
	},
	{
		ID:          "opencode",
		DisplayName: "OpenCode",
		UserDir:     ".config/opencode/skills",
	},
	{
		ID:          "augment",
		DisplayName: "Augment",
		UserDir:     ".augment/skills",
	},
	{
		ID:          "roo",
		DisplayName: "Roo Code",
		UserDir:     ".roo/skills",
	},
}

// LookupAgentDir returns the AgentDir with the given ID, or nil if unknown.
func LookupAgentDir(id string) *AgentDir {
	for i := range AgentDirs {
		if AgentDirs[i].ID == id {
			return &AgentDirs[i]
		}
	}
	return nil
}

// AgentDirIDs returns the supported target IDs in declared order.
func AgentDirIDs() []string {
	ids := make([]string, len(AgentDirs))
	for i, a := range AgentDirs {
		ids[i] = a.ID
	}
	return ids
}

// AgentDirIDsForScope returns the target IDs that have a non-empty directory
// for the given scope.
func AgentDirIDsForScope(scope Scope) []string {
	var ids []string
	for _, a := range AgentDirs {
		var rel string
		switch scope {
		case ScopeProject:
			rel = a.ProjectDir
		case ScopeUser:
			rel = a.UserDir
		}
		if rel != "" {
			ids = append(ids, a.ID)
		}
	}
	return ids
}

// AgentDirsForScope returns the AgentDirs that have a non-empty directory
// for the given scope, in declared order.
func AgentDirsForScope(scope Scope) []AgentDir {
	var out []AgentDir
	for _, a := range AgentDirs {
		var rel string
		switch scope {
		case ScopeProject:
			rel = a.ProjectDir
		case ScopeUser:
			rel = a.UserDir
		}
		if rel != "" {
			out = append(out, a)
		}
	}
	return out
}

// AgentDirGroup is the display-oriented view of one install path: the
// canonical AgentDir for that path plus every tool that reads it. Multiple
// AgentDirs that point at the same path (e.g. Cline and Warp both share
// .agents/skills with Standard at user scope) collapse into one group so
// the multi-select dialog shows one row per unique path.
type AgentDirGroup struct {
	// Rep is the canonical AgentDir (first registry entry to claim the path).
	// Selecting a group installs to Rep's path; behavior is identical to
	// selecting any duplicate, so we pick a single representative.
	Rep AgentDir
	// Path is Rep's scope-relative directory.
	Path string
	// Tools are the short names of every harness that reads Path, in
	// registry order. For Standard, additional names that do NOT have
	// their own AgentDir entry are listed here too.
	Tools []string
}

// standardCoveredTools returns the short names of harnesses that read
// .agents/skills at the given scope but don't have their own AgentDir
// entry. Combined with any duplicate AgentDirs (Cline, Warp) during
// grouping, this yields the full list shown next to the Standard row.
func standardCoveredTools(scope Scope) []string {
	if scope == ScopeUser {
		return []string{"Codex", "Cursor", "Copilot", "Windsurf", "Gemini"}
	}
	return []string{"Cursor", "Codex", "Copilot", "Windsurf", "Gemini", "OpenCode", "Amp"}
}

// GroupAgentDirsForScope is AgentDirsForScope deduplicated by path. The
// resulting groups, in registry order, are what the multi-select dialog
// renders — one row per unique install path.
func GroupAgentDirsForScope(scope Scope) []AgentDirGroup {
	var groups []AgentDirGroup
	pathToIdx := map[string]int{}
	for _, a := range AgentDirs {
		var path string
		switch scope {
		case ScopeProject:
			path = a.ProjectDir
		case ScopeUser:
			path = a.UserDir
		}
		if path == "" {
			continue
		}
		if idx, ok := pathToIdx[path]; ok {
			groups[idx].Tools = append(groups[idx].Tools, a.DisplayName)
			continue
		}
		var tools []string
		if a.ID == AgentDirStandardID {
			tools = standardCoveredTools(scope)
		} else {
			tools = []string{a.DisplayName}
		}
		pathToIdx[path] = len(groups)
		groups = append(groups, AgentDirGroup{Rep: a, Path: path, Tools: tools})
	}
	return groups
}
