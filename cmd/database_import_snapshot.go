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
	"regexp"
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

// databaseImportSnapshotOpts holds the options for the 'database import' command
type databaseImportSnapshotOpts struct {
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
	o := databaseImportSnapshotOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")
	args.AddStringArgument(&o.argInputFile, "INPUT_FILE", "Input file path containing database snapshot (eg, 'database-snapshot.mdb').")

	cmd := &cobra.Command{
		Use:   "import-snapshot [ENVIRONMENT] [INPUT_FILE] [flags]",
		Short: "[preview] Import database snapshot from a file",
		Long: renderLong(&o, `
			PREVIEW: This is a preview feature and interface may change in the future.

			Import database snapshot from a file created by 'database export' into the target
			environment.

			WARNING: This is a destructive operation and will PERMANENTLY OVERWRITE ALL DATA in
			the target environment's database!

			Safety protections:
			- By default, requires manual confirmation before proceeding
			- Use --yes to skip overwrite confirmation (intended for automation)
			- Use --force to bypass game server deployment checks (can put the database in an
			  inconsistent state!)
			- Use --confirm-production when importing to production environments

			For multi-shard environments, each shard snapshot will be restored to the corresponding
			shard in the target environment (shard_0.sql.gz → shard 0, etc.). The target environment
			must have the same number of shards as the snapshot, or otherwise the command will fail.

			NOTE: The import operation can fail when the target environment has a different type of
			database than the one exported. This is caused by the __EFMigrationsHistory table having
			a collation that is not compatible across different database types. This is a known issue
			and will be addressed in the future.

			{Arguments}

			Related commands:
			- 'metaplay database export-snapshot' exports a database snapshot.
		`),
		Example: renderExample(`
			# Import database snapshot to 'nimbly' environment (asks for manual confirmation)
			metaplay database import-snapshot nimbly snapshot.mdb

			# Auto-accept import without confirmation prompt
			metaplay database import-snapshot nimbly snapshot.mdb --yes

			# Import to production environment (requires additional confirmation)
			metaplay database import-snapshot production snapshot.mdb --yes --confirm-production
		`),
		Run: runCommand(&o),
	}

	cmd.Flags().BoolVar(&o.flagYes, "yes", false, "Skip confirmation prompt and proceed with import")
	cmd.Flags().BoolVar(&o.flagForce, "force", false, "Proceed with import even if a game server is deployed (DANGEROUS!)")
	cmd.Flags().BoolVar(&o.flagConfirmProduction, "confirm-production", false, "Required flag when importing to production environments")

	databaseCmd.AddCommand(cmd)
}

func (o *databaseImportSnapshotOpts) Prepare(cmd *cobra.Command, args []string) error {
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

func (o *databaseImportSnapshotOpts) Run(cmd *cobra.Command) error {
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

	// Create Kubernetes client.
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return err
	}

	// Fetch the database shard configuration from Kubernetes secret
	log.Debug().Str("namespace", kubeCli.Namespace).Msg("Fetching database shard configuration")
	dbShards, err := kubeutil.FetchDatabaseShardsFromSecret(cmd.Context(), kubeCli, kubeCli.Namespace)
	if err != nil {
		return err
	}

	// Configure Helm to check for active deployments
	actionConfig, err := helmutil.NewActionConfig(kubeCli.KubeConfig, envConfig.GetKubernetesNamespace())
	if err != nil {
		return fmt.Errorf("failed to initialize Helm config: %v", err)
	}

	// Check for a game server is deployed - dangerous to import if there is a deployment
	helmReleases, err := helmutil.HelmListReleases(actionConfig, "metaplay-gameserver")
	if err != nil {
		return fmt.Errorf("failed to check for existing Helm releases: %v", err)
	}
	hasGameServer := len(helmReleases) > 0

	log.Info().Msg("")
	log.Info().Msg("Import database snapshot:")
	log.Info().Msgf("  Environment:     %s", styles.RenderTechnical(o.argEnvironment))
	if hasGameServer {
		log.Info().Msgf("  Game server:     %s", styles.RenderWarning("⚠️ deployed"))
	} else {
		log.Info().Msgf("  Game server:     %s", styles.RenderSuccess("✓ not deployed"))
	}
	log.Info().Msgf("  Database shards: %s", styles.RenderTechnical(fmt.Sprintf("%d", len(dbShards))))
	log.Info().Msgf("  Import file:     %s", styles.RenderTechnical(o.argInputFile))
	log.Info().Msg("")

	// Check if there's a game server deployed.
	if hasGameServer {
		if !o.flagForce {
			return fmt.Errorf("cannot import database: active game server deployment detected in environment '%s'. Remove the game server deployment before importing the database", o.argEnvironment)
		}

		log.Info().Msgf("%s %s", styles.RenderWarning("⚠️"), fmt.Sprintf("WARNING: active game server deployment detected in environment '%s'", o.argEnvironment))
		log.Info().Msgf("   Proceeding with database import due to --force flag")
		log.Info().Msg("")
	}

	// Show warning and get confirmation.
	if !o.flagYes {
		// Check if we're in non-interactive mode - fail if we can't prompt
		if !tui.IsInteractiveMode() {
			return fmt.Errorf("--yes flag is required in non-interactive mode to confirm the destructive database import operation")
		}

		log.Info().Msg(styles.RenderWarning("⚠️ WARNING: This will PERMANENTLY OVERWRITE ALL DATA in the database!"))
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
		log.Info().Msg("")
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
	return o.importDatabaseContents(cmd.Context(), kubeCli, podName, "debug", dbShards)
}

// Main function to import database contents - reads zip file, validates metadata, and imports all shards
func (o *databaseImportSnapshotOpts) importDatabaseContents(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, dbShards []kubeutil.DatabaseShardConfig) error {
	log.Info().Msgf("Importing database...")

	// Open and validate zip file
	zipReader, metadata, schemaFile, shardFiles, err := o.openAndValidateZipFile()
	if err != nil {
		return fmt.Errorf("failed to validate zip file: %v", err)
	}
	defer zipReader.Close()
	log.Debug().Str("source_env", metadata.Environment).Str("database", metadata.DatabaseName).Int("shards", metadata.NumShards).Msg("Import metadata validated")

	// Number of shards must match
	if len(shardFiles) != len(dbShards) {
		return fmt.Errorf("shard count mismatch: snapshot has %d shards, target environment has %d", metadata.NumShards, len(dbShards))
	}

	// Apply schema to all shards first
	log.Debug().Msg("Apply schema to all shards")
	for _, targetShard := range dbShards {
		log.Debug().Int("shard_index", targetShard.ShardIndex).Str("database_name", targetShard.DatabaseName).Msg("Apply schema to shard")
		err := o.importDatabaseSchema(ctx, zipReader, schemaFile, kubeCli, podName, debugContainerName, targetShard)
		if err != nil {
			return fmt.Errorf("failed to apply schema to shard %d: %v", targetShard.ShardIndex, err)
		}
		log.Debug().Int("shard_index", targetShard.ShardIndex).Msg("Schema applied to shard")
	}

	// Import data to each shard
	log.Debug().Msg("Importing data to all shards")
	for i, shardFile := range shardFiles {
		targetShard := dbShards[i]
		log.Debug().Int("shard_index", targetShard.ShardIndex).Str("database_name", targetShard.DatabaseName).Msg("Start shard data import")

		err := o.importDatabaseShardData(ctx, zipReader, shardFile, kubeCli, podName, debugContainerName, targetShard)
		if err != nil {
			return fmt.Errorf("failed to import shard %d data: %v", targetShard.ShardIndex, err)
		}
		log.Debug().Int("shard_index", targetShard.ShardIndex).Msg("Shard data import completed")
	}

	log.Info().Msg("")
	log.Info().Msgf("✅ Database import completed successfully")

	return nil
}

// Helper function to open zip file and validate metadata, schema, and shard files
func (o *databaseImportSnapshotOpts) openAndValidateZipFile() (*zip.ReadCloser, *DatabaseSnapshotMetadata, *zip.File, []*zip.File, error) {
	// Open zip file
	zipReader, err := zip.OpenReader(o.argInputFile)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to open zip file: %v", err)
	}

	// Find and read metadata file, schema file, and shard files
	var metadataFile *zip.File
	var schemaFile *zip.File
	var shardFiles []*zip.File

	// Pattern to match shard files: shard_{int}.sql[.suffix]
	shardPattern := regexp.MustCompile(`^shard_\d+\.sql(?:\..+)?$`)

	for _, file := range zipReader.File {
		if file.Name == "metadata.json" {
			metadataFile = file
		} else if file.Name == "schema.sql" {
			schemaFile = file
		} else if shardPattern.MatchString(file.Name) {
			shardFiles = append(shardFiles, file)
		}
	}

	if metadataFile == nil {
		zipReader.Close()
		return nil, nil, nil, nil, fmt.Errorf("metadata file 'metadata.json' not found in zip archive")
	}

	if schemaFile == nil {
		zipReader.Close()
		return nil, nil, nil, nil, fmt.Errorf("schema file 'schema.sql' not found in zip archive")
	}

	// Read and parse metadata
	metadataReader, err := metadataFile.Open()
	if err != nil {
		zipReader.Close()
		return nil, nil, nil, nil, fmt.Errorf("failed to open metadata file: %v", err)
	}
	defer metadataReader.Close()

	metadataBytes, err := io.ReadAll(metadataReader)
	if err != nil {
		zipReader.Close()
		return nil, nil, nil, nil, fmt.Errorf("failed to read metadata: %v", err)
	}

	var metadata DatabaseSnapshotMetadata
	err = json.Unmarshal(metadataBytes, &metadata)
	if err != nil {
		zipReader.Close()
		return nil, nil, nil, nil, fmt.Errorf("failed to parse metadata: %v", err)
	}

	// Check version compatibility
	if metadata.Version != 1 {
		zipReader.Close()
		return nil, nil, nil, nil, fmt.Errorf("unsupported snapshot version %d, expected version 1", metadata.Version)
	}

	// Validate metadata
	err = o.validateMetadata(&metadata, targetShards)
	if err != nil {
		zipReader.Close()
		return nil, nil, nil, nil, err
	}

	// Validate shard files
	if len(shardFiles) != metadata.NumShards {
		zipReader.Close()
		return nil, nil, nil, nil, fmt.Errorf("expected %d shard files, found %d", metadata.NumShards, len(shardFiles))
	}

	if len(shardFiles) != len(targetShards) {
		zipReader.Close()
		return nil, nil, nil, nil, fmt.Errorf("shard count mismatch: snapshot has %d shards, target environment has %d", len(shardFiles), len(targetShards))
	}

	return zipReader, &metadata, schemaFile, shardFiles, nil
}

// Helper function to import database schema to a single shard
func (o *databaseImportSnapshotOpts) importDatabaseSchema(ctx context.Context, zipReader *zip.ReadCloser, schemaFile *zip.File, kubeCli *envapi.KubeClient, podName, debugContainerName string, targetShard kubeutil.DatabaseShardConfig) error {
	// Open schema file from zip
	schemaReader, err := schemaFile.Open()
	if err != nil {
		return fmt.Errorf("failed to open schema file %s: %v", schemaFile.Name, err)
	}
	defer schemaReader.Close()

	// Build mariadb import command for schema
	importCmd := fmt.Sprintf("mariadb -h %s -u %s -p%s %s",
		targetShard.ReadWriteHost, // Use primary host for writes
		targetShard.UserId,
		targetShard.Password,
		targetShard.DatabaseName)

	log.Debug().Str("host", targetShard.ReadWriteHost).Str("database", targetShard.DatabaseName).Msg("Executing schema import command")

	// Execute mariadb import command and stream schema directly
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
		In:     schemaReader, // Stream schema directly from zip
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}

	err = execRemoteKubernetesCommand(ctx, kubeCli.RestConfig, req.URL(), ioStreams, false, false)
	if err != nil {
		return fmt.Errorf("schema import failed: %v", err)
	}
	log.Debug().Str("schema_file", schemaFile.Name).Msg("Schema imported successfully")

	return nil
}

// Helper function to import shard data by streaming compressed data to remote execution
func (o *databaseImportSnapshotOpts) importDatabaseShardData(ctx context.Context, zipReader *zip.ReadCloser, shardFile *zip.File, kubeCli *envapi.KubeClient, podName, debugContainerName string, targetShard kubeutil.DatabaseShardConfig) error {
	// Open shard file from zip
	shardReader, err := shardFile.Open()
	if err != nil {
		return fmt.Errorf("failed to open shard file %s: %v", shardFile.Name, err)
	}
	defer shardReader.Close()

	// Build mariadb import command for uncompressed SQL
	importCmd := fmt.Sprintf("mariadb -h %s -u %s -p%s %s",
		targetShard.ReadWriteHost, // Use primary host for writes
		targetShard.UserId,
		targetShard.Password,
		targetShard.DatabaseName)

	log.Debug().Str("host", targetShard.ReadWriteHost).Str("database", targetShard.DatabaseName).Msg("Executing mariadb import command")

	// Execute mariadb import command and stream uncompressed SQL data directly
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
		In:     shardReader, // Stream uncompressed SQL data directly from zip
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
