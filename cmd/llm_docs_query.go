/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/metaplay/cli/pkg/llmdocsclient"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type llmDocsQueryOpts struct {
	UsePositionalArgs

	argQuery     string
	flagKeywords []string
}

func init() {
	o := llmDocsQueryOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argQuery, "QUERY", "Original free-form user query.")

	cmd := &cobra.Command{
		Use:   "search QUERY [flags]",
		Short: "[preview] Submit an end-user search and fetch relevant documentation (machine use only)",
		Long: renderLong(&o, `
			PREVIEW: This command is in preview and subject to change!

			Submit an end-user search, plus pre-extracted keywords, to the
			llm-docs service and print the response.

			Intended for machine consumption (e.g. AI coding agents); the
			output format is not stable for human-driven workflows.
		`),
		Run: runCommand(&o),
		Example: renderExample(`
			# Submit a query with pre-extracted keywords (comma-separated).
			metaplay llm-docs search "How do I implement guilds?" --keywords guilds,implementation

			# Query with no keywords.
			metaplay llm-docs search "What is the recommended .NET version?"
		`),
	}

	llmDocsCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringSliceVarP(&o.flagKeywords, "keywords", "k", nil, "Pre-extracted keyword for the query (repeatable, also accepts comma-separated values)")
}

func (o *llmDocsQueryOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *llmDocsQueryOpts) Run(cmd *cobra.Command) error {
	client, err := newLLMDocsClient(cmd.Context())
	if err != nil {
		return err
	}
	defer client.Close()

	resp, err := client.Search(cmd.Context(), &llmdocsclient.SearchRequest{
		Metadata: buildRequestMetadata(),
		Query:    o.argQuery,
		Keywords: o.flagKeywords,
	})
	if err != nil {
		return wrapLLMDocsError(err, "search documentation")
	}
	log.Info().Msg(resp.Content)
	return nil
}
