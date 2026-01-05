/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package metaproj

import (
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/metaplay/cli/pkg/portalapi"
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
			HumanID:     "tiny-squids",
			Type:        portalapi.EnvironmentTypeDevelopment,
			StackDomain: "dev.example.com",
		},
	}

	multipleEnvs := []portalapi.EnvironmentInfo{
		{
			Name:        "dev",
			HumanID:     "tiny-squids",
			Type:        portalapi.EnvironmentTypeDevelopment,
			StackDomain: "dev.example.com",
		},
		{
			Name:        "staging",
			HumanID:     "happy-pandas",
			Type:        portalapi.EnvironmentTypeStaging,
			StackDomain: "staging.example.com",
		},
		{
			Name:        "prod",
			HumanID:     "brave-lions",
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
    humanId: tiny-squids
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
    humanId: tiny-squids
    type: development
    stackDomain: dev.example.com
  - name: staging
    humanId: happy-pandas
    type: staging
    stackDomain: staging.example.com
  - name: prod
    humanId: brave-lions
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
