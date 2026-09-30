/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type imageListOpts struct {
	UsePositionalArgs

	argEnvironment string
	flagFormat     string
	flagLimit      int
}

func init() {
	o := imageListOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")

	cmd := &cobra.Command{
		Use:   "list ENVIRONMENT [flags]",
		Short: "List Docker images in the target environment's image repository",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			List Docker images in the target environment's image repository, newest built first.

			Tags naming the same image are listed together on one row.

			BUILT is when the image was built, read from its config. The registry protocol does not
			report when an image was pushed, so re-pushing an old image does not change it.

			SIZE is the compressed size of the image's config and layers as the registry stores
			them, summed over every platform of a multi-platform image. It is smaller than
			'docker images' reports for an unpacked image, and for a multi-platform image larger
			than a single platform's download.

			Every tag is read to find when its image was built, so listing a repository with many
			tags takes a while even with --limit.

			{Arguments}

			Related commands:
			- Pull an image to the local machine using 'metaplay image pull ...'.
			- Push a built image to the repository using 'metaplay image push ...'.
		`),
		Example: renderExample(`
			# List the 20 most recent images in environment 'lovely-wombats-build-nimbly'.
			metaplay image list lovely-wombats-build-nimbly

			# List all images in JSON format.
			metaplay image list lovely-wombats-build-nimbly --format=json --limit=0
		`),
	}

	imageCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.StringVar(&o.flagFormat, "format", "text", "Output format: 'text' or 'json'")
	flags.IntVar(&o.flagLimit, "limit", 20, "Maximum number of images to show (0 for all)")
}

func (o *imageListOpts) Prepare(cmd *cobra.Command, args []string) error {
	if o.flagFormat != "text" && o.flagFormat != "json" {
		return clierrors.NewUsageErrorf("Invalid format %q", o.flagFormat).
			WithSuggestion("Use 'text' or 'json'")
	}
	if o.flagLimit < 0 {
		return clierrors.NewUsageErrorf("Invalid limit %d", o.flagLimit).
			WithSuggestion("Use a non-negative number (0 for all)")
	}
	return nil
}

func (o *imageListOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}

	// Resolve environment.
	envConfig, tokenSet, err := resolveEnvironment(cmd.Context(), project, o.argEnvironment)
	if err != nil {
		return err
	}

	// Create TargetEnvironment.
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	// Resolve where the environment's images live, and a credential for them.
	imageRepository, err := targetEnv.ResolveImageRepository()
	if err != nil {
		return err
	}

	// List every image, newest built first.
	images, err := envapi.ListRepositoryImages(cmd.Context(), imageRepository)
	if err != nil {
		return clierrors.Wrap(err, "Failed to list the environment's images").
			WithSuggestion("Check that you have access to this environment, and that its image registry is reachable")
	}
	shown := images
	if o.flagLimit > 0 && len(shown) > o.flagLimit {
		shown = shown[:o.flagLimit]
	}

	// Output in desired format.
	if o.flagFormat == "json" {
		imagesJSON, err := json.MarshalIndent(shown, "", "  ")
		if err != nil {
			return clierrors.Wrap(err, "Failed to marshal images as JSON")
		}
		log.Info().Msg(string(imagesJSON))
		return nil
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Docker Images"))
	log.Info().Msg("")
	log.Info().Msgf("Environment: %s", styles.RenderTechnical(envConfig.HumanID))
	log.Info().Msg("")

	if len(shown) == 0 {
		log.Info().Msg("No images found in the repository.")
	} else {
		header, rows := imageListTable(shown)

		// Compute column widths from the plain text, before styling.
		widths := make([]int, len(header))
		for column, heading := range header {
			widths[column] = len(heading)
			for _, row := range rows {
				widths[column] = max(widths[column], len(row[column]))
			}
		}
		pad := func(column int, text string) string {
			return fmt.Sprintf("%-*s", widths[column], text)
		}

		log.Info().Msgf("  %s  %s  %s  %s  %s", pad(0, header[0]), pad(1, header[1]), pad(2, header[2]), pad(3, header[3]), header[4])
		log.Info().Msg("")
		for _, row := range rows {
			log.Info().Msgf("  %s  %s  %s  %s  %s",
				styles.RenderTechnical(pad(0, row[0])),
				pad(1, row[1]),
				pad(2, row[2]),
				styles.RenderMuted(pad(3, row[3])),
				row[4],
			)
		}
	}

	if footers := imageListFooters(images, o.flagLimit); len(footers) > 0 {
		log.Info().Msg("")
		for _, footer := range footers {
			log.Info().Msg(styles.RenderMuted("  " + footer))
		}
	}
	log.Info().Msg("")

	return nil
}

// imageListTable is the table the text output shows: a heading per column,
// and a row per image, in the order given. An image that could not be read
// keeps its tags and leaves its other columns blank.
func imageListTable(images []envapi.RepositoryImage) (header []string, rows [][]string) {
	header = []string{"TAG", "SDK", "COMMIT", "BUILT", "SIZE"}
	for _, image := range images {
		row := []string{strings.Join(image.Tags, ", "), "", "", "", ""}
		if image.Readable() {
			commit := image.CommitID
			if len(commit) > 12 {
				commit = commit[:12]
			}
			built := ""
			if !image.BuiltAt.IsZero() {
				built = image.BuiltAt.UTC().Format("2006-01-02 15:04")
			}
			row = []string{row[0], image.SdkVersion, commit, built, formatImageSize(image.SizeBytes)}
		}
		rows = append(rows, row)
	}
	return header, rows
}

// imageListFooters says what the table leaves out: images past the limit, and
// how many images could not be read. Counted over every image rather than the
// ones shown, since those that could not be read sort last and are the first a
// limit cuts.
func imageListFooters(images []envapi.RepositoryImage, limit int) []string {
	var footers []string
	if limit > 0 && len(images) > limit {
		footers = append(footers, fmt.Sprintf("Showing %d of %d images. Use --limit to see more.", limit, len(images)))
	}
	unreadable := 0
	for _, image := range images {
		if !image.Readable() {
			unreadable++
		}
	}
	switch unreadable {
	case 0:
	case 1:
		footers = append(footers, "1 image could not be read. Use --format=json --limit=0 to see why.")
	default:
		footers = append(footers, fmt.Sprintf("%d images could not be read. Use --format=json --limit=0 to see why.", unreadable))
	}
	return footers
}

func formatImageSize(bytes int64) string {
	const MB = 1024 * 1024
	const GB = 1024 * 1024 * 1024
	if bytes >= GB {
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
}
