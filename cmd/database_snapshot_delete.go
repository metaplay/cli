/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type databaseSnapshotDeleteOpts struct {
	UsePositionalArgs

	argEnvironment string
	argSnapshotID  string

	flagNoWait bool
	flagYes    bool
}

func init() {
	o := databaseSnapshotDeleteOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")
	args.AddStringArgumentOpt(&o.argSnapshotID, "SNAPSHOT_ID", "Identifier of the snapshot to delete. If omitted in interactive mode, the CLI shows a picker of the environment's manual snapshots.")

	cmd := &cobra.Command{
		Use:     "delete-snapshot [ENVIRONMENT] [SNAPSHOT_ID] [flags]",
		Aliases: []string{"rm-snapshot"},
		Short:   "Delete a manual cloud-managed database snapshot",
		Long: renderLong(&o, `
			Delete a manual cloud-managed database snapshot by identifier. Only
			manual snapshots can be deleted — automated snapshots and backup-service
			snapshots are managed by the cloud provider and cannot be removed via
			this CLI. The snapshot must be in the 'available' state; snapshots still
			being created cannot be deleted until they finish.

			In interactive mode, omitting SNAPSHOT_ID shows a picker listing the
			manual snapshots for the environment. In non-interactive mode, the
			SNAPSHOT_ID argument is required and --yes must be passed to skip the
			confirmation prompt.

			{Arguments}
		`),
		Example: renderExample(`
			# Delete a specific snapshot by id
			metaplay database delete-snapshot nimbly mygame-prod-0-manual-20260409-153042

			# Delete with an interactive picker (interactive mode only)
			metaplay database delete-snapshot nimbly

			# Non-interactive delete, skipping the confirmation prompt
			metaplay database delete-snapshot nimbly mygame-prod-0-manual-20260409-153042 --yes
		`),
		Run: runCommand(&o),
	}

	cmd.Flags().BoolVar(&o.flagNoWait, "no-wait", false, "Return immediately after the delete request is accepted, do not poll for completion")
	cmd.Flags().BoolVar(&o.flagYes, "yes", false, "Skip the confirmation prompt (required in non-interactive mode)")

	databaseCmd.AddCommand(cmd)
}

func (o *databaseSnapshotDeleteOpts) Prepare(cmd *cobra.Command, args []string) error {
	if !tui.IsInteractiveMode() {
		if o.argSnapshotID == "" {
			return clierrors.NewUsageError("SNAPSHOT_ID is required in non-interactive mode").
				WithSuggestion("Pass the snapshot identifier as a positional argument, e.g. 'metaplay database delete-snapshot ENV SNAPSHOT_ID'")
		}
		if !o.flagYes {
			return clierrors.NewUsageError("Confirmation required for destructive operation").
				WithSuggestion("Pass --yes in non-interactive mode to confirm the delete")
		}
	}
	return nil
}

func (o *databaseSnapshotDeleteOpts) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	envConfig, targetEnv, _, err := resolveEnvironmentForDatabaseOps(ctx, o.argEnvironment)
	if err != nil {
		return err
	}

	snapshotID := o.argSnapshotID
	if snapshotID == "" {
		// Interactive-only branch: Prepare() guarantees we're in interactive mode.
		snapshotID, err = pickManualSnapshotInteractively(targetEnv, envConfig.Name)
		if err != nil {
			return err
		}
	}

	// Fetch the snapshot so we can show descriptive info in the confirmation
	// prompt and fail fast with a clean error if the id does not exist.
	snap, err := targetEnv.GetDatabaseSnapshot(snapshotID)
	if err != nil {
		return mapDatabaseHTTPError(err, "look up snapshot")
	}
	if snap.Type != envapi.DatabaseSnapshotTypeManual {
		return clierrors.Newf("Cannot delete snapshot '%s': only manual snapshots can be deleted (this is %q)", snap.Identifier, snap.Type).
			WithSuggestion("Automated and backup-service snapshots are managed by the cloud provider")
	}

	if !o.flagYes {
		// Interactive mode only (Prepare() enforces --yes in non-interactive).
		ok, err := tui.DoConfirmDialog(
			ctx,
			"Delete manual snapshot",
			fmt.Sprintf("Snapshot: %s\nShard: %d\nCreated: %s",
				snap.Identifier,
				snap.ShardIndex,
				formatDatabaseTime(snap.CreatedAt)),
			"Delete this snapshot?",
		)
		if err != nil {
			return err
		}
		if !ok {
			log.Info().Msg("Delete cancelled.")
			return nil
		}
	}

	log.Info().Msgf("Deleting snapshot %s on shard %d",
		styles.RenderTechnical(snap.Identifier),
		snap.ShardIndex)
	log.Info().Msg("")

	result := runShardOperation(
		ctx,
		targetEnv,
		snap.ShardIndex,
		"snapshot delete",
		func() (*envapi.DatabaseOperation, error) {
			return targetEnv.DeleteDatabaseSnapshot(snap.Identifier)
		},
		o.flagNoWait,
		false, // single-shard operation, no "[shard N]" prefix
	)
	return result.Err
}

// pickManualSnapshotInteractively lists the manual snapshots for the given
// environment and presents an interactive picker. Returns the selected
// snapshot identifier. Requires tui.IsInteractiveMode() to be true.
func pickManualSnapshotInteractively(target *envapi.TargetEnvironment, envName string) (string, error) {
	resp, err := target.ListDatabaseSnapshots(envapi.ListDatabaseSnapshotsOptions{
		Type: envapi.DatabaseSnapshotTypeManual,
	})
	if err != nil {
		return "", mapDatabaseHTTPError(err, "list manual snapshots")
	}
	if len(resp.Snapshots) == 0 {
		return "", clierrors.Newf("No manual snapshots found for environment '%s'", envName).
			WithSuggestion("Create one with 'metaplay database create-snapshot ENVIRONMENT'")
	}
	picked, err := tui.ChooseFromListDialog(
		"Select a manual snapshot to delete",
		resp.Snapshots,
		func(s *envapi.DatabaseSnapshot) (string, string) {
			subtitle := fmt.Sprintf("shard %d  created %s  %s",
				s.ShardIndex,
				formatDatabaseAge(s.CreatedAt),
				s.Name)
			return s.Identifier, subtitle
		},
	)
	if err != nil {
		return "", err
	}
	return picked.Identifier, nil
}
