/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

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

// databaseImportOpts holds the options for the 'database import' command
type databaseImportOpts struct {
	UsePositionalArgs

	// Environment and input file
	argEnvironment string
	argInputFile   string

	// Flags
	flagYes               bool
	flagForce             bool
	flagConfirmProduction bool
}

func init() {
	o := databaseImportOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")
	args.AddStringArgument(&o.argInputFile, "INPUT_FILE", "Input file path containing database snapshot (eg, 'database-snapshot.mdb').")

	cmd := &cobra.Command{
		Use:     "import [ENVIRONMENT] [INPUT_FILE] [flags]",
		Aliases: []string{"restore"},
		Short:   "[preview] Import database snapshot from a file",
		Long: renderLong(&o, `
			PREVIEW: This is a preview feature and interface may change in the future.

			Import database snapshot from a file created by 'database export'.

			WARNING: This is a destructive operation and will PERMANENTLY OVERWRITE ALL DATA in the
			target environment's database!

			Safety protections:
			- By default, requires manual confirmation before proceeding
			- Use --yes to skip overwrite confirmation (intended for automation)
			- Use --force to bypass game server deployment checks (can put the database in an inconsistent state!)
			- Use --confirm-production when importing to production environments

			For multi-shard environments, each shard snapshot will be restored to the corresponding
			shard in the target environment (shard_0.sql.gz → shard 0, etc.). The target environment
			must have the same number of shards as the snapshot, or otherwise the command will fail.

			The compressed SQL dumps are streamed directly to the target database without
			decompression on the client side, maintaining network efficiency.

			{Arguments}

			Related commands:
			- 'metaplay database export' creates database snapshot archives.
		`),
		Example: renderExample(`
			# Import database snapshot to 'nimbly' environment (asks for manual confirmation)
			metaplay database import nimbly snapshot.mdb

			# Auto-accept import without confirmation prompt
			metaplay database import nimbly snapshot.mdb --yes

			# Force import even if a game server is deployed (dangerous!)
			metaplay database import nimbly snapshot.mdb --force --yes

			# Import to production environment (requires additional confirmation)
			metaplay database import production snapshot.mdb --yes --confirm-production
		`),
		Run: runCommand(&o),
	}

	cmd.Flags().BoolVar(&o.flagYes, "yes", false, "Skip confirmation prompt and proceed with import")
	cmd.Flags().BoolVar(&o.flagForce, "force", false, "Proceed with import even if a game server is deployed")
	cmd.Flags().BoolVar(&o.flagConfirmProduction, "confirm-production", false, "Required flag when importing to production environments")

	databaseCmd.AddCommand(cmd)
}

func (o *databaseImportOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Both arguments are required
	if o.argEnvironment == "" {
		return fmt.Errorf("ENVIRONMENT argument is required")
	}
	if o.argInputFile == "" {
		return fmt.Errorf("INPUT_FILE argument is required")
	}

	// In non-interactive mode, --yes flag is required for safety
	if !tui.IsInteractiveMode() && !o.flagYes {
		return fmt.Errorf("--yes flag is required in non-interactive mode to confirm the destructive database import operation")
	}

	return nil
}

func (o *databaseImportOpts) Run(cmd *cobra.Command) error {
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
		return fmt.Errorf("production environment detected: %s. The --confirm-production flag is required when importing to production environments", envConfig.Name)
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

	// Check for any active game server Helm deployments - refuse to import if found
	helmReleases, err := helmutil.HelmListReleases(actionConfig, "metaplay-gameserver")
	if err != nil {
		return fmt.Errorf("failed to check for existing Helm releases: %v", err)
	}

	// Check if there's a game server deployed.
	log.Info().Msg("")
	if len(helmReleases) > 0 {
		if !o.flagForce {
			return fmt.Errorf("cannot import database: active game server deployment detected in environment '%s'. Remove the game server deployment before importing the database, or use --force to proceed anyway", o.argEnvironment)
		}

		log.Info().Msgf("%s %s", styles.RenderWarning("⚠️"), fmt.Sprintf("WARNING: active game server deployment detected in environment '%s'", o.argEnvironment))
		log.Info().Msgf("   Proceeding with database import due to --force flag")
	} else {
		log.Info().Msgf("%s %s", styles.RenderSuccess("✓"), "No active game server deployments found, proceeding with database import")
	}
	log.Info().Msg("")

	// Create Kubernetes client.
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return err
	}

	// Show warning and get confirmation.
	if !o.flagYes {
		// Check if we're in non-interactive mode - fail if we can't prompt
		if !tui.IsInteractiveMode() {
			return fmt.Errorf("--yes flag is required in non-interactive mode to confirm the destructive database import operation")
		}

		log.Info().Msg(styles.RenderWarning("⚠️ WARNING: This will PERMANENTLY OVERWRITE ALL DATA in the database!"))
		log.Info().Msgf("   Environment: %s", styles.RenderTechnical(o.argEnvironment))
		log.Info().Msgf("   Import file: %s", styles.RenderTechnical(o.argInputFile))
		log.Info().Msg("")
		log.Info().Msg("This operation cannot be undone. Make sure this is the correct environment.")
		log.Info().Msg("")

		fmt.Print("Type 'yes' to confirm database import: ")
		var confirmation string
		fmt.Scanln(&confirmation)
		if strings.ToLower(confirmation) != "yes" {
			log.Info().Msg("Database import cancelled.")
			return nil
		}
	}

	// Fetch the database shard configuration from Kubernetes secret
	log.Debug().Str("namespace", kubeCli.Namespace).Msg("Fetching database shard configuration")
	shards, err := kubeutil.FetchDatabaseShardsFromSecret(cmd.Context(), kubeCli, kubeCli.Namespace)
	if err != nil {
		return err
	}

	// Validate that we have at least one shard
	if len(shards) == 0 {
		return fmt.Errorf("no database shards found in environment")
	}

	// Fill in shard indices
	for shardNdx := range shards {
		shards[shardNdx].ShardIndex = shardNdx
	}

	// Create a debug container to run mariadb import
	log.Debug().Msg("Creating debug pod for database import")
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

	log.Debug().Str("input_file", o.argInputFile).Msg("Starting database import process")
	return o.importDatabaseContents(cmd.Context(), kubeCli, podName, "debug", shards)
}

// Main function to import database contents - reads zip file, validates metadata, and imports all shards
func (o *databaseImportOpts) importDatabaseContents(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, targetShards []kubeutil.DatabaseShardConfig) error {
	log.Info().Msgf("Importing database...")

	// Open and validate zip file
	zipReader, metadata, shardFiles, err := o.openAndValidateZipFile(targetShards)
	if err != nil {
		return fmt.Errorf("failed to validate zip file: %v", err)
	}
	defer zipReader.Close()

	log.Debug().Str("source_env", metadata.Environment).Str("database", metadata.DatabaseName).Int("shards", metadata.NumShards).Msg("Import metadata validated")

	// Import each shard
	for i, shardFile := range shardFiles {
		targetShard := targetShards[i]
		log.Debug().Int("shard_index", targetShard.ShardIndex).Str("database_name", targetShard.DatabaseName).Msg("Starting shard import")

		err := o.importDatabaseShard(ctx, zipReader, shardFile, kubeCli, podName, debugContainerName, targetShard)
		if err != nil {
			return fmt.Errorf("failed to import shard %d: %v", targetShard.ShardIndex, err)
		}
		log.Debug().Int("shard_index", targetShard.ShardIndex).Msg("Shard import completed")
	}

	log.Info().Msg("")
	log.Info().Msgf("✅ Database import completed successfully")

	return nil
}

// Helper function to open zip file and validate metadata and shard files
func (o *databaseImportOpts) openAndValidateZipFile(targetShards []kubeutil.DatabaseShardConfig) (*zip.ReadCloser, *DatabaseSnapshotMetadata, []*zip.File, error) {
	// Open zip file
	zipReader, err := zip.OpenReader(o.argInputFile)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to open zip file: %v", err)
	}

	// Find and read metadata file
	var metadataFile *zip.File
	var shardFiles []*zip.File

	for _, file := range zipReader.File {
		if file.Name == "export_metadata.json" {
			metadataFile = file
		} else if len(file.Name) > 4 && file.Name[len(file.Name)-7:] == ".sql.gz" {
			shardFiles = append(shardFiles, file)
		}
	}

	if metadataFile == nil {
		zipReader.Close()
		return nil, nil, nil, fmt.Errorf("metadata file 'export_metadata.json' not found in zip archive")
	}

	// Read and parse metadata
	metadataReader, err := metadataFile.Open()
	if err != nil {
		zipReader.Close()
		return nil, nil, nil, fmt.Errorf("failed to open metadata file: %v", err)
	}
	defer metadataReader.Close()

	metadataBytes, err := io.ReadAll(metadataReader)
	if err != nil {
		zipReader.Close()
		return nil, nil, nil, fmt.Errorf("failed to read metadata: %v", err)
	}

	var metadata DatabaseSnapshotMetadata
	err = json.Unmarshal(metadataBytes, &metadata)
	if err != nil {
		zipReader.Close()
		return nil, nil, nil, fmt.Errorf("failed to parse metadata: %v", err)
	}

	// Validate metadata
	err = o.validateMetadata(&metadata, targetShards)
	if err != nil {
		zipReader.Close()
		return nil, nil, nil, err
	}

	// Validate shard files
	if len(shardFiles) != metadata.NumShards {
		zipReader.Close()
		return nil, nil, nil, fmt.Errorf("expected %d shard files, found %d", metadata.NumShards, len(shardFiles))
	}

	if len(shardFiles) != len(targetShards) {
		zipReader.Close()
		return nil, nil, nil, fmt.Errorf("shard count mismatch: dump has %d shards, target environment has %d", len(shardFiles), len(targetShards))
	}

	return zipReader, &metadata, shardFiles, nil
}

// Helper function to validate metadata compatibility
func (o *databaseImportOpts) validateMetadata(metadata *DatabaseSnapshotMetadata, targetShards []kubeutil.DatabaseShardConfig) error {
	// Check version compatibility
	if metadata.Version != 1 {
		return fmt.Errorf("unsupported dump version %d, expected version 1", metadata.Version)
	}

	// Check shard count
	if metadata.NumShards != len(targetShards) {
		return fmt.Errorf("shard count mismatch: dump has %d shards, target environment has %d", metadata.NumShards, len(targetShards))
	}

	// Check database name consistency (all target shards should have same database name)
	if len(targetShards) > 0 {
		expectedDBName := targetShards[0].DatabaseName
		for _, shard := range targetShards {
			if shard.DatabaseName != expectedDBName {
				return fmt.Errorf("target environment has inconsistent database names")
			}
		}

		// Warn if database names differ (but don't fail)
		if metadata.DatabaseName != expectedDBName {
			log.Warn().Str("dump_db", metadata.DatabaseName).Str("target_db", expectedDBName).Msg("Database name mismatch - proceeding anyway")
		}
	}

	// Check compression format
	if metadata.Compression != "gzip" {
		return fmt.Errorf("unsupported compression format '%s', expected 'gzip'", metadata.Compression)
	}

	return nil
}

// Helper function to import a single database shard by streaming compressed data to remote execution
func (o *databaseImportOpts) importDatabaseShard(ctx context.Context, zipReader *zip.ReadCloser, shardFile *zip.File, kubeCli *envapi.KubeClient, podName, debugContainerName string, targetShard kubeutil.DatabaseShardConfig) error {
	// Open shard file from zip
	shardReader, err := shardFile.Open()
	if err != nil {
		return fmt.Errorf("failed to open shard file %s: %v", shardFile.Name, err)
	}
	defer shardReader.Close()

	// Build mariadb import command with gunzip decompression
	importCmd := fmt.Sprintf("gunzip | mariadb -h %s -u %s -p%s %s",
		targetShard.ReadWriteHost, // Use primary host for writes
		targetShard.UserId,
		targetShard.Password,
		targetShard.DatabaseName)

	log.Debug().Str("host", targetShard.ReadWriteHost).Str("database", targetShard.DatabaseName).Msg("Executing mariadb import command")

	// Execute mariadb import command and stream compressed data directly
	req := kubeCli.Clientset.CoreV1().
		RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(kubeCli.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: debugContainerName,
			Command:   []string{"/bin/sh", "-c", importCmd},
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	ioStreams := IOStreams{
		In:     shardReader, // Stream compressed data directly from zip
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}

	err = execRemoteKubernetesCommand(ctx, kubeCli.RestConfig, req.URL(), ioStreams, false, false)
	if err != nil {
		return fmt.Errorf("database import failed: %v", err)
	}
	log.Debug().Str("shard_file", shardFile.Name).Msg("Shard imported successfully")

	return nil
}
