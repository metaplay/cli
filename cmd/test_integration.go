/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/metaplay/cli/pkg/testutil"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type testIntegrationOpts struct {
	flagSkipBuild    bool
	flagDebugNetwork bool
	flagOutputDir    string
	flagTest         string
}

func init() {
	o := testIntegrationOpts{}

	cmd := &cobra.Command{
		Use:     "integration",
		Aliases: []string{"i"},
		Short:   "[preview] Run integration tests",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			PREVIEW: This command is currently in preview and may change in the future. If you encounter
			problems or have feedback, please file an issue at https://github.com/metaplay/cli/issues/new.

			Run Metaplay integration tests for your project.

			The tests are run within containers. The game server and test container images are first built
			and then used to run the tests.

			For each of the tests, the game server container is first started in the background and then
			the test-specific container is run against the game server.

			Tests:
			- bots: Run bots against the background server.
			- dashboard: Run dashboard Playwright tests.
			- system: Run Playwright.NET system tests.
		`),
		Example: renderExample(`
			# Run the full integration test pipeline
			metaplay test integration

			# Run the tests without building the images. Speeds up the run if you already built the images.
			metaplay test integration --skip-build

			# Run only the 'bots' test.
			metaplay test integration --test=bots
		`),
	}

	testCmd.AddCommand(cmd)

	// Flags
	flags := cmd.Flags()
	flags.BoolVar(&o.flagSkipBuild, "skip-build", false, "Skip the docker image build step (faster if you already built the images)")
	flags.BoolVar(&o.flagDebugNetwork, "debug-network", false, "[internal] Run network connectivity tests for debugging (for debugging the CLI itself)")
	flags.StringVar(&o.flagOutputDir, "output-dir", "./integration-test-output", "Directory for test output and results")
	flags.StringVar(&o.flagTest, "test", "", "Run only the specified test (e.g. 'bots', 'dashboard', 'system')")
	_ = flags.MarkDeprecated("only", "use --tests instead")
}

func (o *testIntegrationOpts) Prepare(cmd *cobra.Command, args []string) error { return nil }

func (o *testIntegrationOpts) Run(cmd *cobra.Command) error {
	// Resolve project configuration
	project, err := resolveProject()
	if err != nil {
		return fmt.Errorf("failed to resolve project: %w", err)
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Run Integration Tests"))
	log.Info().Msg("")

	// Ensure Docker is available (binary + daemon)
	if err := checkDockerAvailable(); err != nil {
		return err
	}

	// Check Docker version: warn if using old versions
	dockerVersionInfo, dockerUpgradeRecommended, err := checkDockerVersion()
	if err != nil {
		log.Warn().Msgf("Warning: Failed to check Docker version: %v", err)
	}

	// Resolve docker build engine for integration tests
	buildEngine := "buildkit"
	if dockerSupportsBuildx() {
		buildEngine = "buildx"
	}

	// Check that the build engine is available
	err = checkBuildEngineAvailable(buildEngine)
	if err != nil {
		return err
	}

	// Log Docker information
	dockerVersionBadge := ""
	dockerVersionStr := "unknown"
	if dockerVersionInfo == nil {
		dockerVersionBadge = styles.RenderWarning("[unable to check version]")
	} else {
		dockerVersionStr = dockerVersionInfo.Server.Version
		if dockerUpgradeRecommended {
			dockerVersionBadge = styles.RenderWarning("[version is old; upgrade recommended]")
		}
	}

	// Print information about test run.
	log.Info().Msgf("Docker version:        %s %s", styles.RenderTechnical(dockerVersionStr), dockerVersionBadge)
	log.Info().Msgf("Docker build engine:   %s", styles.RenderTechnical(buildEngine))
	log.Info().Msgf("Build images:          %v", styles.RenderTechnical(fmt.Sprintf("%v", !o.flagSkipBuild)))
	log.Info().Msgf("Test output directory: %s", styles.RenderTechnical(o.flagOutputDir))

	// Create output directory for test results
	if err := os.MkdirAll(o.flagOutputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", o.flagOutputDir, err)
	}

	// Derive container image names once and pass them to phases
	projectID := strings.ToLower(project.Config.ProjectHumanID)
	serverImage := fmt.Sprintf("%s/server:test", projectID)
	pwTsImage := fmt.Sprintf("%s/playwright-ts:test", projectID)
	pwNetImage := fmt.Sprintf("%s/playwright-net:test", projectID)

	// Build the container images first.
	if !o.flagSkipBuild {
		if err := o.buildDockerImages(project, serverImage, pwTsImage, pwNetImage); err != nil {
			return fmt.Errorf("failed to build container images: %w", err)
		}
	} else {
		log.Info().Msg("")
		log.Info().Msg("Skipping container image build step due to --skip-build")
	}

	// runTestCase handles per-test server lifecycle and logs
	tests := []struct {
		name        string
		displayName string
		fn          func(*testutil.BackgroundGameServer) error
	}{
		{"bots", "Run botclient tests", func(server *testutil.BackgroundGameServer) error {
			return o.runBotTests(project, server, serverImage)
		}},
		{"dashboard", "Run dashboard Playwright tests", func(server *testutil.BackgroundGameServer) error {
			return o.runDashboardTests(project, server, pwTsImage)
		}},
		{"system", "Run Playwright.NET system tests", func(server *testutil.BackgroundGameServer) error {
			return o.runSystemTests(project, server, pwNetImage)
		}},
		// {"http-api", "Run HTTP API tests", func(s *testutil.BackgroundGameServer) error { return nil }}, // \todo migrate from Python script
	}

	// Filter tests if --tests flag is specified
	if o.flagTest != "" {
		var filteredTests []struct {
			name        string
			displayName string
			fn          func(*testutil.BackgroundGameServer) error
		}
		for _, p := range tests {
			if p.name == o.flagTest {
				filteredTests = append(filteredTests, p)
				break
			}
		}
		if len(filteredTests) == 0 {
			return fmt.Errorf("unknown test '%s'. Available tests: bots, dashboard, system", o.flagTest)
		}
		tests = filteredTests
	}

	// Run all the active tests.
	for _, p := range tests {
		log.Info().Msg("")
		log.Info().Msg(styles.RenderBright("🔷 " + p.displayName))
		log.Info().Msg("")

		if err := o.runTestCase(project, serverImage, p.displayName, p.fn); err != nil {
			return fmt.Errorf("test '%s' failed: %w", p.displayName, err)
		}

		log.Info().Msg("")
		log.Info().Msgf("%s %s", styles.RenderSuccess("✓"), "Test completed successfully")
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderSuccess("✅ Integration tests successfully completed"))
	return nil
}

// runTestCase starts a background game server, runs the provided test function, and then stops the server.
func (o *testIntegrationOpts) runTestCase(project *metaproj.MetaplayProject, serverImage, displayName string, fn func(*testutil.BackgroundGameServer) error) error {
	// Create and start the background server for this test
	server := testutil.NewGameServer(testutil.GameServerOptions{
		Image:         serverImage,
		ContainerName: fmt.Sprintf("%s-test-server", project.Config.ProjectHumanID),
	})
	ctx := context.Background()

	log.Info().Msg("Starting background game server...")
	if err := server.Start(ctx); err != nil {
		return fmt.Errorf("failed to start background server: %w", err)
	}
	defer func() {
		log.Info().Msg("Shutting down background server...")
		if shutdownErr := server.Shutdown(context.Background()); shutdownErr != nil {
			log.Error().Msgf("Failed to shutdown background server: %v", shutdownErr)
		}
	}()

	log.Info().Msgf("Background server started at %s", server.BaseURL().String())

	// Optional: run network debug checks
	if o.flagDebugNetwork {
		if err := o.debugNetworkConnectivity(ctx, project, server, serverImage); err != nil {
			return fmt.Errorf("network connectivity test failed: %w", err)
		}
	}

	// Execute the test function
	if err := fn(server); err != nil {
		return err
	}

	return nil
}

// startServer starts the game server container, waits for it to be ready,
// lets it run for 10 seconds, then gracefully terminates it.
func (o *testIntegrationOpts) startServer(project *metaproj.MetaplayProject, serverImage string) error {
	log.Info().Msg("")
	log.Info().Msg(styles.RenderBright("🔷 Start game server"))

	// Create and start the server
	server := testutil.NewGameServer(testutil.GameServerOptions{
		Image:         serverImage,
		ContainerName: fmt.Sprintf("%s-test-server", project.Config.ProjectHumanID),
	})
	ctx := context.Background()

	log.Info().Msg("Starting server container...")
	if err := server.Start(ctx); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	log.Info().Msgf("Server started successfully at %s", server.BaseURL().String())
	log.Info().Msg("Letting server run for 10 seconds...")

	// Let the server run for 10 seconds
	time.Sleep(10 * time.Second)

	// Gracefully terminate the server
	log.Info().Msg("Gracefully shutting down server...")
	if err := server.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	log.Info().Msg("Server shutdown completed")
	return nil
}

// runBotTests runs the botclient against the already-running server.
func (o *testIntegrationOpts) runBotTests(project *metaproj.MetaplayProject, server *testutil.BackgroundGameServer, imageName string) error {
	ctx := context.Background()

	botClientOpts := testutil.RunOnceContainerOptions{
		Image:         imageName,
		ContainerName: fmt.Sprintf("%s-test-botclient", project.Config.ProjectHumanID),
		LogPrefix:     "[botclient] ",
		Network:       fmt.Sprintf("container:%s", server.ContainerName()),
		Env: map[string]string{
			"METAPLAY_ENVIRONMENT_FAMILY": "Local",
		},
		Cmd: []string{
			"botclient",
			"-LogLevel=Information",
			// METAPLAY_OPTS (shared with game server)
			"--Environment:EnableKeyboardInput=false",
			"--Environment:ExitOnLogError=true",
			// Bot-specific configuration
			"--Bot:ServerHost=localhost",
			"--Bot:ServerPort=9339",
			"--Bot:EnableTls=false",
			"--Bot:CdnBaseUrl=http://localhost:5552/",
			"-ExitAfter=00:00:30",               // Run for 30 seconds (.NET TimeSpan format)
			"-MaxBots=10",                       // Spawn up to 10 bots
			"-SpawnRate=2",                      // Spawn 2 bots per second
			"-ExpectedSessionDuration=00:00:10", // Each bot session lasts ~10 seconds (.NET TimeSpan format)
		},
	}

	botClient := testutil.NewRunOnceContainer(botClientOpts)
	exitCode, err := botClient.Run(ctx)
	if err != nil {
		return fmt.Errorf("botclient failed to run: %w", err)
	}

	if exitCode != 0 {
		return fmt.Errorf("botclient exited with non-zero code: %d", exitCode)
	}

	return nil
}

// runDashboardTests runs the Playwright TypeScript tests against the dashboard.
func (o *testIntegrationOpts) runDashboardTests(project *metaproj.MetaplayProject, server *testutil.BackgroundGameServer, imageName string) error {
	ctx := context.Background()

	// Create output directory for dashboard test results.
	resultsDir := filepath.ToSlash(filepath.Join(o.flagOutputDir, "dashboard"))
	if err := os.MkdirAll(resultsDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", resultsDir, err)
	}

	// Convert to absolute path for Docker volume mount
	absResultsDir, err := filepath.Abs(resultsDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for %s: %w", resultsDir, err)
	}
	// Convert to forward slashes for Docker compatibility
	absResultsDir = filepath.ToSlash(absResultsDir)

	playwrightOpts := testutil.RunOnceContainerOptions{
		Image:         imageName,
		ContainerName: fmt.Sprintf("%s-test-playwright-ts", project.Config.ProjectHumanID),
		LogPrefix:     "[playwright-ts] ",
		Network:       fmt.Sprintf("container:%s", server.ContainerName()),
		Env: map[string]string{
			"DASHBOARD_BASE_URL": "http://localhost:5550",
			"CI":                 "true",
			"OUTPUT_DIRECTORY":   "/PlaywrightOutput",
		},
		Mounts: []string{
			fmt.Sprintf("%s:/PlaywrightOutput", absResultsDir),
		},
	}

	// Run the Playwright tests container.
	playwright := testutil.NewRunOnceContainer(playwrightOpts)
	exitCode, err := playwright.Run(ctx)
	if err != nil {
		return fmt.Errorf("playwright tests failed to run: %w", err)
	}

	if exitCode != 0 {
		return fmt.Errorf("playwright tests failed with exit code: %d", exitCode)
	}

	return nil
}

// runSystemTests runs the Playwright .NET tests for system testing.
func (o *testIntegrationOpts) runSystemTests(project *metaproj.MetaplayProject, server *testutil.BackgroundGameServer, imageName string) error {
	ctx := context.Background()

	// Create output directory for system test results.
	resultsDir := filepath.ToSlash(filepath.Join(o.flagOutputDir, "system"))
	if err := os.MkdirAll(resultsDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", resultsDir, err)
	}

	// Convert to absolute path for Docker volume mount
	absResultsDir, err := filepath.Abs(resultsDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for %s: %w", resultsDir, err)
	}
	// Convert to forward slashes for Docker compatibility
	absResultsDir = filepath.ToSlash(absResultsDir)

	playwrightOpts := testutil.RunOnceContainerOptions{
		Image:         imageName,
		ContainerName: fmt.Sprintf("%s-test-playwright-net", project.Config.ProjectHumanID),
		LogPrefix:     "[playwright-net] ",
		Network:       fmt.Sprintf("container:%s", server.ContainerName()),
		Env: map[string]string{
			"DASHBOARD_BASE_URL": "http://localhost:5550",
			"OUTPUT_DIRECTORY":   "/PlaywrightOutput",
		},
		Mounts: []string{
			fmt.Sprintf("%s:/PlaywrightOutput", absResultsDir),
		},
	}

	// Run the Playwright .NET tests container.
	playwright := testutil.NewRunOnceContainer(playwrightOpts)
	exitCode, err := playwright.Run(ctx)
	if err != nil {
		return fmt.Errorf("playwright system tests failed to run: %w", err)
	}

	if exitCode != 0 {
		return fmt.Errorf("playwright system tests failed with exit code: %d", exitCode)
	}

	return nil
}

// debugNetworkConnectivity runs network tests to help diagnose connectivity issues to the game server container.
// These tests are run in containers to simulate the same networking as the other test containers will use.
func (o *testIntegrationOpts) debugNetworkConnectivity(ctx context.Context, project *metaproj.MetaplayProject, server *testutil.BackgroundGameServer, serverImage string) error {
	// Test 1: Check if we can resolve localhost from within the botclient container network
	log.Info().Msg("Test 1: DNS resolution test")
	dnsTestOpts := testutil.RunOnceContainerOptions{
		Image:         serverImage,
		ContainerName: fmt.Sprintf("%s-test-dns", project.Config.ProjectHumanID),
		LogPrefix:     "[dns-test] ",
		Network:       fmt.Sprintf("container:%s", server.ContainerName()),
		Cmd:           []string{"nslookup", "localhost"},
	}
	dnsTest := testutil.NewRunOnceContainer(dnsTestOpts)
	if exitCode, err := dnsTest.Run(ctx); err != nil {
		log.Warn().Msgf("DNS test failed to run: %v", err)
	} else if exitCode != 0 {
		log.Warn().Msgf("DNS test failed with exit code: %d", exitCode)
	} else {
		log.Info().Msg("DNS test passed")
	}

	// Test 2: Check if port 9339 is listening
	log.Info().Msg("Test 2: Port connectivity test")
	portTestOpts := testutil.RunOnceContainerOptions{
		Image:         serverImage,
		ContainerName: fmt.Sprintf("%s-test-port", project.Config.ProjectHumanID),
		LogPrefix:     "[port-test] ",
		Network:       fmt.Sprintf("container:%s", server.ContainerName()),
		Cmd:           []string{"netstat", "-tuln"},
	}
	portTest := testutil.NewRunOnceContainer(portTestOpts)
	if exitCode, err := portTest.Run(ctx); err != nil {
		log.Warn().Msgf("Port test failed to run: %v", err)
	} else if exitCode != 0 {
		log.Warn().Msgf("Port test failed with exit code: %d", exitCode)
	} else {
		log.Info().Msg("Port test completed - check logs for port 9339")
	}

	// Test 3: Try to connect to the game server port directly
	log.Info().Msg("Test 3: Direct connection test")
	connectTestOpts := testutil.RunOnceContainerOptions{
		Image:         serverImage,
		ContainerName: fmt.Sprintf("%s-test-connect", project.Config.ProjectHumanID),
		LogPrefix:     "[connect-test] ",
		Network:       fmt.Sprintf("container:%s", server.ContainerName()),
		Cmd:           []string{"timeout", "5", "telnet", "localhost", "9339"},
	}
	connectTest := testutil.NewRunOnceContainer(connectTestOpts)
	if exitCode, err := connectTest.Run(ctx); err != nil {
		log.Warn().Msgf("Connection test failed to run: %v", err)
	} else {
		log.Info().Msgf("Connection test completed with exit code: %d", exitCode)
	}

	// Test 4: Check what processes are running in the server container
	log.Info().Msg("Test 4: Process list test")
	psTestOpts := testutil.RunOnceContainerOptions{
		Image:         serverImage,
		ContainerName: fmt.Sprintf("%s-test-ps", project.Config.ProjectHumanID),
		LogPrefix:     "[ps-test] ",
		Network:       fmt.Sprintf("container:%s", server.ContainerName()),
		Cmd:           []string{"ps", "aux"},
	}
	psTest := testutil.NewRunOnceContainer(psTestOpts)
	if exitCode, err := psTest.Run(ctx); err != nil {
		log.Warn().Msgf("Process test failed to run: %v", err)
	} else if exitCode != 0 {
		log.Warn().Msgf("Process test failed with exit code: %d", exitCode)
	} else {
		log.Info().Msg("Process test completed - check logs for gameserver process")
	}

	return nil
}

// buildDockerImages builds the Docker images used by integration tests. This includes
// the server image, and additional testing images for Playwright.
func (o *testIntegrationOpts) buildDockerImages(project *metaproj.MetaplayProject, serverImage, pwTsImage, pwNetImage string) error {
	// Determine build engine
	// \todo allow specifying this with a flag?
	buildEngine := "buildkit"
	if dockerSupportsBuildx() {
		buildEngine = "buildx"
	}

	// Common build parameters
	commonParams := buildDockerImageParams{
		project:     project,
		buildEngine: buildEngine,
		platforms:   []string{"linux/amd64"},
		commitID:    "test",
		buildNumber: "test",
		extraArgs:   []string{},
	}

	// Build server image
	log.Info().Msg("")
	log.Info().Msg(styles.RenderBright("🔷 Build server image"))
	serverParams := commonParams
	serverParams.imageName = serverImage
	if err := buildDockerImage(serverParams); err != nil {
		return fmt.Errorf("failed to build server image: %w", err)
	}

	// Build Playwright TS core test runner image (target playwright-ts-tests)
	log.Info().Msg("")
	log.Info().Msg(styles.RenderBright("🔷 Build Playwright (TypeScript) test image"))
	pwTsParams := commonParams
	pwTsParams.imageName = pwTsImage
	pwTsParams.target = "playwright-ts-tests"
	if err := buildDockerImage(pwTsParams); err != nil {
		return fmt.Errorf("failed to build playwright-ts image: %w", err)
	}

	// Build Playwright.NET test runner image (target playwright-net-tests)
	log.Info().Msg("")
	log.Info().Msg(styles.RenderBright("🔷 Build Playwright.NET test image"))
	pwNetParams := commonParams
	pwNetParams.imageName = pwNetImage
	pwNetParams.target = "playwright-net-tests"
	if err := buildDockerImage(pwNetParams); err != nil {
		return fmt.Errorf("failed to build playwright-net image: %w", err)
	}

	return nil
}

// dockerSupportsBuildx returns true if docker buildx is available.
func dockerSupportsBuildx() bool {
	// Try `docker buildx version`
	cmd := exec.Command("docker", "buildx", "version")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}
