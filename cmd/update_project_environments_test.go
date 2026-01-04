/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/portalapi"
)

// updateEnvironmentsInYAML is a testable helper that performs the YAML AST manipulation
// for updating environments. It takes the input YAML content and new environments,
// and returns the updated YAML content.
func updateEnvironmentsInYAML(inputYAML string, newPortalEnvironments []portalapi.EnvironmentInfo, existingEnvConfigs []metaproj.ProjectEnvironmentConfig) (string, error) {
	root, err := parser.ParseBytes([]byte(inputYAML), parser.ParseComments)
	if err != nil {
		return "", err
	}

	// Find the 'environments' node
	envsPath, err := yaml.PathString("$.environments")
	if err != nil {
		return "", err
	}

	// Get the environments node
	envsNode, err := envsPath.FilterFile(root)
	if err != nil {
		return "", err
	}

	// Handle the case where environments exists but is null/empty.
	// Only convert null to a sequence if there are environments to add - otherwise keep as null.
	if _, isNull := envsNode.(*ast.NullNode); isNull {
		if len(newPortalEnvironments) == 0 {
			// No environments to add, keep the null node as-is to avoid outputting "environments: []"
			return root.String(), nil
		}

		if err := envsPath.ReplaceWithReader(root, strings.NewReader("[]")); err != nil {
			return "", err
		}
		envsNode, err = envsPath.FilterFile(root)
		if err != nil {
			return "", err
		}
		if seqNode, ok := envsNode.(*ast.SequenceNode); ok {
			seqNode.IsFlowStyle = false
			if seqNode.Start != nil {
				seqNode.Start.Position.Column = 3
				seqNode.Start.Position.IndentNum = 2
			}
		}
	}

	// Cast to sequence node
	envsSeqNode, ok := envsNode.(*ast.SequenceNode)
	if !ok {
		return "", nil
	}

	// Ensure block-style output (not flow-style like []) when there are environments to add.
	// This handles the case where the YAML file already has "environments: []".
	// Also fix indentation when converting from flow-style to block-style.
	if len(newPortalEnvironments) > 0 {
		envsSeqNode.IsFlowStyle = false
		// Reset the Start token position to fix indentation (2 spaces indent)
		if envsSeqNode.Start != nil {
			envsSeqNode.Start.Position.Column = 3
			envsSeqNode.Start.Position.IndentNum = 2
		}
	}

	// Handle all environments from the portal
	for _, portalEnv := range newPortalEnvironments {
		// Find the index of the environment with matching humanId
		foundIndex := -1
		for i, envNode := range envsSeqNode.Values {
			mapNode, ok := envNode.(*ast.MappingNode)
			if !ok {
				continue
			}
			for _, value := range mapNode.Values {
				if value.Key.GetToken().Value == "humanId" && value.Value.GetToken().Value == portalEnv.HumanID {
					foundIndex = i
					break
				}
			}
			if foundIndex != -1 {
				break
			}
		}

		// Initialize new project environment config
		newEnvConfig := metaproj.ProjectEnvironmentConfig{
			Name:        portalEnv.Name,
			HostingType: portalEnv.HostingType,
			HumanID:     portalEnv.HumanID,
			StackDomain: portalEnv.StackDomain,
			Type:        portalEnv.Type,
		}

		// If updating an existing environment, copy non-portal fields
		if foundIndex != -1 {
			for _, oldConfig := range existingEnvConfigs {
				if oldConfig.HumanID == portalEnv.HumanID {
					newEnvConfig.ServerValuesFile = oldConfig.ServerValuesFile
					newEnvConfig.BotClientValuesFile = oldConfig.BotClientValuesFile
					break
				}
			}
		}

		// Convert environment info to YAML
		envYAML, err := yaml.Marshal(newEnvConfig)
		if err != nil {
			return "", err
		}

		// Parse environment YAML to AST
		envAST, err := parser.ParseBytes(envYAML, parser.ParseComments)
		if err != nil {
			return "", err
		}

		// Update an existing node or append a new node
		if foundIndex == -1 {
			envsSeqNode.Values = append(envsSeqNode.Values, envAST.Docs[0].Body)
		} else {
			envsSeqNode.Values[foundIndex] = envAST.Docs[0].Body
		}
	}

	return root.String(), nil
}

func TestUpdateEnvironments(t *testing.T) {
	singleEnv := []portalapi.EnvironmentInfo{
		{
			Name:        "Development",
			HumanID:     "lovely-wombats-build-nimbly",
			Type:        portalapi.EnvironmentTypeDevelopment,
			StackDomain: "dev.metaplay.io",
			HostingType: portalapi.HostingTypeMetaplayHosted,
		},
	}

	twoEnvs := []portalapi.EnvironmentInfo{
		{
			Name:        "Development",
			HumanID:     "tough-falcons",
			Type:        portalapi.EnvironmentTypeDevelopment,
			StackDomain: "dev.metaplay.io",
			HostingType: portalapi.HostingTypeMetaplayHosted,
		},
		{
			Name:        "Production",
			HumanID:     "happy-pandas",
			Type:        portalapi.EnvironmentTypeProduction,
			StackDomain: "prod.metaplay.io",
			HostingType: portalapi.HostingTypeMetaplayHosted,
		},
	}

	tests := []struct {
		name     string
		input    string
		envs     []portalapi.EnvironmentInfo
		expected string
	}{
		{
			name: "null environments with no new envs stays null",
			input: `# Test config
projectID: test-project
environments:
`,
			envs: []portalapi.EnvironmentInfo{},
			expected: `# Test config
projectID: test-project
environments:
`,
		},
		{
			name: "null environments with new env produces block-style with correct indentation",
			input: `# Test config
projectID: test-project
environments:
`,
			envs: singleEnv,
			expected: `# Test config
projectID: test-project
environments:
  - name: Development
    hostingType: metaplay-hosted
    humanId: lovely-wombats-build-nimbly
    type: development
    stackDomain: dev.metaplay.io
`,
		},
		{
			name: "empty list with new env produces block-style with correct indentation",
			input: `# Test config
projectID: test-project
environments: []
`,
			envs: singleEnv,
			expected: `# Test config
projectID: test-project
environments:
  - name: Development
    hostingType: metaplay-hosted
    humanId: lovely-wombats-build-nimbly
    type: development
    stackDomain: dev.metaplay.io
`,
		},
		{
			name: "empty list with multiple envs produces block-style",
			input: `# Test config
projectID: test-project
environments: []
`,
			envs: twoEnvs,
			expected: `# Test config
projectID: test-project
environments:
  - name: Development
    hostingType: metaplay-hosted
    humanId: tough-falcons
    type: development
    stackDomain: dev.metaplay.io
  - name: Production
    hostingType: metaplay-hosted
    humanId: happy-pandas
    type: production
    stackDomain: prod.metaplay.io
`,
		},
		{
			name: "null environments with multiple envs produces block-style",
			input: `# Test config
projectID: test-project
environments:
`,
			envs: twoEnvs,
			expected: `# Test config
projectID: test-project
environments:
  - name: Development
    hostingType: metaplay-hosted
    humanId: tough-falcons
    type: development
    stackDomain: dev.metaplay.io
  - name: Production
    hostingType: metaplay-hosted
    humanId: happy-pandas
    type: production
    stackDomain: prod.metaplay.io
`,
		},
		{
			name: "preserves other fields in the file",
			input: `# Test config
projectID: test-project
buildRootDir: .
sdkRootDir: MetaplaySDK
environments: []
`,
			envs: singleEnv,
			expected: `# Test config
projectID: test-project
buildRootDir: .
sdkRootDir: MetaplaySDK
environments:
  - name: Development
    hostingType: metaplay-hosted
    humanId: lovely-wombats-build-nimbly
    type: development
    stackDomain: dev.metaplay.io
`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := updateEnvironmentsInYAML(tc.input, tc.envs, nil)
			if err != nil {
				t.Fatalf("updateEnvironmentsInYAML failed: %v", err)
			}

			if result != tc.expected {
				t.Errorf("Output mismatch.\nExpected:\n%s\nGot:\n%s", tc.expected, result)
			}
		})
	}
}
