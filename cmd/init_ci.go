/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// CIProvider represents a supported CI/CD provider.
type CIProvider string

const (
	CIProviderGitHubActions CIProvider = "github"
	CIProviderBitbucket     CIProvider = "bitbucket"
	CIProviderGeneric       CIProvider = "generic"
)

// ciProviderInfo contains display information for a CI provider.
type ciProviderInfo struct {
	ID          CIProvider
	Name        string
	Description string
}

var ciProviders = []ciProviderInfo{
	{CIProviderGitHubActions, "GitHub Actions", "Deploy using Metaplay's reusable workflows (recommended)"},
	{CIProviderBitbucket, "Bitbucket Pipelines", "Deploy using Bitbucket's native CI/CD"},
	{CIProviderGeneric, "Generic CI / Manual", "Deploy using any CI system or manually"},
}

type initCIOpts struct {
	flagCIProvider  string // CI provider to use (github, bitbucket, generic)
	flagEnvironment string // Target environment human ID
	flagAutoConfirm bool   // Automatically confirm changes
	flagOutputDir   string // Output directory for CI files (defaults to project root)

	projectDir  string                             // Resolved project directory
	project     *metaproj.MetaplayProject          // Loaded project
	environment *metaproj.ProjectEnvironmentConfig // Target environment
	ciProvider  CIProvider                         // Selected CI provider
}

func init() {
	o := initCIOpts{}

	cmd := &cobra.Command{
		Use:   "ci [flags]",
		Short: "Initialize CI/CD configuration for the project",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Initialize CI/CD pipeline configuration for deploying game servers to Metaplay cloud.

			This command generates CI/CD configuration files for your chosen provider:
			- GitHub Actions: Creates workflow files using Metaplay's reusable workflows
			- Bitbucket Pipelines: Creates pipeline configuration for Bitbucket
			- Generic CI: Creates shell scripts for use with any CI system

			The generated files include all necessary steps to build and deploy your game server
			to the selected environment(s).

			Prerequisites:
			- A Metaplay project with metaplay-project.yaml
			- At least one environment configured in the project
			- A machine user created in the Metaplay portal for CI authentication
		`),
		Example: renderExample(`
			# Interactive setup - choose CI provider and environment
			metaplay init ci

			# Initialize GitHub Actions for a specific environment
			metaplay init ci --ci-provider=github --environment=nimbly

			# Non-interactive mode with auto-confirmation
			metaplay init ci --ci-provider=github --environment=nimbly --yes

			# Initialize for all configured environments
			metaplay init ci --ci-provider=github --environment=all --yes
		`),
	}

	// Register flags.
	flags := cmd.Flags()
	flags.StringVar(&o.flagCIProvider, "ci-provider", "", "CI provider to use: github, bitbucket, or generic")
	flags.StringVarP(&o.flagEnvironment, "environment", "e", "", "Target environment human ID (or 'all' for all environments)")
	flags.BoolVarP(&o.flagAutoConfirm, "yes", "y", false, "Automatically confirm file creation")
	flags.StringVar(&o.flagOutputDir, "output-dir", "", "Output directory for CI files (defaults to project root)")

	initCmd.AddCommand(cmd)
}

func (o *initCIOpts) Prepare(cmd *cobra.Command, args []string) error {
	// Find and load the project
	var err error
	o.projectDir, err = findProjectDirectory()
	if err != nil {
		return err
	}

	o.project, err = loadProject(o.projectDir)
	if err != nil {
		return err
	}

	// Validate CI provider if specified
	if o.flagCIProvider != "" {
		if !isValidCIProvider(o.flagCIProvider) {
			return clierrors.NewUsageErrorf("Invalid CI provider '%s'", o.flagCIProvider).
				WithDetails("Valid options are: github, bitbucket, generic")
		}
		o.ciProvider = CIProvider(o.flagCIProvider)
	}

	// Check for environments before validating a specific one
	if len(o.project.Config.Environments) == 0 {
		return clierrors.NewUsageError("No environments found in metaplay-project.yaml").
			WithSuggestion("Update the local file with 'metaplay update project-environments' or create a new environment via https://portal.metaplay.dev")
	}

	// Validate environment if specified
	if o.flagEnvironment != "" && o.flagEnvironment != "all" {
		o.environment, err = o.project.Config.FindEnvironmentConfig(o.flagEnvironment)
		if err != nil {
			return err
		}
	}

	// Validate output directory if specified
	if o.flagOutputDir != "" {
		info, err := os.Stat(o.flagOutputDir)
		if err != nil {
			return clierrors.NewUsageErrorf("Output directory '%s' does not exist", o.flagOutputDir)
		}
		if !info.IsDir() {
			return clierrors.NewUsageErrorf("Output path '%s' is not a directory", o.flagOutputDir)
		}
	}

	// Must be either in interactive mode or specify --yes with required flags
	if !tui.IsInteractiveMode() {
		if !o.flagAutoConfirm {
			return clierrors.NewUsageError("Use --yes to automatically confirm changes when running in non-interactive mode")
		}
		if o.flagCIProvider == "" {
			return clierrors.NewUsageError("--ci-provider is required in non-interactive mode")
		}
		if o.flagEnvironment == "" {
			return clierrors.NewUsageError("--environment is required in non-interactive mode")
		}
	}

	return nil
}

func (o *initCIOpts) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	// Select CI provider if not specified
	if o.ciProvider == "" {
		provider, err := tui.ChooseFromListDialog(
			"Select CI Provider",
			ciProviders,
			func(p *ciProviderInfo) (string, string) {
				return p.Name, p.Description
			},
		)
		if err != nil {
			return err
		}
		o.ciProvider = provider.ID
		log.Info().Msgf(" %s %s", styles.RenderSuccess("✓"), provider.Name)
	}

	// Select environments to configure
	var environments []metaproj.ProjectEnvironmentConfig
	if o.flagEnvironment == "all" {
		environments = o.project.Config.Environments
	} else if o.environment != nil {
		environments = []metaproj.ProjectEnvironmentConfig{*o.environment}
	} else {
		// Interactive multi-select
		selected, err := tui.ChooseMultipleFromListDialog(
			"Select Target Environments",
			o.project.Config.Environments,
			func(env *metaproj.ProjectEnvironmentConfig) (string, string) {
				return env.Name, fmt.Sprintf("[%s]", env.HumanID)
			},
		)
		if err != nil {
			return err
		}
		environments = selected
		for _, env := range environments {
			log.Info().Msgf(" %s %s %s", styles.RenderSuccess("✓"), env.Name, styles.RenderMuted(fmt.Sprintf("[%s]", env.HumanID)))
		}
	}

	// Determine output directory
	outputDir := o.projectDir
	if o.flagOutputDir != "" {
		outputDir = o.flagOutputDir
	}

	// Generate CI files. Bitbucket uses a single file for all environments,
	// so it is handled separately from the per-environment providers.
	anyCreated := false
	if o.ciProvider == CIProviderBitbucket {
		written, err := o.generateBitbucketFile(ctx, outputDir, environments)
		if err != nil {
			return err
		}
		anyCreated = anyCreated || written
	} else {
		for _, env := range environments {
			written, err := o.generateCIFile(ctx, outputDir, env)
			if err != nil {
				return err
			}
			anyCreated = anyCreated || written
		}
	}

	if anyCreated {
		log.Info().Msg("")
		log.Info().Msg(styles.RenderSuccess("CI configuration initialized successfully!"))
		log.Info().Msg("")
		log.Info().Msg("Next steps:")
		log.Info().Msg("  1. Create a machine user in the Metaplay portal (if not created yet)")
		log.Info().Msg("  2. Store the machine user credentials in your CI system's secrets with name `METAPLAY_CREDENTIALS`")
		log.Info().Msg("  3. Add the machine user to your project and environment with the 'game-admin' role")
		log.Info().Msg("  4. Review, commit and push the generated CI configuration files")
	}

	return nil
}

// generateCIFile generates a CI configuration file for a single environment (GitHub Actions or Generic).
// Returns true if a file was written.
func (o *initCIOpts) generateCIFile(ctx context.Context, outputDir string, env metaproj.ProjectEnvironmentConfig) (bool, error) {
	data := ciTemplateData{
		EnvironmentDisplayName: env.Name,
		EnvironmentHumanID:     env.HumanID,
	}

	var filePath string
	var content string
	var err error

	switch o.ciProvider {
	case CIProviderGitHubActions:
		filePath = filepath.Join(outputDir, ".github", "workflows", fmt.Sprintf("build-deploy-server-%s.yaml", env.HumanID))
		content, err = renderTemplate(githubActionsTmpl, data)
	case CIProviderGeneric:
		filePath = filepath.Join(outputDir, fmt.Sprintf("deploy-%s.sh", env.HumanID))
		content, err = renderTemplate(genericCITmpl, data)
	default:
		return false, clierrors.Newf("Unknown CI provider: %s", o.ciProvider)
	}

	if err != nil {
		return false, clierrors.Wrap(err, "Failed to render CI template")
	}

	// Use executable permissions for shell scripts
	perm := os.FileMode(0644)
	if o.ciProvider == CIProviderGeneric {
		perm = 0755
	}

	// Show what will be created
	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle(fmt.Sprintf("CI Configuration for %s", env.Name)))
	log.Info().Msg("")
	log.Info().Msgf("Environment:  %s %s", styles.RenderTechnical(env.Name), styles.RenderMuted(fmt.Sprintf("[%s]", env.HumanID)))
	log.Info().Msgf("CI Provider:  %s", styles.RenderTechnical(string(o.ciProvider)))
	log.Info().Msgf("Output file:  %s", styles.RenderTechnical(filePath))
	log.Info().Msg("")

	written, err := o.confirmAndWriteFile(ctx, filePath, content, perm)
	if err != nil {
		return false, err
	}
	return written, nil
}

// generateBitbucketFile generates a single Bitbucket Pipelines file containing all environments.
// Returns true if a file was written.
func (o *initCIOpts) generateBitbucketFile(ctx context.Context, outputDir string, environments []metaproj.ProjectEnvironmentConfig) (bool, error) {
	var envData []bitbucketEnvironmentData
	for _, env := range environments {
		envData = append(envData, bitbucketEnvironmentData{
			DisplayName: env.Name,
			HumanID:     env.HumanID,
		})
	}

	data := bitbucketTemplateData{
		Environments: envData,
	}

	content, err := renderTemplate(bitbucketPipelinesTmpl, data)
	if err != nil {
		return false, clierrors.Wrap(err, "Failed to render Bitbucket Pipelines template")
	}

	filePath := filepath.Join(outputDir, "bitbucket-pipelines.yml")

	// Show what will be created
	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("CI Configuration for Bitbucket Pipelines"))
	log.Info().Msg("")
	for _, env := range environments {
		log.Info().Msgf("Environment:  %s %s", styles.RenderTechnical(env.Name), styles.RenderMuted(fmt.Sprintf("[%s]", env.HumanID)))
	}
	log.Info().Msgf("Output file:  %s", styles.RenderTechnical(filePath))
	log.Info().Msg("")

	return o.confirmAndWriteFile(ctx, filePath, content, 0644)
}

// confirmAndWriteFile prompts the user for confirmation (unless --yes) and writes the file.
// Returns true if the file was written, false if skipped.
func (o *initCIOpts) confirmAndWriteFile(ctx context.Context, filePath string, content string, perm os.FileMode) (bool, error) {
	// Check if file already exists
	if _, err := os.Stat(filePath); err == nil {
		log.Info().Msgf("%s File already exists: %s", styles.RenderAttention("⚠"), filePath)
		if !o.flagAutoConfirm {
			confirmed, err := tui.DoConfirmQuestion(ctx, "Overwrite existing file?")
			if err != nil {
				return false, err
			}
			if !confirmed {
				log.Info().Msg("Skipping file...")
				return false, nil
			}
		}
	} else if !o.flagAutoConfirm {
		confirmed, err := tui.DoConfirmQuestion(ctx, "Create this file?")
		if err != nil {
			return false, err
		}
		if !confirmed {
			log.Info().Msg("Skipping file...")
			return false, nil
		}
	}

	// Create directory if needed
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, clierrors.Wrap(err, fmt.Sprintf("Failed to create directory %s", dir)).
			WithSuggestion("Check that you have write permissions to the output directory")
	}

	// Write the file
	if err := os.WriteFile(filePath, []byte(content), perm); err != nil {
		return false, clierrors.Wrap(err, fmt.Sprintf("Failed to write file %s", filePath)).
			WithSuggestion("Check that you have write permissions to the output directory")
	}

	log.Info().Msgf(" %s Created %s", styles.RenderSuccess("✓"), styles.RenderTechnical(filePath))

	return true, nil
}

func isValidCIProvider(provider string) bool {
	switch CIProvider(provider) {
	case CIProviderGitHubActions, CIProviderBitbucket, CIProviderGeneric:
		return true
	default:
		return false
	}
}

// ciTemplateData contains the data passed to GitHub Actions and Generic CI templates.
type ciTemplateData struct {
	EnvironmentDisplayName string
	EnvironmentHumanID     string
}

// bitbucketEnvironmentData contains data for a single environment in the Bitbucket template.
type bitbucketEnvironmentData struct {
	DisplayName string
	HumanID     string
}

// bitbucketTemplateData contains the data passed to the Bitbucket Pipelines template.
type bitbucketTemplateData struct {
	Environments []bitbucketEnvironmentData
}

// Parsed CI templates (parsed once at package init).
// The GitHub Actions template uses [[.Field]] delimiters to avoid conflicts with GitHub's ${{ }} syntax.
var (
	githubActionsTmpl        = template.Must(template.New("github").Delims("[[", "]]").Parse(githubActionsTemplate))
	bitbucketPipelinesTmpl   = template.Must(template.New("bitbucket").Parse(bitbucketPipelinesTemplate))
	genericCITmpl            = template.Must(template.New("generic").Parse(genericCITemplate))
)

func renderTemplate(tmpl *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// GitHub Actions template
const githubActionsTemplate = `# Rename this action to what you want, this is what shows in the left sidebar in Github Actions
name: Build game server and deploy to [[.EnvironmentDisplayName]] ([[.EnvironmentHumanID]])

# Configure when this Github Action is triggered
on:
  # Enable manual triggering
  workflow_dispatch:

  # Trigger on all commits to branch 'main'
  # TODO: Replace this with your own desired trigger (see https://docs.github.com/en/actions/using-workflows/triggering-a-workflow)
  push:
    branches: main

jobs:
  # Build the server and deploy into the cloud
  build-and-deploy-server:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout repo
        uses: actions/checkout@v6

      - name: Setup Metaplay CLI
        uses: metaplay-shared/github-workflows/setup-cli@v0
        with:
          credentials: ${{ secrets.METAPLAY_CREDENTIALS }}

      - name: Build server image
        run: metaplay build image gameserver:$GITHUB_SHA

      - name: Deploy server to target environment
        run: metaplay deploy server [[.EnvironmentHumanID]] gameserver:$GITHUB_SHA
`

// Bitbucket Pipelines template
const bitbucketPipelinesTemplate = `image: atlassian/default-image:5

clone:
  # Ensure we get the images from the repository
  lfs: true
  depth: 5

definitions:
  services:
    # Give docker some extra memory to cope with the builds
    # Be aware: if Docker runs out of memory in Bitbucket Pipelines, the CI job just hangs!
    docker-6gb:
      type: docker
      memory: 6144

pipelines:
  # TODO: You should customize this to fit your branching strategy, now needs to be triggered manually
  #       See: https://support.atlassian.com/bitbucket-cloud/docs/bitbucket-pipelines-configuration-reference/
  custom:{{range .Environments}}
    # Build and deploy the game server into the '{{.HumanID}}' environment
    build-deploy-server-{{.HumanID}}:
      - step:
          size: 2x # must use at least 2x size to have 6GB of memory for Docker
          name: 'Build server and deploy to {{.DisplayName}} ({{.HumanID}})'
          services:
            - docker-6gb
          script:
            # Exit on failures
            - set -eo pipefail
            # Install metaplay CLI
            - bash <(curl -sSfL --retry 10 --retry-all-errors --retry-max-time 60 https://metaplay.github.io/cli/install.sh)
            # Login to Metaplay cloud (using machine user with credentials from the METAPLAY_CREDENTIALS secret)
            - metaplay auth machine-login
            # Build the game server docker image using the commit hash as the tag
            - metaplay build image gameserver:$BITBUCKET_COMMIT
            # Deploy the game server
            - metaplay deploy server {{.HumanID}} gameserver:$BITBUCKET_COMMIT
{{end}}`

// Generic CI template
const genericCITemplate = `#!/bin/bash
# CI script for deploying to {{.EnvironmentDisplayName}} ({{.EnvironmentHumanID}})
#
# This script can be used with any CI system or run manually.
# Adapt it to fit your CI system's environment and secret management.

set -eo pipefail

# Get the Metaplay machine user credentials from a secret in your CI
# For manual deployment, you can set this environment variable before running the script
export METAPLAY_CREDENTIALS="${METAPLAY_CREDENTIALS:?METAPLAY_CREDENTIALS environment variable is required}"

# Configure build identity: image tag & versions
# In CI, these should come from your CI system's environment variables
export IMAGE_TAG="${IMAGE_TAG:-$(git rev-parse HEAD)}"
export COMMIT_ID="${COMMIT_ID:-$(git rev-parse HEAD)}"
export BUILD_NUMBER="${BUILD_NUMBER:-local}"

# Install metaplay CLI (skip if already installed)
if ! command -v metaplay &> /dev/null; then
    echo "Installing Metaplay CLI..."
    bash <(curl -sSfL --retry 10 --retry-all-errors --retry-max-time 60 https://metaplay.github.io/cli/install.sh)
fi

# Login to Metaplay cloud using the machine user
echo "Logging in to Metaplay cloud..."
metaplay auth machine-login

# Build game server docker image
echo "Building game server image..."
metaplay build image gameserver:$IMAGE_TAG --commit-id=$COMMIT_ID --build-number=$BUILD_NUMBER

# Deploy the game server
echo "Deploying game server to {{.EnvironmentHumanID}}..."
metaplay deploy server {{.EnvironmentHumanID}} gameserver:$IMAGE_TAG

echo "Deployment complete!"
`
