/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/auth"
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
	args.AddStringArgument(&o.argEnvironmentHumanID, "ENVIRONMENT", "Target environment ID, eg, 'lovely-wombats-build-nimbly'.")
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

	// Resolve the authentication token to use for the target environment.
	// \todo For environments using custom auth provider (not Metaplay Auth), we can only resolve the auth provider from the metaplay-project.yaml
	//       and thus the `kubectl` operations using this invocation must be run in the project directory where the metaplay-project.yaml is available.
	//       Fix this later by passing the auth provider info or the project config file location as an argument?
	var tokenSet *auth.TokenSet
	if project != nil {
		// If metaplay-project.yaml was found, resolve the environment from it.
		_, tokenSet, err = resolveEnvironment(cmd.Context(), project, o.argEnvironmentHumanID)
		if err != nil {
			return err
		}
	} else {
		// If no metaplay-project.yaml was found, assume the default auth provider is being used.
		authProvider, err := auth.NewDefaultAuthProvider()
		if err != nil {
			return err
		}
		tokenSet, err = tui.RequireLoggedIn(cmd.Context(), authProvider)
		if err != nil {
			return err
		}
	}

	// The kubeconfig hands us the StackAPI base URL directly, so use it as it
	// arrived. Reducing it to a stack domain only to have the constructor
	// rebuild the same URL made the two spellings able to disagree.
	targetEnv := envapi.NewTargetEnvironmentAtStackAPI(tokenSet, o.argStackAPIBaseURL, o.argEnvironmentHumanID)

	// Get the Kubernetes credentials in the execcredential format
	credential, err := targetEnv.GetKubeExecCredential()
	if err != nil {
		return err
	}

	log.Info().Msg(*credential)
	return nil
}
