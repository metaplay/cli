/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/filesetwriter"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/portalapi"
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
	{CIProviderGitHubActions, "GitHub Actions", "Deploy using Metaplay's reusable workflows"},
	{CIProviderBitbucket, "Bitbucket Pipelines", "Deploy using Bitbucket's native CI/CD"},
	{CIProviderGeneric, "Generic CI / Manual", "Deploy using any CI system or manually"},
}

type initCIOpts struct {
	flagCIProvider  string // CI provider to use (github, bitbucket, generic)
	flagEnvironment string // Target environment human ID
	flagOnConflict  string // Conflict resolution: overwrite, rename, skip
	flagAutoConfirm bool   // Automatically confirm file writes
	flagOutputDir   string // Output directory for CI files (defaults to project root)

	projectDir   string                              // Resolved project directory
	project      *metaproj.MetaplayProject           // Loaded project
	environments []metaproj.ProjectEnvironmentConfig  // Resolved target environments (from flag)
	ciProvider   CIProvider                           // Selected CI provider
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
			metaplay init ci --provider=github --environment=nimbly

			# Initialize for multiple environments, overwriting existing files
			metaplay init ci --provider=github --environment=nimbly,prod --on-conflict=overwrite --yes

			# Re-generate files with .new suffix to compare against existing ones
			metaplay init ci --provider=github --environment=all --on-conflict=rename --yes
		`),
	}

	// Register flags.
	flags := cmd.Flags()
	flags.StringVar(&o.flagCIProvider, "provider", "", "CI provider to use: github, bitbucket, or generic")
	flags.StringVarP(&o.flagEnvironment, "environment", "e", "", "Target environment(s): human ID, comma-separated list, or 'all'")
	flags.StringVar(&o.flagOnConflict, "on-conflict", "", "How to handle existing files: overwrite, rename, or skip")
	flags.BoolVarP(&o.flagAutoConfirm, "yes", "y", false, "Automatically confirm file writes")
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

	// Validate --on-conflict if specified
	if o.flagOnConflict != "" {
		if !isValidConflictPolicy(o.flagOnConflict) {
			return clierrors.NewUsageErrorf("Invalid --on-conflict value '%s'", o.flagOnConflict).
				WithDetails("Valid options are: overwrite, rename, skip")
		}
	}

	// Check for environments before validating a specific one
	if len(o.project.Config.Environments) == 0 {
		return clierrors.NewUsageError("No environments found in metaplay-project.yaml").
			WithSuggestion("Update the local file with 'metaplay update project-environments' or create a new environment via https://portal.metaplay.dev")
	}

	// Validate and resolve environment(s) if specified
	if o.flagEnvironment != "" && o.flagEnvironment != "all" {
		parts := strings.Split(o.flagEnvironment, ",")
		for _, part := range parts {
			name := strings.TrimSpace(part)
			if name == "" {
				continue
			}
			env, err := o.project.Config.FindEnvironmentConfig(name)
			if err != nil {
				return err
			}
			o.environments = append(o.environments, *env)
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
			return clierrors.NewUsageError("--provider is required in non-interactive mode")
		}
		if o.flagEnvironment == "" {
			return clierrors.NewUsageError("--environment is required in non-interactive mode")
		}
	}

	return nil
}

// conflictOption is used for the interactive conflict resolution dialog.
type conflictOption struct {
	Policy      filesetwriter.ConflictPolicy
	Name        string
	Description string
}

var conflictOptions = []conflictOption{
	{filesetwriter.Overwrite, "Overwrite", "Replace existing files with freshly generated versions"},
	{filesetwriter.Rename, "Rename", "Write new versions with .new suffix (e.g. deploy-dev.sh.new)"},
	{filesetwriter.Skip, "Skip", "Keep existing files, only write new ones"},
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
	} else if len(o.environments) > 0 {
		environments = o.environments
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

	// Add all files to plan with default Overwrite policy.
	plan := filesetwriter.NewPlan(tui.IsInteractiveMode())
	if err := o.collectCIFiles(plan, outputDir, environments); err != nil {
		return err
	}
	if err := plan.Scan(); err != nil {
		return err
	}

	// Show file preview.
	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("CI Configuration"))
	log.Info().Msg("")
	log.Info().Msgf("CI Provider:  %s", styles.RenderTechnical(string(o.ciProvider)))
	log.Info().Msg("Files to be modified:")
	plan.Preview(false)

	if plan.HasReadOnlyFiles() {
		log.Info().Msg("")
		log.Info().Msg(styles.RenderWarning("Some files are read-only and cannot be written."))
	}

	// If conflicts exist, resolve them via --on-conflict flag or interactive dialog.
	if plan.HasConflicts() {
		var policy filesetwriter.ConflictPolicy
		if o.flagOnConflict != "" {
			policy = parseConflictPolicy(o.flagOnConflict)
		} else if !o.flagAutoConfirm {
			selected, err := tui.ChooseFromListDialog(
				"Some files already exist. How should conflicts be handled?",
				conflictOptions,
				func(opt *conflictOption) (string, string) {
					return opt.Name, opt.Description
				},
			)
			if err != nil {
				return err
			}
			log.Info().Msgf(" %s %s", styles.RenderSuccess("✓"), selected.Name)
			policy = selected.Policy
		} else {
			policy = filesetwriter.Overwrite
		}

		// Re-scan and re-preview if the policy changed the outcome.
		if policy != filesetwriter.Overwrite {
			plan.SetConflictPolicy(policy, ".new")
			if err := plan.Scan(); err != nil {
				return err
			}

			// If all files were skipped, nothing to do.
			if plan.FilesToWrite() == 0 {
				log.Info().Msg("")
				log.Info().Msg("All files already exist, nothing to write.")
				return nil
			}

			log.Info().Msg("")
			log.Info().Msg("Files to be modified:")
			plan.Preview(false)
		}
	}

	// Confirm once for all files.
	log.Info().Msg("")
	if !o.flagAutoConfirm {
		question := fmt.Sprintf("Write %d file(s)?", plan.FilesToWrite())
		confirmed, err := tui.DoConfirmQuestion(ctx, question)
		if err != nil {
			return err
		}
		if !confirmed {
			log.Info().Msg("Aborted.")
			return nil
		}
	}

	// Write all files.
	if err := plan.Execute(); err != nil {
		return err
	}

	// Build portal link (best-effort: fall back to root URL if not logged in).
	portalLink := "https://portal.metaplay.dev"
	if orgUUID := o.tryGetOrganizationUUID(); orgUUID != "" {
		portalLink = fmt.Sprintf("https://portal.metaplay.dev/orgs/%s?tab=1", orgUUID)
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderSuccess("CI configuration initialized successfully!"))
	log.Info().Msg("")
	log.Info().Msg("Next steps:")
	log.Info().Msgf("  1. Create a machine user in the Metaplay portal at %s (if not created yet)", styles.RenderTechnical(portalLink))
	log.Info().Msgf("  2. Store the machine user credentials in your CI system's secrets with name %s", styles.RenderTechnical("METAPLAY_CREDENTIALS"))
	log.Info().Msgf("  3. Add the machine user to your project and environment with the %s role", styles.RenderTechnical("game-admin"))
	log.Info().Msg("  4. Review, commit and push the generated CI configuration files")

	return nil
}

// collectCIFiles adds all files to generate to the plan.
func (o *initCIOpts) collectCIFiles(plan *filesetwriter.Plan, outputDir string, environments []metaproj.ProjectEnvironmentConfig) error {
	if o.ciProvider == CIProviderBitbucket {
		return o.collectBitbucketFile(plan, outputDir, environments)
	}

	for _, env := range environments {
		if err := o.collectCIFile(plan, outputDir, env); err != nil {
			return err
		}
	}
	return nil
}

// collectCIFile renders a single GitHub Actions or Generic CI file and adds it to the plan.
func (o *initCIOpts) collectCIFile(plan *filesetwriter.Plan, outputDir string, env metaproj.ProjectEnvironmentConfig) error {
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
		return clierrors.Newf("Unknown CI provider: %s", o.ciProvider)
	}

	if err != nil {
		return clierrors.Wrap(err, "Failed to render CI template")
	}

	perm := os.FileMode(0644)
	if o.ciProvider == CIProviderGeneric {
		perm = 0755
	}

	plan.Add(filePath, []byte(content), perm)
	return nil
}

// collectBitbucketFile renders a single Bitbucket Pipelines file and adds it to the plan.
func (o *initCIOpts) collectBitbucketFile(plan *filesetwriter.Plan, outputDir string, environments []metaproj.ProjectEnvironmentConfig) error {
	var envData []bitbucketEnvironmentData
	for _, env := range environments {
		envData = append(envData, bitbucketEnvironmentData{
			DisplayName: env.Name,
			HumanID:     env.HumanID,
		})
	}

	content, err := renderTemplate(bitbucketPipelinesTmpl, bitbucketTemplateData{Environments: envData})
	if err != nil {
		return clierrors.Wrap(err, "Failed to render Bitbucket Pipelines template")
	}

	filePath := filepath.Join(outputDir, "bitbucket-pipelines.yml")
	plan.Add(filePath, []byte(content), 0644)
	return nil
}

// tryGetOrganizationUUID attempts to fetch the organization UUID from the portal.
// Returns empty string if the user is not logged in or the fetch fails.
func (o *initCIOpts) tryGetOrganizationUUID() string {
	authProvider, err := getAuthProvider(o.project, "metaplay")
	if err != nil {
		return ""
	}
	tokenSet, err := auth.LoadAndRefreshTokenSet(authProvider)
	if err != nil {
		return ""
	}
	portalClient := portalapi.NewClient(tokenSet)
	projectInfo, err := portalClient.FetchProjectInfo(o.project.Config.ProjectHumanID)
	if err != nil {
		return ""
	}
	return projectInfo.OrganizationUUID
}

func isValidConflictPolicy(value string) bool {
	switch value {
	case "overwrite", "rename", "skip":
		return true
	default:
		return false
	}
}

func parseConflictPolicy(value string) filesetwriter.ConflictPolicy {
	switch value {
	case "rename":
		return filesetwriter.Rename
	case "skip":
		return filesetwriter.Skip
	default:
		return filesetwriter.Overwrite
	}
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

      - name: Generate unique image tag
        run: echo "IMAGE_TAG=$(date -u +%Y%m%d-%H%M%S)-$GITHUB_SHA" >> $GITHUB_ENV

      - name: Build server image
        run: metaplay build image gameserver:${{ env.IMAGE_TAG }}

      - name: Deploy server to target environment
        run: metaplay deploy server [[.EnvironmentHumanID]] gameserver:${{ env.IMAGE_TAG }}
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
            # Generate unique image tag
            - export IMAGE_TAG=$(date -u +%Y%m%d-%H%M%S)-$BITBUCKET_COMMIT
            # Build the game server docker image
            - metaplay build image gameserver:$IMAGE_TAG
            # Deploy the game server
            - metaplay deploy server {{.HumanID}} gameserver:$IMAGE_TAG
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

# Configure build identity
export COMMIT_ID="${COMMIT_ID:-$(git rev-parse HEAD)}"
export BUILD_NUMBER="${BUILD_NUMBER:-local}"

# Generate unique image tag
export IMAGE_TAG="$(date -u +%Y%m%d-%H%M%S)-$COMMIT_ID"

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
