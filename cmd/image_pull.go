/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"errors"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/moby/moby/api/pkg/authconfig"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/client"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// imagePullOpts holds the options for the 'image pull' command.
type imagePullOpts struct {
	UsePositionalArgs

	argEnvironment string
	argImageTag    string // Only the tag, name is derived from environment
}

func init() {
	o := imagePullOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")
	args.AddStringArgument(&o.argImageTag, "TAG", "Docker image tag to pull, eg, '20260601-153000-1a27c25'.")

	cmd := &cobra.Command{
		Use:   "pull ENVIRONMENT TAG",
		Short: "Pull a server Docker image from the target environment's repository to the local machine",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Pull a game server docker image from the target environment's image repository to the local docker daemon.

			{Arguments}

			Related commands:
			- Images are pushed to the environment repository using 'metaplay image push ...'.
			- After pulling, the image can be used locally or potentially deployed elsewhere.
		`),
		Example: renderExample(`
			# Pull the docker image with tag '20260601-153000-1a27c25' from environment 'lovely-wombats-build-nimbly'.
			metaplay image pull lovely-wombats-build-nimbly 20260601-153000-1a27c25
		`),
	}
	imageCmd.AddCommand(cmd)
}

func (o *imagePullOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Validate docker image tag: cannot be empty or contain invalid characters like ':'
	if o.argImageTag == "" || strings.Contains(o.argImageTag, ":") {
		return clierrors.NewUsageErrorf("Invalid TAG '%s'", o.argImageTag).
			WithDetails("Tag must be a valid docker tag (cannot be empty or contain ':')").
			WithSuggestion("Use just the tag, for example 'metaplay image pull lovely-wombats-build-nimbly 20260601-153000-1a27c25'")
	}
	return nil
}

func (o *imagePullOpts) Run(cmd *cobra.Command) error {
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

	// Log attempt
	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Pull Docker Image from Cloud"))
	log.Info().Msg("")
	log.Info().Msgf("Source environment: %s", styles.RenderTechnical(envConfig.HumanID))
	log.Info().Msgf("Docker image tag:   %s", styles.RenderTechnical(o.argImageTag))
	log.Info().Msg("")

	// Create TargetEnvironment.
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	// Resolve where the environment's images live, and a credential for them.
	imageRepository, err := targetEnv.ResolveImageRepository()
	if err != nil {
		return err
	}

	// Construct the full remote image name
	remoteImageName := imageRepository.Reference(o.argImageTag)

	// Use task runner to pull the image.
	taskRunner := tui.NewTaskRunner()

	// Pull the image from the remote repository.
	taskRunner.AddTask("Pull docker image from environment repository", func(output *tui.TaskOutput) error {
		return pullDockerImage(cmd.Context(), output, remoteImageName, imageRepository.Credentials)
	})

	// Run the tasks.
	if err = taskRunner.Run(); err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderSuccess("✅ Successfully pulled image!"))
	return nil
}

// pullDockerImage pulls a docker image from a remote repository to the local machine.
// Output progress into the task output.
func pullDockerImage(ctx context.Context, output *tui.TaskOutput, remoteImageName string, dockerCredentials *envapi.DockerCredentials) error {
	// Create a Docker client
	cli, err := envapi.NewDockerClient()
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()

	// Prepare authentication
	authConfig := registry.AuthConfig{
		Username:      dockerCredentials.Username,
		Password:      dockerCredentials.Password,
		ServerAddress: dockerCredentials.RegistryURL,
	}
	// The daemon decodes X-Registry-Auth as base64url. With standard base64, it silently
	// drops credentials whose encoding contains '+' or '/' and sends the request anonymously.
	authStr, err := authconfig.Encode(authConfig)
	if err != nil {
		return clierrors.Wrap(err, "Failed to encode auth config")
	}

	// Pull the image
	output.AppendLinef("Pulling image %s", remoteImageName)
	pullResponse, err := cli.ImagePull(ctx, remoteImageName, client.ImagePullOptions{
		RegistryAuth: authStr,
	})
	if err != nil {
		// Check for common errors like image not found
		if strings.Contains(err.Error(), "manifest for") && strings.Contains(err.Error(), "not found") {
			return clierrors.Newf("Image '%s' not found in the repository", remoteImageName).
				WithSuggestion("Check the tag and environment are correct")
		}
		return clierrors.Wrap(err, "Failed to pull docker image").
			WithSuggestion("Make sure Docker Desktop is running")
	}
	// Follow pull progress. JSONMessages closes the stream when it ends or ctx is cancelled.
	progressIDs := []string{}                     // Track order of progress IDs
	progresses := map[string]jsonstream.Message{} // Track progress by ID

	for progress, err := range pullResponse.JSONMessages(ctx) {
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return clierrors.New("Image pull cancelled")
			}
			return clierrors.Wrap(err, "Failed to decode pull response")
		}

		// Track progress by ID to show the latest status for each layer
		if progress.ID != "" {
			if _, exists := progresses[progress.ID]; !exists {
				progressIDs = append(progressIDs, progress.ID)
			}
			progresses[progress.ID] = progress
		}

		// If progress has an error, return it
		if progress.Error != nil {
			return clierrors.Newf("Error pulling image: %s", progress.Error.Message)
		}

		// Update the output with current progress information (only in interactive mode).
		if tui.IsInteractiveMode() {
			updateDockerProgressOutput(output, progressIDs, progresses)
		}
	}

	// Final update to ensure the last status is shown
	if tui.IsInteractiveMode() {
		updateDockerProgressOutput(output, progressIDs, progresses)
	}

	return nil
}
