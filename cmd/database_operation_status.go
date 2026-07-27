/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"time"

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
	args.AddStringArgument(&o.argOperationID, "OPERATION_ID", "Operation id returned by an earlier 'database create-snapshot', 'database delete-snapshot', or 'database rollback' command.")

	cmd := &cobra.Command{
		Use:   "operation-status ENVIRONMENT OPERATION_ID [flags]",
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
			metaplay database operation-status nimbly snapshot-create:mygame-prod-0-manual-20260409-153042

			# Watch until the operation completes
			metaplay database operation-status nimbly snapshot-create:mygame-prod-0-manual-20260409-153042 --watch
		`),
		Run: runCommand(&o),
	}

	cmd.Flags().BoolVarP(&o.flagWatch, "watch", "w", false, "Poll until the operation reaches a terminal state")
	cmd.Flags().StringVarP(&o.flagFormat, "format", "f", databaseFormatText, "Output format (text or json)")

	databaseCmd.AddCommand(cmd)
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

	// Render the initial snapshot, then delegate to the shared wait helper
	// so the watch loop gets the same heartbeat cadence as create/delete/
	// rollback and we only maintain one poll implementation.
	if err := o.render(op); err != nil {
		return err
	}
	final, err := waitForDatabaseOperation(ctx, targetEnv, op,
		func(u *envapi.DatabaseOperation) {
			log.Info().Msg("")
			_ = o.render(u)
		},
		func(elapsed time.Duration, u *envapi.DatabaseOperation) {
			msg := fmt.Sprintf("still %s (elapsed %s)", u.Status, formatElapsed(elapsed))
			if u.Progress != nil {
				msg = fmt.Sprintf("still %s (elapsed %s, %d%%)", u.Status, formatElapsed(elapsed), *u.Progress)
			}
			log.Info().Msgf("  %s %s", styles.RenderMuted("…"), msg)
		})
	if err != nil {
		return err
	}
	_ = final
	return nil
}

func (o *databaseOperationStatusOpts) render(op *envapi.DatabaseOperation) error {
	if o.flagFormat == databaseFormatJSON {
		return printDatabaseJSON(op)
	}

	log.Info().Msgf("%s %s", styles.RenderTitle("Operation:"), styles.RenderTechnical(op.OperationID))
	log.Info().Msgf("  type:      %s", styles.RenderTechnical(op.Type))
	log.Info().Msgf("  shard:     %s", styles.RenderTechnical(fmt.Sprintf("%d", op.ShardIndex)))
	log.Info().Msgf("  status:    %s", renderOperationStatusStyled(op.Status))
	// CreatedAt may be zero for operation types where the backend has no
	// stable source for it (e.g. snapshot-delete, where AWS retains no trace
	// of the initiating request). Skip the line when unknown rather than
	// printing a confusing "-  (- ago)" placeholder.
	if !op.CreatedAt.IsZero() {
		log.Info().Msgf("  created:   %s  (%s ago)",
			styles.RenderTechnical(formatDatabaseTime(op.CreatedAt)),
			formatDatabaseAge(op.CreatedAt))
	}
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
