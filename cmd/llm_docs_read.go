/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/metaplay/cli/pkg/llmdocsclient"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type llmDocsReadOpts struct {
	UsePositionalArgs

	argPath string
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

			# Read a docs page (the .md extension is auto-appended on the
			# server when no extension is given).
			metaplay llm-docs read docs/cloud-deployments/getting-started

			# Read SDK source.
			metaplay llm-docs read MetaplaySDK/Backend/Server/Player/PlayerActorBase.cs

			# Read the SDK version metadata.
			metaplay llm-docs read MetaplaySDK/version.yaml
		`),
	}

	llmDocsCmd.AddCommand(cmd)
}

func (o *llmDocsReadOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *llmDocsReadOpts) Run(cmd *cobra.Command) error {
	client, err := newLLMDocsClient(cmd.Context())
	if err != nil {
		return err
	}
	defer client.Close()

	resp, err := client.ReadFile(
		cmd.Context(),
		&llmdocsclient.ReadFileRequest{
			Metadata: buildRequestMetadata(),
			Path:     o.argPath,
		},
	)
	if err != nil {
		return wrapLLMDocsError(err, "read file")
	}
	log.Info().Msg(resp.Content)
	return nil
}
