/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestBuildCliReferenceOutput_ExcludesHiddenAndIncludesRootPath(t *testing.T) {
	root := &cobra.Command{Use: "metaplay", Short: "root"}
	visible := &cobra.Command{Use: "visible", Short: "Visible command"}
	hidden := &cobra.Command{Use: "hidden", Short: "Hidden command", Hidden: true}

	root.AddCommand(visible)
	root.AddCommand(hidden)

	output := buildCliReferenceOutput(root)

	if len(output.Commands) != 2 {
		t.Fatalf("expected 2 commands (root + visible), got %d", len(output.Commands))
	}

	if output.Commands[0].Path[0] != "metaplay" {
		t.Fatalf("expected root path to include 'metaplay', got %#v", output.Commands[0].Path)
	}

	for _, cmd := range output.Commands {
		if len(cmd.Path) > 1 && cmd.Path[1] == "hidden" {
			t.Fatalf("hidden command should not be exported: %#v", cmd.Path)
		}
	}
}

func TestBuildCliReferenceOutput_FlagsAndExamples(t *testing.T) {
	root := &cobra.Command{Use: "metaplay", Short: "root"}
	root.PersistentFlags().String("verbose", "", "Verbose logging [env: METAPLAYCLI_VERBOSE]")

	cmd := &cobra.Command{
		Use:   "test",
		Short: "Test command",
		Example: `
# First example
metaplay test --name foo

# Second example
metaplay test --name bar
`,
	}
	cmd.Flags().StringP("name", "n", "default-name", "Name to use")

	root.AddCommand(cmd)

	output := buildCliReferenceOutput(root)
	if len(output.Commands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(output.Commands))
	}

	rootExported := output.Commands[0]
	if len(rootExported.Flags) != 1 {
		t.Fatalf("expected 1 root-owned flag, got %d", len(rootExported.Flags))
	}
	if rootExported.Flags[0].Name != "verbose" {
		t.Fatalf("expected root flag 'verbose', got %q", rootExported.Flags[0].Name)
	}
	if rootExported.Flags[0].EnvVar != "METAPLAYCLI_VERBOSE" {
		t.Fatalf("expected root env var extraction, got %q", rootExported.Flags[0].EnvVar)
	}

	exported := output.Commands[1]
	if len(exported.Flags) != 1 {
		t.Fatalf("expected 1 child-owned flag, got %d", len(exported.Flags))
	}
	if exported.Flags[0].Name != "name" {
		t.Fatalf("expected local flag 'name', got %q", exported.Flags[0].Name)
	}
	if exported.Flags[0].Shorthand != "n" {
		t.Fatalf("expected shorthand 'n', got %q", exported.Flags[0].Shorthand)
	}
	if exported.Flags[0].DefaultValue != "default-name" {
		t.Fatalf("expected default value, got %q", exported.Flags[0].DefaultValue)
	}

	if len(exported.InheritedFlags) != 1 {
		t.Fatalf("expected 1 inherited flag, got %d", len(exported.InheritedFlags))
	}
	if exported.InheritedFlags[0].EnvVar != "METAPLAYCLI_VERBOSE" {
		t.Fatalf("expected env var extraction, got %q", exported.InheritedFlags[0].EnvVar)
	}

	if len(exported.Examples) != 2 {
		t.Fatalf("expected 2 examples, got %d", len(exported.Examples))
	}
	if exported.Examples[0].Description != "First example" {
		t.Fatalf("unexpected first example description: %q", exported.Examples[0].Description)
	}
	if exported.Examples[0].Command != "metaplay test --name foo" {
		t.Fatalf("unexpected first example command: %q", exported.Examples[0].Command)
	}
}

func TestCollectFlags_AlwaysReturnsArray(t *testing.T) {
	root := &cobra.Command{Use: "metaplay", Short: "root"}
	output := buildCliReferenceOutput(root)

	if output.Commands == nil || len(output.Commands) == 0 {
		t.Fatalf("expected commands to be present")
	}

	if output.Commands[0].Flags == nil {
		t.Fatalf("expected flags to be non-nil empty array")
	}
	if output.Commands[0].InheritedFlags == nil {
		t.Fatalf("expected inherited flags to be non-nil empty array")
	}
	if output.Commands[0].Examples == nil {
		t.Fatalf("expected examples to be non-nil empty array")
	}
	if output.Commands[0].Aliases == nil {
		t.Fatalf("expected aliases to be non-nil empty array")
	}
}
