/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"archive/zip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/filesetwriter"
	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/rs/zerolog/log"
	"github.com/tidwall/sjson"
)

// Check if the specified path is a valid directory.
func isDirectory(path string) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fileInfo.IsDir()
}

// Find a sub-directory that fulfills the predicate function.
// Hidden directories (starting with a dot) are skipped.
func findSubDirectory(name, rootPath string, predicateFunc func(path string, relPath string) (bool, error)) (string, error) {
	var foundPath string
	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip non-directories.
		if !info.IsDir() {
			return nil
		}

		// Skip dot directories (eg, .git).
		if strings.HasPrefix(info.Name(), ".") {
			// log.Debug().Msgf("Skip directory: %s", path)
			return filepath.SkipDir
		}

		// Resolve relative path.
		relPath, err := filepath.Rel(rootPath, path)
		if err != nil {
			return fmt.Errorf("failed to resolve path to %s (from %s): %w", path, rootPath, err)
		}

		// If it's a valid Unity project directory, return it.
		isMatch, err := predicateFunc(rootPath, relPath)
		if err != nil {
			return err
		}

		// If found match, bail out.
		if isMatch {
			foundPath = relPath
			return filepath.SkipAll
		}

		return nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to scan sub-directory: %w", err)
	}

	if foundPath == "" {
		return "", fmt.Errorf("unable to find %s directory within %s", name, rootPath)
	}

	return foundPath, nil
}

// Find an Unity project within the specified root path. Returns the path relative to rootPath.
func findUnityProjectPath(rootPath string) (string, error) {
	return findSubDirectory("Unity project", rootPath, func(rootPath, relPath string) (bool, error) {
		// If it's a valid Unity project directory, return it.
		err := validateUnityProjectPath(rootPath, relPath)
		if err == nil {
			return true, nil
		}

		return false, nil
	})
}

// Check that the provided Unity project directory is valid (relative to the project root).
func validateUnityProjectPath(rootPath string, unityProjectPath string) error {
	// Validate Unity project path
	if filepath.IsAbs(unityProjectPath) {
		return fmt.Errorf("unity-project path must be a relative path: %s", unityProjectPath)
	}
	if strings.Contains(unityProjectPath, "..") {
		return fmt.Errorf("unity-project path must not contain '..': %s", unityProjectPath)
	}

	// Validate that the path exists and is a directory
	unityProjectPathAbs := filepath.Join(rootPath, unityProjectPath)
	info, err := os.Stat(unityProjectPathAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("unity project path does not exist: %s", unityProjectPathAbs)
		}
		return fmt.Errorf("error accessing unity project path: %v", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("unity project path must be a directory: %s", unityProjectPathAbs)
	}

	// Validate that it looks like a Unity project (has required directories and files)
	requiredPaths := map[string]string{
		"Assets":                 "Assets directory",
		"ProjectSettings":        "ProjectSettings directory",
		"Packages":               "Packages directory",
		"Packages/manifest.json": "Unity project manifest",
	}

	for pathSuffix, description := range requiredPaths {
		path := filepath.Join(unityProjectPathAbs, pathSuffix)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("%s does not appear to be a Unity project (no %s found): %s", unityProjectPathAbs, description, path)
			}
			return fmt.Errorf("error accessing Unity project's %s: %v", description, err)
		}
	}

	return nil
}

// applyReplacements replaces placeholder tokens of the form {{{KEY}}} in the input string
// using the provided replacements map. It logs discovered placeholders and whether a
// replacement was provided. Returns the updated string and an error if unreplaced placeholders remain.
// Example: input "Assets/{{{UNITY_PROJECT_DIR}}}/Foo" with map{"UNITY_PROJECT_DIR": "UnityClient"}
// becomes "Assets/UnityClient/Foo".
func applyReplacements(input string, replacements map[string]string) (string, error) {
	// Detect all triple-braced placeholders in the input and print them.
	// Pattern matches {{{SOME_TOKEN}}}, capturing the token name in group 1.
	re := regexp.MustCompile(`\{\{\{([^}]+)\}\}\}`)
	matches := re.FindAllStringSubmatch(input, -1)
	if len(matches) > 0 {
		log.Debug().Msgf("Found %d template placeholder(s):", len(matches))
		seen := map[string]bool{}
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			key := m[1]
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = true
			if val, ok := replacements[key]; ok {
				log.Debug().Msgf("  {{{%s}}} -> %q", key, val)
			} else {
				return "", fmt.Errorf("no replacement provided for placeholder {{{%s}}}", key)
			}
		}
	}

	// Perform token substitution. We intentionally do not attempt to normalize
	// separators here since the input may be used for both Windows and
	// Unity-style (forward slash) paths.
	out := input
	for k, v := range replacements {
		token := "{{{" + k + "}}}"
		out = strings.ReplaceAll(out, token, v)
	}

	// Return an error listing the unreplaced placeholders
	remaining := re.FindAllString(out, -1)
	if len(remaining) > 0 {
		return out, fmt.Errorf("unreplaced placeholders remain: %v", remaining)
	}

	return out, nil
}

// downloadSdkWithProgress downloads the SDK zip to a temp file with a progress bar.
// Returns the path to the downloaded zip file. The caller must remove it when done.
func downloadSdkWithProgress(tokenSet *auth.TokenSet, sdkVersionInfo *portalapi.SdkVersionInfo) (string, error) {
	tmpDir := os.TempDir()
	portalClient := portalapi.NewClient(tokenSet)

	label := fmt.Sprintf("Downloading Metaplay SDK v%s", sdkVersionInfo.Version)
	var sdkZipPath string

	err := tui.RunWithProgressBar(label, func(update func(current, total int64)) error {
		var dlErr error
		sdkZipPath, dlErr = portalClient.DownloadSdkByVersionIDWithProgress(tmpDir, sdkVersionInfo.ID, update)
		return dlErr
	})
	if err != nil {
		return "", fmt.Errorf("failed to download SDK version '%s': %w", sdkVersionInfo.Version, err)
	}

	log.Debug().Msgf("Downloaded SDK version '%s' (ID: %s)", sdkVersionInfo.Version, sdkVersionInfo.ID)
	return sdkZipPath, nil
}

// Download the SDK (into the OS temp directory) and extract to the targetProjectPath.
// Downloads the version specified by versionInfo.
func downloadAndExtractSdk(tokenSet *auth.TokenSet, targetProjectPath string, versionInfo *portalapi.SdkVersionInfo) (*metaproj.MetaplayVersionMetadata, error) {
	// Download the SDK archive to temp directory.
	tmpDir := os.TempDir()
	portalClient := portalapi.NewClient(tokenSet)

	var sdkZipPath string
	var err error

	// Download the specific version
	sdkZipPath, err = portalClient.DownloadSdkByVersionID(tmpDir, versionInfo.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to download SDK version '%s': %w", versionInfo.Version, err)
	}
	log.Debug().Msgf("Downloaded SDK version '%s' (ID: %s)", versionInfo.Version, versionInfo.ID)
	defer os.Remove(sdkZipPath)

	// Validate the SDK archive file.
	sdkMetadata, err := validateSdkZipFile(sdkZipPath)
	if err != nil {
		return nil, fmt.Errorf("invalid Metaplay SDK archive: %v", err)
	}
	log.Debug().Msgf("Use downloaded SDK archive: %s (v%s)", sdkZipPath, sdkMetadata.SdkVersion)

	// Extract SDK into target directory.
	if err := extractSdkFromZip(targetProjectPath, sdkZipPath); err != nil {
		return nil, fmt.Errorf("failed to extract SDK archive: %w", err)
	}

	return sdkMetadata, nil
}

func resolveSdkSource(targetProjectPath, sdkSource string) (string, *metaproj.MetaplayVersionMetadata, error) {
	// Sdk source can be either an existing directory or a path to the MetaplaySDK zip file
	if sdkSource != "" && isDirectory(sdkSource) {
		// Refer (don't copy) to the specified MetaplaySDK directory.
		relativePathToSdk, err := filepath.Rel(targetProjectPath, sdkSource)
		if err != nil {
			return "", nil, fmt.Errorf("failed to construct relative path to MetaplaySDK: %v", err)
		}

		// Ensure the SDK directory is valid.
		sdkMetadata, err := validateSdkDirectory(sdkSource)
		if err != nil {
			return "", nil, err
		}

		return relativePathToSdk, sdkMetadata, nil
	} else {
		// Validate the SDK archive file.
		sdkMetadata, err := validateSdkZipFile(sdkSource)
		if err != nil {
			return "", nil, fmt.Errorf("invalid Metaplay SDK archive: %v", err)
		}
		log.Debug().Msgf("Use local SDK archive file: %s (v%s)", sdkSource, sdkMetadata.SdkVersion)

		// Extract SDK into target directory.
		if err := extractSdkFromZip(targetProjectPath, sdkSource); err != nil {
			return "", nil, fmt.Errorf("failed to extract SDK archive: %v", err)
		}

		return "MetaplaySDK", sdkMetadata, nil
	}
}

// Check that the target directory is a valid MetaplaySDK/ distribution.
// Note: Only works with R32 and above (requires version.yaml).
func validateSdkDirectory(sdkDirPath string) (*metaproj.MetaplayVersionMetadata, error) {
	log.Debug().Msgf("Validate Metaplay SDK directory: %s", sdkDirPath)
	versionMetadata, err := metaproj.LoadSdkVersionMetadata(sdkDirPath)
	if err != nil {
		return nil, err
	}

	return versionMetadata, err
}

func validateSdkZipFile(sdkZipPath string) (*metaproj.MetaplayVersionMetadata, error) {
	// Check if file exists
	fileInfo, err := os.Stat(sdkZipPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("SDK archive file does not exist: %s", sdkZipPath)
		}
		return nil, fmt.Errorf("error accessing SDK archive file: %v", err)
	}

	// Check if it's a regular file (not a directory)
	if !fileInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("SDK archive path is not a regular file: %s", sdkZipPath)
	}

	// Check file extension
	if !strings.HasSuffix(strings.ToLower(sdkZipPath), ".zip") {
		return nil, fmt.Errorf("SDK archive must have .zip extension: %s", sdkZipPath)
	}

	// Open and validate ZIP archive
	reader, err := zip.OpenReader(sdkZipPath)
	if err != nil {
		return nil, fmt.Errorf("invalid ZIP archive: %v", err)
	}
	defer reader.Close()

	// Find and read version.yaml from the ZIP
	var versionFile *zip.File
	for _, f := range reader.File {
		if f.Name == "MetaplaySDK/version.yaml" {
			versionFile = f
			break
		}
	}
	if versionFile == nil {
		return nil, fmt.Errorf("MetaplaySDK/version.yaml not found in ZIP archive")
	}

	// Open and read the version.yaml file
	rc, err := versionFile.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open version.yaml in ZIP: %v", err)
	}
	defer rc.Close()

	content, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("failed to read version.yaml from ZIP: %v", err)
	}

	// Parse the version metadata
	versionMetadata, err := metaproj.ParseVersionMetadata(content)
	if err != nil {
		return nil, err
	}

	return versionMetadata, nil
}

// Extract the MetaplaySDK/ directory from the release zip into the target
// project directory. The MetaplaySamples/ is ignored.
func extractSdkFromZip(targetDir string, sdkZipPath string) error {
	// Open the zip archive
	reader, err := zip.OpenReader(sdkZipPath)
	if err != nil {
		return fmt.Errorf("failed to open ZIP archive: %v", err)
	}
	defer reader.Close()

	log.Debug().Msgf("Extracting SDK to: %s", targetDir)

	// Check that MetaplaySDK/ doesn't exist in target
	metaplaySdkPath := filepath.Join(targetDir, "MetaplaySDK")
	if _, err := os.Stat(metaplaySdkPath); err == nil {
		// \todo enable check later -- just override files for now
		// return fmt.Errorf("MetaplaySDK directory already exists in target: %s", metaplaySdkPath)
	}

	// Extract only files from MetaplaySDK directory
	for _, file := range reader.File {
		// Only process files that are within the MetaplaySDK directory
		if !strings.HasPrefix(file.Name, "MetaplaySDK/") {
			continue
		}

		// Construct target path
		targetPath := filepath.Join(targetDir, file.Name)

		if file.FileInfo().IsDir() {
			// Create directory
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %v", targetPath, err)
			}
			continue
		}

		// Create parent directories if they don't exist
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory for %s: %v", targetPath, err)
		}

		// Create the file
		outFile, err := os.Create(targetPath)
		if err != nil {
			return fmt.Errorf("failed to create file %s: %v", targetPath, err)
		}

		// Open the zip file
		rc, err := file.Open()
		if err != nil {
			outFile.Close()
			return fmt.Errorf("failed to open zip file %s: %v", file.Name, err)
		}

		// Copy the contents
		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()
		if err != nil {
			return fmt.Errorf("failed to write file %s: %v", targetPath, err)
		}
	}

	return nil
}

// installerTemplateFile is a single file entry within an installer template.
// Text files have a non-empty Text field and binary files have a non-empty Bytes field.
type installerTemplateFile struct {
	Path  string
	Text  string
	Bytes string
}

// installerTemplateProject is the parsed form of a project/dashboard template JSON.
type installerTemplateProject struct {
	Version int
	Files   []installerTemplateFile
}

// processTemplateFiles adds all template files to the plan with appropriate replacements
// and conflict policies. This is the shared logic used by both collectFromTemplate and
// collectFromTemplateInZip.
func processTemplateFiles(plan *filesetwriter.Plan, template installerTemplateProject, dstRoot string, replacements map[string]string, skipSample bool) error {
	for _, file := range template.Files {
		// Skip MetaplayHelloWorld files if requested
		if skipSample && strings.Contains(file.Path, "MetaplayHelloWorld") {
			log.Debug().Msgf("Skipping sample file: %s", file.Path)
			continue
		}

		// Resolve destination path (fill in templates)
		fileDstPath, err := applyReplacements(filepath.Join(dstRoot, file.Path), replacements)
		if err != nil {
			return fmt.Errorf("failed to apply replacements to file path %s: %v", file.Path, err)
		}

		// Unity .meta files are skipped on conflict to preserve existing GUIDs
		isMetaFile := strings.HasSuffix(file.Path, ".meta")

		// Resolve file content: with template replacements for text files, base64-decoding for binary files
		if file.Text != "" {
			content, err := applyReplacements(file.Text, replacements)
			if err != nil {
				return fmt.Errorf("failed to apply replacements to file content %s: %v", file.Path, err)
			}
			if isMetaFile {
				plan.AddSkipExisting(fileDstPath, []byte(content), 0644)
			} else {
				plan.Add(fileDstPath, []byte(content), 0644)
			}
		} else if file.Bytes != "" {
			bytes, err := base64.StdEncoding.DecodeString(file.Bytes)
			if err != nil {
				return fmt.Errorf("failed to decode base64 string for file %s: %v", fileDstPath, err)
			}
			if isMetaFile {
				plan.AddSkipExisting(fileDstPath, bytes, 0644)
			} else {
				plan.Add(fileDstPath, bytes, 0644)
			}
		}
	}

	return nil
}

// buildTemplateReplacements constructs the replacement map from a project config and
// extra replacements. Used by both collectFromTemplate and collectFromTemplateInZip.
func buildTemplateReplacements(config *metaproj.ProjectConfig, extraReplacements map[string]string) map[string]string {
	unityProjectDir := config.UnityProjectDir
	if unityProjectDir == "." {
		unityProjectDir = ""
	} else if unityProjectDir != "" && !strings.HasSuffix(unityProjectDir, "/") {
		unityProjectDir = unityProjectDir + "/"
	}

	templateReplacements := map[string]string{
		"RELATIVE_PATH_TO_SDK": config.SdkRootDir,
		"UNITY_PROJECT_DIR":    unityProjectDir,
		"PROJECT_HUMAN_ID":     config.ProjectHumanID,
		"PROJECT_NAME":         config.ProjectHumanID, // Removed in R34
	}
	maps.Copy(templateReplacements, extraReplacements)

	// Log template replacements.
	log.Debug().Msgf("Template replacements:")
	for k, v := range templateReplacements {
		log.Debug().Msgf("  %s: %s", k, v)
	}

	return templateReplacements
}

// collectFromTemplate reads the installer template from the SDK on disk and adds all
// resolved files to the given plan without writing anything to disk.
// dstPath - Root directory for installed files, relative to metaplay project dir.
// skipSample - If true, skip files in MetaplayHelloWorld directory.
func collectFromTemplate(plan *filesetwriter.Plan, project *metaproj.MetaplayProject, dstPath string, templateFileName string, extraReplacements map[string]string, skipSample bool) error {
	// Resolve path to installer template file
	templatePath := filepath.Join(project.GetSdkRootDir(), "Installer", templateFileName)
	if _, err := os.Stat(templatePath); err != nil {
		return fmt.Errorf("unable to find template file at %s: %v", templatePath, err)
	}

	// Read the template file
	templateJSON, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read template file: %v", err)
	}

	// Parse the template
	var template installerTemplateProject
	if err := json.Unmarshal(templateJSON, &template); err != nil {
		return fmt.Errorf("failed to parse template file: %v", err)
	}

	if template.Version != 1 {
		return fmt.Errorf("unsupported installer project template version %d", template.Version)
	}
	if len(template.Files) == 0 {
		return fmt.Errorf("installer project template does not have any files")
	}

	dstRoot := filepath.Join(project.RelativeDir, dstPath)
	replacements := buildTemplateReplacements(&project.Config, extraReplacements)

	return processTemplateFiles(plan, template, dstRoot, replacements, skipSample)
}

// collectFromTemplateInZip reads the installer template from inside a zip archive
// and adds all resolved files to the plan. Used when the SDK has not been extracted
// to disk yet (the template is read directly from the zip).
func collectFromTemplateInZip(plan *filesetwriter.Plan, zipPath string, templateFileName string, dstRoot string, config *metaproj.ProjectConfig, extraReplacements map[string]string, skipSample bool) error {
	// Open the zip archive.
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip archive: %v", err)
	}
	defer reader.Close()

	// Find the template file inside the zip.
	entryName := "MetaplaySDK/Installer/" + templateFileName
	var templateFile *zip.File
	for _, f := range reader.File {
		if f.Name == entryName {
			templateFile = f
			break
		}
	}
	if templateFile == nil {
		return fmt.Errorf("template file %s not found in zip archive", entryName)
	}

	// Read the template JSON from the zip.
	rc, err := templateFile.Open()
	if err != nil {
		return fmt.Errorf("failed to open template file in zip: %v", err)
	}
	defer rc.Close()

	templateJSON, err := io.ReadAll(rc)
	if err != nil {
		return fmt.Errorf("failed to read template file from zip: %v", err)
	}

	// Parse the template.
	var template installerTemplateProject
	if err := json.Unmarshal(templateJSON, &template); err != nil {
		return fmt.Errorf("failed to parse template file: %v", err)
	}

	if template.Version != 1 {
		return fmt.Errorf("unsupported installer project template version %d", template.Version)
	}
	if len(template.Files) == 0 {
		return fmt.Errorf("installer project template does not have any files")
	}

	replacements := buildTemplateReplacements(config, extraReplacements)

	return processTemplateFiles(plan, template, dstRoot, replacements, skipSample)
}

// installFromTemplate installs files from an installer template file in SDK/Installer.
// This is a convenience wrapper around collectFromTemplate that creates, scans, and
// executes a plan in one step. Used by init dashboard.
func installFromTemplate(project *metaproj.MetaplayProject, dstPath string, templateFileName string, extraReplacements map[string]string, skipSample bool) error {
	plan := filesetwriter.NewPlan()
	if err := collectFromTemplate(plan, project, dstPath, templateFileName, extraReplacements, skipSample); err != nil {
		return err
	}
	if err := plan.Scan(); err != nil {
		return err
	}
	return plan.Execute()
}

// computeManifestUpdate reads the Unity project's Packages/manifest.json, adds
// the MetaplaySDK/Client reference, and returns the updated content without writing.
func computeManifestUpdate(project *metaproj.MetaplayProject) (string, []byte, error) {
	const packageName = "io.metaplay.unitysdk"

	manifestPath := filepath.Join(project.GetUnityProjectDir(), "Packages", "manifest.json")

	// Convert the SDK directory to a relative path from manifest.json.
	relativePath, err := filepath.Rel(filepath.Dir(manifestPath), project.GetSdkRootDir())
	if err != nil {
		return "", nil, fmt.Errorf("failed to compute relative path: %w", err)
	}
	log.Debug().Msgf("Relative path to MetaplaySDK (from Unity Packages/ directory): %s", relativePath)

	// Read the Unity project's Packages/manifest.json file
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read manifest.json: %w", err)
	}

	// Prepare the client reference
	clientDir := filepath.ToSlash(filepath.Join(relativePath, "Client"))
	clientRef := fmt.Sprintf("file:%s", clientDir)

	// Add or update the package reference using sjson
	escapedPackageName := strings.ReplaceAll(packageName, ".", "\\.")
	updatedManifest, err := sjson.SetBytes(manifestData, fmt.Sprintf("dependencies.%s", escapedPackageName), clientRef)
	if err != nil {
		return "", nil, fmt.Errorf("failed to update manifest.json: %w", err)
	}

	log.Debug().Msgf("Successfully computed manifest.json update: \"%s\" from \"%s\"", packageName, clientRef)
	return manifestPath, updatedManifest, nil
}

// addReferenceToUnityManifest adds a reference to the MetaplaySDK/Client project
// in the Unity project Packages/manifest.json.
func addReferenceToUnityManifest(project *metaproj.MetaplayProject) error {
	manifestPath, content, err := computeManifestUpdate(project)
	if err != nil {
		return err
	}
	if err := os.WriteFile(manifestPath, content, 0644); err != nil {
		return fmt.Errorf("failed to write manifest.json: %w", err)
	}
	return nil
}
