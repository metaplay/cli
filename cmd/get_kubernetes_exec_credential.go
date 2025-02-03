/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"
	"strings"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type getKubernetesExecCredentialOpts struct {
	environmentHumanId string
	stackApiBaseURL    string
}

func init() {
	o := getKubernetesExecCredentialOpts{}

	cmd := &cobra.Command{
		Use:   "kubernetes-execcredential ENVIRONMENT_HUMAN_ID STACK_API_BASE_URL",
		Short: "[internal] Get kubernetes credentials in execcredential format (used from the generated kubeconfigs)",
		Run:   runCommand(&o),
	}

	cmd.Hidden = true
	getCmd.AddCommand(cmd)
}

func (o *getKubernetesExecCredentialOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("exactly two arguments must be provided, got %d", len(args))
	}

	o.environmentHumanId = args[0]
	o.stackApiBaseURL = args[1]

	return nil
}

func (o *getKubernetesExecCredentialOpts) Run(cmd *cobra.Command) error {
	// Ensure the user is logged in
	tokenSet, err := tui.RequireLoggedIn(cmd.Context())
	if err != nil {
		return err
	}

	// \todo Fix stack domain hack
	stackDomain := strings.Replace(strings.Replace(o.stackApiBaseURL, "https://infra.", "", 1), "/stackapi", "", 1)
	targetEnv := envapi.NewTargetEnvironment(tokenSet, stackDomain, o.environmentHumanId)

	// Get the Kubernetes credentials in the execcredential format
	credential, err := targetEnv.GetKubeExecCredential()
	if err != nil {
		return err
		// log.Error().Msgf("Failed to get environment k8s execcredential: %v", err)
		// os.Exit(1)
	}

	log.Info().Msg(*credential)
	return nil
}
