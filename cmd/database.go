/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/spf13/cobra"
)

var databaseCmd = &cobra.Command{
	Use:     "database",
	Aliases: []string{"db"},
	Short:   "Database management commands",
	Long:    "Commands for managing and interacting with game server databases",
}

func init() {
	rootCmd.AddCommand(databaseCmd)
}

// requireProductionConfirmation returns an error for a destructive database operation on an
// environment that is not known to be development or staging, unless confirmed is set.
func requireProductionConfirmation(envConfig *metaproj.ProjectEnvironmentConfig, confirmed bool) error {
	if confirmed || envConfig.Type.IsKnownNonProduction() {
		return nil
	}
	if !envConfig.Type.IsKnown() {
		return clierrors.Newf("Environment '%s' has unknown type '%s', so it is treated as production", envConfig.Name, envConfig.Type).
			WithSuggestion("Set 'type' to development, staging or production in metaplay-project.yaml, or use --confirm-production")
	}
	return clierrors.Newf("Production environment detected: %s", envConfig.Name).
		WithSuggestion("Use --confirm-production to confirm the operation on a production environment")
}
