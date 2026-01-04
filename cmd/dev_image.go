/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Run a built docker image locally.
type devImageOpts struct {
	UsePositionalArgs

	argImageTag string
	extraArgs   []string
}

func init() {
	o := devImageOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argImageTag, "IMAGE:TAG", "Docker image name and tag, eg, 'mygame:364cff09'.")
	args.SetExtraArgs(&o.extraArgs, "Passed as-is to 'docker run'.")

	cmd := &cobra.Command{
		Use:   "image IMAGE:TAG [flags] [-- EXTRA_ARGS]",
		Short: "Run a server Docker image locally",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Run a pre-built docker image locally.

			The LiveOps Dashboard is served at http://localhost:5550.

			{Arguments}

			Related commands:
			- 'metaplay build image ...' to build a server Docker image.
		`),
		Example: renderExample(`
			# Run the docker image (until terminated).
			metaplay dev image mygame:test

			# Run the latest built local docker image.
			metaplay dev image latest-local
		`),
	}

	devCmd.AddCommand(cmd)
}

func (o *devImageOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *devImageOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := resolveProject()
	if err != nil {
		return err
	}

	// Check that docker is installed and running
	if err := checkCommand("docker", "info"); err != nil {
		return fmt.Errorf("failed to invoke docker. Ensure docker is installed and running.")
	}

	// If no docker image specified, scan the images matching project from the local docker repo
	// and then let the user choose from the images.
	if o.argImageTag == "" {
		selectedImage, err := selectDockerImageInteractively("Select Image to Run Locally", project.Config.ProjectHumanID)
		if err != nil {
			return err
		}
		o.argImageTag = selectedImage.RepoTag
	} else if o.argImageTag == "latest-local" {
		// Resolve the local docker images matching project human ID.
		localImages, err := envapi.ReadLocalDockerImagesByProjectID(project.Config.ProjectHumanID)
		if err != nil {
			return err
		}

		// If there are no images for this project, error out.
		if len(localImages) == 0 {
			return fmt.Errorf("no docker images matching project '%s' found locally; build an image first with 'metaplay build image'", project.Config.ProjectHumanID)
		}

		// Use the first entry (they are reverse sorted by creation time).
		o.argImageTag = localImages[0].RepoTag
	}

	// Construct docker run args.
	dockerRunArgs := []string{
		"run",
		"--rm",
		"-e", "METAPLAY_ENVIRONMENT_FAMILY=Local",
		"-p=127.0.0.1:5550:5550", // LiveOps Dashboard & admin API
		"-p=127.0.0.1:8585:8585", // Health probe proxy
		"-p=127.0.0.1:8888:8888", // SystemHttpServer
		"-p=127.0.0.1:9090:9090", // Metrics
		o.argImageTag,
		"gameserver",                  // Inform entrypoint to start gameserver
		"-AdminApiListenHost=0.0.0.0", // Listen to all traffic
		"--Environment:EnableKeyboardInput=false",
		"--Environment:EnableSystemHttpServer=true",
		"--Environment:SystemHttpListenHost=0.0.0.0",
		"--AdminApi:WebRootPath=wwwroot",
		"--Database:Backend=Sqlite",
		"--Database:SqliteInMemory=true",
	}
	dockerRunArgs = append(dockerRunArgs, o.extraArgs...)

	log.Info().Msg("")
	log.Info().Msgf(styles.RenderMuted("docker %s"), strings.Join(dockerRunArgs, " "))
	log.Info().Msg("")

	// Run the docker image.
	if err := executeCommand(".", nil, "docker", dockerRunArgs...); err != nil {
		return clierrors.Wrap(err, "Docker run failed")
	}

	// The docker container exited normally.
	log.Info().Msgf("Docker container terminated normally")
	return nil
}
