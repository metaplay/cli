/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package metaproj

import (
	"github.com/hashicorp/go-version"
	"github.com/metaplay/cli/pkg/auth"
)

// Name of the Metaplay project config file.
const ConfigFileName = "metaplay-project.yaml"

// Configuration for dashboard ($.features.dashboard in metaplay-project.yaml).
type DashboardFeatureConfig struct {
	UseCustom bool   `yaml:"useCustom"`
	RootDir   string `yaml:"rootDir"`
}

// Configuration for features ($.features in metaplay-project.yaml).
type ProjectFeaturesConfig struct {
	Dashboard DashboardFeatureConfig `yaml:"dashboard"`
}

// IntegrationTestsConfig configures integration test behavior ($.integrationTests in metaplay-project.yaml).
type IntegrationTestsConfig struct {
	Docker    *IntegrationTestDockerConfig    `yaml:"docker,omitempty"`
	Server    *IntegrationTestContainerConfig `yaml:"server,omitempty"`
	BotClient *IntegrationTestContainerConfig `yaml:"botClient,omitempty"`
}

// IntegrationTestDockerConfig configures docker build options for integration tests.
type IntegrationTestDockerConfig struct {
	BuildArgs []string `yaml:"buildArgs,omitempty"`
}

// IntegrationTestContainerConfig configures container runtime options for integration tests.
type IntegrationTestContainerConfig struct {
	Args []string          `yaml:"args,omitempty"`
	Env  map[string]string `yaml:"env,omitempty"`
}

// Metaplay project config file, named `metaplay-project.yaml`.
// Note: When adding new fields, remember to update ValidateProjectConfig().
type ProjectConfig struct {
	ProjectHumanID  string `yaml:"projectID"`       // The project's human ID (as in the portal)
	BuildRootDir    string `yaml:"buildRootDir"`    // Relative path to the docker build root directory
	SdkRootDir      string `yaml:"sdkRootDir"`      // Relative path to the MetaplaySDK directory
	BackendDir      string `yaml:"backendDir"`      // Relative path to the project-specific backend directory
	SharedCodeDir   string `yaml:"sharedCodeDir"`   // Relative path to the shared code directory
	UnityProjectDir string `yaml:"unityProjectDir"` // Relative path to the Unity (client) project

	DotnetRuntimeVersion *version.Version `yaml:"dotnetRuntimeVersion"` // .NET runtime version that the project is using (major.minor), eg, '8.0' or '9.0'

	HelmChartRepository   string `yaml:"helmChartRepository"`   // Helm chart repository to use (defaults to 'https://charts.metaplay.dev')
	ServerChartVersion    string `yaml:"serverChartVersion"`    // Version of the game server Helm chart to use (or 'latest-prerelease' for absolute latest)
	BotClientChartVersion string `yaml:"botClientChartVersion"` // Version of the bot client Helm chart to use (or 'latest-prerelease' for absolute latest)

	AuthProviders map[string]*auth.AuthProviderConfig `yaml:"authProviders,omitempty"`

	Features ProjectFeaturesConfig `yaml:"features"`

	IntegrationTests *IntegrationTestsConfig `yaml:"integrationTests,omitempty"`

	Environments []ProjectEnvironmentConfig `yaml:"environments"`
}
