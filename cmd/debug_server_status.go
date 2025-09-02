/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Deploy a game server to the target environment with specified docker image version.
type debugCheckServerStatus struct {
	UsePositionalArgs

	argEnvironment string
}

func init() {
	o := debugCheckServerStatus{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")

	cmd := &cobra.Command{
		Use:     "server-status ENVIRONMENT [flags]",
		Aliases: []string{"srv"},
		Short:   "Check the status of a game server deployment",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			Check the status of a game server deployment.

			Runs the same checks as 'deploy server' but without deploying anything:
			- All expected pods are present, healthy, and ready.
			- Client-facing domain name resolves correctly.
			- Game server responds to client traffic.
			- Admin domain name resolves correctly.
			- Admin endpoint responds with a success code.

			{Arguments}

			Related commands:
			- 'metaplay deploy server ...' deploys a game server and runs these checks.
			- 'metaplay get server-info ...' shows information about the game server deployment.
		`),
		Example: renderExample(`
			# Let the CLI ask for the target environment to check deployment status.
			metaplay debug server-status

			# Check the status of a game server deployment in a specific environment.
			metaplay debug server-status tough-falcons
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

	// Get environment details.
	envDetails, err := targetEnv.GetDetails()
	if err != nil {
		return err
	}

	// Get docker credentials.
	dockerCredentials, err := targetEnv.GetDockerCredentials(envDetails)
	if err != nil {
		return fmt.Errorf("failed to get docker credentials: %v", err)
	}
	log.Debug().Msgf("Got docker credentials: username=%s", dockerCredentials.Username)

	// Create a Kubernetes client.
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return err
	}

	// Configure Helm.
	actionConfig, err := helmutil.NewActionConfig(kubeCli.KubeConfig, envConfig.GetKubernetesNamespace())
	if err != nil {
		return fmt.Errorf("failed to initialize Helm config: %v", err)
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
		log.Info().Msg(styles.RenderAttention("No deployment found in environment, skipping tests."))
		return nil
	}

	taskRunner := tui.NewTaskRunner()

	// Validate the game server status.
	err = targetEnv.WaitForServerToBeReady(cmd.Context(), taskRunner)
	if err != nil {
		return err
	}

	// Run the tasks.
	if err = taskRunner.Run(); err != nil {
		return err
	}

	log.Info().Msg(styles.RenderSuccess("✅ Game server deployment is ready!"))
	return nil
}
