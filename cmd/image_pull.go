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

// PullImageOptions holds the options for the 'image pull' command.
type PullImageOptions struct {
	UsePositionalArgs

	argEnvironment string
	argImageTag    string // Only the tag, name is derived from environment
}

func init() {
	o := PullImageOptions{}

	args := o.Arguments()
	args.AddStringArgument(&o.argEnvironment, "ENVIRONMENT", "Target environment ID from which to pull the image, eg, 'tough-falcons'.")
	args.AddStringArgument(&o.argImageTag, "TAG", "Docker image tag to pull, eg, '364cff09'.")

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
			# Pull the docker image with tag '1a27c25753' from environment 'tough-falcons'.
			metaplay image pull tough-falcons 1a27c25753
		`),
	}
	imageCmd.AddCommand(cmd)
}

func (o *PullImageOptions) Prepare(cmd *cobra.Command, args []string) error {
	// Validate docker image tag: cannot be empty or contain invalid characters like ':'
	if o.argImageTag == "" || strings.Contains(o.argImageTag, ":") {
		return fmt.Errorf("invalid TAG '%s', must be a valid docker tag (cannot be empty or contain ':')", o.argImageTag)
	}
	return nil
}

func (o *PullImageOptions) Run(cmd *cobra.Command) error {
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

	// Construct the full remote image name
	remoteImageName := fmt.Sprintf("%s:%s", envDetails.Deployment.EcrRepo, o.argImageTag)

	// Use task runner to pull the image.
	taskRunner := tui.NewTaskRunner()

	// Pull the image from the remote repository.
	taskRunner.AddTask("Pull docker image from environment repository", func(output *tui.TaskOutput) error {
		return pullDockerImage(cmd.Context(), output, remoteImageName, dockerCredentials)
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
	cli, err := client.NewClientWithOpts(client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	// Prepare authentication
	authConfig := registry.AuthConfig{
		Username:      dockerCredentials.Username,
		Password:      dockerCredentials.Password,
		ServerAddress: dockerCredentials.RegistryURL,
	}
	authConfigBytes, err := json.Marshal(authConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal auth config: %w", err)
	}
	authStr := base64.StdEncoding.EncodeToString(authConfigBytes)

	// Pull the image
	output.AppendLinef("Pulling image %s", remoteImageName)
	pullResponseReader, err := cli.ImagePull(ctx, remoteImageName, image.PullOptions{
		RegistryAuth: authStr,
	})
	if err != nil {
		// Check for common errors like image not found
		if strings.Contains(err.Error(), "manifest for") && strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("failed to pull docker image: image '%s' not found in the repository. Check the tag and environment", remoteImageName)
		}
		return fmt.Errorf("failed to pull docker image: %w", err)
	}
	defer pullResponseReader.Close()

	// Follow pull progress
	decoder := json.NewDecoder(pullResponseReader)
	progressIDs := []string{}                          // Track order of progress IDs
	progresses := map[string]jsonmessage.JSONMessage{} // Track progress by ID

	for {
		var progress jsonmessage.JSONMessage
		if err := decoder.Decode(&progress); err != nil {
			if err == io.EOF {
				break // End of stream
			}
			if errors.Is(err, context.Canceled) {
				return fmt.Errorf("image pull cancelled")
			}
			return fmt.Errorf("failed to decode pull response: %w", err)
		}

		// Track progress by ID to show the latest status for each layer
		if progress.ID != "" {
			if _, exists := progresses[progress.ID]; !exists {
				progressIDs = append(progressIDs, progress.ID)
			}
			progresses[progress.ID] = progress
		}

		// If progress has an error, return it (unless it's just a status update)
		if progress.Error != nil {
			// Sometimes errors are reported mid-stream but don't halt the pull (e.g., retries)
			// We might want to log these instead of failing immediately.
			// However, for now, treat any error as fatal to be safe.
			log.Warn().Msgf("Error during pull: %s", progress.Error.Message)
			// return fmt.Errorf("error pulling image: %s", progress.Error.Message)
		}

		// Update the output with current progress information (only in interactive mode).
		if tui.IsInteractiveMode() {
			updatePullProgressOutput(output, remoteImageName, progressIDs, progresses)
		}
	}

	// Final update to ensure the last status is shown
	if tui.IsInteractiveMode() {
		updatePullProgressOutput(output, remoteImageName, progressIDs, progresses)
	}

	return nil
}

// updatePullProgressOutput updates the task output with the current pull progress information
func updatePullProgressOutput(output *tui.TaskOutput, imageName string, progressIDs []string, progresses map[string]jsonmessage.JSONMessage) {
	lines := []string{}

	// Add progress for each layer ID encountered
	for _, id := range progressIDs {
		progress, exists := progresses[id]
		if !exists || (progress.Progress == nil && progress.Status == "") {
			continue // Skip empty or non-existent entries
		}

		// Format the progress line
		status := progress.Status
		progressStr := ""
		if progress.Progress != nil {
			progressStr = progress.Progress.String()
		}

		// Some statuses are quite verbose, try to shorten common ones
		if strings.HasPrefix(status, "Pulling fs layer") {
			status = "Pulling layer"
		} else if strings.HasPrefix(status, "Waiting") {
			status = "Waiting"
		} else if strings.HasPrefix(status, "Downloading") {
			status = "Downloading"
		} else if strings.HasPrefix(status, "Verifying Checksum") {
			status = "Verifying"
		} else if strings.HasPrefix(status, "Download complete") {
			status = "Downloaded"
		} else if strings.HasPrefix(status, "Extracting") {
			status = "Extracting"
		} else if strings.HasPrefix(status, "Pull complete") {
			status = "Complete"
		}

		progressLine := fmt.Sprintf("Layer %s: %s %s", id[:min(12, len(id))], status, progressStr)
		lines = append(lines, strings.TrimSpace(progressLine))
	}

	// Update all lines at once
	output.SetFooterLines(lines)
}

func min(a, b int) int {
    if a < b {
        return a
    }
    return b
}
