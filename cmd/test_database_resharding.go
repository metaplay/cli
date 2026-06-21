/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/metaplay/cli/pkg/testutil"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// containerShardDir is the directory inside the server container where the on-disk SQLite shard files
// are stored. The host shard directory is bind-mounted here.
const containerShardDir = "/shards"

type testDatabaseReshardingOpts struct {
	flagSkipBuild   bool
	flagKeepDB      bool
	flagShardDir    string
	flagBotCount    int
	flagBotDuration time.Duration
	flagTimeout     time.Duration
}

func init() {
	o := testDatabaseReshardingOpts{}

	cmd := &cobra.Command{
		Use:   "database-resharding",
		Short: "[preview] Run the database resharding test",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			PREVIEW: This command is currently in preview and may change in the future. If you encounter
			problems or have feedback, please file an issue at https://github.com/metaplay/cli/issues/new.

			Validate end-to-end database resharding for your project.

			The test runs entirely within containers using on-disk SQLite shard files:

			1. Build the game server image (unless --skip-build).
			2. Populate a 4-shard database by running bots against a background server, then shut it down
			   gracefully so the shard files are checkpointed and closed.
			3. Reshard the database through a sequence of shard counts (4 -> 1 -> 2 -> 4), running the
			   server once per step with a different --Database:NumActiveShards.
			4. After each step, open each shard's SQLite file directly and assert that the tables exist
			   only on active shards and that row counts are preserved across steps.

			The 1 -> 2 step duplicates shard 0 to shard 1 first, which exercises the resharding fast-path
			(integer-multiple up-sharding); the other steps exercise the generic path.
		`),
		Example: renderExample(`
			# Run the full database resharding test
			metaplay test database-resharding

			# Run without rebuilding the server image (faster if already built)
			metaplay test database-resharding --skip-build

			# Keep the on-disk shard files after the run for debugging
			metaplay test database-resharding --keep-db

			# Populate with more bots over a longer duration
			metaplay test database-resharding --bot-count=50 --bot-duration=3m
		`),
	}

	testCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.BoolVar(&o.flagSkipBuild, "skip-build", false, "Skip the docker image build step (faster if you already built the image)")
	flags.BoolVar(&o.flagKeepDB, "keep-db", false, "Keep the on-disk SQLite shard directory after the run (for debugging)")
	flags.StringVar(&o.flagShardDir, "shard-dir", "", "Host directory to store the SQLite shard files in (defaults to a temporary directory)")
	flags.IntVar(&o.flagBotCount, "bot-count", 30, "Number of bots to run when populating the database")
	flags.DurationVar(&o.flagBotDuration, "bot-duration", 2*time.Minute, "How long to run the bots when populating the database (e.g., 1m, 2m30s)")
	flags.DurationVar(&o.flagTimeout, "timeout", 1*time.Hour, "Timeout for running the test (e.g., 30m, 1h). Does not apply to the image build.")
}

func (o *testDatabaseReshardingOpts) Prepare(cmd *cobra.Command, args []string) error {
	if o.flagTimeout <= 0 {
		return clierrors.NewUsageError("--timeout must be a positive duration (e.g., 30m, 1h)")
	}
	if o.flagBotCount <= 0 {
		return clierrors.NewUsageError("--bot-count must be a positive integer")
	}
	if o.flagBotDuration <= 0 {
		return clierrors.NewUsageError("--bot-duration must be a positive duration (e.g., 1m, 2m30s)")
	}
	return nil
}

func (o *testDatabaseReshardingOpts) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	// Resolve project configuration.
	project, err := resolveProject()
	if err != nil {
		return fmt.Errorf("failed to resolve project: %w", err)
	}

	projectID := project.Config.ProjectHumanID
	serverImage := fmt.Sprintf("%s/server:test", strings.ToLower(projectID))
	integrationTestsConfig := project.Config.IntegrationTests

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Run Database Resharding Test"))
	log.Info().Msg("")

	// Ensure Docker is available (binary + daemon).
	if err := checkDockerAvailable(ctx); err != nil {
		return err
	}

	// Resolve docker build engine.
	buildEngine := "buildkit"
	if dockerSupportsBuildx(ctx) {
		buildEngine = "buildx"
	}
	if err := checkBuildEngineAvailable(ctx, buildEngine); err != nil {
		return err
	}

	// Resolve the host shard directory (temporary by default) and make it writable by the container user.
	shardDir, cleanupShardDir, err := o.resolveShardDir(projectID)
	if err != nil {
		return err
	}
	defer cleanupShardDir()

	absShardDir, err := filepath.Abs(shardDir)
	if err != nil {
		return clierrors.Wrapf(err, "Failed to resolve absolute path for shard directory %s", shardDir)
	}
	// Bind mount the host shard directory into every server invocation.
	mounts := []string{fmt.Sprintf("%s:%s", filepath.ToSlash(absShardDir), containerShardDir)}

	// Print run information.
	log.Info().Msgf("Build server image:    %s", styles.RenderTechnical(map[bool]string{true: "yes", false: "skip"}[!o.flagSkipBuild]))
	log.Info().Msgf("Shard directory:       %s", styles.RenderTechnical(absShardDir))
	log.Info().Msgf("Populate bots:         %s", styles.RenderTechnical(fmt.Sprintf("%d bots for %s", o.flagBotCount, o.flagBotDuration)))
	log.Info().Msgf("Keep shard files:      %s", styles.RenderTechnical(map[bool]string{true: "yes", false: "no"}[o.flagKeepDB]))
	log.Info().Msgf("Timeout:               %s", styles.RenderTechnical(o.flagTimeout.String()))

	// Build the server image (not subject to --timeout, but still cancelable via Ctrl+C).
	if !o.flagSkipBuild {
		log.Info().Msg("")
		log.Info().Msg(styles.RenderBright("🔷 Build server image"))
		if err := o.buildServerImage(ctx, project, serverImage, integrationTestsConfig); err != nil {
			return fmt.Errorf("failed to build server image: %w", err)
		}
	} else {
		log.Info().Msg("")
		log.Info().Msg("Skipping container image build step due to --skip-build")
	}

	// Apply --timeout to the test phase (derived from cmd.Context() so Ctrl+C still cancels).
	runCtx, cancel := context.WithTimeout(ctx, o.flagTimeout)
	defer cancel()

	// Phase 1: populate a 4-shard on-disk database by running bots, then shut down gracefully.
	log.Info().Msg("")
	log.Info().Msg(styles.RenderBright("🔷 Populate database (4 shards)"))
	if err := o.populateDatabase(runCtx, project, serverImage, mounts, integrationTestsConfig); err != nil {
		return fmt.Errorf("failed to populate database: %w", err)
	}

	// Phase 2: validate the initial state and reshard through 4 -> 1 -> 2 -> 4.
	tables := testutil.DatabaseReshardingTables
	const numShards = 4 // shard files are always max(4, NumActiveShards) => 4 across this sequence
	var steps []testutil.ShardingStepState

	// Validate the initial populated state (4 active shards).
	if err := o.validateStep(absShardDir, tables, numShards, 4, &steps, "Validate initial state (4 active shards)"); err != nil {
		return err
	}

	// Reshard 4 -> 1 (generic path).
	if err := o.runReshardStep(runCtx, project, serverImage, mounts, integrationTestsConfig, 1, "Reshard 4 -> 1 (generic path)"); err != nil {
		return err
	}
	if err := o.validateStep(absShardDir, tables, numShards, 1, &steps, "Validate reshard to 1"); err != nil {
		return err
	}

	// Duplicate shard 0 -> shard 1, then reshard 1 -> 2 (fast-path).
	log.Info().Msg("")
	log.Info().Msg(styles.RenderBright("🔷 Duplicate shard 0 -> shard 1"))
	if err := copyFile(filepath.Join(absShardDir, "Shardy-0.db"), filepath.Join(absShardDir, "Shardy-1.db")); err != nil {
		return clierrors.Wrap(err, "Failed to duplicate database shard")
	}
	log.Info().Msgf("%s Duplicated Shardy-0.db -> Shardy-1.db", styles.RenderSuccess("✓"))
	if err := o.runReshardStep(runCtx, project, serverImage, mounts, integrationTestsConfig, 2, "Reshard 1 -> 2 (fast-path)"); err != nil {
		return err
	}
	if err := o.validateStep(absShardDir, tables, numShards, 2, &steps, "Validate reshard to 2 (fast-path)"); err != nil {
		return err
	}

	// Reshard 2 -> 4 (generic path).
	if err := o.runReshardStep(runCtx, project, serverImage, mounts, integrationTestsConfig, 4, "Reshard 2 -> 4 (generic path)"); err != nil {
		return err
	}
	if err := o.validateStep(absShardDir, tables, numShards, 4, &steps, "Validate final state (4 active shards)"); err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderSuccess("✅ Database resharding test completed successfully"))
	return nil
}

// resolveShardDir resolves the host directory to store SQLite shard files in. If --shard-dir is set, it
// is created if necessary; otherwise a temporary directory is created. The directory is made world-writable
// so the (non-root) container user can write the shard files into the bind mount on Linux. The returned
// cleanup function removes the directory unless --keep-db is set.
func (o *testDatabaseReshardingOpts) resolveShardDir(projectID string) (string, func(), error) {
	var shardDir string
	createdTemp := false
	if o.flagShardDir != "" {
		shardDir = o.flagShardDir
		if err := os.MkdirAll(shardDir, 0o777); err != nil {
			return "", func() {}, clierrors.Wrapf(err, "Failed to create shard directory %s", shardDir)
		}
	} else {
		dir, err := os.MkdirTemp("", fmt.Sprintf("metaplay-resharding-%s-", projectID))
		if err != nil {
			return "", func() {}, clierrors.Wrap(err, "Failed to create temporary shard directory")
		}
		shardDir = dir
		createdTemp = true
	}

	// Ensure the directory is writable by the container user (bind mounts on Linux preserve host perms).
	if err := os.Chmod(shardDir, 0o777); err != nil {
		log.Debug().Msgf("Failed to chmod shard directory %s: %v", shardDir, err)
	}

	cleanup := func() {
		if o.flagKeepDB {
			log.Info().Msg("")
			log.Info().Msgf("Keeping shard files in %s (due to --keep-db)", styles.RenderTechnical(shardDir))
			return
		}
		if !createdTemp && o.flagShardDir != "" {
			// User-provided directory: only remove its contents would be surprising; leave it in place.
			log.Debug().Msgf("Leaving user-provided shard directory %s in place", shardDir)
			return
		}
		if err := os.RemoveAll(shardDir); err != nil {
			log.Warn().Msgf("Failed to remove shard directory %s: %v", shardDir, err)
		}
	}

	return shardDir, cleanup, nil
}

// buildServerImage builds just the game server image (no Playwright images are needed for this test).
func (o *testDatabaseReshardingOpts) buildServerImage(ctx context.Context, project *metaproj.MetaplayProject, serverImage string, integrationTestsConfig *metaproj.IntegrationTestsConfig) error {
	buildEngine := "buildkit"
	if dockerSupportsBuildx(ctx) {
		buildEngine = "buildx"
	}

	var extraBuildArgs []string
	if integrationTestsConfig != nil && integrationTestsConfig.Docker != nil {
		extraBuildArgs = integrationTestsConfig.Docker.BuildArgs
	}

	params := buildDockerImageParams{
		project:     project,
		imageName:   serverImage,
		buildEngine: buildEngine,
		platforms:   []string{}, // Use architecture of host machine
		commitID:    "test",
		buildNumber: "test",
		extraArgs:   extraBuildArgs,
	}
	return buildDockerImage(ctx, params)
}

// populateDatabase starts a background server backed by on-disk SQLite (4 shards), runs bots against it
// to create entity rows, and then shuts the server down gracefully so the shard files are checkpointed
// and closed cleanly before they are read from the host.
func (o *testDatabaseReshardingOpts) populateDatabase(ctx context.Context, project *metaproj.MetaplayProject, serverImage string, mounts []string, integrationTestsConfig *metaproj.IntegrationTestsConfig) error {
	serverOpts := testutil.GameServerOptions{
		Image:                 serverImage,
		ContainerName:         fmt.Sprintf("%s-reshard-populate-server", project.Config.ProjectHumanID),
		Mounts:                mounts,
		SqlitePersistDir:      containerShardDir,
		NumActiveShards:       4,
		DisableSigtermWait:    true, // so the HTTP graceful shutdown lets the server self-exit
		DisableExitOnLogError: true, // on-disk SQLite under bot load can log transient timeouts; don't crash on them
	}
	if integrationTestsConfig != nil && integrationTestsConfig.Server != nil {
		serverOpts.ExtraArgs = integrationTestsConfig.Server.Args
		serverOpts.ExtraEnv = integrationTestsConfig.Server.Env
	}

	server := testutil.NewGameServer(serverOpts)

	log.Info().Msg("Starting background game server (on-disk SQLite, 4 shards)...")
	if err := server.Start(ctx); err != nil {
		return clierrors.Wrap(err, "Failed to start background server")
	}
	log.Info().Msgf("Background server started at %s", server.BaseURL().String())

	// Run bots to populate the database.
	log.Info().Msgf("Running %d bots for %s to populate the database...", o.flagBotCount, o.flagBotDuration)
	if err := o.runPopulateBots(ctx, project, server, serverImage, integrationTestsConfig); err != nil {
		_ = server.Shutdown(context.Background())
		return err
	}

	// Graceful shutdown so SQLite is checkpointed and the shard files are closed cleanly.
	log.Info().Msg("Shutting down background server gracefully...")
	if err := server.GracefulShutdown(context.Background()); err != nil {
		return clierrors.Wrap(err, "Failed to gracefully shut down background server")
	}

	log.Info().Msgf("%s Database populated", styles.RenderSuccess("✓"))
	return nil
}

// runPopulateBots runs the botclient against the already-running server, with a higher bot count and
// longer duration than the lighter integration 'bots' test so that rows land on all shards.
func (o *testDatabaseReshardingOpts) runPopulateBots(ctx context.Context, project *metaproj.MetaplayProject, server *testutil.BackgroundGameServer, imageName string, integrationTestsConfig *metaproj.IntegrationTestsConfig) error {
	botEnv := map[string]string{
		"METAPLAY_ENVIRONMENT_FAMILY": "Local",
	}
	if integrationTestsConfig != nil && integrationTestsConfig.BotClient != nil {
		maps.Copy(botEnv, integrationTestsConfig.BotClient.Env)
	}

	botCmd := []string{
		"botclient",
		"-LogLevel=Information",
		// METAPLAY_OPTS (shared with game server)
		"--Environment:EnableKeyboardInput=false",
		// Note: ExitOnLogError is intentionally NOT set (matching the legacy test): under heavy load the
		// bots can log transient connection errors that should not fail the populate phase.
		// Bot-specific configuration
		"--Bot:ServerHost=localhost",
		"--Bot:ServerPort=9339",
		"--Bot:EnableTls=false",
		"--Bot:CdnBaseUrl=http://localhost:5552/",
		"-ExitAfter=" + formatDotnetTimeSpan(o.flagBotDuration),
		fmt.Sprintf("-MaxBots=%d", o.flagBotCount),
		"-SpawnRate=2",
		"-ExpectedSessionDuration=00:00:10",
	}
	if integrationTestsConfig != nil && integrationTestsConfig.BotClient != nil {
		botCmd = append(botCmd, integrationTestsConfig.BotClient.Args...)
	}

	botClientOpts := testutil.RunOnceContainerOptions{
		Image:         imageName,
		ContainerName: fmt.Sprintf("%s-reshard-botclient", project.Config.ProjectHumanID),
		LogPrefix:     "[botclient] ",
		Network:       fmt.Sprintf("container:%s", server.ContainerName()),
		Env:           botEnv,
		Cmd:           botCmd,
	}

	exitCode, err := testutil.RunContainerToCompletion(ctx, botClientOpts)
	if err != nil {
		return clierrors.Wrap(err, "Botclient failed to run")
	}
	if exitCode != 0 {
		return clierrors.Newf("Botclient exited with non-zero code: %d", exitCode)
	}
	return nil
}

// runReshardStep runs the game server once with the given number of active shards. The server reshards
// the on-disk database at startup and then exits (via -ExitAfter=00:00:00).
func (o *testDatabaseReshardingOpts) runReshardStep(ctx context.Context, project *metaproj.MetaplayProject, serverImage string, mounts []string, integrationTestsConfig *metaproj.IntegrationTestsConfig, numActiveShards int, displayName string) error {
	log.Info().Msg("")
	log.Info().Msg(styles.RenderBright("🔷 " + displayName))

	serverCmd := []string{
		"gameserver",
		"-LogLevel=Information",
		"-ExitAfter=00:00:00", // reshard at startup, then exit immediately
		"-EnableMetrics=false",
		"--Environment:EnableKeyboardInput=false",
		"--Environment:WaitForSigtermBeforeExit=false", // must self-exit
		"--AdminApi:WebRootPath=wwwroot",
		"--Database:Backend=Sqlite",
		"--Database:SqliteInMemory=false",
		"--Database:SqliteDirectory=" + containerShardDir,
		fmt.Sprintf("--Database:NumActiveShards=%d", numActiveShards),
		"--Player:ForceFullDebugConfigForBots=false",
	}

	serverEnv := map[string]string{
		"ASPNETCORE_ENVIRONMENT":      "Development",
		"METAPLAY_ENVIRONMENT_FAMILY": "Local",
	}
	if integrationTestsConfig != nil && integrationTestsConfig.Server != nil {
		serverCmd = append(serverCmd, integrationTestsConfig.Server.Args...)
		maps.Copy(serverEnv, integrationTestsConfig.Server.Env)
	}

	opts := testutil.RunOnceContainerOptions{
		Image:         serverImage,
		ContainerName: fmt.Sprintf("%s-reshard-server", project.Config.ProjectHumanID),
		LogPrefix:     "[reshard] ",
		Env:           serverEnv,
		Cmd:           serverCmd,
		Mounts:        mounts,
	}

	exitCode, err := testutil.RunContainerToCompletion(ctx, opts)
	if err != nil {
		return clierrors.Wrapf(err, "Failed to run resharding step '%s'", displayName)
	}
	if exitCode != 0 {
		return clierrors.Newf("Resharding step '%s' exited with non-zero code: %d", displayName, exitCode)
	}

	log.Info().Msgf("%s %s", styles.RenderSuccess("✓"), displayName)
	return nil
}

// validateStep validates the current shard state against all prior steps and appends the new step state.
func (o *testDatabaseReshardingOpts) validateStep(shardDir string, tables []string, numShards, numActiveShards int, steps *[]testutil.ShardingStepState, displayName string) error {
	log.Info().Msg("")
	log.Info().Msg(styles.RenderBright("🔷 " + displayName))

	state, err := testutil.ValidateShardingStep(shardDir, tables, numShards, numActiveShards, *steps)
	if err != nil {
		return clierrors.Wrapf(err, "Database resharding validation failed (%s)", displayName)
	}
	*steps = append(*steps, state)

	log.Info().Msgf("%s %s", styles.RenderSuccess("✓"), displayName)
	return nil
}

// formatDotnetTimeSpan formats a duration as a .NET TimeSpan string "hh:mm:ss".
func formatDotnetTimeSpan(d time.Duration) string {
	totalSeconds := int(d.Seconds())
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}

// copyFile copies the contents of src to dst, creating or truncating dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
