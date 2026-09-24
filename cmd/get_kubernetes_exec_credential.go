/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
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
	// Every kubeconfig written before the proxy passes the StackAPI it asks,
	// and one pointing at the proxy asks StackAPI nothing.
	if !o.flagProxy && o.argStackAPIBaseURL == "" {
		return clierrors.NewUsageError("A StackAPI base URL is required without --proxy")
	}
	return nil
}

func (o *getKubernetesExecCredentialOpts) Run(cmd *cobra.Command) error {
	if o.flagProxy {
		return o.runForProxy()
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

	log.Info().Msg(*credential)
	return nil
}

// runForProxy answers a kubeconfig pointing at the Kubernetes API proxy, which
// takes the CLI's own access token: that token, refreshed first when it would
// expire within the credential's skew, and reported to expire that much early.
// StackAPI is not asked for anything, so a refresh is one request to the auth
// provider and none to the stack.
func (o *getKubernetesExecCredentialOpts) runForProxy() error {
	// Try to resolve the project & auth provider. As for the credential
	// StackAPI mints, an environment using a custom auth provider resolves it
	// from the metaplay-project.yaml, so kubectl must run where that is found.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}
	providerName := ""
	if project != nil {
		envConfig, err := project.Config.FindEnvironmentConfig(o.argEnvironmentHumanID)
		if err != nil {
			return err
		}
		providerName = envConfig.AuthProvider
	}
	authProvider, err := getAuthProvider(project, providerName)
	if err != nil {
		return err
	}

	credential, err := proxyExecCredential(authProvider)
	if err != nil {
		return err
	}
	log.Info().Msg(credential)
	return nil
}

// proxyExecCredential is the exec credential for the Kubernetes API proxy from
// the session with authProvider. kubectl runs the plugin with no terminal to
// ask on, so a missing session is reported rather than logged in to.
func proxyExecCredential(authProvider *auth.AuthProviderConfig) (string, error) {
	tokenSet, err := auth.LoadAndRefreshTokenSetWithin(authProvider, envapi.ProxyExecCredentialSkew)
	if err != nil {
		return "", err
	}
	if tokenSet == nil {
		return "", clierrors.New("Not logged in").
			WithSuggestion("Run '" + authProvider.LoginCommand() + "' and try again")
	}
	expiresAt, err := auth.AccessTokenExpiresAt(tokenSet)
	if err != nil {
		return "", err
	}
	return envapi.NewProxyExecCredential(tokenSet.AccessToken, expiresAt)
}
