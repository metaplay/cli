/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/llmdocsclient"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
		Short: "Run a ripgrep search against the llm-docs payload (machine use only)",
		Long: renderLong(&o, `
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

			# Restrict search to a glob filter. A glob without a slash matches the
			# file name at any depth.
			metaplay llm-docs ripgrep "EntityActor" --glob "*.cs" --path MetaplaySDK/Backend

			# A glob with a slash matches the full payload path, regardless of --path.
			# A leading '!' excludes; later globs take precedence.
			metaplay llm-docs ripgrep "EntityActor" --glob "**/Server/**/*.cs" --glob "!**/Tests/**"

			# Multi-line regex with line numbers (e.g. find class declarations
			# that span lines).
			metaplay llm-docs ripgrep "class\s+\w+\s*:\s*EntityActor" --multiline -n

			# Scope a search to a subdirectory of the payload.
			metaplay llm-docs ripgrep EntityKind --path MetaplaySDK
		`),
	}

	llmDocsCmd.AddCommand(cmd)
	o.registerFlags(cmd.Flags())
}

// registerFlags defines the ripgrep command's flags on flags, bound to o.
// Shared by init and the tests so both parse argv the same way.
func (o *llmDocsRipgrepOpts) registerFlags(flags *pflag.FlagSet) {
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
	// StringArray, not StringSlice: a slice splits each value at commas,
	// which breaks brace alternatives such as "*.{cs,md}".
	flags.StringArrayVar(&o.flagGlobs, "glob", nil, "Include or exclude files by glob, like rg -g (repeatable, one glob per flag). Without a slash it matches the file name at any depth; with a slash it matches the full payload path, regardless of --path. '**' and '{a,b}' alternatives are supported, a leading '!' excludes, and later globs take precedence")
	flags.StringVar(&o.flagPath, "path", "", "Subdirectory of the docs payload to search in")
}

// llmDocsMaxContextLines caps --context/--before-context/--after-context. The
// server sets no limit of its own; this bound keeps a mistyped value from
// returning whole files and rejects values that would not fit the int32
// request fields.
const llmDocsMaxContextLines = 1000

func (o *llmDocsRipgrepOpts) Prepare(cmd *cobra.Command, args []string) error {
	for _, f := range []struct {
		name  string
		value int
	}{
		{"context", o.flagContext},
		{"before-context", o.flagBeforeContext},
		{"after-context", o.flagAfterContext},
	} {
		if f.value < 0 || f.value > llmDocsMaxContextLines {
			return clierrors.NewUsageErrorf("--%s must be between 0 and %d", f.name, llmDocsMaxContextLines)
		}
	}
	return nil
}

func (o *llmDocsRipgrepOpts) Run(cmd *cobra.Command) error {
	client, reqMeta, err := newLLMDocsClient()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(cmd.Context(), llmDocsDefaultTimeout)
	defer cancel()
	resp, err := client.Ripgrep(ctx, &llmdocsclient.RipgrepRequest{
		Metadata:      reqMeta,
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
		return wrapLLMDocsError(err, "run ripgrep")
	}
	printLLMDocsContent(resp.Output)
	return nil
}
