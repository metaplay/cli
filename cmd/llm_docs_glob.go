/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"

	"github.com/metaplay/cli/pkg/llmdocsclient"
	"github.com/spf13/cobra"
)

type llmDocsGlobOpts struct {
	UsePositionalArgs

	argPattern string
	flagPath   string
}

func init() {
	o := llmDocsGlobOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argPattern, "PATTERN", "Glob pattern matched against payload paths relative to --path (e.g. **/*.md).")

	cmd := &cobra.Command{
		Use:   "glob PATTERN [flags]",
		Short: "List files in the llm-docs payload matching a glob pattern (machine use only)",
		Long: renderLong(&o, `
			List files in the llm-docs payload matching a glob pattern, one per
			line. Intended for machine consumption (e.g. AI coding agents); the
			output format is not stable for human-driven workflows.

			PATTERN is matched against paths relative to --path. '*' matches
			within one path segment, '**' matches zero or more segments, and
			directory parts may appear anywhere in the pattern. Matching
			directories are listed too, unless the last segment is '**'. A
			trailing '/' lists directories only. Backslashes and a leading './'
			are accepted.
		`),
		Run: runCommand(&o),
		Example: renderExample(`
			# All markdown files anywhere in the payload.
			metaplay llm-docs glob "**/*.md"

			# All C# sources under a specific subtree.
			metaplay llm-docs glob "**/*.cs" --path MetaplaySDK/Backend

			# Only top-level entries in a subdirectory (non-recursive).
			metaplay llm-docs glob "*.md" --path docs

			# Files and directories directly inside a directory.
			metaplay llm-docs glob "MetaplaySDK/entrypoint/*"

			# Every file under a directory, at any depth (files only).
			metaplay llm-docs glob "docs/**"

			# Contents of every directory named 'entrypoint'.
			metaplay llm-docs glob "**/entrypoint/*"

			# Directories only: every directory named 'Player'.
			metaplay llm-docs glob "**/Player/"

			# Find a specific file by name anywhere in the payload.
			metaplay llm-docs glob "**/PlayerActorBase.cs"

			# List the sample projects.
			metaplay llm-docs glob "*" --path samples

			# All C# sources in one sample project.
			metaplay llm-docs glob "**/*.cs" --path samples/HelloWorld

			# The same file in every sample project.
			metaplay llm-docs glob "**/PlayerModel.cs" --path samples
		`),
	}

	llmDocsCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVar(&o.flagPath, "path", "", "Subdirectory of the docs payload to search in")
}

func (o *llmDocsGlobOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *llmDocsGlobOpts) Run(cmd *cobra.Command) error {
	client, reqMeta, err := newLLMDocsClient()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(cmd.Context(), llmDocsDefaultTimeout)
	defer cancel()
	resp, err := client.Find(ctx, &llmdocsclient.FindRequest{
		Metadata: reqMeta,
		Pattern:  o.argPattern,
		Path:     o.flagPath,
	})
	if err != nil {
		return wrapLLMDocsError(err, "find files")
	}
	printLLMDocsContent(resp.RenderedOutput)
	return nil
}
