/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Push the (already built) docker image to the remote docker registry.
type PushImageOptions struct {
	argEnvironment string
	argImageName   string
}

func init() {
	o := PushImageOptions{}

	cmd := &cobra.Command{
		Use:   "push-image ENVIRONMENT IMAGE:TAG",
		Short: "Push a built server docker image to the environment's docker image registry",
		Run:   runCommand(&o),
		Long: trimIndent(`
			Push a built game server docker image to the target environment's image registry.

			Arguments:
			- ENVIRONMENT must be one that is declared in the environments list in metaplay-project.yaml.
			- IMAGE:TAG must be a fully-formed docker image name and tag, e.g., 'mygame:1a27c25753'.

			Related commands:
			- The docker image can be built with 'metaplay build docker-image ...'.
			- After pushing, the image can be deployed into the environment using 'metaplay env deploy-server ...'.
		`),
		Example: trimIndent(`
			# Push the docker image 'mygame:1a27c25753' into environment 'tough-falcons'.
			metaplay environment push-image tough-falcons mygame:1a27c25753
		`),
	}
	environmentCmd.AddCommand(cmd)
}

func (o *PushImageOptions) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("exactly two arguments must be provided, got %d", len(args))
	}

	// Environment.
	o.argEnvironment = args[0]

	// Validate docker image name: must be a repository:tag pair.
	o.argImageName = args[1]
	if o.argImageName == "" {
		return fmt.Errorf("IMAGE must be non-empty")
		// log.Error().Msg("Must provide a docker image name with --image-tag=<name>:<tag>")
		// os.Exit(2)
	}
	if !strings.Contains(o.argImageName, ":") {
		return fmt.Errorf("IMAGE must be a full docker image name 'REPOSITORY:TAG', got '%s'", o.argImageName)
		// log.Error().Msg("Must provide a full docker image name with --image-tag=<name>:<tag>")
		// os.Exit(2)
	}

	return nil
}

func (o *PushImageOptions) Run(cmd *cobra.Command) error {
	// Ensure the user is logged in
	tokenSet, err := tui.RequireLoggedIn(cmd.Context())
	if err != nil {
		return err
	}

	// Resolve environment.
	envConfig, err := resolveEnvironment(tokenSet, o.argEnvironment)
	if err != nil {
		return err
	}

	// Create TargetEnvironment.
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	// Resolve image tag.
	imageTag, err := resolveImageTag(o.argImageName)
	if err != nil {
		return err
	}

	// Log attempt
	log.Info().Msgf("Push docker image %s to environment %s...", o.argImageName, targetEnv.HumanId)

	// Get environment details.
	log.Debug().Msg("Get environment details")
	envDetails, err := targetEnv.GetDetails()
	if err != nil {
		log.Error().Msgf("failed to get environment details: %v", err)
		os.Exit(1)
	}

	// Get docker credentials.
	dockerCredentials, err := targetEnv.GetDockerCredentials(envDetails)
	if err != nil {
		log.Error().Msgf("Failed to get docker credentials: %v", err)
		os.Exit(1)
	}
	log.Debug().Msgf("Got docker credentials: username=%s", dockerCredentials.Username)

	// Push the image.
	err = pushDockerImage(cmd.Context(), o.argImageName, imageTag, envDetails.Deployment.EcrRepo, dockerCredentials)
	if err != nil {
		log.Error().Msgf("Failed to push docker image: %v", err)
		os.Exit(1)
	}

	log.Info().Msgf("Successfully pushed image!")
	return nil
}

func resolveImageTag(imageName string) (string, error) {
	// Check if the image name is empty
	if imageName == "" {
		return "", errors.New("must specify a valid docker image name as the image-name argument")
	}

	// Split the image name into parts
	srcImageParts := strings.Split(imageName, ":")
	if len(srcImageParts) != 2 || len(srcImageParts[0]) == 0 || len(srcImageParts[1]) == 0 {
		return "", fmt.Errorf("invalid docker image name '%s', expecting the name in format 'name:tag'", imageName)
	}

	// Return the tag part of the image name
	return srcImageParts[1], nil
}

func pushDockerImage(ctx context.Context, imageName string, imageTag string, dstRepoName string, dockerCredentials *envapi.DockerCredentials) error {
	// Create a Docker client
	// \todo This has been observed to fail on Tuomo's Mac with: "Cannot connect to the Docker daemon
	// at unix:///var/run/docker.sock. Is the docker daemon running?"
	// For details, see comments on https://github.com/metaplay/sdk/pull/3627
	cli, err := client.NewClientWithOpts(client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}

	// Resolve source and destination image names
	srcImageName := imageName
	dstImageName := fmt.Sprintf("%s:%s", dstRepoName, imageTag)

	// If names don't match, tag the source image as the destination
	if srcImageName != dstImageName {
		log.Printf("Tagging image %s as %s", srcImageName, dstImageName)
		if err := cli.ImageTag(ctx, srcImageName, dstImageName); err != nil {
			return fmt.Errorf("failed to tag image: %w", err)
		}
	}

	// Push the image
	log.Debug().Msgf("Pushing image %s", dstImageName)
	authConfig := registry.AuthConfig{
		Username:      dockerCredentials.Username,
		Password:      dockerCredentials.Password,
		ServerAddress: dockerCredentials.RegistryURL,
	}
	authConfigBytes, err := json.Marshal(authConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal auth config: %w", err)
	}

	// Encode with base64
	authStr := string(base64.StdEncoding.EncodeToString(authConfigBytes))

	pushResponseReader, err := cli.ImagePush(ctx, dstImageName, image.PushOptions{
		RegistryAuth: authStr,
	})
	if err != nil {
		return fmt.Errorf("failed to push docker image: %w", err)
	}
	defer pushResponseReader.Close()

	// Follow push progress
	log.Debug().Msg("Following image push stream...")
	decoder := json.NewDecoder(pushResponseReader)
	for {
		var progress jsonmessage.JSONMessage
		if err := decoder.Decode(&progress); err != nil {
			if err.Error() == "EOF" {
				break
			}
			return fmt.Errorf("failed to follow push progress: %w", err)
		}
		if progress.Error != nil {
			log.Printf("Error pushing image: %v", progress.Error)
			return fmt.Errorf("push error: %v", progress.Error)
		}
		if progress.Status != "" {
			log.Debug().Msgf("Docker push progress: %s", progress.Status)
		}
	}

	return nil
}
