/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/rs/zerolog/log"
)

// ModifiedFile represents a file that differs from the original SDK.
type ModifiedFile struct {
	RelativePath string // Path relative to MetaplaySDK/
	ModType      string // "modified", "added", "deleted"
}

// ignoredFilePatterns contains file names and patterns that should be ignored
// during SDK modification detection (OS-generated transient files).
var ignoredFilePatterns = map[string]bool{
	".DS_Store":   true,
	"Thumbs.db":   true,
	"desktop.ini": true,
	"._.DS_Store": true,
}

// ignoredFilePrefixes contains prefixes of files to ignore.
var ignoredFilePrefixes = []string{
	"._", // macOS resource fork files
}

// shouldIgnoreFile returns true if the file should be ignored during comparison.
func shouldIgnoreFile(filename string) bool {
	// Check exact matches
	if ignoredFilePatterns[filename] {
		return true
	}

	// Check prefixes
	for _, prefix := range ignoredFilePrefixes {
		if strings.HasPrefix(filename, prefix) {
			return true
		}
	}

	return false
}

// computeFileChecksum computes SHA256 checksum of a file on disk.
func computeFileChecksum(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// computeZipFileChecksum computes SHA256 checksum of a file within a zip archive.
func computeZipFileChecksum(file *zip.File) (string, error) {
	rc, err := file.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// downloadSdkZipOnly downloads the SDK zip file without extracting it.
// Returns the path to the temporary zip file. Caller is responsible for cleanup.
func downloadSdkZipOnly(tokenSet *auth.TokenSet, versionID string) (string, error) {
	tmpDir := os.TempDir()
	portalClient := portalapi.NewClient(tokenSet)

	sdkZipPath, err := portalClient.DownloadSdkByVersionID(tmpDir, versionID)
	if err != nil {
		return "", fmt.Errorf("failed to download SDK: %w", err)
	}

	return sdkZipPath, nil
}

// detectSdkModifications compares the local SDK directory against the original
// SDK zip file and returns a list of modified files.
func detectSdkModifications(sdkRootDir string, sdkZipPath string) ([]ModifiedFile, error) {
	// Open the SDK zip file
	reader, err := zip.OpenReader(sdkZipPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open SDK zip: %w", err)
	}
	defer reader.Close()

	// Build a map of relativePath -> checksum for all files in MetaplaySDK/ within the zip
	zipChecksums := make(map[string]string)
	for _, file := range reader.File {
		// Only process files within MetaplaySDK/
		if !strings.HasPrefix(file.Name, "MetaplaySDK/") {
			continue
		}

		// Skip directories
		if file.FileInfo().IsDir() {
			continue
		}

		// Get relative path (strip "MetaplaySDK/" prefix)
		relPath := strings.TrimPrefix(file.Name, "MetaplaySDK/")
		if relPath == "" {
			continue
		}

		// Skip ignored files
		if shouldIgnoreFile(filepath.Base(relPath)) {
			continue
		}

		// Compute checksum
		checksum, err := computeZipFileChecksum(file)
		if err != nil {
			return nil, fmt.Errorf("failed to compute checksum for %s in zip: %w", file.Name, err)
		}

		zipChecksums[relPath] = checksum
	}

	log.Debug().Msgf("Found %d files in SDK zip for comparison", len(zipChecksums))

	// Track which zip entries we've seen
	seenInLocal := make(map[string]bool)
	var modifications []ModifiedFile

	// Walk the local SDK directory
	err = filepath.Walk(sdkRootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Get relative path from SDK root
		relPath, err := filepath.Rel(sdkRootDir, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path for %s: %w", path, err)
		}

		// Normalize path separators to forward slashes for comparison
		relPath = filepath.ToSlash(relPath)

		// Skip ignored files
		if shouldIgnoreFile(filepath.Base(relPath)) {
			return nil
		}

		seenInLocal[relPath] = true

		// Check if file exists in zip
		zipChecksum, existsInZip := zipChecksums[relPath]
		if !existsInZip {
			// File was added locally
			modifications = append(modifications, ModifiedFile{
				RelativePath: relPath,
				ModType:      "added",
			})
			return nil
		}

		// Compute local file checksum
		localChecksum, err := computeFileChecksum(path)
		if err != nil {
			return fmt.Errorf("failed to compute checksum for %s: %w", path, err)
		}

		// Compare checksums
		if localChecksum != zipChecksum {
			modifications = append(modifications, ModifiedFile{
				RelativePath: relPath,
				ModType:      "modified",
			})
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk SDK directory: %w", err)
	}

	// Check for deleted files (in zip but not in local)
	for relPath := range zipChecksums {
		if !seenInLocal[relPath] {
			modifications = append(modifications, ModifiedFile{
				RelativePath: relPath,
				ModType:      "deleted",
			})
		}
	}

	log.Debug().Msgf("Found %d modifications in SDK", len(modifications))

	return modifications, nil
}

// formatModifiedFilesList formats the list of modified files for display.
// Limits output to maxDisplay files, showing overflow message if there are more.
func formatModifiedFilesList(modifications []ModifiedFile, maxDisplay int) string {
	if len(modifications) == 0 {
		return ""
	}

	var sb strings.Builder

	// Show individual files up to limit
	displayCount := len(modifications)
	if displayCount > maxDisplay {
		displayCount = maxDisplay
	}

	for i := 0; i < displayCount; i++ {
		m := modifications[i]
		sb.WriteString(fmt.Sprintf("  [%s] %s\n", m.ModType, m.RelativePath))
	}

	// Show overflow message
	if len(modifications) > maxDisplay {
		sb.WriteString(fmt.Sprintf("  ... and %d more file(s)\n", len(modifications)-maxDisplay))
	}

	return sb.String()
}
