/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

// Package skills implements installation and serving of Metaplay agent skills
// (Claude-Code-style markdown bundles with YAML frontmatter) for AI coding tools.
package skills

// AgentDir describes one filesystem location that AI coding tools read skill
// markdown from. The CLI offers two: a "standard" cross-agent dir
// (.agents/skills) consumed by most modern harnesses, and a Claude-Code
// specific dir (.claude/skills). Users may install to either or both.
type AgentDir struct {
	// ID is the kebab-case identifier accepted by --target.
	ID string
	// DisplayName is shown in interactive prompts and tabular output.
	DisplayName string
	// ProjectDir is relative to the project root when --scope=project.
	ProjectDir string
	// UserDir is relative to the user's home directory when --scope=user.
	UserDir string
}

const (
	// AgentDirStandardID is the cross-agent shared dir used by most modern
	// AI coding harnesses (Cursor, Codex, Copilot, OpenCode, Gemini, ...).
	AgentDirStandardID = "standard"
	// AgentDirClaudeID is the Claude-Code-specific dir.
	AgentDirClaudeID = "claude"
)

// DefaultAgentDirID is the dir picked when no --target flag is given and no
// existing on-disk directory is detected. Standard is the safer default
// because it covers more agent harnesses (Cursor, Codex, Copilot, Windsurf,
// ...) than the Claude-Code-specific dir.
const DefaultAgentDirID = AgentDirStandardID

// AgentDirs enumerates the supported install targets.
var AgentDirs = []AgentDir{
	{
		ID:          AgentDirStandardID,
		DisplayName: "Standard (.agents/skills)",
		ProjectDir:  ".agents/skills",
		UserDir:     ".agents/skills",
	},
	{
		ID:          AgentDirClaudeID,
		DisplayName: "Claude Code (.claude/skills)",
		ProjectDir:  ".claude/skills",
		UserDir:     ".claude/skills",
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
