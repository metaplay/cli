/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"math"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/llmdocsclient"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
		Short: "Read a single file from the llm-docs payload (machine use only)",
		Long: renderLong(&o, `
			Read a single file from the llm-docs payload and print its raw
			contents. Intended for machine consumption (e.g. AI coding agents);
			the output format is not stable for human-driven workflows.
		`),
		Run: runCommand(&o),
		Example: renderExample(`
			# Show the root catalog.
			metaplay llm-docs read index.md

			# Read a docs page. The server tries the exact path first, then PATH + ".md".
			metaplay llm-docs read docs/cloud-deployments/getting-started

			# Read a file from a sample project.
			metaplay llm-docs read samples/HelloWorld/Assets/SharedCode/Player/PlayerModel.cs

			# Read the SDK version metadata.
			metaplay llm-docs read MetaplaySDK/version.yaml

			# Read a 100-line slice starting at line 500 (paged read).
			metaplay llm-docs read MetaplaySDK/Backend/Server/Player/PlayerActorBase.cs --offset 500 --limit 100
		`),
	}

	llmDocsCmd.AddCommand(cmd)
	o.registerFlags(cmd.Flags())
}

// registerFlags defines the read command's flags on flags, bound to o.
// Shared by init and the tests so both parse argv the same way.
func (o *llmDocsReadOpts) registerFlags(flags *pflag.FlagSet) {
	flags.IntVar(&o.flagOffset, "offset", 0, "1-indexed line to start reading from (defaults to line 1)")
	flags.IntVar(&o.flagLimit, "limit", 0, "Maximum number of lines to return (defaults to the server-side default)")
}

func (o *llmDocsReadOpts) Prepare(cmd *cobra.Command, args []string) error {
	// The request fields are int32, so larger values would wrap around.
	if cmd.Flags().Changed("offset") && (o.flagOffset < 1 || o.flagOffset > math.MaxInt32) {
		return clierrors.NewUsageErrorf("--offset must be between 1 and %d (lines are 1-indexed)", math.MaxInt32)
	}
	if cmd.Flags().Changed("limit") && (o.flagLimit < 1 || o.flagLimit > math.MaxInt32) {
		return clierrors.NewUsageErrorf("--limit must be between 1 and %d", math.MaxInt32)
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
		return client.wrapError(err, "read file")
	}
	printLLMDocsContent(resp.Content)
	return nil
}
