/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package metaproj

import (
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

func TestRenderProjectConfigYAML_EmptyEnvironments(t *testing.T) {
	sdkMetadata := createTestSdkMetadata()
	project := createTestProjectInfo("test-project")

	yamlContent, config, err := RenderProjectConfigYAML(
		sdkMetadata,
		"Unity",
		"MetaplaySDK",
		"SharedCode",
		"Backend",
		"",
		project,
		[]portalapi.EnvironmentInfo{},
	)

	if err != nil {
		t.Fatalf("RenderProjectConfigYAML failed: %v", err)
	}
	if config == nil {
		t.Fatal("Expected non-nil config")
	}
	if yamlContent == "" {
		t.Fatal("Expected non-empty YAML content")
	}

	// Verify it parses as valid YAML
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(yamlContent), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v\nContent:\n%s", err, yamlContent)
	}

	// Verify environments key exists and is null (implicit null from "environments:" with no value)
	envs, ok := parsed["environments"]
	if !ok {
		t.Fatal("Expected 'environments' key in YAML")
	}
	if envs != nil {
		t.Fatalf("Expected 'environments' to be null, got %T: %v", envs, envs)
	}
}

func TestRenderProjectConfigYAML_NilEnvironments(t *testing.T) {
	sdkMetadata := createTestSdkMetadata()
	project := createTestProjectInfo("test-project")

	yamlContent, config, err := RenderProjectConfigYAML(
		sdkMetadata,
		"Unity",
		"MetaplaySDK",
		"SharedCode",
		"Backend",
		"",
		project,
		nil,
	)

	if err != nil {
		t.Fatalf("RenderProjectConfigYAML failed: %v", err)
	}
	if config == nil {
		t.Fatal("Expected non-nil config")
	}

	// Verify it parses as valid YAML
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(yamlContent), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v\nContent:\n%s", err, yamlContent)
	}

	// Verify environments key exists and is null
	envs, ok := parsed["environments"]
	if !ok {
		t.Fatal("Expected 'environments' key in YAML")
	}
	if envs != nil {
		t.Fatalf("Expected 'environments' to be null, got %T: %v", envs, envs)
	}
}

func TestRenderProjectConfigYAML_SingleEnvironment(t *testing.T) {
	sdkMetadata := createTestSdkMetadata()
	project := createTestProjectInfo("test-project")
	environments := []portalapi.EnvironmentInfo{
		{
			Name:        "dev",
			HumanID:     "tiny-squids",
			Type:        portalapi.EnvironmentTypeDevelopment,
			StackDomain: "dev.example.com",
		},
	}

	yamlContent, config, err := RenderProjectConfigYAML(
		sdkMetadata,
		"Unity",
		"MetaplaySDK",
		"SharedCode",
		"Backend",
		"",
		project,
		environments,
	)

	if err != nil {
		t.Fatalf("RenderProjectConfigYAML failed: %v", err)
	}
	if config == nil {
		t.Fatal("Expected non-nil config")
	}

	// Verify it parses as valid YAML
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(yamlContent), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v\nContent:\n%s", err, yamlContent)
	}

	// Verify environments has one entry
	envs, ok := parsed["environments"].([]interface{})
	if !ok {
		t.Fatal("Expected 'environments' to be a list")
	}
	if len(envs) != 1 {
		t.Fatalf("Expected 1 environment, got %d", len(envs))
	}

	env := envs[0].(map[string]interface{})
	if env["name"] != "dev" {
		t.Errorf("Expected environment name 'dev', got '%v'", env["name"])
	}
	if env["humanId"] != "tiny-squids" {
		t.Errorf("Expected humanId 'tiny-squids', got '%v'", env["humanId"])
	}
}

func TestRenderProjectConfigYAML_MultipleEnvironments(t *testing.T) {
	sdkMetadata := createTestSdkMetadata()
	project := createTestProjectInfo("test-project")
	environments := []portalapi.EnvironmentInfo{
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

	yamlContent, config, err := RenderProjectConfigYAML(
		sdkMetadata,
		"Unity",
		"MetaplaySDK",
		"SharedCode",
		"Backend",
		"",
		project,
		environments,
	)

	if err != nil {
		t.Fatalf("RenderProjectConfigYAML failed: %v", err)
	}
	if config == nil {
		t.Fatal("Expected non-nil config")
	}

	// Verify it parses as valid YAML
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(yamlContent), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v\nContent:\n%s", err, yamlContent)
	}

	// Verify environments has three entries
	envs, ok := parsed["environments"].([]interface{})
	if !ok {
		t.Fatal("Expected 'environments' to be a list")
	}
	if len(envs) != 3 {
		t.Fatalf("Expected 3 environments, got %d", len(envs))
	}

	// Verify each environment
	expectedNames := []string{"dev", "staging", "prod"}
	expectedHumanIDs := []string{"tiny-squids", "happy-pandas", "brave-lions"}
	for i, env := range envs {
		envMap := env.(map[string]interface{})
		if envMap["name"] != expectedNames[i] {
			t.Errorf("Environment %d: expected name '%s', got '%v'", i, expectedNames[i], envMap["name"])
		}
		if envMap["humanId"] != expectedHumanIDs[i] {
			t.Errorf("Environment %d: expected humanId '%s', got '%v'", i, expectedHumanIDs[i], envMap["humanId"])
		}
	}
}

func TestRenderProjectConfigYAML_WithCustomDashboard(t *testing.T) {
	sdkMetadata := createTestSdkMetadata()
	project := createTestProjectInfo("test-project")

	yamlContent, config, err := RenderProjectConfigYAML(
		sdkMetadata,
		"Unity",
		"MetaplaySDK",
		"SharedCode",
		"Backend",
		"CustomDashboard",
		project,
		[]portalapi.EnvironmentInfo{},
	)

	if err != nil {
		t.Fatalf("RenderProjectConfigYAML failed: %v", err)
	}
	if config == nil {
		t.Fatal("Expected non-nil config")
	}

	// Verify it parses as valid YAML
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(yamlContent), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v\nContent:\n%s", err, yamlContent)
	}

	// Verify features.dashboard.useCustom is true
	features, ok := parsed["features"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'features' to be a map")
	}
	dashboard, ok := features["dashboard"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'features.dashboard' to be a map")
	}
	if dashboard["useCustom"] != true {
		t.Errorf("Expected useCustom to be true, got %v", dashboard["useCustom"])
	}
	if dashboard["rootDir"] != "CustomDashboard" {
		t.Errorf("Expected rootDir to be 'CustomDashboard', got %v", dashboard["rootDir"])
	}
}
