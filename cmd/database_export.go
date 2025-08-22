/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/kubeutil"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
)

// databaseExportOpts holds the options for the 'database export' command
type databaseExportOpts struct {
	UsePositionalArgs

	// Environment and output file
	argEnvironment string
	argOutputFile  string
}

func init() {
	o := databaseExportOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")
	args.AddStringArgumentOpt(&o.argOutputFile, "OUTPUT_FILE", "Output file path for the database dump.")

	cmd := &cobra.Command{
		Use:     "export [ENVIRONMENT] [OUTPUT_FILE] [flags]",
		Aliases: []string{"exp"},
		Short:   "[preview] Export database dump from an environment",
		Long: renderLong(&o, `
			PREVIEW: This is a preview feature and interface may change in the future.

			Export a full database dump from the specified environment using mariadb-dump.

			This command starts a temporary debug pod and runs mariadb-dump inside it, connecting
			to the read-only replica of the database and creating a complete dump.

			For multi-shard environments, each shard will be exported as a separate file
			within the zip archive (shard_0.sql.gz, shard_1.sql.gz, etc.).

			The dump will be written to the specified output file as a zip archive containing
			compressed SQL dumps and metadata.

			{Arguments}

			Related commands:
			- 'metaplay debug database' connects to a database shard interactively.
		`),
		Example: renderExample(`
			# Export database from 'nimbly' environment to a local zip file
			metaplay database export nimbly database_dump.zip

			# Export to a file with timestamp
			metaplay database export nimbly "dump_$(date +%Y%m%d_%H%M%S).zip"
		`),
		Run: runCommand(&o),
	}
	databaseCmd.AddCommand(cmd)
}

func (o *databaseExportOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Both arguments are required
	if o.argEnvironment == "" {
		return fmt.Errorf("ENVIRONMENT argument is required")
	}
	if o.argOutputFile == "" {
		return fmt.Errorf("OUTPUT_FILE argument is required")
	}
	return nil
}

func (o *databaseExportOpts) Run(cmd *cobra.Command) error {
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

	// Resolve target environment & game server
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)
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

	// Validate that we have at least one shard
	if len(shards) == 0 {
		return fmt.Errorf("no database shards found in environment")
	}

	// Fill in shard indices
	for shardNdx := range shards {
		shards[shardNdx].ShardIndex = shardNdx
	}

	// Show info
	stderrLogger.Info().Msg("")
	stderrLogger.Info().Msg("Database export info:")
	stderrLogger.Info().Msgf("  Environment: %s", styles.RenderTechnical(o.argEnvironment))
	stderrLogger.Info().Msgf("  Shards:      %s", styles.RenderTechnical(fmt.Sprintf("%d", len(shards))))
	stderrLogger.Info().Msgf("  Output file: %s", styles.RenderTechnical(o.argOutputFile))
	stderrLogger.Info().Msg("")

	// Create a debug container to run mariadb-dump
	log.Debug().Msg("Creating debug pod for database export")
	podName, cleanup, err := kubeutil.CreateDebugPod(
		cmd.Context(),
		kubeCli,
		"joseluisq/alpine-mysql-client:1.8",
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

	// Export the database
	log.Debug().Str("output_file", o.argOutputFile).Msg("Starting database export process")
	return o.exportDatabaseContents(cmd.Context(), kubeCli, podName, "debug", shards)
}

// DatabaseExportMetadata contains information about the database export
type DatabaseExportMetadata struct {
	Version       int       `json:"version"`
	Environment   string    `json:"environment"`
	DatabaseName  string    `json:"database_name"`
	NumShards     int       `json:"num_shards"`
	ExportedAt    time.Time `json:"exported_at"`
	Compression   string    `json:"compression"`
	ExportOptions string    `json:"export_options"`
}

// Main function to export database contents - creates zip file, writes metadata, and exports all shards
func (o *databaseExportOpts) exportDatabaseContents(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, shards []kubeutil.DatabaseShardConfig) error {
	stderrLogger.Info().Msgf("Starting database export...")
	exportOptions := "--single-transaction --routines --triggers --no-tablespaces"

	// Create output zip file
	log.Debug().Str("zip_file", o.argOutputFile).Msg("Creating output zip file")
	zipFile, err := os.Create(o.argOutputFile)
	if err != nil {
		return fmt.Errorf("failed to create zip file: %v", err)
	}
	defer zipFile.Close()

	// Create zip writer
	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// Write metadata to zip
	log.Debug().Msg("Writing metadata to zip file")
	err = o.writeMetadataToZip(zipWriter, shards, exportOptions)
	if err != nil {
		return fmt.Errorf("failed to write metadata: %v", err)
	}

	// Export each shard
	var dumpFileNames []string
	for _, shard := range shards {
		log.Debug().Int("shard_index", shard.ShardIndex).Str("database_name", shard.DatabaseName).Msg("Starting shard export")
		dumpFileName, err := o.exportDatabaseShard(ctx, zipWriter, kubeCli, podName, debugContainerName, exportOptions, shard)
		if err != nil {
			return fmt.Errorf("failed to export shard %d: %v", shard.ShardIndex, err)
		}
		log.Debug().Int("shard_index", shard.ShardIndex).Str("dump_file", dumpFileName).Msg("Shard export completed")
		dumpFileNames = append(dumpFileNames, dumpFileName)
	}

	stderrLogger.Info().Msg("")
	stderrLogger.Info().Msgf("✅ Database export completed successfully")
	stderrLogger.Info().Msgf("   Output written to: %s", styles.RenderTechnical(o.argOutputFile))

	return nil
}

// Helper function to write metadata to zip file
func (o *databaseExportOpts) writeMetadataToZip(zipWriter *zip.Writer, shards []kubeutil.DatabaseShardConfig, exportOptions string) error {

	// Use first shard for database name (all shards should have same database name)
	databaseName := ""
	if len(shards) > 0 {
		databaseName = shards[0].DatabaseName
	}

	// Create metadata
	log.Debug().Str("database_name", databaseName).Int("num_shards", len(shards)).Msg("Creating export metadata")
	metadata := DatabaseExportMetadata{
		Version:       1,
		Environment:   o.argEnvironment,
		DatabaseName:  databaseName,
		NumShards:     len(shards),
		ExportedAt:    time.Now().UTC(),
		Compression:   "gzip",
		ExportOptions: exportOptions,
	}

	// Create metadata JSON in memory
	metadataBytes, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %v", err)
	}

	// Write metadata file to zip
	metadataFileName := "export_metadata.json"
	metadataHeader := &zip.FileHeader{
		Name:     metadataFileName,
		Method:   zip.Store, // No compression for small metadata file
		Modified: time.Now(),
	}
	metadataHeader.SetMode(0644)

	// Write metadata file to zip
	metadataWriter, err := zipWriter.CreateHeader(metadataHeader)
	if err != nil {
		return fmt.Errorf("failed to create metadata writer: %v", err)
	}
	if _, err := metadataWriter.Write(metadataBytes); err != nil {
		return fmt.Errorf("failed to write metadata content: %v", err)
	}

	return nil
}

// Helper function to export a single database shard using mariadb-dump and write directly to zip
func (o *databaseExportOpts) exportDatabaseShard(ctx context.Context, zipWriter *zip.Writer, kubeCli *envapi.KubeClient, podName, debugContainerName, exportOptions string, shard kubeutil.DatabaseShardConfig) (string, error) {
	// Build mariadb-dump command with gzip compression
	// Note: Only gzip supported on the used image, something like zstandard would be better
	dumpCmd := fmt.Sprintf("mariadb-dump -h %s -u %s -p%s %s %s | gzip",
		shard.ReadOnlyHost, // Use read-only replica
		shard.UserId,
		shard.Password,
		exportOptions,
		shard.DatabaseName)
	log.Debug().Str("host", shard.ReadOnlyHost).Str("database", shard.DatabaseName).Msg("Executing mariadb-dump command")

	// Prepare zip header for streaming
	dumpFileName := fmt.Sprintf("shard_%d.sql.gz", shard.ShardIndex)
	dumpHeader := &zip.FileHeader{
		Name:     dumpFileName,
		Method:   zip.Store, // Use Store since data is already gzipped
		Modified: time.Now(),
	}
	dumpHeader.SetMode(0644)

	// Create zip writer for this file - stream directly without buffering
	dumpWriter, err := zipWriter.CreateHeader(dumpHeader)
	if err != nil {
		return "", fmt.Errorf("failed to create dump writer: %v", err)
	}

	// Execute mariadb-dump command and stream output directly to zip
	req := kubeCli.Clientset.CoreV1().
		RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(kubeCli.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: debugContainerName,
			Command:   []string{"/bin/sh", "-c", dumpCmd},
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	ioStreams := IOStreams{
		In:     nil,
		Out:    dumpWriter, // Stream directly to zip writer
		ErrOut: os.Stderr,
	}

	err = execRemoteKubernetesCommand(ctx, kubeCli.RestConfig, req.URL(), ioStreams, false, false)
	if err != nil {
		return "", fmt.Errorf("database export failed: %v", err)
	}
	log.Debug().Str("dump_file", dumpFileName).Msg("Dump streamed directly to zip archive")

	return dumpFileName, nil
}
