/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package metaproj

import (
	"strings"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/metaplay/cli/pkg/portalapi"
	"gopkg.in/yaml.v3"
)

func createTestSdkMetadata() *MetaplayVersionMetadata {
	serverVersion, _ := version.NewVersion("1.0.0")
	botVersion, _ := version.NewVersion("1.0.0")
	return &MetaplayVersionMetadata{
		DefaultDotnetRuntimeVersion:  "8.0",
		DefaultServerChartVersion:    serverVersion,
		DefaultBotClientChartVersion: botVersion,
	}
}

func createTestProjectInfo(humanID string) *portalapi.ProjectInfo {
	return &portalapi.ProjectInfo{
		HumanID: humanID,
		Name:    "Test Project",
	}
}

func TestRenderProjectConfigYAML(t *testing.T) {
	sdkMetadata := createTestSdkMetadata()
	project := createTestProjectInfo("test-project")

	singleEnv := []portalapi.EnvironmentInfo{
		{
			Name:        "dev",
			HumanID:     "lovely-wombats-build-nimbly",
			Type:        portalapi.EnvironmentTypeDevelopment,
			StackDomain: "dev.example.com",
		},
	}

	multipleEnvs := []portalapi.EnvironmentInfo{
		{
			Name:        "dev",
			HumanID:     "lovely-wombats-build-nimbly",
			Type:        portalapi.EnvironmentTypeDevelopment,
			StackDomain: "dev.example.com",
		},
		{
			Name:        "staging",
			HumanID:     "lovely-wombats-build-nimbly",
			Type:        portalapi.EnvironmentTypeStaging,
			StackDomain: "staging.example.com",
		},
		{
			Name:        "prod",
			HumanID:     "lovely-wombats-build-nimbly",
			Type:        portalapi.EnvironmentTypeProduction,
			StackDomain: "prod.example.com",
		},
	}

	tests := []struct {
		name            string
		environments    []portalapi.EnvironmentInfo
		customDashboard string
		expected        string
	}{
		{
			name:            "empty environments produces null, not empty list",
			environments:    []portalapi.EnvironmentInfo{},
			customDashboard: "",
			expected: `# Configure schema to use.
# yaml-language-server: $schema=MetaplaySDK/projectConfigSchema.json
$schema: "MetaplaySDK/projectConfigSchema.json"

# Configure project.
projectID: test-project
buildRootDir: .
sdkRootDir: MetaplaySDK
backendDir: Backend
sharedCodeDir: SharedCode
unityProjectDir: Unity

# Specify .NET runtime version to build project for, only '<major>.<minor>'.
dotnetRuntimeVersion: "8.0"

# Specify Helm chart versions to use for server and bot deployments.
serverChartVersion: 1.0.0
botClientChartVersion: 1.0.0

# Customize Metaplay features used in the game.
features:
  # Configure LiveOps Dashboard.
  dashboard:
    useCustom: false
    rootDir: 

# Project environments.
environments:
`,
		},
		{
			name:            "nil environments produces null, not empty list",
			environments:    nil,
			customDashboard: "",
			expected: `# Configure schema to use.
# yaml-language-server: $schema=MetaplaySDK/projectConfigSchema.json
$schema: "MetaplaySDK/projectConfigSchema.json"

# Configure project.
projectID: test-project
buildRootDir: .
sdkRootDir: MetaplaySDK
backendDir: Backend
sharedCodeDir: SharedCode
unityProjectDir: Unity

# Specify .NET runtime version to build project for, only '<major>.<minor>'.
dotnetRuntimeVersion: "8.0"

# Specify Helm chart versions to use for server and bot deployments.
serverChartVersion: 1.0.0
botClientChartVersion: 1.0.0

# Customize Metaplay features used in the game.
features:
  # Configure LiveOps Dashboard.
  dashboard:
    useCustom: false
    rootDir: 

# Project environments.
environments:
`,
		},
		{
			name:            "single environment with correct indentation",
			environments:    singleEnv,
			customDashboard: "",
			expected: `# Configure schema to use.
# yaml-language-server: $schema=MetaplaySDK/projectConfigSchema.json
$schema: "MetaplaySDK/projectConfigSchema.json"

# Configure project.
projectID: test-project
buildRootDir: .
sdkRootDir: MetaplaySDK
backendDir: Backend
sharedCodeDir: SharedCode
unityProjectDir: Unity

# Specify .NET runtime version to build project for, only '<major>.<minor>'.
dotnetRuntimeVersion: "8.0"

# Specify Helm chart versions to use for server and bot deployments.
serverChartVersion: 1.0.0
botClientChartVersion: 1.0.0

# Customize Metaplay features used in the game.
features:
  # Configure LiveOps Dashboard.
  dashboard:
    useCustom: false
    rootDir: 

# Project environments.
environments:
  - name: dev
    humanId: lovely-wombats-build-nimbly
    type: development
    stackDomain: dev.example.com
`,
		},
		{
			name:            "multiple environments with correct indentation",
			environments:    multipleEnvs,
			customDashboard: "",
			expected: `# Configure schema to use.
# yaml-language-server: $schema=MetaplaySDK/projectConfigSchema.json
$schema: "MetaplaySDK/projectConfigSchema.json"

# Configure project.
projectID: test-project
buildRootDir: .
sdkRootDir: MetaplaySDK
backendDir: Backend
sharedCodeDir: SharedCode
unityProjectDir: Unity

# Specify .NET runtime version to build project for, only '<major>.<minor>'.
dotnetRuntimeVersion: "8.0"

# Specify Helm chart versions to use for server and bot deployments.
serverChartVersion: 1.0.0
botClientChartVersion: 1.0.0

# Customize Metaplay features used in the game.
features:
  # Configure LiveOps Dashboard.
  dashboard:
    useCustom: false
    rootDir: 

# Project environments.
environments:
  - name: dev
    humanId: lovely-wombats-build-nimbly
    type: development
    stackDomain: dev.example.com
  - name: staging
    humanId: lovely-wombats-build-nimbly
    type: staging
    stackDomain: staging.example.com
  - name: prod
    humanId: lovely-wombats-build-nimbly
    type: production
    stackDomain: prod.example.com
`,
		},
		{
			name:            "custom dashboard enabled",
			environments:    []portalapi.EnvironmentInfo{},
			customDashboard: "CustomDashboard",
			expected: `# Configure schema to use.
# yaml-language-server: $schema=MetaplaySDK/projectConfigSchema.json
$schema: "MetaplaySDK/projectConfigSchema.json"

# Configure project.
projectID: test-project
buildRootDir: .
sdkRootDir: MetaplaySDK
backendDir: Backend
sharedCodeDir: SharedCode
unityProjectDir: Unity

# Specify .NET runtime version to build project for, only '<major>.<minor>'.
dotnetRuntimeVersion: "8.0"

# Specify Helm chart versions to use for server and bot deployments.
serverChartVersion: 1.0.0
botClientChartVersion: 1.0.0

# Customize Metaplay features used in the game.
features:
  # Configure LiveOps Dashboard.
  dashboard:
    useCustom: true
    rootDir: CustomDashboard

# Project environments.
environments:
`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			yamlContent, config, err := RenderProjectConfigYAML(
				sdkMetadata,
				"Unity",
				"MetaplaySDK",
				"SharedCode",
				"Backend",
				tc.customDashboard,
				project,
				tc.environments,
			)

			if err != nil {
				t.Fatalf("RenderProjectConfigYAML failed: %v", err)
			}
			if config == nil {
				t.Fatal("Expected non-nil config")
			}

			if yamlContent != tc.expected {
				t.Errorf("Output mismatch.\nExpected:\n%s\nGot:\n%s", tc.expected, yamlContent)
			}
		})
	}
}

// Test validateAlias function
func TestValidateAlias(t *testing.T) {
	tests := []struct {
		alias   string
		isValid bool
	}{
		// Valid aliases
		{"dev", true},
		{"prod", true},
		{"staging", true},
		{"my-env", true},
		{"env1", true},
		{"a", true},
		{"abc123", true},
		{"my-long-alias-name", true},

		// Invalid aliases - empty
		{"", false},

		// Invalid aliases - too long (over 30 chars)
		{"this-is-a-very-long-alias-name-that-exceeds-limit", false},

		// Invalid aliases - invalid characters
		{"Dev", false},     // uppercase
		{"PROD", false},    // all uppercase
		{"my_env", false},  // underscore
		{"my.env", false},  // dot
		{"my env", false},  // space
		{"-dev", false},    // starts with dash
		{"dev-", false},    // ends with dash
		{"my--env", false}, // double dash

		// Reserved aliases
		{"all", false},
		{"default", false},
		{"local", false},
		{"localhost", false},
		{"none", false},
		{"self", false},
		{"metaplay", false},
	}

	for _, test := range tests {
		t.Run(test.alias, func(t *testing.T) {
			err := validateAlias(test.alias)
			if test.isValid && err != nil {
				t.Errorf("Expected alias '%s' to be valid, got error: %v", test.alias, err)
			}
			if !test.isValid && err == nil {
				t.Errorf("Expected alias '%s' to be invalid, but no error returned", test.alias)
			}
		})
	}
}

// Test FindEnvironmentConfig with aliases
func TestFindEnvironmentConfig_ByAlias(t *testing.T) {
	config := &ProjectConfig{
		ProjectHumanID: "test-project",
		Environments: []ProjectEnvironmentConfig{
			{
				Name:        "Development",
				HumanID:     "test-project-dev",
				Type:        portalapi.EnvironmentTypeDevelopment,
				StackDomain: "dev.example.com",
				Aliases:     []string{"dev", "develop"},
			},
			{
				Name:        "Production",
				HumanID:     "test-project-prod",
				Type:        portalapi.EnvironmentTypeProduction,
				StackDomain: "prod.example.com",
				Aliases:     []string{"prod", "live"},
			},
		},
	}

	tests := []struct {
		input       string
		expectedEnv string
		shouldFind  bool
	}{
		// Match by HumanID
		{"test-project-dev", "Development", true},
		{"test-project-prod", "Production", true},

		// Match by HumanID suffix
		{"dev", "Development", true},
		{"prod", "Production", true},

		// Match by Name
		{"Development", "Development", true},
		{"Production", "Production", true},

		// Match by alias
		{"develop", "Development", true},
		{"live", "Production", true},

		// No match
		{"nonexistent", "", false},
		{"staging", "", false},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			env, err := config.FindEnvironmentConfig(test.input)
			if test.shouldFind {
				if err != nil {
					t.Errorf("Expected to find environment for '%s', got error: %v", test.input, err)
				}
				if env != nil && env.Name != test.expectedEnv {
					t.Errorf("Expected environment '%s', got '%s'", test.expectedEnv, env.Name)
				}
			} else {
				if err == nil {
					t.Errorf("Expected error for '%s', but found environment '%s'", test.input, env.Name)
				}
			}
		})
	}
}

// Test that formatEnvironmentList returns a readable list with names and aliases
func TestFormatEnvironmentList(t *testing.T) {
	config := &ProjectConfig{
		Environments: []ProjectEnvironmentConfig{
			{
				Name:    "Development",
				HumanID: "test-dev",
				Aliases: []string{"dev", "develop"},
			},
			{
				Name:    "Production",
				HumanID: "test-prod",
			},
		},
	}

	result := formatEnvironmentList(config)

	// Should contain environment humanIDs and names
	if !strings.Contains(result, "test-dev") {
		t.Error("Expected result to contain 'test-dev'")
	}
	if !strings.Contains(result, "Development") {
		t.Error("Expected result to contain 'Development'")
	}
	if !strings.Contains(result, "test-prod") {
		t.Error("Expected result to contain 'test-prod'")
	}
	if !strings.Contains(result, "Production") {
		t.Error("Expected result to contain 'Production'")
	}

	// Should contain aliases for the dev environment
	if !strings.Contains(result, "dev") {
		t.Error("Expected result to contain alias 'dev'")
	}
	if !strings.Contains(result, "develop") {
		t.Error("Expected result to contain alias 'develop'")
	}
}

// Test parsing integrationTests config section
func TestParseIntegrationTestsConfig(t *testing.T) {
	tests := []struct {
		name     string
		yamlData string
		validate func(t *testing.T, config *ProjectConfig)
	}{
		{
			name:     "no integrationTests section",
			yamlData: `projectID: test-project`,
			validate: func(t *testing.T, config *ProjectConfig) {
				if config.IntegrationTests != nil {
					t.Error("Expected IntegrationTests to be nil when not specified")
				}
			},
		},
		{
			name: "empty integrationTests section",
			yamlData: `projectID: test-project
integrationTests: {}`,
			validate: func(t *testing.T, config *ProjectConfig) {
				if config.IntegrationTests == nil {
					t.Error("Expected IntegrationTests to be non-nil")
					return
				}
				if config.IntegrationTests.Docker != nil {
					t.Error("Expected Docker to be nil")
				}
				if config.IntegrationTests.Server != nil {
					t.Error("Expected Server to be nil")
				}
				if config.IntegrationTests.BotClient != nil {
					t.Error("Expected BotClient to be nil")
				}
			},
		},
		{
			name: "docker buildArgs only",
			yamlData: `projectID: test-project
integrationTests:
  docker:
    buildArgs:
      - "--build-arg"
      - "INCLUDE_DEBUG_TOOLS=true"`,
			validate: func(t *testing.T, config *ProjectConfig) {
				if config.IntegrationTests == nil {
					t.Error("Expected IntegrationTests to be non-nil")
					return
				}
				if config.IntegrationTests.Docker == nil {
					t.Error("Expected Docker to be non-nil")
					return
				}
				if len(config.IntegrationTests.Docker.BuildArgs) != 2 {
					t.Errorf("Expected 2 build args, got %d", len(config.IntegrationTests.Docker.BuildArgs))
				}
				if config.IntegrationTests.Docker.BuildArgs[0] != "--build-arg" {
					t.Errorf("Expected first arg to be '--build-arg', got '%s'", config.IntegrationTests.Docker.BuildArgs[0])
				}
				if config.IntegrationTests.Docker.BuildArgs[1] != "INCLUDE_DEBUG_TOOLS=true" {
					t.Errorf("Expected second arg to be 'INCLUDE_DEBUG_TOOLS=true', got '%s'", config.IntegrationTests.Docker.BuildArgs[1])
				}
			},
		},
		{
			name: "server args and env",
			yamlData: `projectID: test-project
integrationTests:
  server:
    args:
      - "--Clustering:ClusteringPort=6000"
      - "--Metrics:PrometheusPort=9999"
    env:
      MY_CUSTOM_FLAG: "true"
      DEBUG_LEVEL: "verbose"`,
			validate: func(t *testing.T, config *ProjectConfig) {
				if config.IntegrationTests == nil {
					t.Error("Expected IntegrationTests to be non-nil")
					return
				}
				if config.IntegrationTests.Server == nil {
					t.Error("Expected Server to be non-nil")
					return
				}
				if len(config.IntegrationTests.Server.Args) != 2 {
					t.Errorf("Expected 2 server args, got %d", len(config.IntegrationTests.Server.Args))
				}
				if config.IntegrationTests.Server.Args[0] != "--Clustering:ClusteringPort=6000" {
					t.Errorf("Expected first arg to be '--Clustering:ClusteringPort=6000', got '%s'", config.IntegrationTests.Server.Args[0])
				}
				if len(config.IntegrationTests.Server.Env) != 2 {
					t.Errorf("Expected 2 env vars, got %d", len(config.IntegrationTests.Server.Env))
				}
				if config.IntegrationTests.Server.Env["MY_CUSTOM_FLAG"] != "true" {
					t.Errorf("Expected MY_CUSTOM_FLAG to be 'true', got '%s'", config.IntegrationTests.Server.Env["MY_CUSTOM_FLAG"])
				}
				if config.IntegrationTests.Server.Env["DEBUG_LEVEL"] != "verbose" {
					t.Errorf("Expected DEBUG_LEVEL to be 'verbose', got '%s'", config.IntegrationTests.Server.Env["DEBUG_LEVEL"])
				}
			},
		},
		{
			name: "botClient args and env",
			yamlData: `projectID: test-project
integrationTests:
  botClient:
    args:
      - "-ExitAfter=00:01:00"
      - "-MaxBots=50"
      - "-SpawnRate=5"
    env:
      BOT_BEHAVIOR_MODE: "stress"`,
			validate: func(t *testing.T, config *ProjectConfig) {
				if config.IntegrationTests == nil {
					t.Error("Expected IntegrationTests to be non-nil")
					return
				}
				if config.IntegrationTests.BotClient == nil {
					t.Error("Expected BotClient to be non-nil")
					return
				}
				if len(config.IntegrationTests.BotClient.Args) != 3 {
					t.Errorf("Expected 3 botclient args, got %d", len(config.IntegrationTests.BotClient.Args))
				}
				if config.IntegrationTests.BotClient.Args[0] != "-ExitAfter=00:01:00" {
					t.Errorf("Expected first arg to be '-ExitAfter=00:01:00', got '%s'", config.IntegrationTests.BotClient.Args[0])
				}
				if len(config.IntegrationTests.BotClient.Env) != 1 {
					t.Errorf("Expected 1 env var, got %d", len(config.IntegrationTests.BotClient.Env))
				}
				if config.IntegrationTests.BotClient.Env["BOT_BEHAVIOR_MODE"] != "stress" {
					t.Errorf("Expected BOT_BEHAVIOR_MODE to be 'stress', got '%s'", config.IntegrationTests.BotClient.Env["BOT_BEHAVIOR_MODE"])
				}
			},
		},
		{
			name: "full configuration",
			yamlData: `projectID: test-project
integrationTests:
  docker:
    buildArgs:
      - "--build-arg"
      - "INCLUDE_DEBUG_TOOLS=true"
  server:
    args:
      - "--Clustering:ClusteringPort=6000"
    env:
      MY_CUSTOM_FLAG: "true"
  botClient:
    args:
      - "-MaxBots=50"
    env:
      BOT_BEHAVIOR_MODE: "stress"`,
			validate: func(t *testing.T, config *ProjectConfig) {
				if config.IntegrationTests == nil {
					t.Error("Expected IntegrationTests to be non-nil")
					return
				}
				if config.IntegrationTests.Docker == nil {
					t.Error("Expected Docker to be non-nil")
				}
				if config.IntegrationTests.Server == nil {
					t.Error("Expected Server to be non-nil")
				}
				if config.IntegrationTests.BotClient == nil {
					t.Error("Expected BotClient to be non-nil")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var config ProjectConfig
			err := yaml.Unmarshal([]byte(tc.yamlData), &config)
			if err != nil {
				t.Fatalf("Failed to parse YAML: %v", err)
			}
			tc.validate(t, &config)
		})
	}
}
