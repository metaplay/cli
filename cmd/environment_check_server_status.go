/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Perform a post-deploy check on the status of the game server,
// including the following checks:
// - Pods are in the expected state.
// - Client-facing domain name resolves correctly.
// - Game server responds to client traffic.
// - Admin domain name resolves correctly.
// - Admin endpoint responds with a success code.
type CheckServerStatusOpts struct {
	argEnvironment string
}

func init() {
	o := CheckServerStatusOpts{}

	var cmd = &cobra.Command{
		Use:   "check-server-status ENVIRONMENT [flags]",
		Short: "Check the status of a deployed game server",
		Run:   runCommand(&o),
		Long: trimIndent(`
			Check the status of a deployed game server in the target environment.

			These checks are run as part of 'metaplay env deploy-server' so there should
			usually be no need to explicitly run this command.

			Runs the following checks:
			- Pods are in the expected state.
			- Client-facing domain name resolves correctly.
			- Game server responds to client traffic.
			- Admin domain name resolves correctly.
			- Admin endpoint responds with a success code.

			Related commands:
			- 'metaplay env deploy-server ...' to deploy a game server.
			- 'metaplay env server-logs ...' to view server logs.
		`),
		Example: trimIndent(`
			# Check the status of the server in environment tough-falcons.
			metaplay env check-server-status tough-falcons
		`),
	}

	environmentCmd.AddCommand(cmd)
}

func (o *CheckServerStatusOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("exactly one argument must be provided, got %d", len(args))
	}

	o.argEnvironment = args[0]
	return nil
}

func (o *CheckServerStatusOpts) Run(cmd *cobra.Command) error {
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

	// Get kubeconfig to access the environment.
	kubeconfigPayload, err := targetEnv.GetKubeConfigWithEmbeddedCredentials()

	// Configure Helm.
	actionConfig, err := helmutil.NewActionConfig(*kubeconfigPayload, envConfig.getKubernetesNamespace())
	if err != nil {
		return fmt.Errorf("failed to initialize Helm config: %v", err)
	}

	// Resolve all deployed game server Helm releases.
	helmReleases, err := helmutil.HelmListReleases(actionConfig, metaplayGameServerChartName)
	if len(helmReleases) == 0 {
		return fmt.Errorf("no game server deployment found in environment. Use 'metaplay env deploy-server' to deploy a game server.")
	} else if len(helmReleases) > 1 {
		return fmt.Errorf("multiple game server deployment found in environment: %v", helmutil.GetReleaseNames(helmReleases))
	}

	// Check the game server status.
	err = targetEnv.WaitForServerToBeReady(cmd.Context())
	if err != nil {
		return err
	}

	log.Info().Msgf("All game server checks successfully completed!")
	return nil
}
