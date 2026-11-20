/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Check the current status of a game server deployment, without waiting.
type debugCheckServerStatus struct {
	UsePositionalArgs

	argEnvironment string
}

func init() {
	o := debugCheckServerStatus{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")

	cmd := &cobra.Command{
		Use:     "server-status ENVIRONMENT [flags]",
		Aliases: []string{"srv"},
		Short:   "Check the status of a game server deployment",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			Check the current status of a game server deployment.

			Runs the checks of 'deploy server' once, without waiting for them to pass:
			- All expected pods are present, run the deployed image, and are ready.
			- Client-facing domain name resolves.
			- Game server accepts client connections.
			- LiveOps Dashboard domain name resolves.
			- LiveOps Dashboard responds with a success or a redirect to its login.

			All checks run, and each one prints its result. The command fails with exit
			code 1 if any check fails, including while a deployment is still starting.

			{Arguments}

			Related commands:
			- 'metaplay deploy server ...' deploys a game server and waits for these checks to pass.
			- 'metaplay debug logs ...' shows the logs of the game server.
			- 'metaplay get server-info ...' shows information about the game server deployment.
		`),
		Example: renderExample(`
			# Let the CLI ask for the target environment to check deployment status.
			metaplay debug server-status

			# Check the status of a game server deployment in a specific environment.
			metaplay debug server-status nimbly
		`),
	}
	debugCmd.AddCommand(cmd)
}

func (o *debugCheckServerStatus) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *debugCheckServerStatus) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}

	// Resolve project and environment.
	envConfig, tokenSet, err := resolveEnvironment(cmd.Context(), project, o.argEnvironment)
	if err != nil {
		return err
	}

	// Create TargetEnvironment.
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	// Create a Kubernetes client.
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return err
	}

	// Configure Helm.
	actionConfig, err := helmutil.NewActionConfig(kubeCli.KubeConfig, envConfig.GetKubernetesNamespace())
	if err != nil {
		return fmt.Errorf("failed to initialize Helm config: %w", err)
	}

	// Determine if there's an existing release deployed.
	existingRelease, err := helmutil.GetExistingRelease(actionConfig, metaplayGameServerChartName)
	if err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Check Game Server Deployment Status"))
	log.Info().Msg("")
	log.Info().Msgf("Target environment:")
	log.Info().Msgf("  Name:              %s", styles.RenderTechnical(envConfig.Name))
	log.Info().Msgf("  ID:                %s", styles.RenderTechnical(envConfig.HumanID))
	log.Info().Msgf("  Type:              %s", styles.RenderTechnical(string(envConfig.Type)))
	log.Info().Msgf("  Stack domain:      %s", styles.RenderTechnical(envConfig.StackDomain))
	if existingRelease != nil {
		log.Info().Msg("Deployment info:")
		log.Info().Msgf("  Helm release name: %s", styles.RenderTechnical(existingRelease.Name))
		log.Info().Msgf("  Chart version:     %s", styles.RenderTechnical(existingRelease.Chart.Metadata.Version))
		// Print image name/tag from chart values
		if imageTag, ok := existingRelease.Config["image"].(map[string]any)["tag"].(string); ok {
			log.Info().Msgf("  Image tag:         %s", styles.RenderTechnical(imageTag))
		} else {
			log.Info().Msg("  Image tag:         <not available>")
		}
	}
	log.Info().Msg("")

	if existingRelease == nil {
		return clierrors.New("No game server is deployed in the environment").
			WithSuggestion("Deploy a game server first with 'metaplay deploy server'")
	}

	status, err := targetEnv.CheckServerStatus(cmd.Context())
	if err != nil {
		return err
	}

	log.Info().Msg("Game server status:")
	for _, check := range status.Checks {
		marker := styles.RenderSuccess("✓")
		summary := check.Summary
		switch {
		case check.Failed():
			marker = styles.RenderError("✗")
			summary = styles.RenderError(summary)
		case check.Skipped:
			marker = styles.RenderMuted("-")
			summary = styles.RenderMuted(summary)
		}
		log.Info().Msgf("  %s %s: %s", marker, check.Name, summary)
		for _, line := range check.Lines {
			log.Info().Msgf("      %s", line)
		}
	}
	log.Info().Msg("")

	if err := status.Err(); err != nil {
		return err
	}
	log.Info().Msg(styles.RenderSuccess("Game server is ready"))
	return nil
}
