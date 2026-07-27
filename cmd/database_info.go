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

type databaseInfoOpts struct {
	UsePositionalArgs

	argEnvironment string

	flagFormat string
}

// databaseInfoView is the combined capabilities + cluster info view rendered
// by 'metaplay database info'. Used as the top-level JSON output shape so the
// text and JSON outputs contain the same data.
type databaseInfoView struct {
	Environment string                  `json:"environment"`
	Supported   bool                    `json:"supported"`
	Shards      []databaseInfoShardView `json:"shards"`
}

type databaseInfoShardView struct {
	ShardIndex          int                            `json:"shardIndex"`
	ClusterID           string                         `json:"clusterId"`
	Provider            string                         `json:"provider"`
	SupportsSnapshots   bool                           `json:"supportsSnapshots"`
	SupportsRollback    bool                           `json:"supportsRollback"`
	MaxManualSnapshots  int                            `json:"maxManualSnapshots"`
	RollbackWindow      *envapi.DatabaseRollbackWindow `json:"rollbackWindow,omitempty"`
	Status              string                         `json:"status,omitempty"`
	Engine              string                         `json:"engine,omitempty"`
	EngineVersion       string                         `json:"engineVersion,omitempty"`
	ManualSnapshotsUsed int                            `json:"manualSnapshotsUsed"`
}

func init() {
	o := databaseInfoOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")

	cmd := &cobra.Command{
		Use:   "info [ENVIRONMENT] [flags]",
		Short: "Show database operation capabilities for an environment",
		Long: renderLong(&o, `
			Show the cloud-managed database operation capabilities for an environment,
			including the database provider, per-shard cluster state, the manual
			snapshot quota (used and maximum), and the rollback window for point-in-time
			recovery.

			Environments without a dedicated managed database cluster are reported as
			"not supported" — for those environments, the commands under
			'metaplay database create-snapshot' and 'metaplay database rollback' are not
			available.

			{Arguments}
		`),
		Example: renderExample(`
			# Show database info for the 'nimbly' environment
			metaplay database info nimbly

			# Emit JSON for scripting
			metaplay database info nimbly --format=json
		`),
		Run: runCommand(&o),
	}

	cmd.Flags().StringVarP(&o.flagFormat, "format", "f", databaseFormatText, "Output format (text or json)")

	databaseCmd.AddCommand(cmd)
}

func (o *databaseInfoOpts) Prepare(cmd *cobra.Command, args []string) error {
	return validateDatabaseFormat(o.flagFormat)
}

func (o *databaseInfoOpts) Run(cmd *cobra.Command) error {
	envConfig, targetEnv, caps, err := resolveEnvironmentForDatabaseOps(cmd.Context(), o.argEnvironment)
	if err != nil {
		return err
	}

	view := databaseInfoView{
		Environment: envConfig.Name,
		Supported:   len(caps.Shards) > 0,
		Shards:      []databaseInfoShardView{},
	}

	if len(caps.Shards) == 0 {
		if err := o.render(view, envConfig.Name); err != nil {
			return err
		}
		return nil
	}

	// For supported environments, fetch cluster info and merge by shard index.
	info, err := targetEnv.GetDatabaseInfo()
	if err != nil {
		return mapDatabaseHTTPError(err, "fetch database info")
	}

	// Count existing manual snapshots per shard so we can show quota usage.
	manualUsedByShard, err := countManualSnapshotsByShard(targetEnv)
	if err != nil {
		return err
	}

	// Index info by shard for merging.
	infoByShard := make(map[int]envapi.DatabaseShardInfo, len(info.Shards))
	for _, s := range info.Shards {
		infoByShard[s.ShardIndex] = s
	}

	for _, shardCaps := range caps.Shards {
		shardView := databaseInfoShardView{
			ShardIndex:          shardCaps.ShardIndex,
			ClusterID:           shardCaps.ClusterID,
			Provider:            shardCaps.Provider,
			SupportsSnapshots:   shardCaps.SupportsSnapshots,
			SupportsRollback:    shardCaps.SupportsRollback,
			MaxManualSnapshots:  shardCaps.MaxManualSnapshots,
			RollbackWindow:      shardCaps.RollbackWindow,
			ManualSnapshotsUsed: manualUsedByShard[shardCaps.ShardIndex],
		}
		if info, ok := infoByShard[shardCaps.ShardIndex]; ok {
			shardView.Status = info.Status
			shardView.Engine = info.Engine
			shardView.EngineVersion = info.EngineVersion
		}
		view.Shards = append(view.Shards, shardView)
	}

	return o.render(view, envConfig.Name)
}

func (o *databaseInfoOpts) render(view databaseInfoView, envName string) error {
	if o.flagFormat == databaseFormatJSON {
		return printDatabaseJSON(view)
	}

	if !view.Supported {
		printCapabilitiesUnsupported(envName)
		return nil
	}

	provider := "-"
	if len(view.Shards) > 0 {
		provider = view.Shards[0].Provider
	}

	log.Info().Msgf("%s %s", styles.RenderTitle("Environment:"), styles.RenderTechnical(envName))
	log.Info().Msgf("%s %s", styles.RenderTitle("Provider:   "), styles.RenderTechnical(provider))
	log.Info().Msgf("%s", styles.RenderTitle("Shards:"))

	for _, s := range view.Shards {
		log.Info().Msgf("  %s %s", styles.RenderTechnical("―"), styles.RenderBright(
			renderShardHeader(s),
		))
		if s.Engine != "" {
			log.Info().Msgf("      engine:             %s %s",
				styles.RenderTechnical(s.Engine),
				styles.RenderMuted(s.EngineVersion))
		}
		if s.Status != "" {
			log.Info().Msgf("      status:             %s", styles.RenderTechnical(s.Status))
		}
		log.Info().Msgf("      supports snapshots: %s", renderBoolStyled(s.SupportsSnapshots))
		log.Info().Msgf("      supports rollback:  %s", renderBoolStyled(s.SupportsRollback))
		if s.SupportsSnapshots && s.MaxManualSnapshots > 0 {
			log.Info().Msgf("      manual snapshots:   %s",
				styles.RenderTechnical(renderSnapshotQuota(s.ManualSnapshotsUsed, s.MaxManualSnapshots)))
		}
		if s.RollbackWindow != nil {
			log.Info().Msgf("      rollback window:    %s %s %s",
				styles.RenderTechnical(formatDatabaseTime(s.RollbackWindow.EarliestTime)),
				styles.RenderMuted("→"),
				styles.RenderTechnical(formatDatabaseTime(s.RollbackWindow.LatestTime)))
		}
	}
	return nil
}

func renderShardHeader(s databaseInfoShardView) string {
	return fmt.Sprintf("Shard %d  %s", s.ShardIndex, s.ClusterID)
}

func renderSnapshotQuota(used, max int) string {
	return fmt.Sprintf("%d / %d", used, max)
}

// countManualSnapshotsByShard returns, for each shard index, how many manual
// snapshots currently exist for it. Used to show quota usage in 'info'.
func countManualSnapshotsByShard(target *envapi.TargetEnvironment) (map[int]int, error) {
	resp, err := target.ListDatabaseSnapshots(envapi.ListDatabaseSnapshotsOptions{
		Type: envapi.DatabaseSnapshotTypeManual,
	})
	if err != nil {
		return nil, mapDatabaseHTTPError(err, "list manual snapshots")
	}
	counts := make(map[int]int, len(resp.Snapshots))
	for _, s := range resp.Snapshots {
		counts[s.ShardIndex]++
	}
	return counts, nil
}

// renderBoolStyled renders a bool as a styled yes/no value.
func renderBoolStyled(b bool) string {
	if b {
		return styles.RenderSuccess("yes")
	}
	return styles.RenderMuted("no")
}
