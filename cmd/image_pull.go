/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/moby/moby/api/pkg/authconfig"
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
	cli, err := envapi.NewDockerClient(ctx, output.AppendLine)
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

	output.AppendLinef("Pulling image %s", remoteImageName)
	checkPull := func() envapi.RegistryAccess {
		return envapi.CheckRegistryAccess(ctx, dockerCredentials, remoteImageName, false)
	}
	pullResponse, err := cli.ImagePull(ctx, remoteImageName, client.ImagePullOptions{
		RegistryAuth: authStr,
	})
	if err != nil {
		return dockerTransferFailed(ctx, err, "pull", remoteImageName, checkPull)
	}
	if err := followDockerProgress(output, pullResponse.JSONMessages(ctx)); err != nil {
		return dockerTransferFailed(ctx, err, "pull", remoteImageName, checkPull)
	}
	return nil
}
