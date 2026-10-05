/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"math"
	"net/url"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/llmdocsclient"
	"github.com/rs/zerolog/log"
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
	args.AddStringArgument(&o.argPath, "PATH", "Path of the file to read (e.g. index.md, MetaplaySDK/version.yaml), or a https://docs.metaplay.io/ page URL.")

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

			# Read a docs page by its URL (served from docs/cloud-deployments/getting-started.md).
			metaplay llm-docs read https://docs.metaplay.io/cloud-deployments/getting-started

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
	path, err := llmDocsPathFromArg(o.argPath)
	if err != nil {
		return err
	}
	o.argPath = path
	return nil
}

// llmDocsPathFromArg maps a documentation site URL to its llm-docs payload
// path. Other arguments are returned unchanged.
func llmDocsPathFromArg(arg string) (string, error) {
	// SDK doc comments link guides as https://docs.metaplay.io/<path>, and agents
	// pass the URL verbatim. The payload mirrors the site's pages as docs/<path>.md.
	const (
		ioHost  = "docs.metaplay.io"
		devHost = "docs.metaplay.dev"
	)

	lower := strings.ToLower(arg)
	if !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "http://") {
		return arg, nil
	}

	u, err := url.Parse(arg)
	if err != nil {
		return "", clierrors.WrapUsageError(err, "Invalid URL")
	}
	switch strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.") {
	case ioHost, devHost:
	default:
		return "", clierrors.NewUsageErrorf("Not a Metaplay documentation URL: %s", arg).
			WithDetails("Supported hosts: " + ioHost + ", " + devHost)
	}

	path := strings.TrimPrefix(u.Path, "/")
	lowerPath := strings.ToLower(path)
	switch {
	case path == "" || strings.HasSuffix(path, "/"):
		path += "index.md"
	case strings.HasSuffix(lowerPath, ".html"):
		// The site also serves each page at <path>.html.
		path = path[:len(path)-len(".html")] + ".md"
	case strings.HasSuffix(lowerPath, ".md"):
		path = path[:len(path)-len(".md")] + ".md"
	default:
		path += ".md"
	}
	// Release notes live at the payload root rather than under docs/.
	const releaseNotesDir = "miscellaneous/sdk-updates/release-notes/"
	if strings.HasPrefix(path, releaseNotesDir) {
		path = "release-notes/" + strings.TrimPrefix(path, releaseNotesDir)
	} else {
		path = "docs/" + path
	}
	log.Debug().Msgf("llm-docs: resolved %s to %s", arg, path)
	return path, nil
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
