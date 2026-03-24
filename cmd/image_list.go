/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/syncutil"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// imageListEntry combines ECR image info with optional metadata from image labels.
type imageListEntry struct {
	envapi.ECRImage
	SdkVersion string `json:"sdkVersion,omitempty"`
	CommitID   string `json:"commitId,omitempty"`
}

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
			List Docker images available in the target environment's image repository (ECR).

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

	// Get environment details.
	envDetails, err := targetEnv.GetDetails()
	if err != nil {
		return err
	}

	// Get docker credentials for metadata fetching.
	dockerCredentials, err := targetEnv.GetDockerCredentials(envDetails)
	if err != nil {
		return err
	}

	// List images from ECR.
	images, err := targetEnv.ListECRImages(envDetails, o.flagLimit)
	if err != nil {
		return err
	}

	// Sort by push date descending (newest first).
	slices.SortFunc(images, func(a, b envapi.ECRImage) int {
		return b.PushedAt.Compare(a.PushedAt)
	})

	// Apply limit after sorting.
	if o.flagLimit > 0 && len(images) > o.flagLimit {
		images = images[:o.flagLimit]
	}

	// Enrich images with metadata (SDK version, commit ID) from Docker labels.
	entries := fetchImageMetadata(images, envDetails.Deployment.EcrRepo, dockerCredentials)

	// Output in desired format.
	if o.flagFormat == "json" {
		imagesJSON, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			return clierrors.Wrap(err, "Failed to marshal images as JSON")
		}
		log.Info().Msg(string(imagesJSON))
	} else {
		log.Info().Msg("")
		log.Info().Msg(styles.RenderTitle("Docker Images"))
		log.Info().Msg("")
		log.Info().Msgf("Environment: %s", styles.RenderTechnical(envConfig.HumanID))
		log.Info().Msg("")

		if len(entries) == 0 {
			log.Info().Msg("No images found in the repository.")
		} else {
			// Compute column widths from data.
			tagW := len("TAG")
			sdkW := len("SDK")
			for _, e := range entries {
				tag := strings.Join(e.Tags, ", ")
				if len(tag) > tagW {
					tagW = len(tag)
				}
				if len(e.SdkVersion) > sdkW {
					sdkW = len(e.SdkVersion)
				}
			}

			// Print header
			log.Info().Msgf("  %-*s  %-*s  %-12s  %-16s  %s", tagW, "TAG", sdkW, "SDK", "COMMIT", "PUSHED", "SIZE")
			log.Info().Msg("")

			for _, e := range entries {
				tag := strings.Join(e.Tags, ", ")
				pushed := e.PushedAt.Format("2006-01-02 15:04")
				size := formatImageSize(e.SizeBytes)
				commit := e.CommitID
				if len(commit) > 12 {
					commit = commit[:12]
				}

				// Pad plain text before applying ANSI styles.
				log.Info().Msgf("  %s  %s  %-12s  %s  %s",
					styles.RenderTechnical(fmt.Sprintf("%-*s", tagW, tag)),
					fmt.Sprintf("%-*s", sdkW, e.SdkVersion),
					commit,
					styles.RenderMuted(fmt.Sprintf("%-16s", pushed)),
					size,
				)
			}

			// Show truncation footer if applicable
			if o.flagLimit > 0 && len(entries) == o.flagLimit {
				log.Info().Msg("")
				log.Info().Msg(styles.RenderMuted(fmt.Sprintf("  Showing first %d images. Use --limit to see more.", o.flagLimit)))
			}
		}

		log.Info().Msg("")
	}

	return nil
}

// fetchImageMetadata enriches ECR images with SDK version and commit ID from Docker image labels.
// Metadata is fetched concurrently with up to 10 requests in flight.
func fetchImageMetadata(images []envapi.ECRImage, ecrRepo string, creds *envapi.DockerCredentials) []imageListEntry {
	return syncutil.ParallelMap(images, 10, func(img envapi.ECRImage) imageListEntry {
		entry := imageListEntry{ECRImage: img}
		if len(img.Tags) == 0 {
			return entry
		}
		imageRef := fmt.Sprintf("%s:%s", ecrRepo, img.Tags[0])
		info, err := envapi.FetchRemoteDockerImageMetadata(creds, imageRef)
		if err != nil {
			log.Debug().Msgf("Failed to fetch metadata for %s: %v", imageRef, err)
			return entry
		}
		entry.SdkVersion = info.SdkVersion
		entry.CommitID = info.CommitID
		return entry
	})
}

func formatImageSize(bytes int64) string {
	const MB = 1024 * 1024
	const GB = 1024 * 1024 * 1024
	if bytes >= GB {
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
}
