/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Push the (already built) docker image to the remote docker repository.
type imagePushOpts struct {
	UsePositionalArgs

	argEnvironment string
	argImageName   string
}

func init() {
	o := imagePushOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argEnvironment, "ENVIRONMENT", "Target environment ID, eg, 'tough-falcons'.")
	args.AddStringArgument(&o.argImageName, "IMAGE:TAG", "Docker image name and tag, eg, 'mygame:364cff09'.")

	cmd := &cobra.Command{
		Use:   "push ENVIRONMENT IMAGE:TAG",
		Short: "Push a built server Docker image to the target environment's docker image repository",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Push a built game server docker image to the target environment's image repository.

			{Arguments}

			Related commands:
			- The docker image can be built with 'metaplay build image ...'.
			- After pushing, the image can be deployed into the environment using 'metaplay deploy server ...'.
		`),
		Example: renderExample(`
			# Push the docker image 'mygame:1a27c25753' into environment 'tough-falcons'.
			metaplay image push tough-falcons mygame:1a27c25753
		`),
	}
	imageCmd.AddCommand(cmd)
}

func (o *imagePushOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Validate docker image name: must be a repository:tag pair.
	if !strings.Contains(o.argImageName, ":") {
		return fmt.Errorf("IMAGE must be a full docker image name 'NAME:TAG', got '%s'", o.argImageName)
	}

	return nil
}

func (o *imagePushOpts) Run(cmd *cobra.Command) error {
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
	log.Info().Msg(styles.RenderTitle("Push Docker Image to Cloud"))
	log.Info().Msg("")
	log.Info().Msgf("Target environment: %s", styles.RenderTechnical(envConfig.HumanID))
	log.Info().Msgf("Docker image name: %s", styles.RenderTechnical(o.argImageName))
	log.Info().Msg("")

	// Create TargetEnvironment.
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	// Get environment details.
	envDetails, err := targetEnv.GetDetails()
	if err != nil {
		return err
	}

	// Get docker credentials.
	dockerCredentials, err := targetEnv.GetDockerCredentials(envDetails)
	if err != nil {
		return err
	}
	log.Debug().Msgf("Got docker credentials: username=%s", dockerCredentials.Username)

	// Use task runner to push the image.
	taskRunner := tui.NewTaskRunner()

	// Push the image to the remote repository.
	taskRunner.AddTask("Push docker image to environment repository", func(output *tui.TaskOutput) error {
		return pushDockerImage(cmd.Context(), output, o.argImageName, envDetails.Deployment.EcrRepo, dockerCredentials)
	})

	// Run the tasks.
	if err = taskRunner.Run(); err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderSuccess("✅ Successfully pushed image!"))
	return nil
}

// Extrat the tag from a full 'name:tag' docker image name.
func extractDockerImageTag(imageName string) (string, error) {
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

// Push a docker image from the local repo to a remote one.
// Output progress into the task output.
func pushDockerImage(ctx context.Context, output *tui.TaskOutput, imageName, dstRepoName string, dockerCredentials *envapi.DockerCredentials) error {
	// Create a Docker client
	// \todo This has been observed to fail on Tuomo's Mac with: "Cannot connect to the Docker daemon
	// at unix:///var/run/docker.sock. Is the docker daemon running?"
	// For details, see comments on https://github.com/metaplay/sdk/pull/3627
	cli, err := client.NewClientWithOpts(client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}

	// Extract tag from source image.
	imageTag, err := extractDockerImageTag(imageName)
	if err != nil {
		return err
	}

	// Resolve source and destination image names.
	srcImageName := imageName
	dstImageName := fmt.Sprintf("%s:%s", dstRepoName, imageTag)

	// If names don't match, tag the source image as the destination.
	if srcImageName != dstImageName {
		output.AppendLinef("Tagging image %s as %s", srcImageName, dstImageName)
		if err := cli.ImageTag(ctx, srcImageName, dstImageName); err != nil {
			return fmt.Errorf("failed to tag image: %w", err)
		}
	}

	// Push the image
	output.AppendLinef("Pushing image %s", dstImageName)
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
	decoder := json.NewDecoder(pushResponseReader)
	progressIDs := []string{}                          // Track order of progress IDs
	progresses := map[string]jsonmessage.JSONMessage{} // Track progress by ID

	for {
		var progress jsonmessage.JSONMessage
		if err := decoder.Decode(&progress); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to decode push response: %w", err)
		}

		// Track progress by ID to show the latest status for each layer
		if progress.ID != "" {
			// Add ID to the order tracking slice if it's not already there
			if _, exists := progresses[progress.ID]; !exists {
				progressIDs = append(progressIDs, progress.ID)
			}
			progresses[progress.ID] = progress
		}

		// If progress has an error, return it
		if progress.Error != nil {
			return fmt.Errorf("error pushing image: %s", progress.Error.Message)
		}

		// Update the output with current progress information (only in interactive mode).
		if tui.IsInteractiveMode() {
			updateProgressOutput(output, dstImageName, progressIDs, progresses)
		}
	}

	return nil
}

// updateProgressOutput updates the task output with the current push progress information
func updateProgressOutput(output *tui.TaskOutput, imageName string, progressIDs []string, progresses map[string]jsonmessage.JSONMessage) {
	// Start with the header line
	lines := []string{}

	// Add progress for each layer
	for _, id := range progressIDs {
		// Skip empty progress entries
		if progresses[id].Progress == nil && progresses[id].Status == "" {
			continue
		}

		// Format the progress line
		progressLine := fmt.Sprintf("Layer %s: %s", id[:12], progresses[id].Status)
		if progresses[id].Progress != nil {
			progressLine += fmt.Sprintf(" %s", progresses[id].Progress.String())
		}
		lines = append(lines, progressLine)
	}

	// Update all lines at once
	output.SetFooterLines(lines)
}
