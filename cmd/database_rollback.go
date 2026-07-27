/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/syncutil"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type databaseRollbackOpts struct {
	UsePositionalArgs

	argEnvironment string

	flagShard             int
	flagAllShards         bool
	flagTargetTime        string
	flagNoWait            bool
	flagForce             bool
	flagYes               bool
	flagConfirmProduction bool
	flagFormat            string
}

func init() {
	o := databaseRollbackOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")

	cmd := &cobra.Command{
		Use:   "rollback [ENVIRONMENT] [flags]",
		Short: "Roll back an environment's database to a previous point in time",
		Long: renderLong(&o, `
			Roll back an environment's cloud-managed database to a previous point
			in time. This is an in-place operation on the existing database cluster
			(implemented via the provider's point-in-time recovery mechanism) — no
			cluster deletion or restore is performed.

			Warning: Rolling back while a game server is running will cause the
			server to see an inconsistent database state. This command refuses to
			run if a game server is currently deployed in the environment; pass
			--force to override (you should usually 'metaplay remove server' first).

			The target time can be given as either an absolute RFC3339 timestamp
			(e.g. '2026-04-09T15:00:00Z') or as a relative duration before now
			(e.g. '30m' or '2h'). Run 'metaplay database info ENVIRONMENT' to see
			the valid rollback window for the target environment.

			{Arguments}
		`),
		Example: renderExample(`
			# Roll back to 30 minutes ago (single-shard env)
			metaplay database rollback nimbly --target-time=30m --yes

			# Roll back to an exact timestamp on a specific shard
			metaplay database rollback nimbly --target-time=2026-04-09T15:00:00Z --shard=0 --yes

			# Roll back all shards of a multi-shard env in parallel
			metaplay database rollback my-game-prod --all-shards --target-time=1h --yes --confirm-production
		`),
		Run: runCommand(&o),
	}

	cmd.Flags().IntVar(&o.flagShard, "shard", -1, "Target shard index (required for multi-shard environments when --all-shards is not set)")
	cmd.Flags().BoolVar(&o.flagAllShards, "all-shards", false, "Roll back every shard in parallel")
	cmd.Flags().StringVar(&o.flagTargetTime, "target-time", "", "Target time for rollback. Either an RFC3339 timestamp (2026-04-09T15:00:00Z) or a relative duration (30m, 2h)")
	cmd.Flags().BoolVar(&o.flagNoWait, "no-wait", false, "Return immediately after the rollback is initiated, do not poll for completion")
	cmd.Flags().BoolVar(&o.flagForce, "force", false, "Proceed even if a game server is currently deployed in the environment (DANGEROUS)")
	cmd.Flags().BoolVar(&o.flagYes, "yes", false, "Skip the confirmation prompt (required in non-interactive mode)")
	cmd.Flags().BoolVar(&o.flagConfirmProduction, "confirm-production", false, "Required when rolling back a production environment")
	cmd.Flags().StringVarP(&o.flagFormat, "format", "f", databaseFormatText, "Output format (text or json)")

	databaseCmd.AddCommand(cmd)
}

func (o *databaseRollbackOpts) Prepare(cmd *cobra.Command, args []string) error {
	if err := validateDatabaseFormat(o.flagFormat); err != nil {
		return err
	}
	if !tui.IsInteractiveMode() {
		if !o.flagYes {
			return clierrors.NewUsageError("Confirmation required for destructive operation").
				WithSuggestion("Pass --yes in non-interactive mode to confirm the rollback")
		}
		if o.flagTargetTime == "" {
			return clierrors.NewUsageError("--target-time is required in non-interactive mode").
				WithSuggestion("Pass --target-time=<RFC3339 timestamp> or --target-time=<duration like 30m>")
		}
	}
	return nil
}

func (o *databaseRollbackOpts) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	envConfig, targetEnv, caps, err := resolveEnvironmentForDatabaseOps(ctx, o.argEnvironment)
	if err != nil {
		return err
	}

	if envConfig.Type == portalapi.EnvironmentTypeProduction && !o.flagConfirmProduction {
		return clierrors.Newf("Production environment detected: %s", envConfig.Name).
			WithSuggestion("Pass --confirm-production to confirm rolling back a production environment")
	}

	shardIndices, err := resolveTargetShards(ctx, caps, o.flagShard, o.flagAllShards)
	if err != nil {
		return err
	}
	if err := ensureShardsSupportCapability(
		caps,
		shardIndices,
		func(s envapi.DatabaseShardCapabilities) bool { return s.SupportsRollback },
		"Rollback",
	); err != nil {
		return err
	}

	// Refuse if a game server is currently deployed in the environment
	// (split-brain risk). --force overrides with a loud warning.
	if err := o.checkGameServerNotRunning(targetEnv, envConfig); err != nil {
		return err
	}

	// Parse / prompt for the target time.
	targetTime, err := o.resolveTargetTime(caps, shardIndices)
	if err != nil {
		return err
	}

	// Warn (but do not reject) if the target time is outside any selected
	// shard's reported rollback window. The backend is the ultimate arbiter,
	// so we forward the request regardless.
	warnIfOutsideRollbackWindow(caps, shardIndices, targetTime)

	if !o.flagYes {
		if err := o.confirmInteractively(ctx, envConfig.Name, shardIndices, targetTime); err != nil {
			return err
		}
	}

	log.Info().Msgf("Rolling back '%s' on shard(s) %s to %s",
		styles.RenderTechnical(envConfig.Name),
		styles.RenderTechnical(formatShardList(shardIndices)),
		styles.RenderTechnical(formatDatabaseTime(targetTime)))
	log.Info().Msg("")

	multiShard := len(shardIndices) > 1
	results := syncutil.ParallelMap(shardIndices, len(shardIndices), func(shardIndex int) shardOperationResult {
		return runShardOperation(
			ctx,
			targetEnv,
			shardIndex,
			"rollback",
			func() (*envapi.DatabaseOperation, error) {
				return targetEnv.RollbackDatabase(&envapi.RollbackDatabaseRequest{
					ShardIndex: shardIndex,
					TargetTime: targetTime,
				})
			},
			o.flagNoWait,
			multiShard,
		)
	})

	if o.flagFormat == databaseFormatJSON {
		return printCreateResultsJSON(results)
	}

	return aggregateShardResults("rollback", results)
}

// checkGameServerNotRunning refuses the rollback if a game server Helm release
// exists in the environment's namespace, unless --force is set (in which case
// it logs a loud warning and proceeds).
func (o *databaseRollbackOpts) checkGameServerNotRunning(target *envapi.TargetEnvironment, envConfig *metaproj.ProjectEnvironmentConfig) error {
	kubeconfig, err := target.GetKubeConfigWithEmbeddedCredentials()
	if err != nil {
		return clierrors.Wrap(err, "Failed to fetch kubeconfig for the environment")
	}
	actionConfig, err := helmutil.NewActionConfig(kubeconfig, envConfig.GetKubernetesNamespace())
	if err != nil {
		return clierrors.Wrap(err, "Failed to initialize Helm client for the environment")
	}
	helmReleases, err := helmutil.HelmListReleases(actionConfig, "metaplay-gameserver")
	if err != nil {
		return clierrors.Wrap(err, "Failed to check for existing game server deployments")
	}
	if len(helmReleases) == 0 {
		log.Info().Msgf("%s No active game server deployments found, proceeding with rollback",
			styles.RenderSuccess("✓"))
		return nil
	}
	if !o.flagForce {
		return clierrors.New("Cannot rollback database while a game server is deployed").
			WithDetails(
				"Rolling back the database while the game server is running would leave",
				"the server in an inconsistent state — it will still have in-memory data",
				"that no longer matches the rolled-back database.",
			).
			WithSuggestion(fmt.Sprintf("Remove the game server first with 'metaplay remove server %s', or pass --force to override", o.argEnvironment))
	}
	log.Warn().Msgf("%s Active game server deployment detected in environment '%s'",
		styles.RenderWarning("⚠"), o.argEnvironment)
	log.Warn().Msgf("   Proceeding anyway because --force was passed.")
	log.Warn().Msgf("   The game server will likely need to be restarted after the rollback.")
	log.Info().Msg("")
	return nil
}

// resolveTargetTime parses the --target-time flag (or prompts the user for
// one in interactive mode). The rollback window from capabilities is displayed
// as context when prompting.
func (o *databaseRollbackOpts) resolveTargetTime(caps *envapi.DatabaseCapabilitiesResponse, shardIndices []int) (time.Time, error) {
	now := time.Now().UTC()
	if o.flagTargetTime != "" {
		return parseRollbackTargetTime(o.flagTargetTime, now)
	}
	// Prepare() guarantees we are in interactive mode here.
	printRollbackWindowHint(caps, shardIndices)
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Target time (RFC3339 timestamp or duration like 30m): ")
	input, err := reader.ReadString('\n')
	if err != nil {
		return time.Time{}, clierrors.Wrap(err, "Failed to read target time")
	}
	return parseRollbackTargetTime(strings.TrimSpace(input), now)
}

// confirmInteractively shows a confirmation dialog summarising the rollback
// about to happen. In interactive mode only (Prepare() enforces --yes in
// non-interactive mode).
func (o *databaseRollbackOpts) confirmInteractively(
	ctx context.Context,
	envName string,
	shardIndices []int,
	targetTime time.Time,
) error {
	body := fmt.Sprintf(
		"Environment: %s\nShards: %s\nTarget time: %s\n\n"+
			"All database changes after the target time will be lost.",
		envName,
		formatShardList(shardIndices),
		formatDatabaseTime(targetTime),
	)
	ok, err := tui.DoConfirmDialog(ctx, "Roll back database", body, "Continue?")
	if err != nil {
		return err
	}
	if !ok {
		return clierrors.New("Rollback cancelled").WithSuggestion("No changes were made to the database")
	}
	return nil
}

// printRollbackWindowHint logs the reported rollback windows for the selected
// shards, so the user can pick a valid target time during the interactive
// prompt.
func printRollbackWindowHint(caps *envapi.DatabaseCapabilitiesResponse, shardIndices []int) {
	log.Info().Msgf("%s", styles.RenderTitle("Rollback window:"))
	selected := make(map[int]bool, len(shardIndices))
	for _, idx := range shardIndices {
		selected[idx] = true
	}
	for _, s := range caps.Shards {
		if !selected[s.ShardIndex] {
			continue
		}
		if s.RollbackWindow == nil {
			log.Info().Msgf("  shard %d: %s", s.ShardIndex, styles.RenderMuted("rollback not available"))
			continue
		}
		log.Info().Msgf("  shard %d: %s → %s",
			s.ShardIndex,
			styles.RenderTechnical(formatDatabaseTime(s.RollbackWindow.EarliestTime)),
			styles.RenderTechnical(formatDatabaseTime(s.RollbackWindow.LatestTime)))
	}
	log.Info().Msg("")
}

// warnIfOutsideRollbackWindow logs a warning (but does not fail) if the
// target time falls outside the reported rollback window of any selected
// shard. The backend is the ultimate arbiter and may reject the request.
func warnIfOutsideRollbackWindow(caps *envapi.DatabaseCapabilitiesResponse, shardIndices []int, targetTime time.Time) {
	selected := make(map[int]bool, len(shardIndices))
	for _, idx := range shardIndices {
		selected[idx] = true
	}
	for _, s := range caps.Shards {
		if !selected[s.ShardIndex] || s.RollbackWindow == nil {
			continue
		}
		if targetTime.Before(s.RollbackWindow.EarliestTime) || targetTime.After(s.RollbackWindow.LatestTime) {
			log.Warn().Msgf("%s target time %s is outside the rollback window of shard %d (%s → %s)",
				styles.RenderWarning("⚠"),
				formatDatabaseTime(targetTime),
				s.ShardIndex,
				formatDatabaseTime(s.RollbackWindow.EarliestTime),
				formatDatabaseTime(s.RollbackWindow.LatestTime))
		}
	}
}
