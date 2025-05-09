/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"strings"

	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type getKubernetesExecCredentialOpts struct {
	UsePositionalArgs

	argEnvironmentHumanID string
	argStackAPIBaseURL    string
}

func init() {
	o := getKubernetesExecCredentialOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argEnvironmentHumanID, "ENVIRONMENT", "Target environment ID, eg, 'tough-falcons'.")
	args.AddStringArgument(&o.argStackAPIBaseURL, "STACK_API", "StackAPI base URL for environment, eg, 'https://infra.p1.metaplay.io/stackapi'.")

	cmd := &cobra.Command{
		Use:   "kubernetes-execcredential ENVIRONMENT STACK_API",
		Short: "[internal] Get kubernetes credentials in execcredential format (used from the generated kubeconfigs)",
		Run:   runCommand(&o),
	}

	cmd.Hidden = true
	getCmd.AddCommand(cmd)
}

func (o *getKubernetesExecCredentialOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *getKubernetesExecCredentialOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}

	// Resolve environment.
	_, tokenSet, err := resolveEnvironment(cmd.Context(), project, o.argEnvironmentHumanID)
	if err != nil {
		return err
	}

	// \todo Fix stack domain hack
	stackDomain := strings.Replace(strings.Replace(o.argStackAPIBaseURL, "https://infra.", "", 1), "/stackapi", "", 1)
	targetEnv := envapi.NewTargetEnvironment(tokenSet, stackDomain, o.argEnvironmentHumanID)

	// Get the Kubernetes credentials in the execcredential format
	credential, err := targetEnv.GetKubeExecCredential()
	if err != nil {
		return err
	}

	log.Info().Msg(*credential)
	return nil
}
