/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/llmdocsclient"
	"github.com/spf13/cobra"
)

type llmDocsReadOpts struct {
	UsePositionalArgs

	argPath    string
	flagOffset int
	flagLimit  int
}

func init() {
	o := llmDocsReadOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argPath, "PATH", "Path of the file to read (e.g. index.md, MetaplaySDK/version.yaml).")

	cmd := &cobra.Command{
		Use:   "read PATH",
		Short: "[preview] Read a single file from the llm-docs payload (machine use only)",
		Long: renderLong(&o, `
			PREVIEW: This command is in preview and subject to change!

			Read a single file from the llm-docs payload and print its raw
			contents. Intended for machine consumption (e.g. AI coding agents);
			the output format is not stable for human-driven workflows.
		`),
		Run: runCommand(&o),
		Example: renderExample(`
			# Show the root catalog.
			metaplay llm-docs read index.md

			# Read a docs page (.md is auto-appended server-side when no extension is given).
			metaplay llm-docs read docs/cloud-deployments/getting-started

			# Read a file from a sample project.
			metaplay llm-docs read samples/HelloWorld/Assets/SharedCode/Player/PlayerModel.cs

			# Read the SDK version metadata.
			metaplay llm-docs read MetaplaySDK/version.yaml

			# Read a 100-line slice starting at line 500 (paged read).
			metaplay llm-docs read samples/HelloWorld/Assets/SharedCode/Player/PlayerModel.cs --offset 500 --limit 100
		`),
	}

	llmDocsCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.IntVar(&o.flagOffset, "offset", 0, "1-indexed line to start reading from (defaults to line 1)")
	flags.IntVar(&o.flagLimit, "limit", 0, "Maximum number of lines to return (defaults to the server-side default)")
}

func (o *llmDocsReadOpts) Prepare(cmd *cobra.Command, args []string) error {
	if cmd.Flags().Changed("offset") && o.flagOffset < 1 {
		return clierrors.NewUsageError("--offset must be >= 1 (lines are 1-indexed)")
	}
	if cmd.Flags().Changed("limit") && o.flagLimit < 1 {
		return clierrors.NewUsageError("--limit must be >= 1")
	}
	return nil
}

func (o *llmDocsReadOpts) Run(cmd *cobra.Command) error {
	client, reqMeta, err := newLLMDocsClient()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(cmd.Context(), llmDocsDefaultTimeout)
	defer cancel()
	req := &llmdocsclient.ReadFileRequest{
		Metadata: reqMeta,
		Path:     o.argPath,
	}
	if cmd.Flags().Changed("offset") {
		offset := int32(o.flagOffset)
		req.Offset = &offset
	}
	if cmd.Flags().Changed("limit") {
		limit := int32(o.flagLimit)
		req.Limit = &limit
	}
	resp, err := client.ReadFile(ctx, req)
	if err != nil {
		return wrapLLMDocsError(err, "read file")
	}
	printLLMDocsContent(resp.Content)
	return nil
}
