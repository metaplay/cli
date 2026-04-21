/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/metaplay/cli/pkg/llmdocsclient"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type llmDocsInfoOpts struct{}

func init() {
	o := llmDocsInfoOpts{}

	cmd := &cobra.Command{
		Use:   "info",
		Short: "Show deployment info for the llm-docs service (machine use only)",
		Long: renderLong(&o, `
			Show the deployment info JSON for the llm-docs service. Intended for
			machine consumption (e.g. AI coding agents); the output format is
			not stable for human-driven workflows.
		`),
		Run: runCommand(&o),
		Example: renderExample(`
			metaplay llm-docs info
		`),
	}

	llmDocsCmd.AddCommand(cmd)
}

func (o *llmDocsInfoOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *llmDocsInfoOpts) Run(cmd *cobra.Command) error {
	client, err := newLLMDocsClient(cmd.Context())
	if err != nil {
		return err
	}
	defer client.Close()

	resp, err := client.GetInfo(
		cmd.Context(),
		&llmdocsclient.GetInfoRequest{Metadata: buildRequestMetadata()},
	)
	if err != nil {
		return wrapLLMDocsError(err, "read deployment info")
	}
	log.Info().Msg(resp.DeploymentInfoJson)
	return nil
}
