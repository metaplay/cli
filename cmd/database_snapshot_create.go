/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"strings"
	"time"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/syncutil"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Validation limits mirroring the backend for early client-side feedback.
const (
	databaseSnapshotNameMaxLength        = 64
	databaseSnapshotDescriptionMaxLength = 256
)

type databaseSnapshotCreateOpts struct {
	UsePositionalArgs

	argEnvironment string

	flagShard       int
	flagAllShards   bool
	flagName        string
	flagDescription string
	flagNoWait      bool
	flagFormat      string
}

func init() {
	o := databaseSnapshotCreateOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")

	cmd := &cobra.Command{
		Use:   "create [ENVIRONMENT] [flags]",
		Short: "Create a manual cloud-managed database snapshot",
		Long: renderLong(&o, `
			Create a manual snapshot of the environment's cloud-managed database.
			Manual snapshots are retained until explicitly deleted and are counted
			against the per-shard manual snapshot quota — use
			'metaplay database info' to see the quota and current usage.

			For multi-shard environments, pass --shard=N to target one shard, or
			--all-shards to take a snapshot on every shard in parallel. By default
			the command waits for the snapshot to become available; use --no-wait
			to return immediately with just the operation id.

			If --name is omitted, a timestamped default name is generated. Both
			--name and --description are recorded as tags on the resulting snapshot
			and surfaced by 'metaplay database snapshot list'.

			{Arguments}
		`),
		Example: renderExample(`
			# Create a snapshot on the single shard of 'nimbly'
			metaplay database snapshot create nimbly --name=pre-migration

			# Fan out across all shards on a production env in parallel
			metaplay database snapshot create my-game-prod --all-shards --name=pre-v2

			# Fire-and-forget (returns after the request is accepted)
			metaplay database snapshot create nimbly --name=foo --no-wait
		`),
		Run: runCommand(&o),
	}

	cmd.Flags().IntVar(&o.flagShard, "shard", -1, "Target shard index (required for multi-shard environments when --all-shards is not set)")
	cmd.Flags().BoolVar(&o.flagAllShards, "all-shards", false, "Create a snapshot on every shard in parallel")
	cmd.Flags().StringVar(&o.flagName, "name", "", "User-provided snapshot name (max 64 characters)")
	cmd.Flags().StringVar(&o.flagDescription, "description", "", "Free-form description (max 256 characters)")
	cmd.Flags().BoolVar(&o.flagNoWait, "no-wait", false, "Return immediately after the request is accepted, do not poll for completion")
	cmd.Flags().StringVarP(&o.flagFormat, "format", "f", databaseFormatText, "Output format (text or json)")

	databaseSnapshotCmd.AddCommand(cmd)
}

func (o *databaseSnapshotCreateOpts) Prepare(cmd *cobra.Command, args []string) error {
	if err := validateDatabaseFormat(o.flagFormat); err != nil {
		return err
	}
	if len(o.flagName) > databaseSnapshotNameMaxLength {
		return clierrors.NewUsageErrorf("--name must be at most %d characters", databaseSnapshotNameMaxLength)
	}
	if len(o.flagDescription) > databaseSnapshotDescriptionMaxLength {
		return clierrors.NewUsageErrorf("--description must be at most %d characters", databaseSnapshotDescriptionMaxLength)
	}
	return nil
}

func (o *databaseSnapshotCreateOpts) Run(cmd *cobra.Command) error {
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

	// Fetch capabilities so we can validate shard selection and report clear
	// errors for unsupported environments before issuing any writes.
	caps, err := targetEnv.GetDatabaseCapabilities()
	if err != nil {
		return mapDatabaseHTTPError(err, "fetch database capabilities")
	}
	shardIndices, err := resolveTargetShards(ctx, caps, o.flagShard, o.flagAllShards)
	if err != nil {
		return err
	}

	multiShard := len(shardIndices) > 1
	log.Info().Msgf("Creating database snapshot for environment '%s' (shard(s): %s)",
		styles.RenderTechnical(envConfig.Name),
		styles.RenderTechnical(formatShardList(shardIndices)))
	log.Info().Msg("")

	// For --all-shards, fan out across shards in parallel so long-running
	// snapshots finish together. Ordering of results is preserved.
	results := syncutil.ParallelMap(shardIndices, len(shardIndices), func(shardIndex int) shardOperationResult {
		name := o.resolveSnapshotName(shardIndex, time.Now().UTC())
		return runShardOperation(
			ctx,
			targetEnv,
			shardIndex,
			"snapshot create",
			func() (*envapi.DatabaseOperation, error) {
				return targetEnv.CreateDatabaseSnapshot(&envapi.CreateDatabaseSnapshotRequest{
					ShardIndex:  shardIndex,
					Name:        name,
					Description: o.flagDescription,
				})
			},
			o.flagNoWait,
			multiShard,
		)
	})

	if o.flagFormat == databaseFormatJSON {
		return printCreateResultsJSON(results)
	}

	if err := aggregateShardResults("snapshot create", results); err != nil {
		return err
	}
	return nil
}

// resolveSnapshotName returns the user-provided --name, or a generated
// default name of the form "cli-YYYYMMDD-HHMMSS" (per-shard suffix added if
// multi-shard so each shard's snapshot has a distinct tag value).
func (o *databaseSnapshotCreateOpts) resolveSnapshotName(shardIndex int, now time.Time) string {
	if o.flagName != "" {
		return o.flagName
	}
	return fmt.Sprintf("cli-%s", now.Format("20060102-150405"))
}

// formatShardList renders a compact human-readable list of shard indices.
func formatShardList(indices []int) string {
	parts := make([]string, len(indices))
	for i, idx := range indices {
		parts[i] = fmt.Sprintf("%d", idx)
	}
	return strings.Join(parts, ", ")
}

// printCreateResultsJSON emits the per-shard results as a JSON array. Shards
// with errors report their error message string; shards that succeeded report
// the final DatabaseOperation.
func printCreateResultsJSON(results []shardOperationResult) error {
	type shardJSON struct {
		ShardIndex int                       `json:"shardIndex"`
		Error      string                    `json:"error,omitempty"`
		Operation  *envapi.DatabaseOperation `json:"operation,omitempty"`
	}
	out := make([]shardJSON, len(results))
	for i, r := range results {
		entry := shardJSON{ShardIndex: r.ShardIndex, Operation: r.Operation}
		if r.Err != nil {
			entry.Error = r.Err.Error()
		}
		out[i] = entry
	}
	if err := printDatabaseJSON(map[string]any{"results": out}); err != nil {
		return err
	}
	// Still surface non-zero exit on any failure.
	for _, r := range results {
		if r.Err != nil {
			return clierrors.New("One or more shards failed, see JSON output for details")
		}
	}
	return nil
}

