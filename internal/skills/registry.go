/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

// Package skills implements installation and serving of Metaplay agent skills
// (Claude-Code-style markdown bundles with YAML frontmatter) for AI coding tools.
package skills

// AgentHost describes a single AI coding tool that consumes skill markdown.
//
// At install time, a skill named "foo" is written as <Dir>/foo/SKILL.md, where
// <Dir> is ProjectDir (joined to the project root) when --scope=project, or
// UserDir (joined to the user's home directory) when --scope=user.
type AgentHost struct {
	// ID is the kebab-case identifier accepted by --agent.
	ID string
	// DisplayName is shown in help text and tabular output.
	DisplayName string
	// ProjectDir is relative to the project root.
	ProjectDir string
	// UserDir is relative to the user's home directory.
	UserDir string
}

// SharedProjectDir is the cross-agent project-scope directory that several
// AI tools read from — installing once here satisfies all agents that point
// their ProjectDir at it.
const SharedProjectDir = ".agents/skills"

// DefaultAgentID is the agent assumed when --agent is not given.
//
// Metaplay developers use Claude Code as the primary harness, so it is the
// default. Override with --agent or via metaplay-project.yaml in the future.
const DefaultAgentID = "claude-code"

// AgentHosts enumerates every AI coding tool the CLI knows how to install
// skill wrappers for.
//
// Paths are sourced from github.com/cli/cli (internal/skills/registry); keep
// in periodic sync as new tools are added there. Order is rough popularity for
// the Metaplay user base, with the universal fallback last.
var AgentHosts = []AgentHost{
	{
		ID:          "claude-code",
		DisplayName: "Claude Code",
		ProjectDir:  ".claude/skills",
		UserDir:     ".claude/skills",
	},
	{
		ID:          "github-copilot",
		DisplayName: "GitHub Copilot (incl. VS Code)",
		ProjectDir:  SharedProjectDir,
		UserDir:     ".copilot/skills",
	},
	{
		ID:          "cursor",
		DisplayName: "Cursor",
		ProjectDir:  SharedProjectDir,
		UserDir:     ".cursor/skills",
	},
	{
		ID:          "codex",
		DisplayName: "OpenAI Codex CLI",
		ProjectDir:  SharedProjectDir,
		UserDir:     ".codex/skills",
	},
	{
		ID:          "windsurf",
		DisplayName: "Windsurf (Codeium)",
		ProjectDir:  ".windsurf/skills",
		UserDir:     ".codeium/windsurf/skills",
	},
	{
		ID:          "opencode",
		DisplayName: "OpenCode (opencode.ai)",
		ProjectDir:  SharedProjectDir,
		UserDir:     ".config/opencode/skills",
	},
	{
		ID:          "pi",
		DisplayName: "Pi (pi.dev)",
		ProjectDir:  ".pi/skills",
		UserDir:     ".pi/agent/skills",
	},
	{
		ID:          "gemini-cli",
		DisplayName: "Gemini CLI",
		ProjectDir:  SharedProjectDir,
		UserDir:     ".gemini/skills",
	},
	{
		ID:          "antigravity",
		DisplayName: "Google Antigravity",
		ProjectDir:  SharedProjectDir,
		UserDir:     ".gemini/antigravity/skills",
	},
	{
		ID:          "cline",
		DisplayName: "Cline",
		ProjectDir:  SharedProjectDir,
		UserDir:     SharedProjectDir,
	},
	{
		ID:          "continue",
		DisplayName: "Continue",
		ProjectDir:  ".continue/skills",
		UserDir:     ".continue/skills",
	},
	{
		ID:          "amp",
		DisplayName: "Amp",
		ProjectDir:  SharedProjectDir,
		UserDir:     ".config/agents/skills",
	},
	{
		ID:          "augment",
		DisplayName: "Augment",
		ProjectDir:  ".augment/skills",
		UserDir:     ".augment/skills",
	},
	{
		ID:          "goose",
		DisplayName: "Goose",
		ProjectDir:  ".goose/skills",
		UserDir:     ".config/goose/skills",
	},
	{
		ID:          "crush",
		DisplayName: "Crush",
		ProjectDir:  ".crush/skills",
		UserDir:     ".config/crush/skills",
	},
	{
		ID:          "roo",
		DisplayName: "Roo Code",
		ProjectDir:  ".roo/skills",
		UserDir:     ".roo/skills",
	},
	{
		ID:          "kilo",
		DisplayName: "Kilo Code",
		ProjectDir:  ".kilocode/skills",
		UserDir:     ".kilocode/skills",
	},
	{
		ID:          "junie",
		DisplayName: "Junie (JetBrains)",
		ProjectDir:  ".junie/skills",
		UserDir:     ".junie/skills",
	},
	{
		ID:          "qwen-code",
		DisplayName: "Qwen Code",
		ProjectDir:  ".qwen/skills",
		UserDir:     ".qwen/skills",
	},
	{
		ID:          "warp",
		DisplayName: "Warp",
		ProjectDir:  SharedProjectDir,
		UserDir:     SharedProjectDir,
	},
	{
		ID:          "droid",
		DisplayName: "Factory Droid",
		ProjectDir:  ".factory/skills",
		UserDir:     ".factory/skills",
	},
	{
		ID:          "openhands",
		DisplayName: "OpenHands",
		ProjectDir:  ".openhands/skills",
		UserDir:     ".openhands/skills",
	},
	{
		ID:          "universal",
		DisplayName: "Universal fallback",
		ProjectDir:  SharedProjectDir,
		UserDir:     ".config/agents/skills",
	},
}

// LookupAgent returns the AgentHost with the given ID, or nil if unknown.
func LookupAgent(id string) *AgentHost {
	for i := range AgentHosts {
		if AgentHosts[i].ID == id {
			return &AgentHosts[i]
		}
	}
	return nil
}

// AgentIDs returns the set of known agent IDs in the registry's declared order.
func AgentIDs() []string {
	ids := make([]string, len(AgentHosts))
	for i, a := range AgentHosts {
		ids[i] = a.ID
	}
	return ids
}
