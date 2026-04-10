/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"

	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type databaseOperationListOpts struct {
	UsePositionalArgs

	argEnvironment string

	flagFormat string
}

func init() {
	o := databaseOperationListOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")

	cmd := &cobra.Command{
		Use:     "list [ENVIRONMENT] [flags]",
		Aliases: []string{"ls"},
		Short:   "List in-progress database operations for an environment",
		Long: renderLong(&o, `
			List in-progress database operations (snapshot creates, snapshot deletes,
			and point-in-time rollbacks) for an environment. Completed or failed
			operations are not returned — use 'metaplay database operation status' to
			look up a specific operation by id, or 'metaplay database snapshot list'
			to see finalized snapshots.

			This command is useful for discovering operations after a previous CLI
			invocation disconnected.

			{Arguments}
		`),
		Example: renderExample(`
			# Show in-progress operations
			metaplay database operation list nimbly

			# Emit JSON for scripting
			metaplay database operation list nimbly --format=json
		`),
		Run: runCommand(&o),
	}

	cmd.Flags().StringVarP(&o.flagFormat, "format", "f", databaseFormatText, "Output format (text or json)")

	databaseOperationCmd.AddCommand(cmd)
}

func (o *databaseOperationListOpts) Prepare(cmd *cobra.Command, args []string) error {
	return validateDatabaseFormat(o.flagFormat)
}

func (o *databaseOperationListOpts) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	project, err := tryResolveProject()
	if err != nil {
		return err
	}
	envConfig, tokenSet, err := resolveEnvironment(ctx, project, o.argEnvironment)
	if err != nil {
		return err
	}
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	resp, err := targetEnv.ListDatabaseOperations()
	if err != nil {
		return mapDatabaseHTTPError(err, "list operations")
	}

	if o.flagFormat == databaseFormatJSON {
		return printDatabaseJSON(resp)
	}

	if len(resp.Operations) == 0 {
		log.Info().Msgf("%s No in-progress database operations for environment '%s'.",
			styles.RenderMuted("―"),
			styles.RenderTechnical(envConfig.Name))
		return nil
	}

	renderOperationTable(resp.Operations)
	return nil
}

// renderOperationTable prints a table of database operations to the info log.
func renderOperationTable(ops []envapi.DatabaseOperation) {
	headers := []string{"OPERATION ID", "TYPE", "SHARD", "STATUS", "AGE"}
	rows := make([][]string, 0, len(ops))
	for _, op := range ops {
		rows = append(rows, []string{
			op.OperationID,
			op.Type,
			fmt.Sprintf("%d", op.ShardIndex),
			op.Status,
			formatDatabaseAge(op.CreatedAt),
		})
	}
	printDatabaseTable(headers, rows)
}
