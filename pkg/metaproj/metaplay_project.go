/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package metaproj

import (
	"fmt"
	"html/template"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// Metaplay project: helper type to wrap the resolved project, including relative path to project,
// parsed metaplay-project.yaml and parsed MetaplaySDK/version.yaml.
type MetaplayProject struct {
	Config          ProjectConfig
	RelativeDir     string
	VersionMetadata MetaplayVersionMetadata
}

func (project *MetaplayProject) UsesCustomDashboard() bool {
	return project.Config.Features.Dashboard.UseCustom
}

func (project *MetaplayProject) GetBuildRootDir() string {
	return filepath.Join(project.RelativeDir, project.Config.BuildRootDir)
}

func (project *MetaplayProject) GetSdkRootDir() string {
	return filepath.Join(project.RelativeDir, project.Config.SdkRootDir)
}

func (project *MetaplayProject) GetBackendDir() string {
	return filepath.Join(project.RelativeDir, project.Config.BackendDir)
}

func (project *MetaplayProject) GetSharedCodeDir() string {
	return filepath.Join(project.RelativeDir, project.Config.SharedCodeDir)
}

func (project *MetaplayProject) GetUnityProjectDir() string {
	return filepath.Join(project.RelativeDir, project.Config.UnityProjectDir)
}

// Return the relative directory to Backend/Server.
func (project *MetaplayProject) GetServerDir() string {
	return filepath.Join(project.RelativeDir, project.Config.BackendDir, "Server")
}

func (project *MetaplayProject) GetBotClientDir() string {
	return filepath.Join(project.RelativeDir, project.Config.BackendDir, "BotClient")
}

func (project *MetaplayProject) GetDashboardDir() string {
	dashboardConfig := project.Config.Features.Dashboard
	if !dashboardConfig.UseCustom {
		log.Panic().Msgf("Trying to access custom dashboard dir for a project that has no customized dashboard")
	}
	return filepath.Join(project.RelativeDir, dashboardConfig.RootDir)
}

func (project *MetaplayProject) GetServerValuesFiles(envConfig *ProjectEnvironmentConfig) []string {
	if envConfig.ServerValuesFile != "" {
		return []string{
			filepath.Join(project.RelativeDir, envConfig.ServerValuesFile),
		}
	} else {
		return []string{}
	}
}

func (project *MetaplayProject) GetBotClientValuesFiles(envConfig *ProjectEnvironmentConfig) []string {
	if envConfig.BotClientValuesFile != "" {
		return []string{
			filepath.Join(project.RelativeDir, envConfig.BotClientValuesFile),
		}
	} else {
		return []string{}
	}
}

// Load the Metaplay project config file (metaplay-project.yaml) from the project directory.
func LoadProjectConfigFile(projectDir string) (*ProjectConfig, error) {
	// Check that the provided path points to a file or directory.
	info, err := os.Stat(projectDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("the provided project path '%s' is not a directory", projectDir)
	}

	// Build the full path to the config file in the directory.
	configFilePath := filepath.Join(projectDir, ConfigFileName)

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
	err = ValidateProjectConfig(projectDir, &projectConfig)
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
func ValidateProjectConfig(projectDir string, config *ProjectConfig) error {
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
	if err := validateProjectDir(projectDir, "unityProjectDir", config.UnityProjectDir); err != nil {
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

	// Validate auth provider (if specified).
	authProviderCfg := config.AuthProvider
	if authProviderCfg != nil {
		// Validate required fields
		if authProviderCfg.Name == "" {
			return fmt.Errorf("authProvider.name is required")
		}

		if authProviderCfg.ClientID == "" {
			return fmt.Errorf("authProvider.clientId is required")
		}

		// Validate URLs
		endpoints := map[string]string{
			"authEndpoint":     authProviderCfg.AuthEndpoint,
			"tokenEndpoint":    authProviderCfg.TokenEndpoint,
			"userInfoEndpoint": authProviderCfg.UserInfoEndpoint,
		}
		for name, endpoint := range endpoints {
			if endpoint == "" {
				return fmt.Errorf("authProvider.%s is required", name)
			}
			parsedURL, err := url.Parse(endpoint)
			if err != nil {
				return fmt.Errorf("authProvider.%s is not a valid URL: %v", name, err)
			}
			if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
				return fmt.Errorf("authProvider.%s must use http or https scheme", name)
			}
			if parsedURL.Host == "" {
				return fmt.Errorf("authProvider.%s must include a host", name)
			}
		}

		// Validate scopes.
		if authProviderCfg.Scopes == "" {
			return fmt.Errorf("authProvider.scopes are required")
		}
		scopes := strings.Fields(authProviderCfg.Scopes)
		if len(scopes) == 0 {
			return fmt.Errorf("authProvider.must specify at least one scope")
		}
		for _, scope := range scopes {
			if !regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`).MatchString(scope) {
				return fmt.Errorf("invalid authProvider.scopes '%s': must contain only alphanumeric characters, underscores, dots, and hyphens", scope)
			}
		}

		// Validate audience
		if authProviderCfg.Audience == "" {
			return fmt.Errorf("authProvider.audience is required")
		}
		if !regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`).MatchString(authProviderCfg.Audience) {
			return fmt.Errorf("invalid authProvider.audience: must contain only alphanumeric characters, underscores, dots, and hyphens")
		}
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
		if envConfig.HumanID == "" {
			return fmt.Errorf("environment '%s' did not specify required field 'humanId'", envName)
		}
		if err := ValidateEnvironmentID(envConfig.HumanID); err != nil {
			return fmt.Errorf("environment '%s' specified invalid 'humanId': %w", envName, err)
		}
		if envConfig.StackDomain == "" {
			return fmt.Errorf("environment '%s' did not specify required field 'stackDomain'", envName)
		}
		if envConfig.Type == "" {
			return fmt.Errorf("environment '%s' did not specify required field 'type'", envName)
		}
		if err := ValidateEnvironmentID(envConfig.HumanID); err != nil {
			return fmt.Errorf("environment '%s' specified invalid 'humanId': %w", envName, err)
		}
		if envConfig.ServerValuesFile != "" {
			err := validateHelmValuesFile(filepath.Join(projectDir, envConfig.ServerValuesFile))
			if err != nil {
				return fmt.Errorf("environment '%s' failed to validate 'serverValuesFile': %w", envName, err)
			}
		}
		if envConfig.BotClientValuesFile != "" {
			err := validateHelmValuesFile(filepath.Join(projectDir, envConfig.BotClientValuesFile))
			if err != nil {
				return fmt.Errorf("environment '%s' failed to validate 'botclientValuesFile': %w", envName, err)
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

func ParseVersionMetadata(versionFileContent []byte) (*MetaplayVersionMetadata, error) {
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
	if versionMetadata.DefaultDotnetRuntimeVersion == "" {
		return nil, fmt.Errorf("MetaplaySDK/version.yaml defaultDotnetRuntimeVersion is missing")
	}
	if versionMetadata.DefaultServerChartVersion == nil {
		return nil, fmt.Errorf("MetaplaySDK/version.yaml defaultServerChartVersion is nil")
	}
	if versionMetadata.DefaultBotClientChartVersion == nil {
		return nil, fmt.Errorf("MetaplaySDK/version.yaml defaultServerChartVersion is nil")
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

// Load the MetaplaySDK/version.yaml containing metadata about the Metaplay SDK version,
// e.g., required .NET and Node/pnpm minimum versions.
func LoadSdkVersionMetadata(sdkRootDir string) (*MetaplayVersionMetadata, error) {
	// Read MetaplaySDK/version.yaml content.
	// If unable to read the file, try to determine the SDK version from the Dockerfile.server to
	// identify earlier SDK packages.
	versionFilePath := filepath.Join(sdkRootDir, "version.yaml")
	log.Debug().Msgf("Read SDK version metadata from %s", versionFilePath)
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

	// Parse and validate the 'version.yaml' contents.
	return ParseVersionMetadata(versionFileContent)
}

func NewMetaplayProject(projectDir string, projectConfig *ProjectConfig, versionMetadata *MetaplayVersionMetadata) (*MetaplayProject, error) {
	if filepath.IsAbs(projectDir) {
		return nil, fmt.Errorf("projectDir must be relative, got '%s'", projectDir)
	}

	// Return project.
	return &MetaplayProject{
		Config:          *projectConfig,
		RelativeDir:     projectDir,
		VersionMetadata: *versionMetadata,
	}, nil
}

// Find a matching environment from the project config.
// The first environment that matches 'environment' is chosen.
// The 'environment' argument can match either the humanID or the name of the project.
func (projectConfig *ProjectConfig) FindEnvironmentConfig(environment string) (*ProjectEnvironmentConfig, error) {
	for _, envConfig := range projectConfig.Environments {
		// Match by HumanID.
		if envConfig.HumanID == environment {
			return &envConfig, nil
		}

		// Match by human ID suffix, e.g., 'quickly' matches env 'lovely-wombats-build-quickly' for project 'lovely-wombats-build'.
		suffixed := fmt.Sprintf("%s-%s", projectConfig.ProjectHumanID, environment)
		if envConfig.HumanID == suffixed {
			return &envConfig, nil
		}

		// Match by display name.
		if envConfig.Name == environment {
			return &envConfig, nil
		}
	}

	environmentIDs := strings.Join(getEnvironmentIDs(projectConfig), ", ")
	return nil, fmt.Errorf("no environment matching '%s' found in project config. The valid environments are: %s", environment, environmentIDs)
}

func (projectConfig *ProjectConfig) GetEnvironmentByHumanID(humanID string) (*ProjectEnvironmentConfig, error) {
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
func ValidateProjectID(id string) error {
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
func ValidateEnvironmentID(id string) error {
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

var projectFileTemplate = template.Must(template.New("Metaplay project config").Parse(
	`# Configure schema to use.
# yaml-language-server: $schema={{.SdkRootDir}}/projectConfigSchema.json
$schema: "{{.SdkRootDir}}/projectConfigSchema.json"

# Configure project.
projectID: {{.ProjectID}}
buildRootDir: .
sdkRootDir: {{.SdkRootDir}}
backendDir: {{.BackendDir}}
sharedCodeDir: {{.SharedCodeDir}}
unityProjectDir: {{.UnityProjectDir}}

# Specify .NET runtime version to build project for, only '<major>.<minor>'.
dotnetRuntimeVersion: "{{.DotnetRuntimeVersion}}"

# Specify Helm chart versions to use for server and bot deployments.
serverChartVersion: {{.ServerChartVersion}}
botClientChartVersion: {{.BotClientChartVersion}}

# Customize Metaplay features used in the game.
features:
  # Configure LiveOps Dashboard.
  dashboard:
    useCustom: false

# Project environments.
environments:
{{range .Environments}}  - name: {{.Name}}
    humanId: {{.HumanID}}
    type: {{.Type}}
    stackDomain: {{.StackDomain}}
{{end}}`))

// \todo Clean this up
func GenerateProjectConfigFile(
	sdkMetadata *MetaplayVersionMetadata,
	rootPath string,
	pathToUnityProject string,
	pathToMetaplaySdk string,
	project *portalapi.ProjectInfo,
	environments []portalapi.EnvironmentInfo) (*ProjectConfig, error) {
	// Data for the template
	data := struct {
		SchemaPath            string
		ProjectID             string
		BuildRootDir          string
		SdkRootDir            string
		BackendDir            string
		SharedCodeDir         string
		UnityProjectDir       string
		DotnetRuntimeVersion  string
		ServerChartVersion    string
		BotClientChartVersion string
		Environments          []portalapi.EnvironmentInfo
	}{
		ProjectID:             project.HumanID,
		BuildRootDir:          ".",
		SdkRootDir:            filepath.ToSlash(pathToMetaplaySdk),
		BackendDir:            "Backend",
		SharedCodeDir:         filepath.ToSlash(filepath.Join(pathToUnityProject, "Assets", "SharedCode")),
		UnityProjectDir:       filepath.ToSlash(pathToUnityProject),
		DotnetRuntimeVersion:  sdkMetadata.DefaultDotnetRuntimeVersion,
		ServerChartVersion:    sdkMetadata.DefaultServerChartVersion.String(),
		BotClientChartVersion: sdkMetadata.DefaultBotClientChartVersion.String(),
		Environments:          environments,
	}

	// Render the template.
	var result strings.Builder
	err := projectFileTemplate.Execute(&result, data)
	if err != nil {
		log.Panic().Msgf("Failed to render Metaplay project config file template: %v", err)
	}

	var projectConfig ProjectConfig
	err = yaml.Unmarshal([]byte(result.String()), &projectConfig)
	if err != nil {
		log.Panic().Msgf("Failed to parse generated Metaplay project file: %v\nFull YAML:\n%s", err, result.String())
	}

	// Write metaplay-project.yaml.
	configFilePath := filepath.Join(rootPath, "metaplay-project.yaml")
	log.Debug().Msgf("Write project configuration to: %s", configFilePath)
	if err := os.WriteFile(configFilePath, []byte(result.String()), 0644); err != nil {
		return nil, fmt.Errorf("failed to write project configuration file: %w", err)
	}

	return &projectConfig, nil
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
