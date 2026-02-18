/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	gitignore "github.com/monochromegane/go-gitignore"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/rs/zerolog/log"
)

// ModifiedFile represents a file that differs from the original SDK.
type ModifiedFile struct {
	RelativePath string // Path relative to MetaplaySDK/
	ModType      string // "modified", "added", "deleted"
	IsBinary     bool   // True if file is binary (cannot be included in patch)
}

// SdkModificationResult contains both the list of modifications and the generated patch.
type SdkModificationResult struct {
	Modifications []ModifiedFile
	PatchContent  string // Git-compatible unified diff
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

// gitignoreEntry represents a .gitignore file and its base directory.
type gitignoreEntry struct {
	baseDir string // Directory containing the .gitignore (relative to SDK root)
	matcher gitignore.IgnoreMatcher
}

// gitignoreMatcher wraps gitignore matching functionality for a directory tree.
type gitignoreMatcher struct {
	entries []gitignoreEntry
}

// buildGitignoreMatcherForDir scans a directory tree for all .gitignore files
// and builds a matcher that respects gitignore rules at each level.
func buildGitignoreMatcherForDir(rootDir string) *gitignoreMatcher {
	matcher := &gitignoreMatcher{}

	// Walk the directory tree to find all .gitignore files
	filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Continue on errors
		}

		if info.IsDir() {
			return nil
		}

		if info.Name() == ".gitignore" {
			// Read the file content and use NewGitIgnoreFromReader
			// This avoids issues with NewGitIgnore's base path handling
			content, err := os.ReadFile(path)
			if err != nil {
				log.Debug().Msgf("Failed to read .gitignore at %s: %v", path, err)
				return nil
			}

			gi := gitignore.NewGitIgnoreFromReader("", strings.NewReader(string(content)))

			// Get base directory relative to root
			baseDir, _ := filepath.Rel(rootDir, filepath.Dir(path))
			baseDir = filepath.ToSlash(baseDir)
			if baseDir == "." {
				baseDir = ""
			}

			matcher.entries = append(matcher.entries, gitignoreEntry{
				baseDir: baseDir,
				matcher: gi,
			})
		}

		return nil
	})

	log.Debug().Msgf("Found %d .gitignore files in %s", len(matcher.entries), rootDir)
	return matcher
}

// isIgnored checks if a path should be ignored based on gitignore rules.
// The path should be relative to the root directory used when building the matcher.
// This also checks if any parent directory of the path is ignored.
func (m *gitignoreMatcher) isIgnored(relativePath string, isDir bool) bool {
	if m == nil || len(m.entries) == 0 {
		return false
	}

	// Normalize path
	relativePath = filepath.ToSlash(relativePath)

	// Check the path itself and all parent directories
	// For a path like "Backend/Attributes/bin/Debug/file.dll", we need to check:
	// - Backend (as dir)
	// - Backend/Attributes (as dir)
	// - Backend/Attributes/bin (as dir)
	// - Backend/Attributes/bin/Debug (as dir)
	// - Backend/Attributes/bin/Debug/file.dll (as file or dir based on isDir)
	parts := strings.Split(relativePath, "/")
	for i := 1; i <= len(parts); i++ {
		partialPath := strings.Join(parts[:i], "/")
		isLastPart := i == len(parts)
		checkAsDir := !isLastPart || isDir

		if m.isPathIgnored(partialPath, checkAsDir) {
			return true
		}
	}

	return false
}

// isPathIgnored checks if a specific path matches any gitignore pattern.
func (m *gitignoreMatcher) isPathIgnored(relativePath string, isDir bool) bool {
	for _, entry := range m.entries {
		// Check if this gitignore applies to the path
		// A gitignore applies if the path is within or below its base directory
		if entry.baseDir != "" && !strings.HasPrefix(relativePath, entry.baseDir+"/") {
			continue
		}

		// Get path relative to the gitignore's base directory
		var pathForMatch string
		if entry.baseDir == "" {
			pathForMatch = relativePath
		} else {
			pathForMatch = strings.TrimPrefix(relativePath, entry.baseDir+"/")
		}

		if entry.matcher.Match(pathForMatch, isDir) {
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

// computeChecksumBytes computes SHA256 checksum of a byte slice.
func computeChecksumBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// readZipFileContent reads the entire content of a file from a zip archive.
func readZipFileContent(file *zip.File) ([]byte, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// isBinaryContent checks if content appears to be binary (contains null bytes).
func isBinaryContent(data []byte) bool {
	checkSize := min(len(data), 8192)
	for i := range checkSize {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

// generateUnifiedDiff creates a git-compatible unified diff for a single file.
// Returns empty string for binary files (they cannot be included in text patches).
func generateUnifiedDiff(pathInPatch string, oldContent, newContent []byte, isNew, isDeleted bool) string {
	var buf bytes.Buffer

	// Skip binary files - they cannot be represented in text patches
	if isBinaryContent(oldContent) || isBinaryContent(newContent) {
		return ""
	}

	// Convert to strings
	oldText := string(oldContent)
	newText := string(newContent)

	// Use go-diff to compute line-based diff
	dmp := diffmatchpatch.New()

	// Convert text to line-based representation for diffing
	chars1, chars2, lineArray := dmp.DiffLinesToChars(oldText, newText)

	// Compute diff on the line-based representation
	diffs := dmp.DiffMain(chars1, chars2, false)

	// Convert back to lines
	diffs = dmp.DiffCharsToLines(diffs, lineArray)

	// Clean up
	diffs = dmp.DiffCleanupSemantic(diffs)

	// Write git-style header
	buf.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", pathInPatch, pathInPatch))
	if isNew {
		buf.WriteString("new file mode 100644\n")
	} else if isDeleted {
		buf.WriteString("deleted file mode 100644\n")
	}

	if isNew {
		buf.WriteString("--- /dev/null\n")
	} else {
		buf.WriteString(fmt.Sprintf("--- a/%s\n", pathInPatch))
	}

	if isDeleted {
		buf.WriteString("+++ /dev/null\n")
	} else {
		buf.WriteString(fmt.Sprintf("+++ b/%s\n", pathInPatch))
	}

	// Generate unified diff content from the diffs
	hunkContent := formatDiffsAsUnifiedHunks(diffs, 3)
	buf.WriteString(hunkContent)

	return buf.String()
}

// formatDiffsAsUnifiedHunks formats go-diff output as unified diff hunks.
func formatDiffsAsUnifiedHunks(diffs []diffmatchpatch.Diff, contextLines int) string {
	if len(diffs) == 0 {
		return ""
	}

	// Check if there are any actual changes
	hasChanges := false
	for _, d := range diffs {
		if d.Type != diffmatchpatch.DiffEqual {
			hasChanges = true
			break
		}
	}
	if !hasChanges {
		return ""
	}

	var buf bytes.Buffer

	// Convert diffs to line operations
	type lineOp struct {
		op   diffmatchpatch.Operation
		line string
	}
	var ops []lineOp

	for _, d := range diffs {
		lines := strings.Split(d.Text, "\n")
		// Handle trailing newline - if text ends with \n, last element is empty
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		for _, line := range lines {
			ops = append(ops, lineOp{op: d.Type, line: line})
		}
	}

	if len(ops) == 0 {
		return ""
	}

	// Find ranges of changes and group into hunks with context
	type hunkRange struct {
		start int
		end   int
	}
	var changeRanges []hunkRange

	inChange := false
	changeStart := 0
	for i, op := range ops {
		if op.op != diffmatchpatch.DiffEqual {
			if !inChange {
				inChange = true
				changeStart = i
			}
		} else {
			if inChange {
				changeRanges = append(changeRanges, hunkRange{start: changeStart, end: i})
				inChange = false
			}
		}
	}
	if inChange {
		changeRanges = append(changeRanges, hunkRange{start: changeStart, end: len(ops)})
	}

	// Merge nearby ranges (within 2*contextLines)
	var mergedRanges []hunkRange
	for _, r := range changeRanges {
		if len(mergedRanges) == 0 {
			mergedRanges = append(mergedRanges, r)
		} else {
			last := &mergedRanges[len(mergedRanges)-1]
			if r.start-last.end <= 2*contextLines {
				last.end = r.end
			} else {
				mergedRanges = append(mergedRanges, r)
			}
		}
	}

	// Generate hunks
	for _, r := range mergedRanges {
		// Expand range to include context
		hunkStart := max(r.start-contextLines, 0)
		hunkEnd := min(r.end+contextLines, len(ops))

		// Count old and new lines, track positions
		oldLineNum := 1
		newLineNum := 1

		// Calculate starting positions by scanning ops before hunk
		for i := range hunkStart {
			switch ops[i].op {
			case diffmatchpatch.DiffEqual:
				oldLineNum++
				newLineNum++
			case diffmatchpatch.DiffDelete:
				oldLineNum++
			case diffmatchpatch.DiffInsert:
				newLineNum++
			}
		}

		oldStart := oldLineNum
		newStart := newLineNum

		// Build hunk content and count lines
		var hunkLines []string
		oldCount := 0
		newCount := 0

		for i := hunkStart; i < hunkEnd; i++ {
			op := ops[i]
			// Strip trailing CR to normalize line endings in the patch (patches use LF only)
			line := strings.TrimSuffix(op.line, "\r")
			switch op.op {
			case diffmatchpatch.DiffEqual:
				hunkLines = append(hunkLines, " "+line)
				oldCount++
				newCount++
			case diffmatchpatch.DiffDelete:
				hunkLines = append(hunkLines, "-"+line)
				oldCount++
			case diffmatchpatch.DiffInsert:
				hunkLines = append(hunkLines, "+"+line)
				newCount++
			}
		}

		// Adjust start positions for new/deleted files (git diff format requires 0 when count is 0)
		if oldCount == 0 {
			oldStart = 0
		}
		if newCount == 0 {
			newStart = 0
		}

		// Write hunk header
		buf.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount))

		// Write hunk lines
		for _, line := range hunkLines {
			buf.WriteString(line)
			buf.WriteString("\n")
		}
	}

	return buf.String()
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

// zipFileEntry holds both checksum and file reference for zip entries.
type zipFileEntry struct {
	checksum string
	file     *zip.File
}

// DetectSdkModificationsWithPatch compares the local SDK directory against the original
// SDK zip file and returns both the list of modifications and a git-compatible patch.
func DetectSdkModificationsWithPatch(sdkRootDir string, sdkZipPath string) (*SdkModificationResult, error) {
	// Build gitignore matcher by scanning the SDK directory for all .gitignore files
	gitMatcher := buildGitignoreMatcherForDir(sdkRootDir)

	// Open the SDK zip file
	reader, err := zip.OpenReader(sdkZipPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open SDK zip: %w", err)
	}
	defer reader.Close()

	// Build a map of relativePath -> {checksum, *zip.File} for all files in MetaplaySDK/ within the zip
	zipEntries := make(map[string]zipFileEntry)
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

		// Skip ignored files (OS-specific)
		if shouldIgnoreFile(filepath.Base(relPath)) {
			continue
		}

		// Skip files ignored by .gitignore (path relative to SDK root)
		if gitMatcher.isIgnored(relPath, false) {
			continue
		}

		// Compute checksum
		checksum, err := computeZipFileChecksum(file)
		if err != nil {
			return nil, fmt.Errorf("failed to compute checksum for %s in zip: %w", file.Name, err)
		}

		zipEntries[relPath] = zipFileEntry{checksum: checksum, file: file}
	}

	log.Debug().Msgf("Found %d files in SDK zip for comparison", len(zipEntries))

	// Track which zip entries we've seen
	seenInLocal := make(map[string]bool)
	var modifications []ModifiedFile
	var patchBuf bytes.Buffer

	// Walk the local SDK directory - detect and generate diff in one pass
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

		// Skip ignored files (OS-specific)
		if shouldIgnoreFile(filepath.Base(relPath)) {
			return nil
		}

		// Skip files ignored by .gitignore (path relative to SDK root)
		if gitMatcher.isIgnored(relPath, false) {
			return nil
		}

		seenInLocal[relPath] = true

		// Read local file content (we'll use it for both checksum and diff)
		localContent, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", path, err)
		}

		patchPath := "MetaplaySDK/" + relPath

		// Check if file exists in zip
		entry, existsInZip := zipEntries[relPath]
		if !existsInZip {
			// File was added locally
			isBinary := isBinaryContent(localContent)
			modifications = append(modifications, ModifiedFile{
				RelativePath: relPath,
				ModType:      "added",
				IsBinary:     isBinary,
			})
			if !isBinary {
				patchBuf.WriteString(generateUnifiedDiff(patchPath, nil, localContent, true, false))
			}
			return nil
		}

		// Compute local file checksum from content we already read
		localChecksum := computeChecksumBytes(localContent)

		// Compare checksums
		if localChecksum != entry.checksum {
			// Read original content from zip for diff
			origContent, err := readZipFileContent(entry.file)
			if err != nil {
				log.Debug().Msgf("Could not read original file for diff: %v", err)
				origContent = nil
			}

			isBinary := isBinaryContent(localContent) || isBinaryContent(origContent)
			modifications = append(modifications, ModifiedFile{
				RelativePath: relPath,
				ModType:      "modified",
				IsBinary:     isBinary,
			})
			if !isBinary && origContent != nil {
				patchBuf.WriteString(generateUnifiedDiff(patchPath, origContent, localContent, false, false))
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk SDK directory: %w", err)
	}

	// Check for deleted files (in zip but not in local)
	for relPath, entry := range zipEntries {
		if !seenInLocal[relPath] {
			// Read original content from zip for diff
			origContent, err := readZipFileContent(entry.file)
			if err != nil {
				log.Debug().Msgf("Could not read original file for diff: %v", err)
				origContent = nil
			}

			isBinary := isBinaryContent(origContent)
			modifications = append(modifications, ModifiedFile{
				RelativePath: relPath,
				ModType:      "deleted",
				IsBinary:     isBinary,
			})
			if !isBinary && origContent != nil {
				patchBuf.WriteString(generateUnifiedDiff("MetaplaySDK/"+relPath, origContent, nil, false, true))
			}
		}
	}

	log.Debug().Msgf("Found %d modifications in SDK", len(modifications))

	return &SdkModificationResult{
		Modifications: modifications,
		PatchContent:  patchBuf.String(),
	}, nil
}

// countBinaryFiles returns the number of binary files in the modifications list.
func countBinaryFiles(modifications []ModifiedFile) int {
	count := 0
	for _, m := range modifications {
		if m.IsBinary {
			count++
		}
	}
	return count
}

// printModifiedFilesList prints the list of modified files to the log.
// Limits output to maxDisplay files, showing overflow message if there are more.
func printModifiedFilesList(modifications []ModifiedFile, maxDisplay int) {
	if len(modifications) == 0 {
		return
	}

	// Show individual files up to limit
	displayCount := min(len(modifications), maxDisplay)

	for i := range displayCount {
		m := modifications[i]
		if m.IsBinary {
			log.Info().Msgf("  [%s] %s (binary)", m.ModType, m.RelativePath)
		} else {
			log.Info().Msgf("  [%s] %s", m.ModType, m.RelativePath)
		}
	}

	// Show overflow message
	if len(modifications) > maxDisplay {
		log.Info().Msgf("  ... and %d more file(s)", len(modifications)-maxDisplay)
	}
}
