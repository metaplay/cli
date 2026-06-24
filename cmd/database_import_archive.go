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

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/metaplay/cli/pkg/kubeutil"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
)

// databaseImportArchiveOpts holds the options for the 'database import-archive' command
type databaseImportArchiveOpts struct {
	UsePositionalArgs

	// Environment and input file
	argEnvironment string
	argInputFile   string

	// Flags
	flagYes               bool
	flagForce             bool
	flagConfirmProduction bool
	flagDryRun            bool
}

func init() {
	o := databaseImportArchiveOpts{}

	args := o.Arguments()
	args.AddStringArgument(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")
	args.AddStringArgument(&o.argInputFile, "INPUT_FILE", "Input file path containing database archive (eg, 'database-archive.mdb').")

	cmd := &cobra.Command{
		Use:   "import-archive [ENVIRONMENT] [INPUT_FILE] [flags]",
		Short: "Import database archive from a file",
		Long: renderLong(&o, `
			Import database archive from a file created by 'database export-archive' into the target
			environment.

			WARNING: This is a destructive operation and will PERMANENTLY OVERWRITE ALL DATA in
			the target environment's database!

			Safety protections:
			- By default, requires manual confirmation before proceeding
			- Use --dry-run to preview the import summary (including any database master-version
			  warning) without importing anything
			- Use --yes to skip overwrite confirmation (intended for automation)
			- Use --force to bypass game server deployment checks (can put the database in an
			  inconsistent state!)
			- Use --confirm-production when importing to production environments

			For multi-shard environments, each shard archive will be restored to the corresponding
			shard in the target environment (shard_0.sql → shard 0, etc.). The target environment
			must have the same number of shards as the archive, or otherwise the command will fail.

			{Arguments}

			Related commands:
			- 'metaplay database export-archive' exports a database archive.
		`),
		Example: renderExample(`
			# Preview the import summary without importing anything
			metaplay database import-archive nimbly archive.mdb --dry-run

			# Import database archive to 'nimbly' environment (asks for manual confirmation)
			metaplay database import-archive nimbly archive.mdb

			# Auto-accept import without confirmation prompt
			metaplay database import-archive nimbly archive.mdb --yes

			# Import to production environment (requires additional confirmation)
			metaplay database import-archive production archive.mdb --yes --confirm-production
		`),
		Run: runCommand(&o),
	}

	cmd.Flags().BoolVar(&o.flagYes, "yes", false, "Skip confirmation prompt and proceed with import")
	cmd.Flags().BoolVar(&o.flagForce, "force", false, "Proceed with import even if a game server is deployed (DANGEROUS!)")
	cmd.Flags().BoolVar(&o.flagConfirmProduction, "confirm-production", false, "Required flag when importing to production environments")
	cmd.Flags().BoolVar(&o.flagDryRun, "dry-run", false, "Show the import summary (including any master-version warning) without importing anything")

	databaseCmd.AddCommand(cmd)
}

func (o *databaseImportArchiveOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Both arguments are required
	if o.argEnvironment == "" {
		return fmt.Errorf("ENVIRONMENT argument is required")
	}
	if o.argInputFile == "" {
		return fmt.Errorf("INPUT_FILE argument is required")
	}

	// In non-interactive mode, --yes flag is required for safety (unless this is a dry run, which
	// imports nothing).
	if !tui.IsInteractiveMode() && !o.flagYes && !o.flagDryRun {
		return fmt.Errorf("--yes flag is required in non-interactive mode to confirm the destructive database import operation")
	}

	return nil
}

func (o *databaseImportArchiveOpts) Run(cmd *cobra.Command) error {
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

	// Open and validate the archive up front so we can show its shard count and fail fast on mismatch.
	zipReader, metadata, schemaFile, shardFiles, err := o.openAndValidateZipFile()
	if err != nil {
		return fmt.Errorf("failed to validate zip file: %v", err)
	}
	defer func() { _ = zipReader.Close() }()
	log.Debug().Str("source_env", metadata.Environment).Str("database", metadata.DatabaseName).Int("shards", metadata.NumShards).Msg("Import metadata validated")

	// Detect whether importing this archive risks being wiped on the next server deploy due to a
	// database master-version mismatch in an environment that nukes the DB on mismatch. Best-effort:
	// requires a local project config and the archive's captured master version.
	masterVersionWarning := o.checkMasterVersionMismatch(project, envConfig, metadata)

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Import Database Archive"))
	log.Info().Msg("")
	log.Info().Msg("Archive:")
	log.Info().Msgf("  %-16s %s", "File:", styles.RenderTechnical(o.argInputFile))
	log.Info().Msgf("  %-16s %s", "Database shards:", styles.RenderTechnical(fmt.Sprintf("%d", len(shardFiles))))
	if metadata.MasterVersion != nil {
		log.Info().Msgf("  %-16s %s", "Master version:", styles.RenderTechnical(fmt.Sprintf("%d", *metadata.MasterVersion)))
	} else {
		log.Info().Msgf("  %-16s %s", "Master version:", styles.RenderMuted("not recorded in archive"))
	}
	log.Info().Msg("")
	log.Info().Msg("Target environment:")
	log.Info().Msgf("  %-16s %s", "Name:", styles.RenderTechnical(o.argEnvironment))
	if hasGameServer {
		log.Info().Msgf("  %-16s %s", "Game server:", styles.RenderWarning("⚠️ deployed"))
	} else {
		log.Info().Msgf("  %-16s %s", "Game server:", styles.RenderSuccess("✓ not deployed"))
	}
	log.Info().Msgf("  %-16s %s", "Database shards:", styles.RenderTechnical(fmt.Sprintf("%d", len(dbShards))))
	if masterVersionWarning != "" {
		log.Info().Msg("")
		log.Info().Msgf("%s %s", styles.RenderWarning("⚠️"), styles.RenderWarning(masterVersionWarning))
	}
	log.Info().Msg("")

	// The archive and target environment must have the same number of shards.
	if len(shardFiles) != len(dbShards) {
		return fmt.Errorf("shard count mismatch: archive has %d shards, target environment has %d", len(shardFiles), len(dbShards))
	}

	// Check if there's a game server deployed.
	if hasGameServer {
		if !o.flagForce {
			return fmt.Errorf("cannot import database: active game server deployment detected in environment '%s'. Remove the game server deployment before importing the database", o.argEnvironment)
		}

		log.Info().Msgf("%s %s", styles.RenderWarning("⚠️"), fmt.Sprintf("WARNING: active game server deployment detected in environment '%s'", o.argEnvironment))
		log.Info().Msgf("   Proceeding with database import due to --force flag")
		log.Info().Msg("")
	}

	// If dry-run mode, stop here (after all pre-flight checks, before importing anything). Use the
	// archive's recorded master version; for older archives that don't record it, it can't be read
	// back here because nothing is imported.
	if o.flagDryRun {
		log.Info().Msg(styles.RenderMuted("Dry-run mode: skipping import"))
		o.logMasterVersionMatchReminder(metadata.MasterVersion)
		return nil
	}

	// Show warning and get confirmation.
	if !o.flagYes {
		// Check if we're in non-interactive mode - fail if we can't prompt
		if !tui.IsInteractiveMode() {
			return fmt.Errorf("--yes flag is required in non-interactive mode to confirm the destructive database import operation")
		}

		log.Info().Msg(styles.RenderWarning("⚠️ WARNING: This will PERMANENTLY OVERWRITE ALL DATA in the database!"))
		log.Info().Msg("")
		if masterVersionWarning != "" {
			log.Info().Msg(styles.RenderWarning(masterVersionWarning))
			log.Info().Msg("")
		}
		log.Info().Msg("This operation cannot be undone. Make sure this is the correct environment.")
		log.Info().Msg("")

		fmt.Print("Type 'yes' to confirm database import: ")
		var confirmation string
		_, _ = fmt.Scanln(&confirmation)
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
	err = o.importDatabaseContents(cmd.Context(), kubeCli, podName, "debug", dbShards, zipReader, schemaFile, shardFiles)
	if err != nil {
		// Check if the error was due to context cancellation (e.g., user pressed Ctrl+C)
		if cmd.Context().Err() != nil {
			return clierrors.Wrap(cmd.Context().Err(), "Database import cancelled").
				WithSuggestion("Run the command again to retry the import")
		}
		return err
	}

	// Remind the user (after every successful import) that the deployed master version must match the
	// imported data, otherwise a nuke-on-mismatch environment wipes it on the next server deploy. For
	// older archives that don't record the master version, read it back from the freshly imported data
	// (the same way export-archive captures it) so we can always show it.
	importedMasterVersion := metadata.MasterVersion
	if importedMasterVersion == nil && len(dbShards) > 0 {
		shard := dbShards[0]
		importedMasterVersion = queryDatabaseMasterVersion(cmd.Context(), kubeCli, podName, "debug",
			shard.ReadWriteHost, shard.UserId, shard.Password, shard.DatabaseName)
	}
	o.logMasterVersionMatchReminder(importedMasterVersion)

	return nil
}

// Main function to import database contents - reads zip file, validates metadata, and imports all shards
func (o *databaseImportArchiveOpts) importDatabaseContents(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, dbShards []kubeutil.DatabaseShardConfig, zipReader *zip.ReadCloser, schemaFile *zip.File, shardFiles []*zip.File) error {
	log.Info().Msgf("Importing database...")

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

// masterVersionMismatch describes a detected database master-version mismatch that would cause the
// imported data to be wiped on the next server deploy.
type masterVersionMismatch struct {
	ArchiveMasterVersion int  // master version stored in the archive
	ProjectMasterVersion int  // master version the project is configured to deploy
	NukeIsDevDefault     bool // true if the nuke-on-mismatch behavior comes from the development default, false if explicitly configured
}

// warningLine renders a human-readable, single-line warning describing the mismatch.
func (m *masterVersionMismatch) warningLine() string {
	reason := "This environment is configured to reset"
	if m.NukeIsDevDefault {
		reason = "A development env resets"
	}
	return fmt.Sprintf("This archive is at MasterVersion %d but the project deploys %d. %s the DB on a MasterVersion mismatch — deploying a server will wipe this import.",
		m.ArchiveMasterVersion, m.ProjectMasterVersion, reason)
}

// detectMasterVersionMismatch returns a non-nil result when importing an archive risks being wiped on
// the next server deploy: the archive's master version differs from the project's configured master
// version, and the target environment nukes the database on a mismatch. Returns nil when there is no
// such risk or it can't be determined (missing archive/project master version).
func detectMasterVersionMismatch(archiveMasterVersion *int, dbOpts *metaproj.DatabaseRuntimeOptions, envType portalapi.EnvironmentType) *masterVersionMismatch {
	// Need both the archive's and the project's configured master version to compare.
	if archiveMasterVersion == nil || dbOpts == nil || dbOpts.MasterVersion == nil {
		return nil
	}

	// No mismatch means no risk.
	if *archiveMasterVersion == *dbOpts.MasterVersion {
		return nil
	}

	// Determine whether the environment nukes the database on a master-version mismatch. Development
	// environments default to nuking; this can be overridden explicitly in the runtime options.
	nukeIsDevDefault := envType == portalapi.EnvironmentTypeDevelopment
	nukesOnMismatch := nukeIsDevDefault
	if dbOpts.NukeOnVersionMismatch != nil {
		nukesOnMismatch = *dbOpts.NukeOnVersionMismatch
		nukeIsDevDefault = false
	}
	if !nukesOnMismatch {
		return nil
	}

	return &masterVersionMismatch{
		ArchiveMasterVersion: *archiveMasterVersion,
		ProjectMasterVersion: *dbOpts.MasterVersion,
		NukeIsDevDefault:     nukeIsDevDefault,
	}
}

// checkMasterVersionMismatch reads the project's database runtime options and returns a warning line
// when importing this archive risks being wiped on the next server deploy (see
// detectMasterVersionMismatch). Returns "" when there is no project config, the options can't be read,
// or there is no mismatch risk. This is a best-effort safety check and never fails the import.
func (o *databaseImportArchiveOpts) checkMasterVersionMismatch(project *metaproj.MetaplayProject, envConfig *metaproj.ProjectEnvironmentConfig, metadata *DatabaseArchiveMetadata) string {
	if project == nil {
		return ""
	}

	dbOpts, err := project.ReadDatabaseRuntimeOptions(envConfig)
	if err != nil {
		log.Debug().Err(err).Msg("Could not read database runtime options; skipping master-version mismatch check")
		return ""
	}

	mismatch := detectMasterVersionMismatch(metadata.MasterVersion, dbOpts, envConfig.Type)
	if mismatch == nil {
		return ""
	}
	return mismatch.warningLine()
}

// logMasterVersionMatchReminder prints a reminder — shown after both a real import and a dry-run —
// that the server's deployed Database:MasterVersion must match the imported archive, followed by the
// archive's master version (or a note when it could not be determined).
func (o *databaseImportArchiveOpts) logMasterVersionMatchReminder(masterVersion *int) {
	log.Info().Msg("")
	log.Info().Msg(styles.RenderWarning("Make sure that your game server's Database:MasterVersion matches the version in the imported"))
	log.Info().Msg(styles.RenderWarning("archive. Otherwise, the database will be wiped when the game server is deployed."))
	log.Info().Msg("")
	if masterVersion != nil {
		log.Info().Msgf("The archive is at Database:MasterVersion %s.", styles.RenderTechnical(fmt.Sprintf("%d", *masterVersion)))
	} else {
		log.Info().Msgf("%s This archive doesn't include a Database:MasterVersion — it was created with an earlier", styles.RenderWarning("⚠️"))
		log.Info().Msg("version of the CLI that didn't record it.")
	}
}

// Helper function to open zip file and validate metadata, schema, and shard files
func (o *databaseImportArchiveOpts) openAndValidateZipFile() (*zip.ReadCloser, *DatabaseArchiveMetadata, *zip.File, []*zip.File, error) {
	// Open zip file
	zipReader, err := zip.OpenReader(o.argInputFile)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to open zip file: %v", err)
	}

	// Find and read metadata file, schema file, and shard files
	var metadataFile *zip.File
	var schemaFile *zip.File
	var shardFiles []*zip.File

	// Pattern to match shard files: shard_{int}.sql
	shardPattern := regexp.MustCompile(`^shard_\d+\.sql$`)

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
		_ = zipReader.Close()
		return nil, nil, nil, nil, fmt.Errorf("metadata file 'metadata.json' not found in zip archive")
	}

	if schemaFile == nil {
		_ = zipReader.Close()
		return nil, nil, nil, nil, fmt.Errorf("schema file 'schema.sql' not found in zip archive")
	}

	// Read and parse metadata
	metadataReader, err := metadataFile.Open()
	if err != nil {
		_ = zipReader.Close()
		return nil, nil, nil, nil, fmt.Errorf("failed to open metadata file: %v", err)
	}
	defer func() { _ = metadataReader.Close() }()

	// Read metadata.json.
	metadataBytes, err := io.ReadAll(metadataReader)
	if err != nil {
		_ = zipReader.Close()
		return nil, nil, nil, nil, fmt.Errorf("failed to read metadata: %v", err)
	}

	// Parse archive metadata.
	var metadata DatabaseArchiveMetadata
	err = json.Unmarshal(metadataBytes, &metadata)
	if err != nil {
		_ = zipReader.Close()
		return nil, nil, nil, nil, fmt.Errorf("failed to parse metadata: %v", err)
	}

	// Check version compatibility
	if metadata.Version != 1 {
		_ = zipReader.Close()
		return nil, nil, nil, nil, fmt.Errorf("unsupported archive version %d, expected version 1", metadata.Version)
	}

	return zipReader, &metadata, schemaFile, shardFiles, nil
}

// Helper function to import database schema to a single shard
func (o *databaseImportArchiveOpts) importDatabaseSchema(ctx context.Context, zipReader *zip.ReadCloser, schemaFile *zip.File, kubeCli *envapi.KubeClient, podName, debugContainerName string, targetShard kubeutil.DatabaseShardConfig) error {
	// Open schema file from zip
	schemaReader, err := schemaFile.Open()
	if err != nil {
		return fmt.Errorf("failed to open schema file %s: %v", schemaFile.Name, err)
	}
	defer func() { _ = schemaReader.Close() }()

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
func (o *databaseImportArchiveOpts) importDatabaseShardData(ctx context.Context, zipReader *zip.ReadCloser, shardFile *zip.File, kubeCli *envapi.KubeClient, podName, debugContainerName string, targetShard kubeutil.DatabaseShardConfig) error {
	// Open shard file from zip
	shardReader, err := shardFile.Open()
	if err != nil {
		return fmt.Errorf("failed to open shard file %s: %v", shardFile.Name, err)
	}
	defer func() { _ = shardReader.Close() }()

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
