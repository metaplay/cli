/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
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
	args.AddStringArgument(&o.argPattern, "PATTERN", "Glob pattern to match files against (e.g. **/*.md).")

	cmd := &cobra.Command{
		Use:   "glob PATTERN [flags]",
		Short: "[preview] List files in the llm-docs payload matching a glob pattern (machine use only)",
		Long: renderLong(&o, `
			PREVIEW: This command is in preview and subject to change!

			List files in the llm-docs payload matching a glob pattern, one per
			line. Intended for machine consumption (e.g. AI coding agents); the
			output format is not stable for human-driven workflows.
		`),
		Run: runCommand(&o),
		Example: renderExample(`
			# All markdown files anywhere in the payload (recursive via **).
			metaplay llm-docs glob "**/*.md"

			# All C# sources under a specific subtree.
			metaplay llm-docs glob "**/*.cs" --path MetaplaySDK/Backend

			# Only top-level entries in a subdirectory (non-recursive).
			metaplay llm-docs glob "*.md" --path docs

			# Match both files and directories using the bare '*' pattern.
			metaplay llm-docs glob "*" --path docs/cloud-deployments

			# Find a specific file by name anywhere in the payload.
			metaplay llm-docs glob "**/PlayerActorBase.cs"
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
	defer client.Close()

	resp, err := client.Find(cmd.Context(), &llmdocsclient.FindRequest{
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
