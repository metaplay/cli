/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills_test

import (
	"fmt"
	"os"

	"github.com/metaplay/cli/pkg/skills"
)

// ExampleRunInstall demonstrates a typical non-interactive install: load
// the skills bundled with the package, install Claude Code wrappers under
// a project directory, and inspect the per-action results.
func ExampleRunInstall() {
	loaded, err := skills.LoadAll(skills.EmbeddedFS())
	if err != nil {
		fmt.Println("load:", err)
		return
	}

	tmp, _ := os.MkdirTemp("", "skills-example-*")
	defer os.RemoveAll(tmp)

	scope := skills.ScopeProject
	res, err := skills.RunInstall(skills.InstallRequest{
		Skills:     loaded,
		Scope:      &scope,
		ProjectDir: tmp,
		TargetIDs:  []string{skills.AgentDirClaudeID},
		Version:    "1.0.0",
	})
	if err != nil {
		fmt.Println("install:", err)
		return
	}

	for _, a := range res.Actions {
		if a.Status == skills.StatusWritten {
			fmt.Printf("wrote %s\n", a.SkillID)
		}
	}
	// Unordered output:
	// wrote metaplay-develop
	// wrote metaplay-devops
	// wrote metaplay-docs
	// wrote metaplay-troubleshoot
}

// ExampleResolve shows how to fetch a skill or sub-skill by address.
func ExampleResolve() {
	loaded, _ := skills.LoadAll(skills.EmbeddedFS())

	wrapper, err := skills.Resolve(loaded, "metaplay-develop")
	if err != nil {
		fmt.Println("resolve:", err)
		return
	}
	fmt.Printf("wrapper present: %v\n", len(wrapper) > 0)

	subSkill, err := skills.Resolve(loaded, "metaplay-develop-review-models")
	if err != nil {
		fmt.Println("sub-skill:", err)
		return
	}
	fmt.Printf("sub-skill present: %v\n", len(subSkill) > 0)
	// Output:
	// wrapper present: true
	// sub-skill present: true
}
