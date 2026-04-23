/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/llmdocsclient"
	"github.com/spf13/cobra"
)

type llmDocsSearchOpts struct {
	UsePositionalArgs

	argQuery     string
	flagKeywords string

	keywords []string
}

func init() {
	o := llmDocsSearchOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argQuery, "QUERY", "Original free-form user query.")

	cmd := &cobra.Command{
		Use:   "search QUERY --keywords KEYWORDS",
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
			# Quote the whole --keywords value if any keyword contains spaces.
			metaplay llm-docs search "How do I implement guilds?" --keywords "guilds,guild actor,members,social,multiplayer"

			metaplay llm-docs search "Recommended .NET version?" --keywords dotnet,runtime,version,SDK,LTS
		`),
	}

	llmDocsCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVarP(&o.flagKeywords, "keywords", "k", "", "Comma-separated list of pre-extracted keywords for the query")
	_ = cmd.MarkFlagRequired("keywords")
}

func (o *llmDocsSearchOpts) Prepare(cmd *cobra.Command, args []string) error {
	for k := range strings.SplitSeq(o.flagKeywords, ",") {
		k = strings.TrimSpace(k)
		if k != "" {
			o.keywords = append(o.keywords, k)
		}
	}
	if len(o.keywords) == 0 {
		return clierrors.NewUsageError("--keywords must contain at least one non-empty keyword")
	}
	return nil
}

func (o *llmDocsSearchOpts) Run(cmd *cobra.Command) error {
	client, reqMeta, err := newLLMDocsClient()
	if err != nil {
		return err
	}
	defer client.Close()

	resp, err := client.Search(cmd.Context(), &llmdocsclient.SearchRequest{
		Metadata: reqMeta,
		Query:    o.argQuery,
		Keywords: o.keywords,
	})
	if err != nil {
		return wrapLLMDocsError(err, "search documentation")
	}
	printLLMDocsContent(resp.Content)
	return nil
}
