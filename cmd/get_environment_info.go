/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type getEnvironmentInfoOpts struct {
	argEnvironment string
}

func init() {
	o := getEnvironmentInfoOpts{}

	cmd := &cobra.Command{
		Use:     "environment-info ENVIRONMENT [flags]",
		Aliases: []string{"env-info"},
		Short:   "Get information about the target environment",
		Run:     runCommand(&o),
	}

	getCmd.AddCommand(cmd)
}

func (o *getEnvironmentInfoOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("exactly one argument must be provided, got %d", len(args))
	}

	// Store target environment.
	o.argEnvironment = args[0]

	return nil
}

func (o *getEnvironmentInfoOpts) Run(cmd *cobra.Command) error {
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

	// Fetch the information from the environment via StackAPI.
	envInfo, err := targetEnv.GetDetails()
	if err != nil {
		return err
	}

	// Pretty-print as JSON.
	envInfoJson, err := json.MarshalIndent(envInfo, "", "  ")
	if err != nil {
		return err
	}

	log.Info().Msg(string(envInfoJson))
	return nil
}
