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
	"strings"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/kubeutil"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	scheme "k8s.io/client-go/kubernetes/scheme"
)

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
	flagOutput       string // If set, write output to this file instead of stdout
}

func init() {
	o := debugDatabaseOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")
	args.AddStringArgumentOpt(&o.argShardIndex, "SHARD", "Optional: Database shard index to connect to. If not specified, the first shard (index 0) will be used.")

	cmd := &cobra.Command{
		Use:     "database [ENVIRONMENT] [POD] [SHARD] [flags]",
		Aliases: []string{"db"},
		Short:   "[preview] Connect to a database shard for the specified environment",
		Long: renderLong(&o, `
			PREVIEW: This is a preview feature and interface may change in the future.

			Connect to a database shard for the specified environment using MariaDB CLI.

			This command starts a temporary debug pod and runs an SQL client inside it, connecting
			to the specified database replica (read-only or read-write), and shard.

			By default, the read-only replica will be used, for safety. Use --read-write to connect
			to the read-write replica.

			Optionally, you can use --query <sql> to specify a SQL statement to execute immediately
			and output the result, or --query-file <filename> to read the SQL statement from a file.

			When running non-interactive queries (using --query or --query-file), you can use
			--output <filename> to write the output to a file instead of stdout.

			{Arguments}

			Related commands:
			- 'metaplay debug shell' starts a debug shell in a running game server pod.
		`),
		Example: renderExample(`
			# Connect to a database shard in the 'nimbly' environment using the first shard
			metaplay debug database nimbly

			# Connect to the second shard in the 'nimbly' environment (0-based indexing is used)
			metaplay debug database nimbly 1

			# Connect to the read-write replica instead of the default read-only replica
			metaplay debug database nimbly --read-write

			# Run a query on the first shard and exit immediately after
			metaplay debug database nimbly 0 --query "SELECT COUNT(*) FROM Players"

			# Run a query on the first shard and write the output to a file
			metaplay debug database nimbly 0 --query "SELECT COUNT(*) FROM Players" --output count.txt

			# Run a query from a file on the first shard and exit immediately after
			metaplay debug database nimbly 0 --query-file ./my_query.sql
		`),
		Run: runCommand(&o),
	}
	cmd.Flags().BoolVar(&o.flagReadWrite, "read-write", false, "Connect to the read-write replica (default: read-only)")
	cmd.Flags().StringVarP(&o.flagQuery, "query", "q", "", "Run this SQL query and exit (non-interactive)")
	cmd.Flags().StringVar(&o.flagQueryFile, "query-file", "", "Read SQL query from a file and execute it (non-interactive)")
	cmd.Flags().StringVar(&o.flagOutput, "output", "", "Write output to a file instead of stdout (non-interactive)")
	debugCmd.AddCommand(cmd)
}

func (o *debugDatabaseOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Handle mutually exclusive query flags
	if o.flagQuery != "" && o.flagQueryFile != "" {
		return fmt.Errorf("only one of --query or --query-file may be specified")
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
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return err
	}

	// Fetch the database shard configuration from Kubernetes secret
	shards, err := kubeutil.FetchDatabaseShardsFromSecret(cmd.Context(), kubeCli, kubeCli.Namespace)
	if err != nil {
		return err
	}

	// Fill in shard indices
	for shardNdx := range shards {
		shards[shardNdx].ShardIndex = shardNdx
	}

	// Create a debug container to run MySQL client
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
	replicaType := map[bool]string{false: "read-only", true: "read-write"}[o.flagReadWrite]
	stderrLogger.Info().Msg("")
	stderrLogger.Info().Msg("Database info:")
	stderrLogger.Info().Msgf("  Shard:   %s (%d available)", styles.RenderTechnical(fmt.Sprintf("%d", shardIndex)), len(shards))
	stderrLogger.Info().Msgf("  Replica: %s", styles.RenderTechnical(replicaType))
	stderrLogger.Info().Msg("")

	// Connect to the database shard
	return o.connectToDatabaseShard(cmd.Context(), kubeCli, podName, "debug", targetShard)
}

// Helper function to connect to the database shard
func (o *debugDatabaseOpts) connectToDatabaseShard(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName string, shard kubeutil.DatabaseShardConfig) error {
	var host string
	if o.flagReadWrite {
		host = shard.ReadWriteHost
	} else {
		host = shard.ReadOnlyHost
	}

	// Determine whether to run in interactive mode or a single query
	isInteractive := o.flagQuery == "" && o.flagQueryFile == ""

	// Use of --output is only allowed with a non-interactive query
	if o.flagOutput != "" && isInteractive {
		return fmt.Errorf("--output is only allowed with a non-interactive query (--query or --query-file)")
	}

	// Determine command for starting SQL CLI (note: optional query is piped to mariadb)
	sqlcliCmd := fmt.Sprintf("mariadb -h %s -u %s -p%s %s",
		host,
		shard.UserId,
		shard.Password,
		shard.DatabaseName)

	if o.flagQuery != "" {
		stderrLogger.Info().Msgf("Run query: %s", o.flagQuery)
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
			Command:   []string{"/bin/sh", "-c", sqlcliCmd},
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       isInteractive,
		}, scheme.ParameterCodec)

	// Setup stdin for piping queries or interactive mode
	var stdin io.Reader
	if isInteractive {
		stdin = os.Stdin
	} else if o.flagQuery != "" {
		// Pipe the query to mariadb stdin
		stdin = strings.NewReader(o.flagQuery)
	} else if o.flagQueryFile != "" {
		// Stream query file directly to mariadb stdin
		queryFile, err := os.Open(o.flagQueryFile)
		if err != nil {
			return fmt.Errorf("failed to open query file '%s': %v", o.flagQueryFile, err)
		}
		defer queryFile.Close()
		stdin = queryFile
	}

	// Setup output channel to stdout or file
	var out io.Writer = os.Stdout
	var file *os.File
	if o.flagOutput != "" && !isInteractive {
		var err error
		file, err = os.Create(o.flagOutput)
		if err != nil {
			return fmt.Errorf("failed to open output file '%s': %v", o.flagOutput, err)
		}
		defer file.Close()
		out = file
	}

	ioStreams := IOStreams{
		In:     stdin,
		Out:    out,
		ErrOut: os.Stderr,
	}

	err := execRemoteKubernetesCommand(ctx, kubeCli.RestConfig, req.URL(), ioStreams, isInteractive, false)
	if err == nil && o.flagOutput != "" && !isInteractive {
		stderrLogger.Info().Msg("")
		stderrLogger.Info().Msgf("✅ Wrote output to %s", styles.RenderTechnical(o.flagOutput))
	}
	return err
}

// chooseDatabaseShardDialog shows a dialog to select a database shard interactively.
// The 'shards' argument should be a slice of databaseShardConfig.
func (o *debugDatabaseOpts) chooseDatabaseShardDialog(shards []kubeutil.DatabaseShardConfig) (*kubeutil.DatabaseShardConfig, error) {
	if !tui.IsInteractiveMode() {
		return nil, fmt.Errorf("in non-interactive mode, database shard must be explicitly specified")
	}

	selected, err := tui.ChooseFromListDialog(
		"Select Database Shard",
		shards,
		func(shard *kubeutil.DatabaseShardConfig) (string, string) {
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
