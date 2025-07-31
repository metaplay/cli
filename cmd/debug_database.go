/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	scheme "k8s.io/client-go/kubernetes/scheme"
)

// Structure to hold the database shard configuration parsed from the YAML file
type databaseShardConfig struct {
	ShardIndex    int    // added via code, not in the YAML file
	DatabaseName  string `yaml:"DatabaseName"`
	Password      string `yaml:"Password"`
	ReadOnlyHost  string `yaml:"ReadOnlyHost"`
	ReadWriteHost string `yaml:"ReadWriteHost"`
	UserId        string `yaml:"UserId"`
}

type metaplayInfraDatabase struct {
	Backend         string                `yaml:"Backend"`
	NumActiveShards int                   `yaml:"NumActiveShards"`
	Shards          []databaseShardConfig `yaml:"Shards"`
}

type metaplayInfraOptions struct {
	Database metaplayInfraDatabase `yaml:"Database"`
}

// debugDatabaseOpts holds the options for the 'debug database' command
type debugDatabaseOpts struct {
	UsePositionalArgs

	// Environment and pod selection
	argEnvironment   string
	argShardIndex    string
	parsedShardIndex int    // parsed and validated in Prepare
	flagReadWrite    bool   // If true, connect to read-write replica; otherwise, read-only
	flagQuery        string // If set, run this SQL query and exit, otherwise run in interactive mode
	flagQueryFile    string // If set, read SQL query from this file and exit (non-interactive)

	DiagnosticsImage string // Diagnostic container image name to use
}

func init() {
	o := debugDatabaseOpts{
		DiagnosticsImage: "metaplay/diagnostics:latest",
	}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")
	args.AddStringArgumentOpt(&o.argShardIndex, "SHARD", "Optional: Database shard index to connect to. If not specified, the first shard (index 0) will be used.")

	cmd := &cobra.Command{
		Use:     "database [ENVIRONMENT] [POD] [SHARD] [flags]",
		Aliases: []string{"db"},
		Short:   "[preview] Connect to a database shard for the specified environment",
		Long: renderLong(&o, `
			PREVIEW: This is a preview feature and interface may change in the future.

			Connect to a database shard for the specified environment using MySQL CLI.

			This command starts an ephemeral debug container and runs the MySQL client inside
			it, connecting to the desired database replica (read-only or read-write) and database
			shard.

			If a shard name is not specified, the first shard (index 0) will be used.

			By default, the read-only replica will be used, for safety. Use --read-write to connect
			to the read-write replica.

			Optionally, you can use --query to specify a SQL statement to execute immediately and print the result, or --query-file to read the SQL statement from a file.

			{Arguments}
		`),
		Example: renderExample(`
			# Connect to a database shard in the 'tough-falcons' environment using the first shard
			metaplay debug database tough-falcons

			# Connect to the second shard in the 'tough-falcons' environment (0-based indexing is used)
			metaplay debug database tough-falcons 1

			# Connect to the read-write replica instead of the default read-only replica
			metaplay debug database tough-falcons --read-write

			# Run a query on the first shard and exit immediately after
			metaplay debug database tough-falcons 0 --query "SELECT COUNT(*) FROM Players"

			# Run a query from a file on the first shard and exit immediately after
			metaplay debug database tough-falcons 0 --query-file ./my_query.sql
		`),
		Run: runCommand(&o),
	}
	cmd.Flags().BoolVar(&o.flagReadWrite, "read-write", false, "Connect to the read-write replica (default: read-only)")
	cmd.Flags().StringVarP(&o.flagQuery, "query", "q", "", "Run this SQL query and exit (non-interactive)")
	cmd.Flags().StringVar(&o.flagQueryFile, "query-file", "", "Read SQL query from a file and execute it (non-interactive)")
	debugCmd.AddCommand(cmd)
}

func (o *debugDatabaseOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Handle mutually exclusive query flags
	if o.flagQuery != "" && o.flagQueryFile != "" {
		return fmt.Errorf("only one of --query or --query-file may be specified")
	}
	if o.flagQueryFile != "" {
		content, err := os.ReadFile(o.flagQueryFile)
		if err != nil {
			return fmt.Errorf("failed to read query file '%s': %v", o.flagQueryFile, err)
		}
		o.flagQuery = string(content)
	}

	// Parse shard index argument if provided
	o.parsedShardIndex = 0 // default
	if o.argShardIndex != "" {
		idx, err := strconv.Atoi(o.argShardIndex)
		if err != nil {
			return fmt.Errorf("invalid argument SHARD '%s': must be an integer", o.argShardIndex)
		}
		if idx < 0 {
			return fmt.Errorf("invalid argument SHARD '%s': must be non-negative", o.argShardIndex)
		}
		o.parsedShardIndex = idx
	} else {
		// In non-interactive mode, SHARD argument must be specified
		if !tui.IsInteractiveMode() {
			return fmt.Errorf("in non-interactive mode, argument SHARD must be specified")
		}
	}
	// Non-interactive mode requires the query in the command line
	if o.flagQuery == "" && !tui.IsInteractiveMode() {
		return fmt.Errorf("in non-interactive mode, argument QUERY must be specified")
	}
	return nil
}

func (o *debugDatabaseOpts) Run(cmd *cobra.Command) error {
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
	gameServer, err := targetEnv.GetGameServer(cmd.Context())
	if err != nil {
		return err
	}

	// Get all shard sets and pods from all clusters associated with the game server.
	shardSetsWithPods, err := gameServer.GetAllShardSetsWithPods()
	if err != nil {
		return err
	}
	if len(shardSetsWithPods) == 0 || len(shardSetsWithPods[0].Pods) == 0 {
		return fmt.Errorf("no pods found in any shard set")
	}
	kubeCli := shardSetsWithPods[0].ShardSet.Cluster.KubeClient
	pod := &shardSetsWithPods[0].Pods[0]

	// Fetch the infrastructure options YAML using the debug container
	stderrLogger.Debug().Msgf("Fetch infra options YAML from pod: %s", pod.Name)
	yamlContent, err := o.fetchInfraOptionsYaml(cmd.Context(), kubeCli, pod.Name, metaplayServerContainerName)
	if err != nil {
		return err
	}

	stderrLogger.Debug().Msgf("Infrastructure options YAML:\n%s", yamlContent)

	// Parse the infrastructure options (only database section)
	var infra metaplayInfraOptions
	err = yaml.Unmarshal([]byte(yamlContent), &infra)
	if err != nil {
		return fmt.Errorf("failed to parse infrastructure options YAML: %v", err)
	}

	shards := infra.Database.Shards
	if len(shards) == 0 {
		return fmt.Errorf("no database shards found in infrastructure configuration")
	}

	// Fill in shard indices
	for shardNdx := range shards {
		shards[shardNdx].ShardIndex = shardNdx
	}

	// Create a debug container to run MySQL client
	debugContainerName, cleanup, err := createDebugContainer(
		cmd.Context(),
		kubeCli,
		pod.Name,
		metaplayServerContainerName,
		false,
		false,
		[]string{"sleep", "3600"},
	)
	if err != nil {
		return err
	}
	// Make sure the debug container is cleaned up even if we return early
	defer cleanup()

	// Interactive shard selection if no index provided and multiple shards exist
	shardIndex := o.parsedShardIndex
	if o.argShardIndex == "" && len(shards) > 1 {
		selected, err := o.chooseDatabaseShardDialog(shards)
		if err != nil {
			return err
		}
		// Find selected index
		for i, s := range shards {
			if &s == selected {
				shardIndex = i
				break
			}
		}
	}
	if shardIndex < 0 || shardIndex >= len(shards) {
		return fmt.Errorf("invalid database shard index %d. Must be between 0 and %d (inclusive)", shardIndex, len(shards)-1)
	}
	targetShard := shards[shardIndex]

	// Show info
	replicaType := "read-only"
	if o.flagReadWrite {
		replicaType = "read-write"
	}
	stderrLogger.Info().Msg("")
	stderrLogger.Info().Msgf("Use database shard:   %s (%d available)", styles.RenderTechnical(fmt.Sprintf("%d", shardIndex)), len(shards))
	stderrLogger.Info().Msgf("Use database replica: %s", styles.RenderTechnical(replicaType))
	stderrLogger.Info().Msg("")

	// Connect to the database shard
	return o.connectToDatabaseShard(cmd.Context(), kubeCli, pod.Name, debugContainerName, targetShard)
}

// Helper function to fetch the infrastructure options YAML from the pod
func (o *debugDatabaseOpts) fetchInfraOptionsYaml(ctx context.Context, kubeCli *envapi.KubeClient, podName, containerName string) (string, error) {
	// Use readFileFromPod with followSymlinks=true to handle symlinked files
	contents, err := readFileFromPod(ctx, kubeCli, podName, containerName, "/etc/metaplay", "runtimeoptions.yaml")
	if err != nil {
		stderrLogger.Error().Msgf("Failed to read infrastructure options: %v", err)
		return "", fmt.Errorf("failed to read infrastructure options: %w", err)
	}

	if len(contents) == 0 {
		stderrLogger.Warn().Msg("Infrastructure options file is empty")
	}

	return string(contents), nil
}

// Helper function to connect to the database shard
func (o *debugDatabaseOpts) connectToDatabaseShard(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, shard databaseShardConfig) error {
	var host string
	if o.flagReadWrite {
		host = shard.ReadWriteHost
	} else {
		host = shard.ReadOnlyHost
	}

	// Determine whether to run in interactive mode or a single query
	isInteractive := o.flagQuery == ""

	// Determine command for starting MySQL CLI.
	mysqlCmd := fmt.Sprintf("mysql -h %s -u %s -p%s %s",
		host,
		shard.UserId,
		shard.Password,
		shard.DatabaseName)

	if o.flagQuery != "" {
		stderrLogger.Info().Msgf("Run query: %s", o.flagQuery)
		mysqlCmd += fmt.Sprintf(" -e %q", o.flagQuery)
	}

	req := kubeCli.Clientset.CoreV1().
		RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(kubeCli.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: debugContainerName,
			Command:   []string{"/bin/bash", "-c", mysqlCmd},
			Stdin:     isInteractive,
			Stdout:    true,
			Stderr:    true,
			TTY:       isInteractive,
		}, scheme.ParameterCodec)

	// Use shared remote command execution utility
	var stdin io.Reader
	if isInteractive {
		stdin = os.Stdin
	}

	ioStreams := IOStreams{
		In:     stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}

	return execRemoteKubernetesCommand(ctx, kubeCli.RestConfig, req.URL(), ioStreams, isInteractive, false)
}

// chooseDatabaseShardDialog shows a dialog to select a database shard interactively.
// The 'shards' argument should be a slice of databaseShardConfig.
func (o *debugDatabaseOpts) chooseDatabaseShardDialog(shards []databaseShardConfig) (*databaseShardConfig, error) {
	if !tui.IsInteractiveMode() {
		return nil, fmt.Errorf("in non-interactive mode, database shard must be explicitly specified")
	}

	selected, err := tui.ChooseFromListDialog(
		"Select Database Shard",
		shards,
		func(shard *databaseShardConfig) (string, string) {
			indexStr := fmt.Sprintf("#%d", shard.ShardIndex)
			if o.flagReadWrite {
				return indexStr, shard.ReadWriteHost
			} else {
				return indexStr, shard.ReadOnlyHost
			}
		},
	)
	if err != nil {
		return nil, err
	}

	log.Info().Msgf(" %s #%d %s", styles.RenderSuccess("✓"), selected.ShardIndex, styles.RenderMuted(selected.DatabaseName))
	return selected, nil
}
