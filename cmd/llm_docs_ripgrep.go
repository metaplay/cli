/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/metaplay/cli/pkg/llmdocsclient"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type llmDocsRipgrepOpts struct {
	UsePositionalArgs

	argPattern string

	flagFixed         bool
	flagIgnoreCase    bool
	flagLineNumbers   bool
	flagFilesOnly     bool
	flagCountOnly     bool
	flagMultiline     bool
	flagContext       int
	flagBeforeContext int
	flagAfterContext  int
	flagFileTypes     []string
	flagGlobs         []string
	flagPath          string
}

func init() {
	o := llmDocsRipgrepOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argPattern, "PATTERN", "Regex (or literal string with --fixed) to search for.")

	cmd := &cobra.Command{
		Use:   "ripgrep PATTERN [flags]",
		Short: "[preview] Run a ripgrep search against the llm-docs payload (machine use only)",
		Long: renderLong(&o, `
			PREVIEW: This command is in preview and subject to change!

			Run a ripgrep search against the llm-docs payload and print the raw
			text response. Intended for machine consumption (e.g. AI coding
			agents); the output format is not stable for human-driven workflows.
		`),
		Run: runCommand(&o),
		Example: renderExample(`
			# Default regex search across the whole payload.
			metaplay llm-docs ripgrep "Player(Actor|State)"

			# Case-insensitive literal-string search, restricted to markdown docs.
			metaplay llm-docs ripgrep "in-app purchase" --fixed -i --type md

			# Show two lines of surrounding context for each match.
			metaplay llm-docs ripgrep EntityKind -C 2

			# List only the files that contain the pattern.
			metaplay llm-docs ripgrep PlayerActorBase -l

			# Count matches per file in C# sources.
			metaplay llm-docs ripgrep "throw new" -c --type cs

			# Restrict search to a glob filter (matched against file names).
			metaplay llm-docs ripgrep "EntityActor" --glob "*.cs" --path MetaplaySDK/Backend

			# Multi-line regex with line numbers (e.g. find class declarations
			# that span lines).
			metaplay llm-docs ripgrep "class\s+\w+\s*:\s*EntityActor" --multiline -n

			# Scope a search to a subdirectory of the payload.
			metaplay llm-docs ripgrep EntityKind --path MetaplaySDK
		`),
	}

	llmDocsCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.BoolVarP(&o.flagFixed, "fixed", "F", false, "Treat PATTERN as a literal string instead of a regex")
	flags.BoolVarP(&o.flagIgnoreCase, "ignore-case", "i", false, "Case-insensitive matching")
	flags.BoolVarP(&o.flagLineNumbers, "line-numbers", "n", false, "Show line numbers in matches")
	flags.BoolVarP(&o.flagFilesOnly, "files-with-matches", "l", false, "List only the files that contain matches")
	flags.BoolVarP(&o.flagCountOnly, "count", "c", false, "Show only the count of matches per file")
	flags.BoolVar(&o.flagMultiline, "multiline", false, "Allow matches to span multiple lines")
	flags.IntVarP(&o.flagContext, "context", "C", 0, "Lines of context before and after each match")
	flags.IntVarP(&o.flagBeforeContext, "before-context", "B", 0, "Lines of context before each match")
	flags.IntVarP(&o.flagAfterContext, "after-context", "A", 0, "Lines of context after each match")
	flags.StringSliceVar(&o.flagFileTypes, "type", nil, "Restrict search to file types (repeatable, e.g. --type md --type go)")
	flags.StringSliceVar(&o.flagGlobs, "glob", nil, "Restrict search to glob patterns (repeatable, e.g. --glob '*.md')")
	flags.StringVar(&o.flagPath, "path", "", "Subdirectory of the docs payload to search in")
}

func (o *llmDocsRipgrepOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *llmDocsRipgrepOpts) Run(cmd *cobra.Command) error {
	client, err := newLLMDocsClient(cmd.Context())
	if err != nil {
		return err
	}
	defer client.Close()

	resp, err := client.Ripgrep(cmd.Context(), &llmdocsclient.RipgrepRequest{
		Metadata:      buildRequestMetadata(),
		Pattern:       o.argPattern,
		Fixed:         o.flagFixed,
		IgnoreCase:    o.flagIgnoreCase,
		LineNumbers:   o.flagLineNumbers,
		FilesOnly:     o.flagFilesOnly,
		CountOnly:     o.flagCountOnly,
		Multiline:     o.flagMultiline,
		Context:       int32(o.flagContext),
		ContextBefore: int32(o.flagBeforeContext),
		ContextAfter:  int32(o.flagAfterContext),
		FileTypes:     o.flagFileTypes,
		Globs:         o.flagGlobs,
		Path:          o.flagPath,
	})
	if err != nil {
		return wrapLLMDocsError(err, "search documentation")
	}
	log.Info().Msg(resp.Output)
	return nil
}
