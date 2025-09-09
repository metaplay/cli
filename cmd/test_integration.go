/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/metaplay/cli/pkg/testutil"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type testIntegrationOpts struct {
	flagSkipBuild bool
}

func init() {
	o := testIntegrationOpts{}

	cmd := &cobra.Command{
		Use:   "integration",
		Short: "Run integration test pipeline",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Run the integration test pipeline with multiple named phases.
			This is a scaffold: phase implementations are placeholders for now.

			Phases:
			- build-images: Build Docker images.
			- test-bots: Run bots against the background server.
			- test-dashboard: Run the dashboard Playwright tests.
			- test-system: Run the system tests.
			- test-http-api: HTTP API tests.
		`),
		Example: renderExample(`
			# Run the full integration test pipeline
			metaplay test integration
		`),
	}

	testCmd.AddCommand(cmd)

	// Flags
	flags := cmd.Flags()
	flags.BoolVar(&o.flagSkipBuild, "skip-build", false, "Skip the 'build-images' phase")
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

	log.Info().Msgf("Docker version:      %s %s", styles.RenderTechnical(dockerVersionStr), dockerVersionBadge)
	log.Info().Msgf("Docker build engine: %s", styles.RenderTechnical(buildEngine))

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

	// Start the background game server before all test phases
	log.Info().Msg("")
	log.Info().Msg(styles.RenderBright("🔷 Start background game server"))

	server := testutil.NewGameServer(testutil.GameServerOptions{
		Image:         serverImage,
		ContainerName: fmt.Sprintf("%s-integration-server", project.Config.ProjectHumanID),
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

	// Execute test phases with the running server
	phases := []struct {
		name string
		fn   func() error
	}{
		{"test-bots", func() error { return o.testBots(project, server) }},
		{"test-dashboard", func() error { return phasePlaceholder("test-dashboard") }},
		{"test-system", func() error { return phasePlaceholder("test-system") }},
		{"test-http-api", func() error { return phasePlaceholder("test-http-api") }},
	}

	for _, p := range phases {
		log.Info().Msg("")
		log.Info().Msg(styles.RenderBright("🔷 " + p.name))

		if err := p.fn(); err != nil {
			return fmt.Errorf("phase '%s' failed: %w", p.name, err)
		}
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderSuccess("✅ Integration tests successfully completed"))
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

// testBots runs the botclient against the already-running server.
func (o *testIntegrationOpts) testBots(project *metaproj.MetaplayProject, server *testutil.BackgroundGameServer) error {
	ctx := context.Background()

	// Run the botclient against the server
	log.Info().Msg("Running botclient...")

	// Get the server image from the server container (we need to derive it from the project)
	serverImage := fmt.Sprintf("%s/server:test", project.Config.ProjectHumanID)

	botClientOpts := testutil.RunOnceContainerOptions{
		Image:         serverImage,
		ContainerName: fmt.Sprintf("%s-test-botclient", project.Config.ProjectHumanID),
		LogPrefix:     "[botclient] ",
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

	// Use network mode to connect to the server container
	botClientOpts.ExtraDockerArgs = []string{
		"--network", fmt.Sprintf("container:%s", server.ContainerName()),
	}

	botClient := testutil.NewRunOnce(botClientOpts)
	exitCode, err := botClient.Run(ctx)
	if err != nil {
		return fmt.Errorf("botclient failed to run: %w", err)
	}

	if exitCode != 0 {
		return fmt.Errorf("botclient exited with non-zero code: %d", exitCode)
	}

	log.Info().Msg("Botclient completed successfully")
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

// phasePlaceholder is a stub implementation for an integration test phase.
func phasePlaceholder(name string) error {
	log.Info().Msg(styles.RenderMuted("(placeholder) implementation pending for: " + name))
	return nil
}
