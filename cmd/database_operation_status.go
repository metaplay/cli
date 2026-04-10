/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"time"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type databaseOperationStatusOpts struct {
	UsePositionalArgs

	argEnvironment string
	argOperationID string

	flagWatch  bool
	flagFormat string
}

func init() {
	o := databaseOperationStatusOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")
	args.AddStringArgument(&o.argOperationID, "OPERATION_ID", "Operation id returned by an earlier 'database snapshot create', 'database snapshot delete', or 'database rollback' command.")

	cmd := &cobra.Command{
		Use:   "status ENVIRONMENT OPERATION_ID [flags]",
		Short: "Show the status of a database operation",
		Long: renderLong(&o, `
			Show the status of an async database operation (snapshot create, snapshot
			delete, or point-in-time rollback) by id. When --watch is passed, the
			command polls the operation until it reaches a terminal state.

			Operation ids are prefixed with their operation type (e.g.
			'snapshot-create:...', 'rollback:...') and should be treated as opaque.

			{Arguments}
		`),
		Example: renderExample(`
			# Show a single status snapshot
			metaplay database operation status nimbly snapshot-create:mygame-prod-0-manual-20260409-153042

			# Watch until the operation completes
			metaplay database operation status nimbly snapshot-create:mygame-prod-0-manual-20260409-153042 --watch
		`),
		Run: runCommand(&o),
	}

	cmd.Flags().BoolVarP(&o.flagWatch, "watch", "w", false, "Poll until the operation reaches a terminal state")
	cmd.Flags().StringVarP(&o.flagFormat, "format", "f", databaseFormatText, "Output format (text or json)")

	databaseOperationCmd.AddCommand(cmd)
}

func (o *databaseOperationStatusOpts) Prepare(cmd *cobra.Command, args []string) error {
	return validateDatabaseFormat(o.flagFormat)
}

func (o *databaseOperationStatusOpts) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	_, targetEnv, _, err := resolveEnvironmentForDatabaseOps(ctx, o.argEnvironment)
	if err != nil {
		return err
	}

	op, err := targetEnv.GetDatabaseOperation(o.argOperationID)
	if err != nil {
		return mapDatabaseHTTPError(err, "get operation")
	}

	if !o.flagWatch || op.IsTerminal() {
		return o.render(op)
	}

	// --watch loop: poll until terminal, rendering each state change.
	lastStatus := op.Status
	if err := o.render(op); err != nil {
		return err
	}
	ticker := time.NewTicker(databaseOperationPollInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return clierrors.Wrap(ctx.Err(), "Watch cancelled")
		case <-ticker.C:
			next, err := targetEnv.GetDatabaseOperation(o.argOperationID)
			if err != nil {
				return mapDatabaseHTTPError(err, "get operation")
			}
			if next.Status != lastStatus {
				log.Info().Msg("")
				if err := o.render(next); err != nil {
					return err
				}
				lastStatus = next.Status
			}
			if next.IsTerminal() {
				return nil
			}
		}
	}
}

func (o *databaseOperationStatusOpts) render(op *envapi.DatabaseOperation) error {
	if o.flagFormat == databaseFormatJSON {
		return printDatabaseJSON(op)
	}

	log.Info().Msgf("%s %s", styles.RenderTitle("Operation:"), styles.RenderTechnical(op.OperationID))
	log.Info().Msgf("  type:      %s", styles.RenderTechnical(op.Type))
	log.Info().Msgf("  shard:     %s", styles.RenderTechnical(fmt.Sprintf("%d", op.ShardIndex)))
	log.Info().Msgf("  status:    %s", renderOperationStatusStyled(op.Status))
	log.Info().Msgf("  created:   %s  (%s ago)",
		styles.RenderTechnical(formatDatabaseTime(op.CreatedAt)),
		formatDatabaseAge(op.CreatedAt))
	if op.CompletedAt != nil {
		log.Info().Msgf("  completed: %s", styles.RenderTechnical(formatDatabaseTime(*op.CompletedAt)))
	}
	if op.Progress != nil {
		log.Info().Msgf("  progress:  %d%%", *op.Progress)
	}
	if op.Error != nil && *op.Error != "" {
		log.Info().Msgf("  error:     %s", styles.RenderError(*op.Error))
	}
	if len(op.Metadata) > 0 {
		log.Info().Msgf("  metadata:")
		for k, v := range op.Metadata {
			log.Info().Msgf("    %s: %v", styles.RenderTechnical(k), v)
		}
	}
	return nil
}

// renderOperationStatusStyled colors an operation status based on its value.
func renderOperationStatusStyled(status string) string {
	switch status {
	case envapi.DatabaseOperationStatusCompleted:
		return styles.RenderSuccess(status)
	case envapi.DatabaseOperationStatusFailed:
		return styles.RenderError(status)
	case envapi.DatabaseOperationStatusPending, envapi.DatabaseOperationStatusInProgress:
		return styles.RenderWarning(status)
	default:
		return styles.RenderTechnical(status)
	}
}
