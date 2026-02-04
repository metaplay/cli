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
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// CLI only supports Metaplay SDK versions 32.0 and above. The legacy metaplay-auth CLI
// was used with earlier SDK versions.
var OldestSupportedSdkVersion = version.Must(version.NewVersion("32.0.0"))

// Reserved alias names that cannot be used for environment aliases.
var reservedAliases = []string{
	"all",
	"default",
	"local",
	"localhost",
	"none",
	"self",
	"metaplay",
}

// validateAlias checks if an environment alias is valid.
func validateAlias(alias string) error {
	if alias == "" {
		return fmt.Errorf("alias cannot be empty")
	}
	if len(alias) > 30 {
		return fmt.Errorf("alias must be at most 30 characters")
	}
	// Only lowercase alphanumeric and dashes allowed, cannot start/end with dash.
	validAlias := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	if !validAlias.MatchString(alias) {
		return fmt.Errorf("alias must contain only lowercase alphanumeric characters and dashes, and cannot start or end with a dash")
	}
	// Check reserved aliases.
	for _, reserved := range reservedAliases {
		if alias == reserved {
			return fmt.Errorf("alias '%s' is reserved and cannot be used", alias)
		}
	}
	return nil
}

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

	// Apply defaults to project config.
	err = ApplyProjectConfigDefaults(&projectConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to apply defaults to metaplay-project.yaml: %v", err)
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

// Apply any defaults to the project config which are not required to be specified.
func ApplyProjectConfigDefaults(config *ProjectConfig) error {
	for ndx, envConfig := range config.Environments {
		// Default value for hosting type depending on stack domain.
		if envConfig.HostingType == "" {
			// Stack domain with '.metaplay.' in it indicates Metaplay-hosted
			hostingType := portalapi.HostingTypeSelfHosted
			if strings.Contains(envConfig.StackDomain, ".metaplay.") {
				hostingType = portalapi.HostingTypeMetaplayHosted
			}
			config.Environments[ndx].HostingType = hostingType
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

	// Validate auth providers (if specified).
	if config.AuthProviders == nil {
		config.AuthProviders = make(map[string]*auth.AuthProviderConfig)
	}

	// Validate each auth provider
	for name, authProviderCfg := range config.AuthProviders {
		// Validate required fields
		if authProviderCfg.Name == "" {
			return fmt.Errorf("authProviders[%s].name is required", name)
		}

		if authProviderCfg.ClientID == "" {
			return fmt.Errorf("authProviders[%s].clientId is required", name)
		}

		// Validate URLs
		endpoints := map[string]string{
			"authEndpoint":     authProviderCfg.AuthEndpoint,
			"tokenEndpoint":    authProviderCfg.TokenEndpoint,
			"userInfoEndpoint": authProviderCfg.UserInfoEndpoint,
		}
		for endpointName, endpoint := range endpoints {
			if endpoint == "" {
				return fmt.Errorf("authProviders[%s].%s is required", name, endpointName)
			}
			parsedURL, err := url.Parse(endpoint)
			if err != nil {
				return fmt.Errorf("authProviders[%s].%s is not a valid URL: %v", name, endpointName, err)
			}
			if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
				return fmt.Errorf("authProviders[%s].%s must use http or https scheme", name, endpointName)
			}
			if parsedURL.Host == "" {
				return fmt.Errorf("authProviders[%s].%s must include a host", name, endpointName)
			}
		}

		// Validate scopes.
		if authProviderCfg.Scopes == "" {
			return fmt.Errorf("authProviders[%s].scopes are required", name)
		}
		scopes := strings.Fields(authProviderCfg.Scopes)
		if len(scopes) == 0 {
			return fmt.Errorf("authProviders[%s].must specify at least one scope", name)
		}
		for _, scope := range scopes {
			if !regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`).MatchString(scope) {
				return fmt.Errorf("invalid authProviders[%s].scopes '%s': must contain only alphanumeric characters, underscores, dots, and hyphens", name, scope)
			}
		}

		// Validate audience
		if authProviderCfg.Audience == "" {
			return fmt.Errorf("authProviders[%s].audience is required", name)
		}
		// \note: The hyphen must be the last in the class to avoid getting parsed as A-B syntax
		if !regexp.MustCompile(`^[a-zA-Z0-9_./:-]+$`).MatchString(authProviderCfg.Audience) {
			return fmt.Errorf("invalid authProviders[%s].audience: must contain only alphanumeric characters, underscores, dots, colon, forward slashes, and hyphens", name)
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
		if err := ValidateEnvironmentID(envConfig.HostingType, envConfig.HumanID); err != nil {
			return fmt.Errorf("environment '%s' specified invalid 'humanId': %w", envName, err)
		}
		if envConfig.StackDomain == "" {
			return fmt.Errorf("environment '%s' did not specify required field 'stackDomain'", envName)
		}
		if envConfig.Type == "" {
			return fmt.Errorf("environment '%s' did not specify required field 'type'", envName)
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
		// Validate the environment's auth provider if specified
		if envConfig.AuthProvider != "" {
			// Check that the specified provider exists in the map
			if _, exists := config.AuthProviders[envConfig.AuthProvider]; !exists {
				return fmt.Errorf("environment '%s' specifies auth provider '%s' which is not defined in authProviders", envName, envConfig.AuthProvider)
			}
		}
	}

	// Validate environment aliases.
	aliasToEnvName := make(map[string]string)
	for _, envConfig := range config.Environments {
		for _, alias := range envConfig.Aliases {
			// Validate alias format.
			if err := validateAlias(alias); err != nil {
				return fmt.Errorf("environment '%s' has invalid alias: %w", envConfig.Name, err)
			}

			// Check for duplicate aliases across environments.
			if existingEnv, exists := aliasToEnvName[alias]; exists {
				return fmt.Errorf("alias '%s' is used by both environments '%s' and '%s'", alias, existingEnv, envConfig.Name)
			}
			aliasToEnvName[alias] = envConfig.Name

			// Check that alias doesn't conflict with any humanID or name.
			for _, otherEnv := range config.Environments {
				if alias == otherEnv.HumanID {
					return fmt.Errorf("alias '%s' in environment '%s' conflicts with humanId of environment '%s'", alias, envConfig.Name, otherEnv.Name)
				}
				if alias == otherEnv.Name {
					return fmt.Errorf("alias '%s' in environment '%s' conflicts with name of environment '%s'", alias, envConfig.Name, otherEnv.Name)
				}
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
		if sdkVersion.LessThan(OldestSupportedSdkVersion) {
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
// The 'environment' argument can match the humanID, name, or an alias of the environment.
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

		// Match by alias.
		for _, alias := range envConfig.Aliases {
			if alias == environment {
				return &envConfig, nil
			}
		}
	}

	environmentIDs := strings.Join(getEnvironmentIdentifiers(projectConfig), ", ")
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

// getEnvironmentIdentifiers returns all valid identifiers for environments in the project,
// including humanIDs and aliases.
func getEnvironmentIdentifiers(projectConfig *ProjectConfig) []string {
	identifiers := make([]string, 0)
	for _, env := range projectConfig.Environments {
		identifiers = append(identifiers, env.HumanID)
		identifiers = append(identifiers, env.Aliases...)
	}
	return identifiers
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

// ValidateEnvironmentID checks whether the given environment ID is valid.
// Rules for all hosting types:
// - Must be between 2 and 50 characters.
// - Must only contain lowercase alphanumeric characters and dashes.
// For Metaplay-hosted environments:
// - Must be dash-separated segments, with 2 to 4 segments allowed.
// - Each segment must consist of lowercase alphanumeric characters.
// - Examples: 'lovely-wombats-build-nimbly' or 'idler-develop5', or 'yellow-gritty-tuna-jumps'.
// For self-hosted environments, only the global checks are applied.
func ValidateEnvironmentID(hostingType portalapi.HostingType, id string) error {
	if id == "" {
		return fmt.Errorf("environment ID is empty")
	}

	// Sanity check length.
	if len(id) < 2 {
		return fmt.Errorf("environment ID must be at least 2 characters long, got '%s'", id)
	}
	if len(id) > 50 {
		return fmt.Errorf("environment ID must be at most 50 characters long, got '%s'", id)
	}

	// Only alphanumeric characters and dashes are allowed.
	validChars := regexp.MustCompile(`^[a-z0-9-]+$`)
	if !validChars.MatchString(id) {
		return fmt.Errorf("environment ID '%s' contains invalid characters - only alphanumeric characters and dashes are allowed", id)
	}

	if hostingType == portalapi.HostingTypeMetaplayHosted {
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
	} else if hostingType == portalapi.HostingTypeSelfHosted {
		// Cannot start or end with a dash.
		if strings.HasPrefix(id, "-") {
			return fmt.Errorf("environment ID '%s' cannot start with a dash", id)
		}
		if strings.HasSuffix(id, "-") {
			return fmt.Errorf("environment ID '%s' cannot end with a dash", id)
		}
		if strings.Contains(id, "--") {
			return fmt.Errorf("environment ID '%s' cannot contain consecutive dashes", id)
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
    useCustom: {{.UseCustomDashboard}}
    rootDir: {{.CustomDashboardPath}}

# Project environments.
environments:
{{range .Environments}}  - name: {{.Name}}
    humanId: {{.HumanID}}
    type: {{.Type}}
    stackDomain: {{.StackDomain}}
{{end}}`))

// RenderProjectConfigYAML generates the YAML content for a project config file.
// Returns the YAML string and the parsed ProjectConfig.
func RenderProjectConfigYAML(
	sdkMetadata *MetaplayVersionMetadata,
	pathToUnityProject string,
	pathToMetaplaySdk string,
	sharedCodePath string,
	gameBackendPath string,
	customDashboardPath string,
	project *portalapi.ProjectInfo,
	environments []portalapi.EnvironmentInfo) (string, *ProjectConfig, error) {
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
		UseCustomDashboard    bool
		CustomDashboardPath   string
		Environments          []portalapi.EnvironmentInfo
	}{
		ProjectID:             project.HumanID,
		BuildRootDir:          ".",
		SdkRootDir:            filepath.ToSlash(pathToMetaplaySdk),
		BackendDir:            filepath.ToSlash(gameBackendPath),
		SharedCodeDir:         filepath.ToSlash(sharedCodePath),
		UnityProjectDir:       filepath.ToSlash(pathToUnityProject),
		DotnetRuntimeVersion:  sdkMetadata.DefaultDotnetRuntimeVersion,
		ServerChartVersion:    sdkMetadata.DefaultServerChartVersion.String(),
		BotClientChartVersion: sdkMetadata.DefaultBotClientChartVersion.String(),
		UseCustomDashboard:    customDashboardPath != "",
		CustomDashboardPath:   filepath.ToSlash(customDashboardPath),
		Environments:          environments,
	}

	// Render the template.
	var result strings.Builder
	err := projectFileTemplate.Execute(&result, data)
	if err != nil {
		return "", nil, fmt.Errorf("failed to render Metaplay project config file template: %w", err)
	}

	var projectConfig ProjectConfig
	err = yaml.Unmarshal([]byte(result.String()), &projectConfig)
	if err != nil {
		return "", nil, fmt.Errorf("failed to parse generated Metaplay project file: %w\nFull YAML:\n%s", err, result.String())
	}

	return result.String(), &projectConfig, nil
}

// GenerateProjectConfigFile generates and writes a project config file to disk.
// \todo Clean this up
func GenerateProjectConfigFile(
	sdkMetadata *MetaplayVersionMetadata,
	rootPath string,
	pathToUnityProject string,
	pathToMetaplaySdk string,
	sharedCodePath string,
	gameBackendPath string,
	customDashboardPath string,
	project *portalapi.ProjectInfo,
	environments []portalapi.EnvironmentInfo) (*ProjectConfig, error) {

	yamlContent, projectConfig, err := RenderProjectConfigYAML(
		sdkMetadata,
		pathToUnityProject,
		pathToMetaplaySdk,
		sharedCodePath,
		gameBackendPath,
		customDashboardPath,
		project,
		environments,
	)
	if err != nil {
		log.Panic().Msgf("%v", err)
	}

	// Write metaplay-project.yaml.
	configFilePath := filepath.Join(rootPath, "metaplay-project.yaml")
	log.Debug().Msgf("Write project configuration to: %s", configFilePath)
	if err := os.WriteFile(configFilePath, []byte(yamlContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write project configuration file: %w", err)
	}

	return projectConfig, nil
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
	var parsedData map[string]any
	if err := yaml.Unmarshal(data, &parsedData); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}

	return nil
}
