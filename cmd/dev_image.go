/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
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
type devImageOpts struct {
	UsePositionalArgs

	argImageTag string
	extraArgs   []string
}

func init() {
	o := devImageOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argImageTag, "IMAGE:TAG", "Docker image name and tag, eg, 'mygame:364cff09'.")
	args.SetExtraArgs(&o.extraArgs, "Passed as-is to 'dotnet run'.")

	cmd := &cobra.Command{
		Use:   "image IMAGE:TAG [flags] [-- EXTRA_ARGS]",
		Short: "Run a server Docker image locally",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Run a pre-built docker image locally.

			The LiveOps Dashboard is served at http://localhost:5550.

			Prometheus metrics are served at http://localhost:9090/metrics.

			{Arguments}

			Related commands:
			- 'metaplay build image ...' to build a server Docker image.
		`),
		Example: trimIndent(`
			# Run the docker image (until terminated).
			metaplay dev image mygame:test
		`),
	}

	devCmd.AddCommand(cmd)
}

func (o *devImageOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *devImageOpts) Run(cmd *cobra.Command) error {
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
