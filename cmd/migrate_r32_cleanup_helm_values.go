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

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type migrateHelmValuesFilesOpts struct {
	UsePositionalArgs
}

// Parsed Helm server deployment values file.
type serverValuesFile struct {
	Environment       string `yaml:"environment"`
	EnvironmentFamily string `yaml:"environmentFamily"`
	Config            struct {
		Files []string `yaml:"files"`
	} `yaml:"config"`
	Tenant struct {
		DiscoveryEnabled bool `yaml:"discoveryEnabled"`
	} `yaml:"tenant"`
	Shards []struct {
		Resources struct {
			Requests struct {
				Memory string `yaml:"memory"`
				CPU    string `yaml:"cpu"`
			} `yaml:"requests"`
		} `yaml:"resources"`
	} `yaml:"shards"`
}

func init() {
	o := migrateHelmValuesFilesOpts{}

	var cmd = &cobra.Command{
		Use:   "cleanup-helm-values",
		Short: "Remove default values from Helm values files",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Remove default values from Helm values files in the project's Backend/Deployments/ directory.
			This migration removes entries that are now considered defaults, including:
			- environment: develop
			- environmentFamily: Development
			- config.files for standard Options.yaml files
			- tenant.discoveryEnabled: true
			- shards with standard resource requests

			{Arguments}
		`),
		Example: trimIndent(`
			# Remove default values from Helm values files
			metaplay migrate cleanup-helm-values
		`)}

	migrateR32Cmd.AddCommand(cmd)
}

func (o *migrateHelmValuesFilesOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *migrateHelmValuesFilesOpts) Run(cmd *cobra.Command) error {
	// Load project config
	project, err := resolveProject()
	if err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Migrate Helm Values Files"))
	log.Info().Msg("")

	// Run the migration
	if err := migrateHelmValuesFiles(project); err != nil {
		log.Error().Msgf("Failed to migrate Helm values files: %s", err)
		os.Exit(1)
	}

	log.Info().Msg("")
	log.Info().Msgf("✅ %s", styles.RenderSuccess("Migration completed successfully!"))
	log.Info().Msg("")
	log.Info().Msg("Next steps:")
	log.Info().Msg("  1. Review the changes")
	log.Info().Msg("  2. Commit and push the changes")

	return nil
}

// migrateHelmValuesFiles scans the Backend/Deployments directory for .yaml files
// and removes default values that are no longer needed
func migrateHelmValuesFiles(project *metaproj.MetaplayProject) error {
	// Get the project's backend deployments directory
	backendDir := project.GetBackendDir()
	deploymentsDir := filepath.Join(backendDir, "Deployments")

	if _, err := os.Stat(deploymentsDir); os.IsNotExist(err) {
		return fmt.Errorf("deployments directory does not exist: %s", deploymentsDir)
	}

	log.Info().Msgf("Update YAML files in: %s", styles.RenderTechnical(deploymentsDir))

	// Find all .yaml files in the directory
	yamlFiles, err := findYamlFiles(deploymentsDir)
	if err != nil {
		return fmt.Errorf("failed to find YAML files: %s", err)
	}

	log.Info().Msg("")
	// log.Info().Msgf("Found %s YAML files", styles.RenderTechnical(fmt.Sprintf("%d", len(yamlFiles))))

	modifiedCount := 0

	// Process each YAML file
	for _, yamlFile := range yamlFiles {
		log.Info().Msgf("%s:", styles.RenderTechnical(filepath.Base(yamlFile)))

		// Read the file
		content, err := os.ReadFile(yamlFile)
		if err != nil {
			log.Warn().Msgf("Failed to read file %s: %s", yamlFile, err)
			continue
		}

		// Parse the YAML to AST
		astFile, err := parser.ParseBytes(content, parser.ParseComments)
		if err != nil {
			panic(err)
		}

		// Parse the YAML to struct
		var serverValues serverValuesFile
		if err := yaml.Unmarshal(content, &serverValues); err != nil {
			panic(err)
		}

		// Check if the file was modified by the migration
		removeDefaultValues(astFile, &serverValues)

		// Render back to text
		updatedContent := []byte(astFile.String())

		// If the content has changed, write the updated content back to the file
		if !bytes.Equal(content, updatedContent) {
			// Write the updated YAML back to the file
			if err := os.WriteFile(yamlFile, updatedContent, 0644); err != nil {
				log.Warn().Msgf("Failed to write updated YAML to file %s: %s", yamlFile, err)
				continue
			}

			modifiedCount++
			log.Info().Msgf("  %s Updated!", styles.RenderSuccess("✓"))
		} else {
			log.Info().Msgf("  %s No changes", styles.RenderSuccess("✓"))
		}
	}

	// log.Info().Msgf("Modified %d out of %d files", modifiedCount, len(yamlFiles))

	return nil
}

// findYamlFiles returns all .yaml files in the specified directory and its subdirectories
func findYamlFiles(rootDir string) ([]string, error) {
	var yamlFiles []string

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Only process regular files (not directories)
		if !info.IsDir() {
			// Check if the file has a .yaml or .yml extension
			if strings.HasSuffix(strings.ToLower(path), ".yaml") || strings.HasSuffix(strings.ToLower(path), ".yml") {
				yamlFiles = append(yamlFiles, path)
			}
		}

		return nil
	})

	return yamlFiles, err
}

// removeDefaultValues removes default values from the Helm values
// Returns true if any values were removed, false otherwise
func removeDefaultValues(astFile *ast.File, values *serverValuesFile) {
	// Remove $.environment key if uses one of the defaults
	if values.Environment == "develop" || values.Environment == "stable" || values.Environment == "staging" || values.Environment == "production" {
		if removed, err := removeYamlNode(astFile, "$", "environment"); err == nil && removed {
			log.Info().Msgf("  - Removed 'environment: %s'", values.Environment)
		}
	}

	// Remove $.environmentFamily key if present
	if removed, err := removeYamlNode(astFile, "$", "environmentFamily"); err == nil && removed {
		log.Info().Msgf("  - Removed 'environmentFamily: %s'", values.EnvironmentFamily)
	}

	// Remove $.tenant.discoveryEnabled key if present
	if values.Tenant.DiscoveryEnabled {
		if removed, err := removeYamlNode(astFile, "$.tenant", "discoveryEnabled"); err == nil && removed {
			log.Info().Msgf("  - Removed 'tenant.discoveryEnabled: %t'", values.Tenant.DiscoveryEnabled)
		}

		// Remove $.tenant if empty.
		if removed, err := removeYamlNodeIfEmpty(astFile, "$", "tenant"); err == nil && removed {
			log.Info().Msg("  - Removed 'tenant'")
		}
	}

	// Remove $.config.files if have default values
	if len(values.Config.Files) == 2 {
		files := values.Config.Files
		if files[0] == "./Config/Options.base.yaml" && (files[1] == "./Config/Options.dev.yaml" || files[1] == "./Config/Options.staging.yaml" || files[1] == "./Config/Options.production.yaml") {
			if removed, err := removeYamlNode(astFile, "$.config", "files"); err == nil && removed {
				log.Info().Msgf("  - Removed 'config.files: %v'", files)
			}

			// Remove $.config if empty.
			if removed, err := removeYamlNodeIfEmpty(astFile, "$", "config"); err == nil && removed {
				log.Info().Msg("  - Removed 'config'")
			}
		}
	}

	// Remove $.shards key if present
	if removed, err := removeYamlNode(astFile, "$", "shards"); err == nil && removed {
		log.Info().Msg("  - Removed 'shards'")
	}
}

// removeYamlNode removes a node from the YAML document using the parent path and the object name
// Example: removeYamlNode(astFile, "$.parent", "child2")
func removeYamlNode(astFile *ast.File, parentPathStr string, childName string) (bool, error) {
	// Create a PathString object for the parent path
	parentPath, err := yaml.PathString(parentPathStr)
	if err != nil {
		return false, fmt.Errorf("error creating parent path '%s': %w", parentPathStr, err)
	}

	// Find the parent node
	parentNode, err := parentPath.FilterFile(astFile)
	if err != nil {
		// If the path doesn't exist, that's not an error - just return false
		return false, nil
	}

	// Remove the node from the parent mapping
	if mapNode, ok := parentNode.(*ast.MappingNode); ok {
		originalLength := len(mapNode.Values)
		mapNode.Values = removeMapping(mapNode.Values, childName)
		// Return true if a node was removed
		return len(mapNode.Values) < originalLength, nil
	}

	return false, fmt.Errorf("parent node at path '%s' is not a mapping node", parentPathStr)
}

// removeMapping removes a key from a list of MappingValueNodes
func removeMapping(nodes []*ast.MappingValueNode, key string) []*ast.MappingValueNode {
	for i, node := range nodes {
		if node.Key.String() == key {
			return append(nodes[:i], nodes[i+1:]...)
		}
	}
	return nodes
}

// removeYamlNode removes a node from the YAML document if it is empty.
// Example: removeYamlNode(astFile, "$", "child")
func removeYamlNodeIfEmpty(astFile *ast.File, parentPathStr string, childName string) (bool, error) {
	// Create a PathString object for the child path
	childPathStr := parentPathStr + "." + childName
	childPath, err := yaml.PathString(childPathStr)
	if err != nil {
		return false, fmt.Errorf("error creating child path '%s': %w", parentPathStr, err)
	}

	// Find the child node
	childNode, err := childPath.FilterFile(astFile)
	if err != nil {
		// If the path doesn't exist, that's not an error - just return false
		return false, nil
	}

	// If child node is empty, remove it.
	if mapNode, ok := childNode.(*ast.MappingNode); ok {
		// If the mapping node has no values, it's empty
		if len(mapNode.Values) == 0 {
			// Remove the empty node from its parent
			return removeYamlNode(astFile, parentPathStr, childName)
		}
		return false, nil
	} else if seqNode, ok := childNode.(*ast.SequenceNode); ok {
		// If the sequence node has no values, it's empty
		if len(seqNode.Values) == 0 {
			// Remove the empty node from its parent
			return removeYamlNode(astFile, parentPathStr, childName)
		}
		return false, nil
	}

	return false, fmt.Errorf("child node at path '%s' is not a mapping or sequence node", childPathStr)
}
