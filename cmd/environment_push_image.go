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
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var flagImageName string

// Push the (already built) docker image to the remote docker registry.
var environmentPushImageCmd = &cobra.Command{
	Use:   "push-image",
	Short: "Push a built server docker image to the environment's docker image registry",
	Run:   runPushImageCmd,
}

func init() {
	environmentCmd.AddCommand(environmentPushImageCmd)

	environmentPushImageCmd.Flags().StringVar(&flagImageName, "image-tag", "", "Full name of the docker image, e.g., 'mygame:<sha>'")

	environmentPushImageCmd.MarkFlagRequired("image-tag")
}

func runPushImageCmd(cmd *cobra.Command, args []string) {
	// Ensure we have fresh tokens.
	tokenSet, err := auth.EnsureValidTokenSet()
	if err != nil {
		log.Error().Msgf("Failed to get credentials: %v", err)
		os.Exit(1)
	}

	// Resolve target environment.
	targetEnv, err := resolveTargetEnvironment(tokenSet)
	if err != nil {
		log.Error().Msgf("Failed to resolve environment: %v", err)
		os.Exit(1)
	}

	// Validate docker image name: must be a repository:tag pair.
	if flagImageName == "" {
		log.Error().Msg("Must provide a docker image name with --image-tag=<name>:<tag>")
		os.Exit(2)
	}
	if !strings.Contains(flagImageName, ":") {
		log.Error().Msg("Must provide a full docker image name with --image-tag=<name>:<tag>")
		os.Exit(2)
	}

	// Resolve image tag.
	imageTag, err := resolveImageTag(flagImageName)

	// Log attempt
	log.Info().Msgf("Pushing docker image %s to target environment %s...", flagImageName, targetEnv.HumanId)

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
	err = pushDockerImage(flagImageName, imageTag, envDetails.Deployment.EcrRepo, dockerCredentials)
	if err != nil {
		log.Error().Msgf("Failed to push docker image: %v", err)
		os.Exit(1)
	}

	log.Info().Msgf("Successfully pushed image!")
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

func pushDockerImage(imageName string, imageTag string, dstRepoName string, dockerCredentials *envapi.DockerCredentials) error {
	ctx := context.Background()

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
