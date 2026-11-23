/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/llmdocsclient"
)

const llmDocsKeywordsSuggestion = "Pass the query's keywords as a comma-separated list, eg, --keywords guilds,social,multiplayer"

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
		Use:   "search QUERY [--keywords KEYWORDS]",
		Short: "Search documentation with a free-form query and --keywords (machine use only)",
		Long: renderLong(&o, `
			Submit an end-user search, plus pre-extracted keywords, to the
			llm-docs service and print the response.

			Always pass --keywords: identifiers, feature names and distinctive
			terms rank better than keywords the service derives from the
			query, and older services reject a search without them.

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
	flags.StringVarP(&o.flagKeywords, "keywords", "k", "", "Comma-separated list of pre-extracted keywords for the query (recommended)")
}

func (o *llmDocsSearchOpts) Prepare(cmd *cobra.Command, args []string) error {
	for k := range strings.SplitSeq(o.flagKeywords, ",") {
		k = strings.TrimSpace(k)
		if k != "" {
			o.keywords = append(o.keywords, k)
		}
	}
	return nil
}

func (o *llmDocsSearchOpts) Run(cmd *cobra.Command) error {
	client, reqMeta, err := newLLMDocsClient()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(cmd.Context(), llmDocsSearchTimeout)
	defer cancel()
	resp, err := client.Search(ctx, &llmdocsclient.SearchRequest{
		Metadata: reqMeta,
		Query:    o.argQuery,
		Keywords: o.keywords,
	})
	if err != nil {
		if len(o.keywords) == 0 {
			if cliErr := missingKeywordsError(err); cliErr != nil {
				return cliErr
			}
		}
		return client.wrapError(err, "search documentation")
	}
	printLLMDocsContent(resp.Content)
	return nil
}

// missingKeywordsError returns nil unless err is a service rejecting a search
// for lacking keywords, which services that predate server-side keyword
// extraction do.
func missingKeywordsError(err error) *clierrors.CLIError {
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument || !strings.Contains(st.Message(), "keywords") {
		return nil
	}
	return clierrors.New("This llm-docs service requires --keywords").
		WithDetails(st.Message()).
		WithSuggestion(llmDocsKeywordsSuggestion)
}
