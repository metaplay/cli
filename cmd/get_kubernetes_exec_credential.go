/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/envapi"
)

type getKubernetesExecCredentialOpts struct {
	UsePositionalArgs

	argEnvironmentHumanID string
	argStackAPIBaseURL    string
	flagProxy             bool
}

func init() {
	o := getKubernetesExecCredentialOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argEnvironmentHumanID, "ENVIRONMENT", "Target environment ID, eg, 'lovely-wombats-build-nimbly'.")
	args.AddStringArgumentOpt(&o.argStackAPIBaseURL, "STACK_API", "StackAPI base URL for environment, eg, 'https://infra.p1.metaplay.io/stackapi'. Required without --proxy.")

	cmd := &cobra.Command{
		Use:   "kubernetes-execcredential ENVIRONMENT [STACK_API]",
		Short: "[internal] Get kubernetes credentials in execcredential format (used from the generated kubeconfigs)",
		Run:   runCommand(&o),
	}

	cmd.Hidden = true
	getCmd.AddCommand(cmd)
	cmd.Flags().BoolVar(&o.flagProxy, "proxy", false, "Answer with the CLI's own access token, for a kubeconfig pointing at the Kubernetes API proxy, rather than asking StackAPI for a Kubernetes credential")
}

func (o *getKubernetesExecCredentialOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Kubeconfigs pointing at the Kubernetes API proxy pass --proxy, and all
	// others pass the StackAPI to ask for credentials.
	if !o.flagProxy && o.argStackAPIBaseURL == "" {
		return clierrors.NewUsageError("A StackAPI base URL is required without --proxy").
			WithSuggestion("Get a new kubeconfig with 'metaplay get kubeconfig'")
	}
	return nil
}

func (o *getKubernetesExecCredentialOpts) Run(cmd *cobra.Command) error {
	// kubectl reads the credential from stdout, so nothing else may reach it,
	// with --verbose or without: the log goes to stderr, which kubectl shows.
	log.Logger = stderrLogger

	if o.flagProxy {
		return o.runForProxy(cmd)
	}

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

	_, err = fmt.Fprintln(cmd.OutOrStdout(), *credential)
	return err
}

// runForProxy prints the credential for a kubeconfig pointing at the Kubernetes
// API proxy: the CLI's own access token, without asking StackAPI for anything.
func (o *getKubernetesExecCredentialOpts) runForProxy(cmd *cobra.Command) error {
	// A stack serves the proxy only for environments using the default auth
	// provider, so there is no need to resolve the project to find one, and
	// kubectl can run anywhere.
	authProvider, err := auth.NewDefaultAuthProvider()
	if err != nil {
		return err
	}

	credential, err := envapi.NewProxyExecCredential(authProvider)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), credential)
	return err
}
