/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package metaproj

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/go-version"
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/metaplay/cli/pkg/sitedeploy"
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
		unityProjectDir string
		expected        string
	}{
		{
			name:            "empty environments produces null, not empty list",
			environments:    []portalapi.EnvironmentInfo{},
			customDashboard: "",
			unityProjectDir: "Unity",
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
			unityProjectDir: "Unity",
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
			unityProjectDir: "Unity",
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
			unityProjectDir: "Unity",
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
			unityProjectDir: "Unity",
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
		{
			name:            "empty unity project dir omits the field",
			environments:    []portalapi.EnvironmentInfo{},
			customDashboard: "",
			unityProjectDir: "",
			expected: `# Configure schema to use.
# yaml-language-server: $schema=MetaplaySDK/projectConfigSchema.json
$schema: "MetaplaySDK/projectConfigSchema.json"

# Configure project.
projectID: test-project
buildRootDir: .
sdkRootDir: MetaplaySDK
backendDir: Backend
sharedCodeDir: SharedCode

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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			yamlContent, config, err := RenderProjectConfigYAML(
				sdkMetadata,
				tc.unityProjectDir,
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
			if config.UnityProjectDir != tc.unityProjectDir {
				t.Errorf("unityProjectDir: got %q, want %q", config.UnityProjectDir, tc.unityProjectDir)
			}
		})
	}
}

// User-provided paths and names must parse back unchanged, including characters that YAML
// reads as null, comments, anchors or collections, or that HTML escaping would mangle.
func TestRenderProjectConfigYAML_RoundTripsSpecialCharacters(t *testing.T) {
	values := []string{".", "Unity", "null", "~", "yes", "#Unity", "Unity #2", "[Unity]", "&Unity", "Tom's Game", "R&D+1", "<Unity>"}
	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			envs := []portalapi.EnvironmentInfo{{Name: value, HumanID: "lovely-wombats-build-nimbly", Type: portalapi.EnvironmentTypeDevelopment, StackDomain: "dev.example.com"}}
			sharedCodeDir := value + "/Assets/SharedCode"
			yamlContent, config, err := RenderProjectConfigYAML(createTestSdkMetadata(), value, value+"/MetaplaySDK", sharedCodeDir, value+"/Backend", value+"/Dashboard", createTestProjectInfo("test-project"), envs)
			if err != nil {
				t.Fatalf("RenderProjectConfigYAML failed: %v", err)
			}

			got := map[string]string{
				"unityProjectDir": config.UnityProjectDir,
				"sdkRootDir":      config.SdkRootDir,
				"sharedCodeDir":   config.SharedCodeDir,
				"backendDir":      config.BackendDir,
				"rootDir":         config.Features.Dashboard.RootDir,
				"name":            config.Environments[0].Name,
			}
			want := map[string]string{
				"unityProjectDir": value,
				"sdkRootDir":      value + "/MetaplaySDK",
				"sharedCodeDir":   sharedCodeDir,
				"backendDir":      value + "/Backend",
				"rootDir":         value + "/Dashboard",
				"name":            value,
			}
			for field, wantValue := range want {
				if got[field] != wantValue {
					t.Errorf("%s: got %q, want %q\nFull YAML:\n%s", field, got[field], wantValue, yamlContent)
				}
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

// The authProviders key identifies a provider, but the map value cannot see it.
// Stamping it on load lets an error raised in pkg/auth name the argument that
// resolves back to this provider.
func TestApplyProjectConfigDefaults_StampsAuthProviderKeys(t *testing.T) {
	config := &ProjectConfig{
		AuthProviders: map[string]*auth.AuthProviderConfig{
			"corp": {Name: "Corp SSO", ClientID: "corp-client"},
			"alt":  {Name: "Corp SSO", ClientID: "alt-client"},
		},
	}

	if err := ApplyProjectConfigDefaults(config); err != nil {
		t.Fatalf("ApplyProjectConfigDefaults returned an error: %v", err)
	}

	// Both share a display name, so only the key tells them apart.
	if got := config.AuthProviders["corp"].LoginCommand(); got != "metaplay auth login corp" {
		t.Errorf("LoginCommand() = %q, want it to name the key 'corp'", got)
	}
	if got := config.AuthProviders["alt"].LoginCommand(); got != "metaplay auth login alt" {
		t.Errorf("LoginCommand() = %q, want it to name the key 'alt'", got)
	}
}

// A nil map is the common case for a project that defines no providers of its own.
func TestApplyProjectConfigDefaults_NoAuthProviders(t *testing.T) {
	if err := ApplyProjectConfigDefaults(&ProjectConfig{}); err != nil {
		t.Fatalf("ApplyProjectConfigDefaults returned an error: %v", err)
	}
}

// unityProjectDir is optional, but a value that is given must still point to a directory.
func TestValidateProjectConfig_UnityProjectDir(t *testing.T) {
	projectDir := t.TempDir()
	for _, dir := range []string{"sdk", "Backend", "Shared", "Unity"} {
		if err := os.Mkdir(filepath.Join(projectDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	testCases := []struct {
		name            string
		unityProjectDir string
		wantErr         bool
	}{
		{name: "missing", unityProjectDir: "", wantErr: false},
		{name: "valid", unityProjectDir: "Unity", wantErr: false},
		{name: "nonexistent", unityProjectDir: "NoSuchDir", wantErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := &ProjectConfig{
				ProjectHumanID:        "test-project",
				BuildRootDir:          ".",
				SdkRootDir:            "sdk",
				BackendDir:            "Backend",
				SharedCodeDir:         "Shared",
				UnityProjectDir:       tc.unityProjectDir,
				DotnetRuntimeVersion:  version.Must(version.NewVersion("10.0")),
				ServerChartVersion:    "0.9.0",
				BotClientChartVersion: "0.9.0",
			}
			err := ValidateProjectConfig(projectDir, config)
			if tc.wantErr && err == nil {
				t.Errorf("expected an error for unityProjectDir %q", tc.unityProjectDir)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for unityProjectDir %q: %v", tc.unityProjectDir, err)
			}
		})
	}
}

// webClients entries are checked when the project loads, so a mistake in one is
// reported before a deploy starts. Their sourceDir is build output and need not
// exist yet.
func TestValidateProjectConfig_WebClients(t *testing.T) {
	projectDir := t.TempDir()
	for _, dir := range []string{"sdk", "Backend", "Shared"} {
		if err := os.Mkdir(filepath.Join(projectDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	blazor := func(name string) WebClientConfig {
		return WebClientConfig{Name: name, SourceDir: "Client/publish/wwwroot", Config: sitedeploy.Config{Preset: "blazor"}}
	}

	testCases := []struct {
		name       string
		webClients []WebClientConfig
		wantErr    bool
	}{
		{name: "none", webClients: nil, wantErr: false},
		{name: "valid", webClients: []WebClientConfig{blazor("webclient"), blazor("other")}, wantErr: false},
		{name: "missing name", webClients: []WebClientConfig{blazor("")}, wantErr: true},
		{name: "invalid name", webClients: []WebClientConfig{blazor("web client")}, wantErr: true},
		{name: "duplicate name", webClients: []WebClientConfig{blazor("webclient"), blazor("webclient")}, wantErr: true},
		{name: "missing sourceDir", webClients: []WebClientConfig{{Name: "webclient"}}, wantErr: true},
		{name: "unknown preset", webClients: []WebClientConfig{{Name: "webclient", SourceDir: "x", Config: sitedeploy.Config{Preset: "nope"}}}, wantErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := &ProjectConfig{
				ProjectHumanID:        "test-project",
				BuildRootDir:          ".",
				SdkRootDir:            "sdk",
				BackendDir:            "Backend",
				SharedCodeDir:         "Shared",
				DotnetRuntimeVersion:  version.Must(version.NewVersion("10.0")),
				ServerChartVersion:    "0.9.0",
				BotClientChartVersion: "0.9.0",
				WebClients:            tc.webClients,
			}
			err := ValidateProjectConfig(projectDir, config)
			if tc.wantErr && err == nil {
				t.Errorf("expected an error")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// A project whose SDK has not been installed yet must still load. The missing
// SDK is reported when the metadata is needed, with a hint at the config.
func TestSdkMetadata_MissingSdk(t *testing.T) {
	t.Chdir(t.TempDir())

	project, err := NewMetaplayProject(".", &ProjectConfig{SdkRootDir: "MetaplaySDK"})
	if err != nil {
		t.Fatalf("NewMetaplayProject returned an error: %v", err)
	}

	metadata, err := project.SdkMetadata()
	if err == nil {
		t.Fatalf("SdkMetadata() = %+v, want an error for the missing SDK", metadata)
	}
	if !errors.Is(err, ErrSdkNotFound) {
		t.Errorf("SdkMetadata() error = %v, want it to wrap ErrSdkNotFound", err)
	}
	cliErr, ok := errors.AsType[*clierrors.CLIError](err)
	if !ok {
		t.Fatalf("SdkMetadata() error = %T, want *CLIError", err)
	}
	if !strings.Contains(cliErr.Suggestion, "sdkRootDir") {
		t.Errorf("Suggestion = %q, want it to mention 'sdkRootDir'", cliErr.Suggestion)
	}
}
