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

type GetInfoOpts struct {
	argEnvironment string
}

func init() {
	o := GetInfoOpts{}

	cmd := &cobra.Command{
		Use:   "get-info ENVIRONMENT [flags]",
		Short: "Get information about a specific environment",
		Run:   runCommand(&o),
	}

	environmentCmd.AddCommand(cmd)
}

func (o *GetInfoOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("exactly one argument must be provided, got %d", len(args))
	}

	// Store target environment.
	o.argEnvironment = args[0]

	return nil
}

func (o *GetInfoOpts) Run(cmd *cobra.Command) error {
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
