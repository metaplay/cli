/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type migrateDashboardFilesOpts struct {
	UsePositionalArgs
}

// OrderedMap is a custom type to maintain the order of keys in a JSON object
type OrderedMap struct {
	Order []string               // Slice to maintain the order of keys
	Map   map[string]interface{} // Map to store the key-value pairs
}

func (om *OrderedMap) MarshalJSON() ([]byte, error) {
	// Create a buffer to write the JSON
	buf := bytes.NewBuffer(nil)

	// Start the JSON object
	buf.WriteString("{")
	buf.WriteString("\n")

	// Iterate over the keys in the specified order
	for i, key := range om.Order {
		// Write indentation
		buf.WriteString("  ")

		// Marshal the key
		keyBytes, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}

		// Marshal the value
		valueBuf := bytes.NewBuffer(nil)
		encoder := json.NewEncoder(valueBuf)
		encoder.SetEscapeHTML(false)  // Disable HTML escaping
		encoder.SetIndent("  ", "  ") // Set indentation for pretty printing

		// Add space before the value
		valueBuf.WriteString(" ")

		if err := encoder.Encode(om.Map[key]); err != nil {
			return nil, err
		}

		// Remove the trailing newline added by `Encode`
		valueBytes := bytes.TrimRight(valueBuf.Bytes(), "\n")

		// Write the key-value pair to the buffer
		buf.Write(keyBytes)
		buf.WriteString(":")
		buf.Write(valueBytes)

		// Add a comma if it's not the last key
		if i < len(om.Order)-1 {
			buf.WriteString(",")
		}

		// Add a newline for pretty printing
		buf.WriteString("\n")
	}

	// End the JSON object
	buf.WriteString("}")

	return buf.Bytes(), nil
}

func (om *OrderedMap) UnmarshalJSON(b []byte) error {
	json.Unmarshal(b, &om.Map)

	index := make(map[string]int)
	for key := range om.Map {
		om.Order = append(om.Order, key)
		esc, _ := json.Marshal(key) //Escape the key
		index[key] = bytes.Index(b, esc)
	}

	sort.Slice(om.Order, func(i, j int) bool { return index[om.Order[i]] < index[om.Order[j]] })
	return nil
}

func init() {
	o := migrateDashboardFilesOpts{}

	var cmd = &cobra.Command{
		Use:   "dashboard-files",
		Short: "Update dashboard scaffolding files to the latest version",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Update the LiveOps Dashboard scaffolding files to the latest version compatible with your project.

			{Arguments}
		`),
		Example: trimIndent(`
			# Update dashboard files to the latest version
			metaplay migrate dashboard-files
		`)}

	migrateR32Cmd.AddCommand(cmd)
}

func (o *migrateDashboardFilesOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *migrateDashboardFilesOpts) Run(cmd *cobra.Command) error {
	// Load project config.
	project, err := resolveProject()
	if err != nil {
		return err
	}

	// Check that project uses a custom dashboard, otherwise error out
	if !project.UsesCustomDashboard() {
		return fmt.Errorf("project does not have a custom dashboard to update")
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Update Dashboard Files"))
	log.Info().Msg("")

	// Update dashboard files
	log.Info().Msg("Updating dashboard files...")
	if err := migrateDashboardFiles(project); err != nil {
		log.Error().Msgf("Failed to update dashboard files: %s", err)
		os.Exit(1)
	}

	log.Info().Msgf("✅ %s", styles.RenderSuccess("Dashboard files updated successfully"))
	log.Info().Msg("")
	log.Info().Msg("Next steps:")
	log.Info().Msg("  1. Review the changes")
	log.Info().Msg("  2. Run 'metaplay build dashboard' to build the updated dashboard")
	log.Info().Msg("  3. Test the dashboard locally with 'metaplay dev dashboard'")
	log.Info().Msg("  4. Commit and push the changes")

	return nil
}

func migrateDashboardFiles(project *metaproj.MetaplayProject) error {
	projectRootPath := project.RelativeDir
	dashboardPath := project.GetDashboardDir()
	defaultDashboardPath := filepath.Join(project.GetSdkRootDir(), "Frontend", "DefaultDashboard")

	log.Info().Msgf("Project root: %s", projectRootPath)
	log.Info().Msgf("Dashboard directory: %s", dashboardPath)

	log.Info().Msgf("Cleaning up temporary files in %s", projectRootPath)
	// Collect all node_modules folders to delete
	var foldersToDelete []string
	if err := filepath.Walk(projectRootPath, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() && info.Name() == "node_modules" {
			foldersToDelete = append(foldersToDelete, path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("Failed to collect node_modules folders: %w", err)
	}

	// Delete the collected node_modules folders
	for _, folder := range foldersToDelete {
		log.Info().Msgf("Deleting node_modules folder: %s", folder)
		if err := os.RemoveAll(folder); err != nil {
			return fmt.Errorf("Failed to delete node_modules folder: %w", err)
		}
	}

	// Log the number of deleted folders
	if len(foldersToDelete) == 0 {
		log.Info().Msgf("No node_modules folders found in %s", dashboardPath)
	} else {
		log.Info().Msgf("Deleted %d node_modules folders in %s", len(foldersToDelete), dashboardPath)
	}

	log.Info().Msgf("Deleting pnpm-lock.yaml file in %s", dashboardPath)
	if err := os.RemoveAll(filepath.Join(dashboardPath, "pnpm-lock.yaml")); err != nil {
		return fmt.Errorf("Failed to delete pnpm-lock.yaml: %w", err)
	}

	// Update package.json while preserving name and custom dependencies
	packageJsonPath := filepath.Join(dashboardPath, "package.json")
	defaultPackageJsonPath := filepath.Join(defaultDashboardPath, "package.json")
	if err := updatePackageJson(packageJsonPath, defaultPackageJsonPath); err != nil {
		return fmt.Errorf("Failed to update package.json: %w", err)
	}

	// Run `pnpm install`
	log.Info().Msg("Running 'pnpm install'...")
	cmd := exec.Command("pnpm", "install")
	cmd.Dir = dashboardPath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Failed to run 'pnpm install': %w", err)
	}

	log.Info().Msg("Dashboard migration completed successfully.")
	return nil
}

func updatePackageJson(packageJsonPath, defaultPackageJsonPath string) error {
	log.Info().Msg("Updating package.json while preserving project name and custom dependencies...")
	defaultContent, err := os.ReadFile(defaultPackageJsonPath)
	if err != nil {
		return err
	}

	projectContent, err := os.ReadFile(packageJsonPath)
	if err != nil {
		return err
	}

	var targetPkg, projectPkg OrderedMap
	if err := json.Unmarshal(defaultContent, &targetPkg); err != nil {
		return err
	}

	var origDefaultPkg map[string]interface{}
	if err := json.Unmarshal(defaultContent, &origDefaultPkg); err != nil {
		return err
	}

	if err := json.Unmarshal(projectContent, &projectPkg); err != nil {
		return err
	}

	// Preserve the project name
	if name, exists := projectPkg.Map["name"]; exists {
		targetPkg.Map["name"] = name
	}

	// Preserve custom dependencies
	if dependencies, exists := projectPkg.Map["dependencies"].(map[string]interface{}); exists {
		if defaultDependencies, exists := targetPkg.Map["dependencies"].(map[string]interface{}); exists {
			for key, value := range dependencies {
				// Check if the key already exists in the default package.json
				if _, exists := defaultDependencies[key]; !exists {
					// If it doesn't exist, add it
					defaultDependencies[key] = value
				}
			}
		}
	}

	if devDependencies, exists := projectPkg.Map["devDependencies"].(map[string]interface{}); exists {
		if defaultDevDependencies, exists := targetPkg.Map["devDependencies"].(map[string]interface{}); exists {
			for key, value := range devDependencies {
				// Check if the key already exists in the default package.json
				if _, exists := defaultDevDependencies[key]; !exists {
					// If it doesn't exist, add it
					defaultDevDependencies[key] = value
				}
			}
		}
	}

	if peerDependencies, exists := projectPkg.Map["peerDependencies"].(map[string]interface{}); exists {
		if defaultPeerDependencies, exists := targetPkg.Map["peerDependencies"].(map[string]interface{}); exists {
			for key, value := range peerDependencies {
				// Check if the key already exists in the default package.json
				if _, exists := defaultPeerDependencies[key]; !exists {
					// If it doesn't exist, add it
					defaultPeerDependencies[key] = value
				}
			}
		}
	}

	if optionalDependencies, exists := projectPkg.Map["optionalDependencies"].(map[string]interface{}); exists {
		if defaultOptionalDependencies, exists := targetPkg.Map["optionalDependencies"].(map[string]interface{}); exists {
			for key, value := range optionalDependencies {
				// Check if the key already exists in the default package.json
				if _, exists := defaultOptionalDependencies[key]; !exists {
					// If it doesn't exist, add it
					defaultOptionalDependencies[key] = value
				}
			}
		}
	}
	// Preserve custom scripts values
	if scripts, exists := projectPkg.Map["scripts"].(map[string]interface{}); exists {
		if defaultScripts, exists := targetPkg.Map["scripts"].(map[string]interface{}); exists {
			for key, value := range scripts {
				defaultScripts[key] = value
			}
		}
	}

	// Check if the target package.json is the same as the original
	if reflect.DeepEqual(targetPkg, projectPkg) {
		log.Info().Msgf("package.json is already up to date with the SDK version, no changes made.")
		return nil
	}

	// Print the diff
	printDiff(origDefaultPkg, projectPkg.Map)

	// Marshal the updated package.json content
	targetPkgBytes, err := targetPkg.MarshalJSON()
	if err != nil {
		return err
	}

	log.Info().Msgf("Writing updated package.json to %s", packageJsonPath)
	return os.WriteFile(packageJsonPath, targetPkgBytes, 0644)
}

// printDiff prints the differences between the original and project json content, opening dependencies and scripts
func printDiff(original, project map[string]interface{}) {
	// Print the diff
	log.Info().Msgf("Differences between package.json and default package.json:")
	for key, value := range original {
		if _, exists := project[key]; !exists {
			log.Info().Msgf("  - %s: %v", key, value)
		} else {
			// Open dependency values
			if key == "dependencies" || key == "devDependencies" || key == "peerDependencies" || key == "optionalDependencies" || key == "scripts" {
				openAndPrintDiff(key, value, project)
				continue
			}

			// Compare the values
			if fmt.Sprintf("%v", value) != fmt.Sprintf("%v", project[key]) {
				log.Info().Msgf("  - %s: %v (default) vs %v (project)", key, value, project[key])
			}
		}
	}
	// Print the project-specific values
	for key, value := range project {
		// Check if the key exists in the default package.json
		if key == "dependencies" || key == "devDependencies" || key == "peerDependencies" || key == "optionalDependencies" || key == "scripts" {
			openAndPrintDiff(key, value, project)
			continue
		}
		if _, exists := original[key]; !exists {
			log.Info().Msgf("  - %s: %v (project)", key, value)
		}
	}

	log.Info().Msgf("End of diff")
}

func openAndPrintDiff(key string, value interface{}, project map[string]interface{}) {
	// Check if the values are maps
	if _, ok := value.(map[string]interface{}); !ok {
		log.Info().Msgf("  - %s: %v (default) vs %v (project)", key, value, project[key])
		return
	}

	defaultMap := value.(map[string]interface{})
	projectMap, ok := project[key].(map[string]interface{})
	if !ok {
		log.Info().Msgf("  - %s: %v (default) vs %v (project)", key, value, project[key])
		return
	}
	// Compare the key value pairs
	for depKey, depValue := range defaultMap {
		// Check if the key exists in the project package.json
		if _, exists := projectMap[depKey]; !exists {
			log.Info().Msgf("  - %s: %s: %v (default)", key, depKey, depValue)
		} else {
			// Compare the values
			if fmt.Sprintf("%v", depValue) != fmt.Sprintf("%v", projectMap[depKey]) {
				log.Info().Msgf("  - %s: %s: %v (default) vs %v (project)", key, depKey, depValue, projectMap[depKey])
			}
		}
	}

	// Print the project-specific values
	for depKey, depValue := range projectMap {
		// Check if the key exists in the default package.json
		if _, exists := defaultMap[depKey]; !exists {
			log.Info().Msgf("  - %s: %s: %v (project)", key, depKey, depValue)
		}
	}
}
