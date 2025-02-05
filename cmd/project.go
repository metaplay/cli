/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// Environment family (given to Helm / C# server).
type EnvironmentFamily string

const (
	EnvironmentFamilyDevelopment = "Development"
	EnvironmentFamilyStaging     = "Staging"
	EnvironmentFamilyProduction  = "Production"
)

// Map EnvironmentType (from portal) to an EnvironmentFamily (compatible with Helm and C#)
var environmentTypeToFamilyMapping = map[portalapi.EnvironmentType]string{
	portalapi.EnvironmentTypeDevelopment: EnvironmentFamilyDevelopment,
	portalapi.EnvironmentTypeStaging:     EnvironmentFamilyStaging,
	portalapi.EnvironmentTypeProduction:  EnvironmentFamilyProduction,
}

// Mapping from EnvironmentType to the runtime options file to include (in addition to Options.base.yaml).
var environmentTypeToRuntimeOptionsFileMapping = map[portalapi.EnvironmentType]string{
	portalapi.EnvironmentTypeDevelopment: "./Config/Options.dev.yaml",
	portalapi.EnvironmentTypeStaging:     "./Config/Options.staging.yaml",
	portalapi.EnvironmentTypeProduction:  "./Config/Options.production.yaml",
}

// Per-environment configuration from 'metaplay-project.yaml'.
// Note: When adding new fields, remember to update validateProjectConfig().
type ProjectEnvironmentConfig struct {
	Name             string                    `yaml:"name"`                       // Name of the environment.
	Slug             string                    `yaml:"slug"`                       // Mutable slug of the environment, eg, 'develop'.
	HumanID          string                    `yaml:"humanId"`                    // Stable human ID of the environment. Also the Kubernetes namespace.
	Type             portalapi.EnvironmentType `yaml:"type"`                       // Type of the environment (eg, development, Staging, production).
	StackDomain      string                    `yaml:"stackDomain"`                // Stack base domain (eg, 'p1.metaplay.io').
	ServerValuesFile string                    `yaml:"serverValuesFile,omitempty"` // Relative path (from metaplay-project.yaml) to the game server deployment Helm values file.
	BotsValuesFile   string                    `yaml:"botsValuesFile,omitempty"`   // Relative path (from metaplay-project.yaml) to the bot client deployment Helm values file.
}

// Get the Kubernetes namespace for this environment. Same as HumanID but
// using explicit getter for clarity.
func (envConfig *ProjectEnvironmentConfig) getKubernetesNamespace() string {
	return envConfig.HumanID
}

// Convert the environment type (from portal) to an environment family (for C#).
func (envConfig *ProjectEnvironmentConfig) getEnvironmentFamily() string {
	envFamily, found := environmentTypeToFamilyMapping[envConfig.Type]
	if !found {
		log.Panic().Msgf("Invalid EnvironmentType: %s", envConfig.Type)
	}
	return envFamily
}

// Get the environment-type specific runtime options file to include in Helm values.
func (envConfig *ProjectEnvironmentConfig) getEnvironmentSpecificRuntimeOptionsFile() string {
	configFilePath, found := environmentTypeToRuntimeOptionsFileMapping[envConfig.Type]
	if !found {
		log.Panic().Msgf("Invalid EnvironmentType: %s", envConfig.Type)
	}
	return configFilePath
}

type ProjectFeaturesConfig struct {
	Dashboard struct {
		UseCustom bool   `yaml:"useCustom"`
		RootDir   string `yaml:"rootDir"`
	} `yaml:"dashboard"`
}

// Metaplay project config file, named `metaplay-project.yaml`.
// Note: When adding new fields, remember to update validateProjectConfig().
type ProjectConfig struct {
	ProjectHumanID string `yaml:"projectID"`     // The project's humanID (as in the portal) -- \todo not yet implemented in portal
	BuildRootDir   string `yaml:"buildRootDir"`  // Relative path to the docker build root directory
	SdkRootDir     string `yaml:"sdkRootDir"`    // Relative path to the MetaplaySDK directory
	BackendDir     string `yaml:"backendDir"`    // Relative path to the project-specific backend directory
	SharedCodeDir  string `yaml:"sharedCodeDir"` // Relative path to the shared code directory

	DotnetRuntimeVersion *version.Version `yaml:"dotnetRuntimeVersion"` // .NET runtime version that the project is using (major.minor), eg, '8.0' or '9.0'

	HelmChartRepository   string `yaml:"helmChartRepository"`   // Helm chart repository to use (defaults to 'https://charts.metaplay.dev')
	ServerChartVersion    string `yaml:"serverChartVersion"`    // Version of the game server Helm chart to use (or 'latest-prerelease' for absolute latest)
	BotClientChartVersion string `yaml:"botClientChartVersion"` // Version of the bot client Helm chart to use (or 'latest-prerelease' for absolute latest)

	Features ProjectFeaturesConfig `yaml:"features"`

	Environments []ProjectEnvironmentConfig `yaml:"environments"`
}

// Represents MetaplaySDK/version.yaml.
type MetaplayVersionMetadata struct {
	SdkVersion               *version.Version `yaml:"sdkVersion"`
	MinInfraVersion          *version.Version `yaml:"minInfraVersion"`
	MinServerChartVersion    *version.Version `yaml:"minServerChartVersion"`
	MinBotClientChartVersion *version.Version `yaml:"minBotClientChartVersion"`
	MinDotnetSdkVersion      *version.Version `yaml:"minDotnetSdkVersion"` // Minimum .NET SDK version required to build projects.
	RecommendedNodeVersion   *version.Version `yaml:"nodeVersion"`
	RecommendedPnpmVersion   *version.Version `yaml:"pnpmVersion"`
}

// Metaplay project.
type MetaplayProject struct {
	config          ProjectConfig
	relativeDir     string
	versionMetadata MetaplayVersionMetadata
}

func (project *MetaplayProject) usesCustomDashboard() bool {
	return project.config.Features.Dashboard.UseCustom
}

func (project *MetaplayProject) getBuildRootDir() string {
	return filepath.Join(project.relativeDir, project.config.BuildRootDir)
}

func (project *MetaplayProject) getSdkRootDir() string {
	return filepath.Join(project.relativeDir, project.config.SdkRootDir)
}

func (project *MetaplayProject) getBackendDir() string {
	return filepath.Join(project.relativeDir, project.config.BackendDir)
}

func (project *MetaplayProject) getSharedCodeDir() string {
	return filepath.Join(project.relativeDir, project.config.SharedCodeDir)
}

// Return the relative directory to Backend/Server.
func (project *MetaplayProject) getServerDir() string {
	return filepath.Join(project.relativeDir, project.config.BackendDir, "Server")
}

func (project *MetaplayProject) getBotClientDir() string {
	return filepath.Join(project.relativeDir, project.config.BackendDir, "BotClient")
}

func (project *MetaplayProject) getDashboardDir() string {
	dashboardConfig := project.config.Features.Dashboard
	if !dashboardConfig.UseCustom {
		log.Panic().Msgf("Trying to access custom dashboard dir for a project that has no customized dashboard")
	}
	return filepath.Join(project.relativeDir, dashboardConfig.RootDir)
}

func (project *MetaplayProject) getServerValuesFiles(envConfig *ProjectEnvironmentConfig) []string {
	if envConfig.ServerValuesFile != "" {
		return []string{
			filepath.Join(project.relativeDir, envConfig.ServerValuesFile),
		}
	} else {
		return []string{}
	}
}

func (project *MetaplayProject) getBotsValuesFiles(envConfig *ProjectEnvironmentConfig) []string {
	if envConfig.BotsValuesFile != "" {
		return []string{
			filepath.Join(project.relativeDir, envConfig.BotsValuesFile),
		}
	} else {
		return []string{}
	}
}

// Name of the Metaplay project config file.
const projectConfigFileName = "metaplay-project.yaml"

// Locate the Metaplay project directory, i.e., where metaplay-project.yaml is located.
// If flagProjectConfigPath is given, use it as the directory or project file path.
// Otherwise, try to locate the config file from the current directory.
// The (relative or absolute) path to the project directory is returned.
// \todo Does not handle case mismatches well, eg: -p ..\samples\idler breaks in docker build on Windows
func findProjectDirectory() (string, error) {
	// If the flag is provided, check if it's a valid directory or file path
	if flagProjectConfigPath != "" {
		log.Debug().Msgf("Try to locate Metaplay project in path '%s'", flagProjectConfigPath)
		info, err := os.Stat(flagProjectConfigPath)
		if err != nil {
			return "", fmt.Errorf("provided path '%s' is not a file or directory", flagProjectConfigPath)
		}

		if info.IsDir() {
			// Check if the config file exists in the specified directory
			configFilePath := filepath.Join(flagProjectConfigPath, projectConfigFileName)
			if _, err := os.Stat(configFilePath); err == nil {
				return flagProjectConfigPath, nil
			}
			return "", fmt.Errorf("unable to find metaplay-project.yaml in directory '%s'", flagProjectConfigPath)
		} else {
			// Check if the specified file is the config file
			if filepath.Base(flagProjectConfigPath) == projectConfigFileName {
				return filepath.Dir(flagProjectConfigPath), nil
			}
			return "", errors.New("specified file is not metaplay-project.yaml")
		}
	}

	// Check that metaplay-project.yaml exists in this directory
	if _, err := os.Stat(projectConfigFileName); err != nil {
		return "", errors.New("metaplay-project.yaml file not found in the current directory, use --project=<path> to point to your project directory")
	}

	return ".", nil
}

// Load the Metaplay project config file (metaplay-project.yaml) from the project directory.
func loadProjectConfigFile(projectDir string) (*ProjectConfig, error) {
	// Check that the provided path points to a file or directory.
	info, err := os.Stat(projectDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("the provided project path '%s' is not a directory", projectDir)
	}

	// Build the full path to the config file in the directory.
	configFilePath := filepath.Join(projectDir, projectConfigFileName)

	// Read the file content.
	content, err := os.ReadFile(configFilePath)
	if err != nil {
		return nil, err
	}

	// Unmarshal the YAML content into the ProjectConfig struct.
	var projectConfig ProjectConfig
	err = yaml.Unmarshal(content, &projectConfig)
	if err != nil {
		return nil, err
	}

	// Validate the project config.
	err = validateProjectConfig(projectDir, &projectConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to validate metaplay-project.yaml: %v", err)
	}

	return &projectConfig, nil
}

// Validate that a project-specific directory in 'metaplay-project.yaml' is valid.
func validateProjectDir(projectDir, fieldName, dirValue string) error {
	// Directory must be specified.
	if dirValue == "" {
		return fmt.Errorf("required field '%s' is missing", fieldName)
	}

	// Check that path is not absolute.
	if filepath.IsAbs(dirValue) {
		return fmt.Errorf("field '%s' ('%s') specifies an absolute path: all paths must be relative", fieldName, dirValue)
	}

	// Check that directory exists.
	path := filepath.Join(projectDir, dirValue)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("field '%s' ('%s') does not point to a valid directory (relative from metaplay-project.yaml)", fieldName, dirValue)
	}
	if !info.IsDir() {
		return fmt.Errorf("field '%s' ('%s') does not point to a valid directory (relative from metaplay-project.yaml)", fieldName, dirValue)
	}

	return nil
}

// validateHelmChartRepositoryURL checks if the given input is a valid Helm chart repository URL.
// It returns nil if the URL is valid, or an error describing the issue if invalid.
func validateHelmChartRepositoryURL(chartRepo string) error {
	// Empty repo is allowed (we use the default).
	if chartRepo == "" {
		return nil
	}

	parsedURL, err := url.Parse(chartRepo)
	if err != nil {
		return fmt.Errorf("invalid helmChartRepository URL: %w", err)
	}

	// Check if the scheme is either "http" or "https"
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("invalid helmChartRepository URL scheme: %s (must be 'http' or 'https')", parsedURL.Scheme)
	}

	// Check if the host is not empty
	if parsedURL.Host == "" {
		return fmt.Errorf("invalid helmChartRepository URL: host is empty")
	}

	return nil
}

// Validate that a particular Helm chart version is a valid one (only do local
// checks, don't validate existence in the remote repository).
func validateHelmChartVersion(fieldName string, chartVersion string) error {
	// Field must be specified.
	if fieldName == "" {
		return fmt.Errorf("missing required field %s: specify the version of the chart you want to use", fieldName)
	}

	// Validate that values other than "latest-prerelease" parse correctly.
	if chartVersion != "latest-prerelease" {
		_, err := version.NewVersion(chartVersion)
		if err != nil {
			return fmt.Errorf("invalid version string: %w", err)
		}
	}

	return nil
}

// Check that the provided project config is a valid one.
func validateProjectConfig(projectDir string, config *ProjectConfig) error {
	// Project identity and directories.
	if config.ProjectHumanID == "" {
		return fmt.Errorf("missing required field 'projectID'")
	}
	if err := validateProjectDir(projectDir, "buildRootDir", config.BuildRootDir); err != nil {
		return err
	}
	if err := validateProjectDir(projectDir, "sdkRootDir", config.SdkRootDir); err != nil {
		return err
	}
	if err := validateProjectDir(projectDir, "backendDir", config.BackendDir); err != nil {
		return err
	}
	if err := validateProjectDir(projectDir, "sharedCodeDir", config.SharedCodeDir); err != nil {
		return err
	}

	// Check project .NET version.
	if config.DotnetRuntimeVersion == nil {
		return fmt.Errorf("missing dotnetRuntimeVersion. Must specify the 'major.minor' for the .NET runtime framework to use, e.g., '9.0'.")
	}
	dotnetMajorVersion := config.DotnetRuntimeVersion.Segments()[0]
	dotnetPatchVersion := config.DotnetRuntimeVersion.Segments()[2]
	if dotnetMajorVersion < 8 {
		return fmt.Errorf("invalid dotnetRuntimeVersion ('%s'). Only versions 8.x or later are supported.", config.DotnetRuntimeVersion)
	}
	if dotnetPatchVersion != 0 {
		return fmt.Errorf("invalid dotnetRuntimeVersion ('%s'). Only specify 'major.minor' version, eg, '9.0'.", config.DotnetRuntimeVersion)
	}

	// Helm charts.
	if err := validateHelmChartRepositoryURL(config.HelmChartRepository); err != nil {
		return err
	}
	if err := validateHelmChartVersion("serverChartVersion", config.ServerChartVersion); err != nil {
		return err
	}
	if err := validateHelmChartVersion("botClientChartVersion", config.BotClientChartVersion); err != nil {
		return err
	}

	// Validate project features.

	dashboardConfig := config.Features.Dashboard
	if dashboardConfig.UseCustom {
		if dashboardConfig.RootDir == "" {
			return fmt.Errorf("when custom dashboard is used, rootDir must be specified")
		}
		if err := validateProjectDir(projectDir, "features.dashboard.rootDir", dashboardConfig.RootDir); err != nil {
			return err
		}
	} else {
		// if dashboardConfig.RootDir != "" {
		// 	return fmt.Errorf("when custom dashboard is not used, rootDir must be empty")
		// }
	}

	// Validate environments.
	for endNdx, envConfig := range config.Environments {
		envName := envConfig.Name
		if envConfig.Name == "" {
			return fmt.Errorf("environment at index %d did not specify required field 'name'", endNdx)
		}
		// \todo should we require slug? it doesn't make sense for self-hosted envs?
		if envConfig.Slug == "" {
			return fmt.Errorf("environment '%s' did not specify required field 'slug'", envName)
		}
		if envConfig.HumanID == "" {
			return fmt.Errorf("environment '%s' did not specify required field 'humanId'", envName)
		}
		if err := validateEnvironmentID(envConfig.HumanID); err != nil {
			return fmt.Errorf("environment '%s' specified invalid 'humanId': %w", envName, err)
		}
		if envConfig.StackDomain == "" {
			return fmt.Errorf("environment '%s' did not specify required field 'stackDomain'", envName)
		}
		if envConfig.Type == "" {
			return fmt.Errorf("environment '%s' did not specify required field 'type'", envName)
		}
		if err := validateEnvironmentID(envConfig.HumanID); err != nil {
			return fmt.Errorf("environment '%s' specified invalid 'humanId': %w", envName, err)
		}
		if envConfig.ServerValuesFile != "" {
			err := validateHelmValuesFile(filepath.Join(projectDir, envConfig.ServerValuesFile))
			if err != nil {
				return fmt.Errorf("environment '%s' failed to validate 'serverValuesFile': %w", envName, err)
			}
		}
		if envConfig.BotsValuesFile != "" {
			err := validateHelmValuesFile(filepath.Join(projectDir, envConfig.BotsValuesFile))
			if err != nil {
				return fmt.Errorf("environment '%s' failed to validate 'botsValuesFile': %w", envName, err)
			}
		}
	}

	return nil
}

// Resolve the Metaplay SDK version from the Dockerfile.
func extractSDKVersionFromDockerfile(dockerfilePath string) (*version.Version, error) {
	// Read the Dockerfile content
	dockerfileContent, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return nil, fmt.Errorf("the sdkRootDir in your project's metaplay-project.yaml does not point to a valid MetaplaySDK directory")
	}

	// Define the regular expression to capture the version
	versionRegex := regexp.MustCompile(`LABEL\s+io\.metaplay\.sdk_version\s*=\s*([^\s\\]+)`)

	// Search for the version
	matches := versionRegex.FindStringSubmatch(string(dockerfileContent))
	if len(matches) < 2 {
		return nil, fmt.Errorf("failed to extract SDK version from Dockerfile.server")
	}

	// Return the captured version
	versionStr := strings.Trim(matches[1], `"`)
	version, err := version.NewVersion(versionStr)
	return version, err
}

// Load the MetaplaySDK/version.yaml containing metadata about the Metaplay SDK version,
// e.g., required .NET and Node/pnpm minimum versions.
func loadVersionMetadata(projectDir string, projectConfig *ProjectConfig) (*MetaplayVersionMetadata, error) {
	// Read MetaplaySDK/version.yaml content.
	sdkRootDir := filepath.Join(projectDir, projectConfig.SdkRootDir)
	versionFilePath := filepath.Join(sdkRootDir, "version.yaml")
	versionFileContent, readVersionErr := os.ReadFile(versionFilePath)
	if readVersionErr != nil {
		// Detect SDK version from Dockerfile.server (or bad MetaplaySDK directory if file not found).
		sdkVersion, err := extractSDKVersionFromDockerfile(filepath.Join(sdkRootDir, "Dockerfile.server"))
		if err != nil {
			return nil, err
		}

		// Check that the SDK version is the minimum supported by the CLI.
		minSupportedVersion, _ := version.NewVersion("32.0.0-aaaaa") // allow prerelease SDK versions
		if sdkVersion.LessThan(minSupportedVersion) {
			return nil, fmt.Errorf("minimum Metaplay SDK version supported by this CLI is Release 32, your project is using %s", sdkVersion)
		}

		// Generic error when we don't know what went wrong.
		return nil, fmt.Errorf("failed to read Metaplay SDK version metadata: %v", readVersionErr)
	}

	// Unmarshal the YAML content into the ProjectConfig struct.
	var versionMetadata MetaplayVersionMetadata
	err := yaml.Unmarshal(versionFileContent, &versionMetadata)
	if err != nil {
		return nil, err
	}

	// Make sure all versions are defined.
	if versionMetadata.SdkVersion == nil {
		return nil, fmt.Errorf("MetaplaySDK/version.yaml sdkVersion is nil")
	}
	if versionMetadata.MinInfraVersion == nil {
		return nil, fmt.Errorf("MetaplaySDK/version.yaml minInfraVersion is nil")
	}
	if versionMetadata.MinServerChartVersion == nil {
		return nil, fmt.Errorf("MetaplaySDK/version.yaml minServerChartVersion is nil")
	}
	if versionMetadata.MinBotClientChartVersion == nil {
		return nil, fmt.Errorf("MetaplaySDK/version.yaml minBotClientChartVersion is nil")
	}
	if versionMetadata.MinDotnetSdkVersion == nil {
		return nil, fmt.Errorf("MetaplaySDK/version.yaml minDotnetSdkVersion is nil")
	}
	if versionMetadata.RecommendedNodeVersion == nil {
		return nil, fmt.Errorf("MetaplaySDK/version.yaml nodeVersion is nil")
	}
	if versionMetadata.RecommendedPnpmVersion == nil {
		return nil, fmt.Errorf("MetaplaySDK/version.yaml pnpmVersion is nil")
	}

	return &versionMetadata, nil
}

// Locate and load the project config file, based on the --project flag.
func resolveProject() (*MetaplayProject, error) {
	// Find the path with the project config file.
	projectDir, err := findProjectDirectory()
	if err != nil {
		return nil, err
	}
	log.Debug().Msgf("Project located in directory %s", projectDir)

	// Load the project config file.
	projectConfig, err := loadProjectConfigFile(projectDir)
	if err != nil {
		return nil, err
	}
	log.Debug().Msgf("Project config loaded")

	// Load version metadata from MetaplaySDK/version.yaml.
	versionMetadata, err := loadVersionMetadata(projectDir, projectConfig)
	if err != nil {
		return nil, err
	}
	log.Debug().Msgf("Version metadata loaded: %+v", versionMetadata)

	// Return project.
	return &MetaplayProject{
		config:          *projectConfig,
		relativeDir:     projectDir,
		versionMetadata: *versionMetadata,
	}, nil
}

// Resolve the environment configuration. First, try the project config, if available.
// Otherwise, fetch the information from the portal.
func resolveEnvironment(tokenSet *auth.TokenSet, environment string) (*ProjectEnvironmentConfig, error) {
	// If a metaplay-project.yaml can be located, resolve the environment
	// from the project config.
	project, err := resolveProject()
	if err == nil {
		// Find target environment.
		envConfig, err := project.config.findEnvironmentConfig(environment)
		if err != nil {
			return nil, err
		}

		return envConfig, nil
	}

	// Check that the specified environment ID is a valid human ID.
	if err := validateEnvironmentID(environment); err != nil {
		return nil, fmt.Errorf("full environment ID must be specified when metaplay-project.yaml not found: %w", err)
	}

	// No project config found, try to resolve the environment from the portal.
	portalClient := portalapi.NewClient(tokenSet)
	portalEnv, err := portalClient.FetchEnvironmentInfoByHumanID(environment)
	if err != nil {
		return nil, err
	}

	// Convert to ProjectEnvironmentConfig.
	envConfig := ProjectEnvironmentConfig{
		Name:        portalEnv.Name,
		Slug:        portalEnv.Slug,
		HumanID:     portalEnv.HumanID,
		StackDomain: portalEnv.StackDomain,
		Type:        portalEnv.Type,
	}
	return &envConfig, nil
}

// Helper for resolving both the MetaplayProject and a specific environment at the same time.
// This operation is common enough to justify its own method.
func resolveProjectAndEnvironment(environment string) (*MetaplayProject, *ProjectEnvironmentConfig, error) {
	// Resolve the project.
	project, err := resolveProject()
	if err != nil {
		return nil, nil, err
	}

	// Find target environment.
	envConfig, err := project.config.findEnvironmentConfig(environment)
	if err != nil {
		return nil, nil, err
	}

	return project, envConfig, nil
}

// Find a matching environment from the project config.
// For now, matching is done againts humanId and slugs, more to be added as needed.
func (projectConfig *ProjectConfig) findEnvironmentConfig(environment string) (*ProjectEnvironmentConfig, error) {
	// Match by HumanID.
	for _, envConfig := range projectConfig.Environments {
		if envConfig.HumanID == environment {
			return &envConfig, nil
		}
	}

	// Match by slug.
	for _, envConfig := range projectConfig.Environments {
		if envConfig.Slug == environment {
			return &envConfig, nil
		}
	}

	environmentIDs := strings.Join(getEnvironmentIDs(projectConfig), ", ")
	return nil, fmt.Errorf("no environment matching '%s' found in project config. The valid environments are: %s", environment, environmentIDs)
}

func (projectConfig *ProjectConfig) getEnvironmentByHumanID(humanID string) (*ProjectEnvironmentConfig, error) {
	// Match by HumanID.
	for _, envConfig := range projectConfig.Environments {
		if envConfig.HumanID == humanID {
			return &envConfig, nil
		}
	}
	return nil, fmt.Errorf("no environment with humanID '%s' found", humanID)
}

func getEnvironmentIDs(projectConfig *ProjectConfig) []string {
	names := make([]string, len(projectConfig.Environments))
	for ndx, env := range projectConfig.Environments {
		names[ndx] = env.HumanID
	}
	return names
}

// Validate the given project ID:
// - It must be a dash-separated segmented name (eg, 'gorgeous-bear').
// - Allow 2-3 segments in the name.
// - Only lower-case ASCII alphanumeric characters are allowed in the segments (no upper-case letters, no special characters).
// \todo Numbers are allowed because of some legacy environments like 'idler-develop5'
// \todo Only allow 3 segments in the future (for now, we still use 2-segment names as we mock the human IDs)
func validateProjectID(id string) error {
	if id == "" {
		return fmt.Errorf("project ID is empty")
	}

	// Split the string by dashes
	parts := strings.Split(id, "-")

	// Check number of parts (2-3 segments allowed).
	if len(parts) < 2 || len(parts) > 3 {
		return fmt.Errorf("project ID must have 2-3 dash-separated segments, '%s' has %d segments", id, len(parts))
	}

	// Validate each segment contains only lower-case ASCII characters
	validSegment := regexp.MustCompile(`^[a-z]+$`)
	for i, part := range parts {
		if !validSegment.MatchString(part) {
			return fmt.Errorf("segment %d ('%s') in project ID contains invalid characters - only lower-case ASCII characters (a-z) are allowed", i+1, part)
		}
	}

	return nil
}

// validateEnvironmentID checks whether the given environment ID is valid.
// The valid format must be dash-separated parts with alphanumeric characters
// only. There can be either 2 to 4 segments. Eg, 'tiny-squids' or 'idler-develop5',
// or 'yellow-gritty-tuna-jumps'.
func validateEnvironmentID(id string) error {
	if id == "" {
		return fmt.Errorf("environment ID is empty")
	}

	// Split the string by dashes
	parts := strings.Split(id, "-")

	// Check number of parts (2-4 segments are allowed)
	if len(parts) < 2 || len(parts) > 4 {
		return fmt.Errorf("environment ID must have 2-4 dash-separated segments, got %d segments in '%s'", len(parts), id)
	}

	// Validate each segment contains only alphanumeric characters
	validSegment := regexp.MustCompile(`^[a-z0-9]+$`)
	for i, part := range parts {
		if !validSegment.MatchString(part) {
			return fmt.Errorf("segment %d ('%s') in environment ID contains invalid characters - only lower-case ASCII alphanumeric characters (a-z, 0-9) are allowed", i+1, part)
		}
	}

	return nil
}

func isValidEnvironmentType(envType portalapi.EnvironmentType) bool {
	_, found := environmentTypeToFamilyMapping[envType]
	return found
}

// validateHelmValuesFile validates the given Helm values file path.
func validateHelmValuesFile(filePath string) error {
	// Check if the file has a .yaml or .yml suffix
	if ext := filepath.Ext(filePath); ext != ".yaml" && ext != ".yml" {
		return fmt.Errorf("file must have .yaml or .yml extension, got: %s", ext)
	}

	// Check if the file exists and can be opened
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("unable to open file: %w", err)
	}
	defer file.Close()

	// Check if the file can be parsed as YAML
	data, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("unable to read file: %w", err)
	}
	var parsedData map[string]interface{}
	if err := yaml.Unmarshal(data, &parsedData); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}

	return nil
}
