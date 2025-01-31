/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Run a built docker image locally.
type RunDockerImageOpts struct {
	argImageTag string
	extraArgs   []string
}

func init() {
	o := RunDockerImageOpts{}

	cmd := &cobra.Command{
		Use:   "docker-image IMAGE:TAG [flags] [-- EXTRA_ARGS]",
		Short: "Run the docker image locally",
		Run:   runCommand(&o),
		Long: trimIndent(`
			Run a pre-built docker image locally.

			The LiveOps Dashboard is served at http://localhost:5550.

			Prometheus metrics are served at http://localhost:9090/metrics.

			Arguments:
			- EXTRA_ARGS is passed directly to 'dotnet run'.

			Related commands:
			- 'metaplay build docker-image ...' to build an image to run.
		`),
		Example: trimIndent(`
			# Run the docker image (until terminated).
			metaplay run docker-image mygame:test
		`),
	}

	runCmd.AddCommand(cmd)
}

func (o *RunDockerImageOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("at lest one argument must be provided, got %d", len(args))
	}

	o.argImageTag = args[0]
	o.extraArgs = args[1:]

	return nil
}

func (o *RunDockerImageOpts) Run(cmd *cobra.Command) error {
	// Load project config.
	// project, err := resolveProject()
	// if err != nil {
	// 	return err
	// }

	// Check that docker is installed and running
	if err := checkCommand("docker", "info"); err != nil {
		return fmt.Errorf("failed to invoke docker. Ensure docker is installed and running.")
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
	log.Info().Msgf("Execute: docker %s", strings.Join(dockerRunArgs, " "))

	// Run the docker image.
	if err := executeCommand(".", nil, "docker", dockerRunArgs...); err != nil {
		log.Error().Msgf("Docker run failed: %v", err)
		os.Exit(1)
	}

	// The docker container exited normally.
	log.Info().Msgf("Docker container terminated normally")
	return nil
}
