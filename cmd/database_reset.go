/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/metaplay/cli/pkg/kubeutil"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
)

// databaseResetOpts holds the options for the 'database reset' command
type databaseResetOpts struct {
	UsePositionalArgs

	// Environment argument
	argEnvironment string

	// Flags
	flagYes               bool
	flagForce             bool
	flagConfirmProduction bool
}

func init() {
	o := databaseResetOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")

	cmd := &cobra.Command{
		Use:     "reset [ENVIRONMENT] [flags]",
		Aliases: []string{"nuke"},
		Short:   "Reset database by dropping all tables",
		Long: renderLong(&o, `
			Reset the database by dropping all tables in all shards. This operation is designed
			to handle database schema version mismatches and ensure a clean database state.

			The reset process uses the following sequence:
			1. Mark reset in progress by setting MasterVersion to -4004
			2. Drop all tables except MetaInfo (preserves reset state)
			3. Drop MetaInfo tables in reverse shard order

			This ensures the reset can be resumed if interrupted and maintains consistency.

			WARNING: This operation is DESTRUCTIVE and will delete ALL data in the database.
			Use with extreme caution and only on development/staging environments.

			{Arguments}
		`),
		Example: renderExample(`
			# Reset database in nimbly environment (requires confirmation)
			metaplay database reset nimbly

			# Auto-accept reset without confirmation prompt
			metaplay database reset nimbly --yes
		`),
		Run: runCommand(&o),
	}

	cmd.Flags().BoolVar(&o.flagYes, "yes", false, "Skip confirmation prompt and proceed with reset")
	cmd.Flags().BoolVar(&o.flagForce, "force", false, "Proceed with reset even if a game server is deployed (DANGEROUS!!)")
	cmd.Flags().BoolVar(&o.flagConfirmProduction, "confirm-production", false, "Required flag when resetting production environments")

	databaseCmd.AddCommand(cmd)
}

func (o *databaseResetOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Environment argument is required
	if o.argEnvironment == "" {
		return clierrors.NewUsageError("ENVIRONMENT argument is required").
			WithSuggestion("Specify the target environment, e.g., 'metaplay database reset develop'")
	}

	// In non-interactive mode, --yes flag is required for safety
	if !tui.IsInteractiveMode() && !o.flagYes {
		return clierrors.NewUsageError("Confirmation required for destructive operation").
			WithSuggestion("Use --yes flag in non-interactive mode to confirm database reset")
	}

	return nil
}

func (o *databaseResetOpts) Run(cmd *cobra.Command) error {
	// Resolve the project & auth provider
	project, err := tryResolveProject()
	if err != nil {
		return err
	}

	// Resolve environment config
	envConfig, tokenSet, err := resolveEnvironment(cmd.Context(), project, o.argEnvironment)
	if err != nil {
		return err
	}

	// Check if this is a production environment and require additional confirmation
	if envConfig.Type == portalapi.EnvironmentTypeProduction && !o.flagConfirmProduction {
		return clierrors.Newf("Production environment detected: %s", envConfig.Name).
			WithSuggestion("Use --confirm-production flag to confirm reset of production environments")
	}

	// Resolve target environment & game server
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	// Get kubeconfig to access the environment for Helm operations
	kubeconfigPayload, err := targetEnv.GetKubeConfigWithEmbeddedCredentials()
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig: %v", err)
	}
	log.Debug().Msg("Resolved kubeconfig to access environment")

	// Configure Helm to check for active deployments
	actionConfig, err := helmutil.NewActionConfig(kubeconfigPayload, envConfig.GetKubernetesNamespace())
	if err != nil {
		return fmt.Errorf("failed to initialize Helm config: %v", err)
	}

	// Check for any active game server Helm deployments - refuse to reset if found
	helmReleases, err := helmutil.HelmListReleases(actionConfig, "metaplay-gameserver")
	if err != nil {
		return clierrors.Wrap(err, "Failed to check for existing Helm releases")
	}

	// Check if there's a game server deployed.
	log.Info().Msg("")
	if len(helmReleases) > 0 {
		if !o.flagForce {
			return clierrors.New("Cannot reset database while game server is deployed").
				WithSuggestion(fmt.Sprintf("Remove the game server first with 'metaplay remove server %s', or use --force to proceed anyway", o.argEnvironment))
		}

		log.Warn().Msgf("%s %s", styles.RenderWarning("⚠️"), fmt.Sprintf("WARNING: active game server deployment detected in environment '%s'", o.argEnvironment))
		log.Warn().Msgf("   Proceeding with database reset due to --force flag.")
		log.Warn().Msgf("   Your game server will stop functioning and you'll need to re-deploy it after the reset.")
		log.Info().Msg("")
	} else {
		log.Info().Msgf("%s %s", styles.RenderSuccess("✓"), "No active game server deployments found, proceeding with database reset")
	}
	log.Info().Msg("")

	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return err
	}

	// Fetch the database shard configuration from Kubernetes secret
	log.Debug().Str("namespace", kubeCli.Namespace).Msg("Fetching database shard configuration")
	shards, err := kubeutil.FetchDatabaseShardsFromSecret(cmd.Context(), kubeCli, kubeCli.Namespace)
	if err != nil {
		return err
	}

	// Show warning and get confirmation
	if !o.flagYes {
		// Check if we're in non-interactive mode - fail if we can't prompt
		if !tui.IsInteractiveMode() {
			return fmt.Errorf("--yes flag is required in non-interactive mode to confirm the destructive database reset operation")
		}

		log.Info().Msg(styles.RenderWarning("⚠️ WARNING: This will PERMANENTLY DELETE ALL DATA in the database!"))
		log.Info().Msgf("   Environment: %s", styles.RenderTechnical(o.argEnvironment))
		log.Info().Msgf("   Shards:      %s", styles.RenderTechnical(fmt.Sprintf("%d", len(shards))))
		log.Info().Msg("")
		log.Info().Msg("This operation cannot be undone. Make sure you have backups if needed.")
		log.Info().Msg("")

		fmt.Print("Type 'yes' to confirm database reset: ")
		var confirmation string
		fmt.Scanln(&confirmation)
		if strings.ToLower(confirmation) != "yes" {
			log.Info().Msg("Database reset cancelled.")
			return nil
		}
	}

	// Create a debug container to run SQL commands
	log.Debug().Msg("Creating debug pod for database reset")
	podName, cleanup, err := kubeutil.CreateDebugPod(
		cmd.Context(),
		kubeCli,
		debugDatabaseImage,
		false,
		false,
		[]string{"sleep", "3600"},
	)
	if err != nil {
		return err
	}
	log.Debug().Str("pod_name", podName).Msg("Debug pod created successfully")
	// Make sure the debug container is cleaned up even if we return early
	defer cleanup()

	log.Debug().Str("environment", o.argEnvironment).Msg("Starting database reset process")

	// Get table names from all shards once at the beginning
	allShardTables, err := o.getAllShardTables(cmd.Context(), kubeCli, podName, "debug", shards)
	if err != nil {
		return fmt.Errorf("failed to get table information from shards: %v", err)
	}

	// Check if database is already empty
	totalTables := 0
	for _, tables := range allShardTables {
		totalTables += len(tables)
	}

	if totalTables == 0 {
		log.Info().Msgf("✅ Database is already empty - no reset needed")
		log.Info().Msgf("   Environment: %s", styles.RenderTechnical(o.argEnvironment))
		return nil
	}

	return o.resetDatabaseContents(cmd.Context(), kubeCli, podName, "debug", shards, allShardTables)
}

// getAllShardTables gets table names from all shards once and returns a map of shard index to table names
func (o *databaseResetOpts) getAllShardTables(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, shards []kubeutil.DatabaseShardConfig) (map[int][]string, error) {
	allShardTables := make(map[int][]string)

	for _, shard := range shards {
		tables, err := o.getTableNames(ctx, kubeCli, podName, debugContainerName, shard)
		if err != nil {
			// If we can't connect to a shard or it doesn't exist, consider it empty
			log.Debug().Int("shard_index", shard.ShardIndex).Err(err).Msg("Failed to get table names from shard, considering it empty")
			allShardTables[shard.ShardIndex] = []string{}
			continue
		}

		allShardTables[shard.ShardIndex] = tables
		log.Debug().Int("shard_index", shard.ShardIndex).Int("table_count", len(tables)).Msg("Retrieved table names from shard")
	}

	return allShardTables, nil
}

// Reset the database in the target environment. Uses the same sequence as the game server
// does for a resumable reset flow.
func (o *databaseResetOpts) resetDatabaseContents(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, shards []kubeutil.DatabaseShardConfig, allShardTables map[int][]string) error {
	log.Info().Msgf("Starting database reset...")

	// Phase 0: Mark reset in progress by setting MasterVersion to -4004
	log.Info().Msg("Phase 0: Mark reset in progress...")
	err := o.markResetInProgress(ctx, kubeCli, podName, debugContainerName, shards[0])
	if err != nil {
		return fmt.Errorf("failed to mark reset in progress: %v", err)
	}

	// Phase 1: Drop all tables except MetaInfo in all shards
	log.Info().Msg("Phase 1: Drop all tables except MetaInfo...")
	for _, shard := range shards {
		log.Debug().Int("shard_index", shard.ShardIndex).Str("database_name", shard.DatabaseName).Msg("Starting shard reset phase 1")
		tables := allShardTables[shard.ShardIndex]
		err := o.resetShardPhase1(ctx, kubeCli, podName, debugContainerName, shard, tables)
		if err != nil {
			return fmt.Errorf("failed to reset shard %d phase 1: %v", shard.ShardIndex, err)
		}
		log.Debug().Int("shard_index", shard.ShardIndex).Msg("Shard reset phase 1 completed")
	}

	// Phase 2: Drop MetaInfo tables in reverse shard order
	log.Info().Msg("Phase 2: Drop MetaInfo tables in reverse order...")
	// Iterate shards in reverse order (highest index first)
	for i := len(shards) - 1; i >= 0; i-- {
		shard := shards[i]
		log.Debug().Int("shard_index", shard.ShardIndex).Str("database_name", shard.DatabaseName).Msg("Starting shard reset phase 2")
		err := o.resetShardPhase2(ctx, kubeCli, podName, debugContainerName, shard)
		if err != nil {
			return fmt.Errorf("failed to reset shard %d phase 2: %v", shard.ShardIndex, err)
		}
		log.Debug().Int("shard_index", shard.ShardIndex).Msg("Shard reset phase 2 completed")
	}

	log.Info().Msgf("✅ Database reset completed successfully")
	log.Info().Msgf("   Environment: %s", styles.RenderTechnical(o.argEnvironment))
	log.Info().Msgf("   All tables dropped from %d shards", len(shards))

	return nil
}

// Helper function to mark reset in progress by setting MasterVersion to -4004
func (o *databaseResetOpts) markResetInProgress(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, mainShard kubeutil.DatabaseShardConfig) error {
	const resetInProgressVersion = -4004

	// Build SQL command to insert new MetaInfo record with reset in progress marker
	sqlCmd := fmt.Sprintf(`INSERT INTO MetaInfo (Version, Timestamp, MasterVersion, NumShards)
		SELECT Version + 1, NOW(), %d, 0 FROM MetaInfo WHERE Version = (SELECT MAX(Version) FROM MetaInfo);`,
		resetInProgressVersion)

	err := o.executeSQLCommand(ctx, kubeCli, podName, debugContainerName, mainShard, sqlCmd)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to mark reset in progress (table may not exist yet)")
		return fmt.Errorf("failed to mark reset in progress: %v", err)
	}
	log.Debug().Msg("Marked reset in progress")

	return nil
}

// Helper function to reset a single shard - Phase 1: Drop all tables except MetaInfo
func (o *databaseResetOpts) resetShardPhase1(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, shard kubeutil.DatabaseShardConfig, tables []string) error {
	// Filter out MetaInfo table (case-insensitive)
	var tablesToDrop []string
	for _, table := range tables {
		if strings.ToLower(table) != "metainfo" {
			tablesToDrop = append(tablesToDrop, table)
		}
	}

	log.Debug().Int("shard_index", shard.ShardIndex).Int("total_tables", len(tables)).Int("tables_to_drop", len(tablesToDrop)).Msg("Phase 1: Dropping tables except MetaInfo")

	// Drop each table
	for _, table := range tablesToDrop {
		sqlCmd := fmt.Sprintf("DROP TABLE IF EXISTS `%s`;", table)
		err := o.executeSQLCommand(ctx, kubeCli, podName, debugContainerName, shard, sqlCmd)
		if err != nil {
			return fmt.Errorf("failed to drop table %s: %v", table, err)
		}
		log.Debug().Int("shard_index", shard.ShardIndex).Str("table", table).Msg("Dropped table")
	}

	return nil
}

// Helper function to reset a single shard - Phase 2: Drop MetaInfo table
func (o *databaseResetOpts) resetShardPhase2(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, shard kubeutil.DatabaseShardConfig) error {
	log.Debug().Int("shard_index", shard.ShardIndex).Msg("Phase 2: Dropping MetaInfo table")

	sqlCmd := "DROP TABLE IF EXISTS `MetaInfo`;"
	err := o.executeSQLCommand(ctx, kubeCli, podName, debugContainerName, shard, sqlCmd)
	if err != nil {
		return fmt.Errorf("failed to drop MetaInfo table: %v", err)
	}

	log.Debug().Int("shard_index", shard.ShardIndex).Msg("Dropped MetaInfo table")
	return nil
}

// Helper function to get list of table names from a database shard
func (o *databaseResetOpts) getTableNames(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, shard kubeutil.DatabaseShardConfig) ([]string, error) {
	sqlCmd := "SHOW TABLES;"

	// Execute the command and capture output
	output, err := o.executeSQLCommandWithOutput(ctx, kubeCli, podName, debugContainerName, shard, sqlCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to execute SHOW TABLES: %v", err)
	}

	// Parse the output to extract table names
	var tables []string
	lines := strings.Split(strings.TrimSpace(output), "\n")

	// Skip header line and process each table name
	for i, line := range lines {
		if i == 0 {
			continue // Skip header
		}
		tableName := strings.TrimSpace(line)
		if tableName != "" {
			tables = append(tables, tableName)
		}
	}

	return tables, nil
}

// Helper function to execute a SQL command on a database shard
func (o *databaseResetOpts) executeSQLCommand(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, shard kubeutil.DatabaseShardConfig, sqlCmd string) error {
	// Build mariadb command
	mariadbCmd := fmt.Sprintf("cat | mariadb -h %s -u %s -p%s %s",
		shard.ReadWriteHost, // Use primary host for writes
		shard.UserId,
		shard.Password,
		shard.DatabaseName)

	log.Debug().Str("host", shard.ReadWriteHost).Str("database", shard.DatabaseName).Str("sql", sqlCmd).Msg("Executing SQL command")

	// Execute mariadb command and pipe SQL
	req := kubeCli.Clientset.CoreV1().
		RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(kubeCli.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: debugContainerName,
			Command:   []string{"/bin/sh", "-c", mariadbCmd},
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	ioStreams := IOStreams{
		In:     strings.NewReader(sqlCmd),
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}

	err := execRemoteKubernetesCommand(ctx, kubeCli.RestConfig, req.URL(), ioStreams, false, false)
	if err != nil {
		return fmt.Errorf("SQL command execution failed: %v", err)
	}

	return nil
}

// Helper function to execute a SQL command and capture its output
func (o *databaseResetOpts) executeSQLCommandWithOutput(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, shard kubeutil.DatabaseShardConfig, sqlCmd string) (string, error) {
	// Build mariadb command
	mariadbCmd := fmt.Sprintf("cat | mariadb -h %s -u %s -p%s %s",
		shard.ReadWriteHost, // Use primary host for writes
		shard.UserId,
		shard.Password,
		shard.DatabaseName)

	log.Debug().Str("host", shard.ReadWriteHost).Str("database", shard.DatabaseName).Str("sql", sqlCmd).Msg("Executing SQL command with output capture")

	// Execute mariadb command and capture output
	req := kubeCli.Clientset.CoreV1().
		RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(kubeCli.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: debugContainerName,
			Command:   []string{"/bin/sh", "-c", mariadbCmd},
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	// Capture output in a buffer
	var outputBuffer strings.Builder
	ioStreams := IOStreams{
		In:     strings.NewReader(sqlCmd),
		Out:    &outputBuffer,
		ErrOut: os.Stderr,
	}

	err := execRemoteKubernetesCommand(ctx, kubeCli.RestConfig, req.URL(), ioStreams, false, false)
	if err != nil {
		return "", fmt.Errorf("SQL command execution failed: %v", err)
	}

	return outputBuffer.String(), nil
}
