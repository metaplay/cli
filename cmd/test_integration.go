/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type testIntegrationOpts struct{}

func init() {
	o := testIntegrationOpts{}

	cmd := &cobra.Command{
		Use:   "integration",
		Short: "Run integration test pipeline (scaffold)",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Run the integration test pipeline with multiple named phases.
			This is a scaffold: phase implementations are placeholders for now.

			Phases:
			- build-images: Build Docker images.
			- test-bots: Run the game server in the background and then bots against it.
			- test-dashboard: Run the dashboard Playwright tests.
			- test-system: Run the system tests.
			- test-http-api: HTTP API tests.
		`),
		Example: renderExample(`
			# Run the full integration test pipeline (scaffold)
			metaplay test integration
		`),
	}

	testCmd.AddCommand(cmd)
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

	// Phase execution order
	phases := []struct {
		name string
		fn   func() error
	}{
		{"build-images", func() error { return o.buildDockerImages(project) }},
		{"test-bots", func() error { return phasePlaceholder("test-bots") }},
		{"test-dashboard", func() error { return phasePlaceholder("test-dashboard") }},
		{"test-system", func() error { return phasePlaceholder("test-system") }},
		{"test-http-api", func() error { return phasePlaceholder("test-http-api") }},
	}

	for _, p := range phases {
		// log.Info().Msg(styles.RenderBright("🔷 " + p.name))
		if err := p.fn(); err != nil {
			return fmt.Errorf("phase '%s' failed: %w", p.name, err)
		}
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderSuccess("✅ Integration tests successfully completed"))
	return nil
}

// buildDockerImages builds the Docker images used by integration tests. This includes
// the server image, and additional testing images for Playwright.
func (o *testIntegrationOpts) buildDockerImages(project *metaproj.MetaplayProject) error {
	// Determine build engine
	// \todo allow specifying this with a flag?
	buildEngine := "buildkit"
	if dockerSupportsBuildx() {
		buildEngine = "buildx"
	}

	// Derive image tags (scoped by project ID)
	projectID := project.Config.ProjectHumanID
	serverImage := fmt.Sprintf("%s/server:test", strings.ToLower(projectID))
	pwTsImage := fmt.Sprintf("%s/playwright-ts:test", strings.ToLower(projectID))
	pwNetImage := fmt.Sprintf("%s/playwright-net:test", strings.ToLower(projectID))

	// Common build parameters
	commonParams := buildDockerImageParams{
		project:     project,
		buildEngine: buildEngine,
		platforms:   []string{"linux/amd64"}, // Default platform for integration tests
		commitID:    "test",                  // Test build
		buildNumber: "test",                  // Test build
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
