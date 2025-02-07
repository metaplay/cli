/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"archive/zip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/rs/zerolog/log"
	"github.com/tidwall/sjson"
	"gopkg.in/yaml.v3"
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

// Download the SDK (into the OS temp directory) and extract to the targetProjectPath.
// Use sdkVersion == "" for latest.
func downloadAndExtractSdk(tokenSet *auth.TokenSet, targetProjectPath string, sdkVersion string) (*MetaplayVersionMetadata, error) {
	// Download the SDK archive to temp directory.
	tmpDir := os.TempDir()
	portalClient := portalapi.NewClient(tokenSet)
	sdkZipPath, err := portalClient.DownloadSdk(tmpDir, sdkVersion)
	if err != nil {
		return nil, err
	}
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

func resolveSdkSource(targetProjectPath, sdkSource string) (string, *MetaplayVersionMetadata, error) {
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
func validateSdkDirectory(sdkDirPath string) (*MetaplayVersionMetadata, error) {
	log.Debug().Msgf("Validate Metaplay SDK directory: %s", sdkDirPath)
	versionMetadata, err := loadSdkVersionMetadata(sdkDirPath)
	if err != nil {
		return nil, err
	}

	return versionMetadata, err
}

func validateSdkZipFile(sdkZipPath string) (*MetaplayVersionMetadata, error) {
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
	versionMetadata, err := parseVersionMetadata(content)
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

// Install files from installer template file in SDK/Installer.
// dstPath - Root directory for installed files, relative to metaplay project dir.
func installFromTemplate(project *MetaplayProject, dstPath string, templateFileName string) error {
	// Single template file within an installer project. Text files have non-empty
	// `File` and binary files a non-empty `Bytes`. Text files support text replacement.
	type installerTemplateFile struct {
		Path  string
		Text  string
		Bytes string
	}

	// SDK installer project template. Project is either the SDK or the dashboard.
	type installerTemplateProject struct {
		Version int
		Files   []installerTemplateFile
	}

	// Resolve path to installer template file
	templatePath := filepath.Join(project.getSdkRootDir(), "Installer", templateFileName)
	if _, err := os.Stat(templatePath); err != nil {
		return fmt.Errorf("unable to find template file at %s: %v", templatePath, err)
	}

	// Read the template file
	templateJson, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read template file: %v", err)
	}

	// Parse the template
	var template installerTemplateProject
	if err := json.Unmarshal(templateJson, &template); err != nil {
		return fmt.Errorf("failed to parse template file: %v", err)
	}

	if template.Version != 1 {
		return fmt.Errorf("unsupported installer project template version %d", template.Version)
	}
	if len(template.Files) == 0 {
		return fmt.Errorf("installer project template does not have any files")
	}

	dstRoot := filepath.Join(project.relativeDir, dstPath)
	projectName := getProjectName(project)
	backendSolutionFileName := fmt.Sprintf("%s-Server.sln", projectName)
	unityProjectDir := project.config.UnityProjectDir
	if unityProjectDir == "." {
		unityProjectDir = ""
	} else if unityProjectDir != "" && !strings.HasSuffix(unityProjectDir, "/") {
		unityProjectDir = unityProjectDir + "/"
	}

	// Template replace rules.
	relativePathToSdk := project.config.SdkRootDir
	projectNameLower := strings.ToLower(projectName)
	log.Debug().Msgf("Template replace:")
	log.Debug().Msgf("  PROJECT_NAME: %s", projectNameLower)
	log.Debug().Msgf("  PROJECT_HUMAN_ID: %s", project.config.ProjectHumanID)
	log.Debug().Msgf("  BACKEND_SOLUTION_FILENAME: %s", backendSolutionFileName)
	log.Debug().Msgf("  RELATIVE_PATH_TO_SDK: %s", relativePathToSdk)
	log.Debug().Msgf("  UNITY_PROJECT_DIR: %s", unityProjectDir)

	// Extract all files from the template definition
	for _, file := range template.Files {
		// Resolve destination path (fill in templates)
		dstPath := filepath.Join(dstRoot, file.Path)
		dstPath = strings.ReplaceAll(dstPath, "{{{BACKEND_SOLUTION_FILENAME}}}", backendSolutionFileName)
		dstPath = strings.ReplaceAll(dstPath, "{{{UNITY_PROJECT_DIR}}}", unityProjectDir)
		if strings.Contains(dstPath, "{{{") || strings.Contains(dstPath, "}}}") {
			return fmt.Errorf("template file path %s contains unhandled template strings", dstPath)
		}

		// Ensure destination directory exists
		dirName := filepath.Dir(dstPath)
		if err := os.MkdirAll(dirName, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %v", dirName, err)
		}

		// Write the file: with template replacements for text files, base64-decoding for binary files
		if file.Text != "" {
			log.Debug().Msgf("Write: %s text", dstPath)
			content := file.Text
			content = strings.ReplaceAll(content, "{{{RELATIVE_PATH_TO_SDK}}}", relativePathToSdk)
			content = strings.ReplaceAll(content, "{{{PROJECT_NAME}}}", projectNameLower)
			content = strings.ReplaceAll(content, "{{{PROJECT_HUMAN_ID}}}", project.config.ProjectHumanID)
			content = strings.ReplaceAll(content, "{{{UNITY_PROJECT_DIR}}}", unityProjectDir)
			if strings.Contains(content, "{{{") || strings.Contains(content, "}}}") {
				return fmt.Errorf("template file %s contains unhandled template strings", dstPath)
			}
			if err := os.WriteFile(dstPath, []byte(content), 0644); err != nil {
				return fmt.Errorf("failed to write file %s: %v", dstPath, err)
			}
		} else if file.Bytes != "" {
			log.Debug().Msgf("Write: %s binary", dstPath)
			bytes, err := base64.StdEncoding.DecodeString(file.Bytes)
			if err != nil {
				return fmt.Errorf("failed to decode base64 string for file %s: %v", dstPath, err)
			}
			if err := os.WriteFile(dstPath, bytes, 0644); err != nil {
				return fmt.Errorf("failed to write file %s: %v", dstPath, err)
			}
		}
	}

	return nil
}

func readUnityProductName(unityProjectPath string) (string, error) {
	// Derive path to the Unity ProjectSettings asset file
	projectSettingsPath := filepath.Join(unityProjectPath, "ProjectSettings", "ProjectSettings.asset")

	// Read the file content.
	content, err := os.ReadFile(projectSettingsPath)
	if err != nil {
		return "", err
	}

	type PlayerSettings struct {
		ProductName string `yaml:"productName"`
	}
	var settings struct {
		PlayerSettings PlayerSettings `yaml:"PlayerSettings"`
	}

	if err := yaml.Unmarshal(content, &settings); err != nil {
		return "", err
	}
	log.Debug().Msgf("Project name from Unity PlayerSettings: %s", settings.PlayerSettings.ProductName)
	return settings.PlayerSettings.ProductName, nil
}

func getProjectName(project *MetaplayProject) string {
	// Try reading product name from Unity PlayerSettings
	productName, err := readUnityProductName(project.getUnityProjectDir())
	if err != nil {
		// Fallback to using name of project parent dir
		log.Warn().Msgf("Unable to read project name from Unity PlayerSettings, using parent directory name")
		absPath, err := filepath.Abs(project.relativeDir)
		if err != nil {
			log.Panic().Msgf("Could not read parent directory name: %v", err)
		}
		productName = filepath.Base(absPath)
	}

	// Replace whitespace with hyphen
	productName = strings.ReplaceAll(productName, " ", "-")
	// Only accept letters, digits and '-'
	productName = filterInvalidFileNameChars(productName)
	return productName
}

func filterInvalidFileNameChars(productName string) string {
	invalidChars := `<>:"/\|?*`
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(invalidChars, r) {
			return -1
		}
		return r
	}, productName)
}

// Add reference to the MetaplaySDK/Client project in the Unity project Packages/manifest.json.
func addReferenceToUnityManifest(project *MetaplayProject) error {
	const packageName = "io.metaplay.unitysdk"

	manifestPath := filepath.Join(project.getUnityProjectDir(), "Packages", "manifest.json")

	// Convert the SDK directory to a relative path from manifest.json.
	relativePath, err := filepath.Rel(filepath.Dir(manifestPath), project.getSdkRootDir())
	if err != nil {
		return fmt.Errorf("failed to compute relative path: %w", err)
	}
	log.Debug().Msgf("Relative path to MetaplaySDK: %s", relativePath)

	// // Check if the SDK directory exists
	// if _, err := os.Stat(relativePathToSdk); os.IsNotExist(err) {
	// 	return fmt.Errorf("SDK directory \"%s\" not found", relativePathToSdk)
	// }

	// // Check if the Client/package.json file exists
	// clientPackagePath := filepath.Join(relativePathToSdk, "Client", "package.json")
	// if _, err := os.Stat(clientPackagePath); os.IsNotExist(err) {
	// 	return fmt.Errorf("SDK directory \"%s\" is invalid -- does not contain Client/package.json", relativePathToSdk)
	// }

	// Read the Unity project's Packages/manifest.json file
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read manifest.json: %w", err)
	}

	// Prepare the client reference
	clientDir := filepath.ToSlash(filepath.Join(relativePath, "Client"))
	clientRef := fmt.Sprintf("file:%s", clientDir)

	// Add or update the package reference using sjson
	escapedPackageName := strings.ReplaceAll(packageName, ".", "\\.")
	updatedManifest, err := sjson.SetBytes(manifestData, fmt.Sprintf("dependencies.%s", escapedPackageName), clientRef)
	if err != nil {
		return fmt.Errorf("failed to update manifest.json: %w", err)
	}

	// Write the updated manifest.json back to disk
	if err := os.WriteFile(manifestPath, updatedManifest, 0644); err != nil {
		return fmt.Errorf("failed to write manifest.json: %w", err)
	}

	log.Debug().Msgf("Successfully added reference to manifest.json: \"%s\" from \"%s\"", packageName, clientRef)
	return nil
}
