/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package filesetwriter

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
)

// ConflictPolicy determines what happens when a target file already exists.
type ConflictPolicy int

const (
	Overwrite ConflictPolicy = iota // Replace the existing file (default).
	Skip                            // Don't write; keep the original.
	Rename                          // Write to AlternatePath instead.
	Update                          // Like Overwrite, but shown as less scary in preview.
)

// PlannedFile represents a single file to be written.
type PlannedFile struct {
	Path          string         // Target path to write to.
	Content       []byte         // File content.
	Perm          os.FileMode    // Permission bits (0644, 0755, etc).
	OnConflict    ConflictPolicy // What to do if Path already exists.
	AlternatePath string         // Used when OnConflict is Rename. Written if Path exists.
	Message       string         // Optional message shown in preview (used by Update).
}

// FileAction describes the resolved action for a file after scanning.
type FileAction int

const (
	ActionCreate    FileAction = iota // File is new, will be created.
	ActionOverwrite                   // File exists, will be overwritten.
	ActionSkip                        // File exists, will be skipped.
	ActionRename                      // File exists, will be written to alternate path.
	ActionUpdate                      // File exists, will be updated with explanation.
)

// FileResult is the scan result for a single planned file.
type FileResult struct {
	File      PlannedFile // The original planned file.
	Action    FileAction  // Resolved action after scan.
	WritePath string      // Actual path that will be written (may differ for Rename).
	Exists    bool        // Target path already exists on disk.
	ReadOnly  bool        // File at WritePath is read-only.
}

// ZipExtraction describes a zip archive to be extracted during Execute.
type ZipExtraction struct {
	ZipPath string // Path to the zip file.
	Prefix  string // Only extract entries with this prefix (e.g., "MetaplaySDK/").
	DestDir string // Destination directory.
	count   int    // Number of files to extract (populated by Scan).
}

// Plan holds planned file operations and their resolved outcomes.
type Plan struct {
	files          []PlannedFile
	zipExtractions []ZipExtraction
	results        []FileResult
	scanned        bool
	written        []string // Paths successfully written during Execute.
	interactive    bool     // Show animated progress (spinner, \r overwrites).
}

// NewPlan creates a new empty file plan.
func NewPlan() *Plan {
	return &Plan{interactive: true}
}

// SetInteractive controls whether Execute shows animated progress (spinner,
// \r line overwrites). Set to false for CI / non-interactive environments.
func (p *Plan) SetInteractive(interactive bool) *Plan {
	p.interactive = interactive
	return p
}

// Add appends a file that will overwrite any existing file at the path.
func (p *Plan) Add(path string, content []byte, perm os.FileMode) *Plan {
	p.files = append(p.files, PlannedFile{
		Path:       path,
		Content:    content,
		Perm:       perm,
		OnConflict: Overwrite,
	})
	return p
}

// AddSkipExisting appends a file that will be skipped if it already exists.
func (p *Plan) AddSkipExisting(path string, content []byte, perm os.FileMode) *Plan {
	p.files = append(p.files, PlannedFile{
		Path:       path,
		Content:    content,
		Perm:       perm,
		OnConflict: Skip,
	})
	return p
}

// AddWithRename appends a file that will be written to alternatePath if
// the primary path already exists.
func (p *Plan) AddWithRename(path, alternatePath string, content []byte, perm os.FileMode) *Plan {
	p.files = append(p.files, PlannedFile{
		Path:          path,
		Content:       content,
		Perm:          perm,
		OnConflict:    Rename,
		AlternatePath: alternatePath,
	})
	return p
}

// AddUpdate appends a file that will be updated if it exists, or created if absent.
// The message is shown in preview to explain the update (e.g., "added io.metaplay.unitysdk reference").
func (p *Plan) AddUpdate(path string, content []byte, perm os.FileMode, message string) *Plan {
	p.files = append(p.files, PlannedFile{
		Path:       path,
		Content:    content,
		Perm:       perm,
		OnConflict: Update,
		Message:    message,
	})
	return p
}

// AddZipExtraction adds a zip archive to be extracted during Execute.
// Only entries matching the given prefix are extracted. The prefix is stripped
// from the entry path before writing to destDir.
func (p *Plan) AddZipExtraction(zipPath, prefix, destDir string) *Plan {
	p.zipExtractions = append(p.zipExtractions, ZipExtraction{
		ZipPath: zipPath,
		Prefix:  prefix,
		DestDir: destDir,
	})
	return p
}

// Scan inspects the filesystem and resolves the action for each planned file.
// For zip extractions, it counts the files to be extracted.
func (p *Plan) Scan() error {
	p.results = make([]FileResult, 0, len(p.files))

	for _, f := range p.files {
		r := FileResult{File: f}

		info, err := os.Stat(f.Path)
		if err != nil && !os.IsNotExist(err) {
			return clierrors.Wrap(err, fmt.Sprintf("Failed to stat %s", f.Path))
		}
		r.Exists = err == nil

		if !r.Exists {
			// File does not exist: always create regardless of policy.
			r.Action = ActionCreate
			r.WritePath = f.Path
		} else {
			switch f.OnConflict {
			case Overwrite:
				r.Action = ActionOverwrite
				r.WritePath = f.Path
				r.ReadOnly = isReadOnly(info)
			case Skip:
				r.Action = ActionSkip
				r.WritePath = ""
			case Rename:
				r.Action = ActionRename
				r.WritePath = f.AlternatePath
				// Check the alternate path for read-only status.
				if altInfo, altErr := os.Stat(f.AlternatePath); altErr == nil {
					r.ReadOnly = isReadOnly(altInfo)
				}
			case Update:
				r.Action = ActionUpdate
				r.WritePath = f.Path
				r.ReadOnly = isReadOnly(info)
			}
		}

		p.results = append(p.results, r)
	}

	// Scan zip extractions: count files matching prefix.
	for i := range p.zipExtractions {
		ze := &p.zipExtractions[i]
		reader, err := zip.OpenReader(ze.ZipPath)
		if err != nil {
			return clierrors.Wrap(err, fmt.Sprintf("Failed to open zip archive %s", ze.ZipPath))
		}
		count := 0
		for _, f := range reader.File {
			if strings.HasPrefix(f.Name, ze.Prefix) && !f.FileInfo().IsDir() {
				count++
			}
		}
		reader.Close()
		ze.count = count
	}

	p.scanned = true
	return nil
}

// Results returns the scan results. Panics if Scan has not been called.
func (p *Plan) Results() []FileResult {
	if !p.scanned {
		panic("filesetwriter: Results() called before Scan()")
	}
	return p.results
}

// FilesToWrite returns the number of files that will actually be written
// (excludes skipped files). Includes files from zip extractions.
func (p *Plan) FilesToWrite() int {
	if !p.scanned {
		panic("filesetwriter: FilesToWrite() called before Scan()")
	}
	count := 0
	for _, r := range p.results {
		if r.Action != ActionSkip {
			count++
		}
	}
	for _, ze := range p.zipExtractions {
		count += ze.count
	}
	return count
}

// HasReadOnlyFiles returns true if any writable target is read-only.
func (p *Plan) HasReadOnlyFiles() bool {
	if !p.scanned {
		panic("filesetwriter: HasReadOnlyFiles() called before Scan()")
	}
	for _, r := range p.results {
		if r.ReadOnly && r.Action != ActionSkip {
			return true
		}
	}
	return false
}

// HasConflicts returns true if any result has an action that represents a
// genuinely unexpected change: an overwrite or a rename. Routine actions
// (create, skip, update) are not considered conflicts.
func (p *Plan) HasConflicts() bool {
	if !p.scanned {
		panic("filesetwriter: HasConflicts() called before Scan()")
	}
	for _, r := range p.results {
		if r.Action == ActionOverwrite || r.Action == ActionRename {
			return true
		}
	}
	return false
}

// previewEntry represents a single line in the preview output — either an
// individual file or a collapsed directory summary.
type previewEntry struct {
	isGroup bool       // true for collapsed directory groups.
	dir     string     // for groups: the directory path.
	count   int        // for groups: number of new files.
	result  FileResult // for individual files.
}

// buildPreviewEntries collapses runs of ActionCreate files into directory
// summaries where an entire directory subtree contains only new files.
// Non-ActionCreate files are always shown individually.
func buildPreviewEntries(results []FileResult) []previewEntry {
	// Build set of tainted directories. A directory is tainted if it directly
	// contains a non-ActionCreate file. Tainting propagates to all ancestors.
	tainted := map[string]bool{}
	for _, r := range results {
		if r.Action == ActionCreate {
			continue
		}
		dir := filepath.Dir(r.File.Path)
		for {
			if tainted[dir] {
				break
			}
			tainted[dir] = true
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	// For each ActionCreate file, find the shallowest un-tainted ancestor.
	fileCollapse := make([]string, len(results))
	groupCounts := map[string]int{}
	for i, r := range results {
		if r.Action != ActionCreate {
			continue
		}
		dir := findCollapseDir(r.WritePath, tainted)
		if dir != "" {
			fileCollapse[i] = dir
			groupCounts[dir]++
		}
	}

	// Groups of size 1 are shown as individual files.
	for i, dir := range fileCollapse {
		if dir != "" && groupCounts[dir] < 2 {
			fileCollapse[i] = ""
		}
	}

	// Build entries in insertion order.
	entries := []previewEntry{}
	emitted := map[string]bool{}
	for i, r := range results {
		if dir := fileCollapse[i]; dir != "" {
			if !emitted[dir] {
				emitted[dir] = true
				entries = append(entries, previewEntry{
					isGroup: true,
					dir:     dir,
					count:   groupCounts[dir],
				})
			}
			continue
		}
		entries = append(entries, previewEntry{result: r})
	}
	return entries
}

// findCollapseDir returns the shallowest un-tainted ancestor directory for
// the given path. Returns "" if no un-tainted ancestor exists (the file
// should be shown individually). The filesystem root (., /, C:\) is never
// used as a collapse target.
func findCollapseDir(path string, tainted map[string]bool) string {
	dir := filepath.Dir(path)
	best := ""
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		if !tainted[dir] {
			best = dir
		}
		dir = parent
	}
	return best
}

// Preview logs a summary of the planned file operations, collapsing
// directories that contain only new files into summary lines.
func (p *Plan) Preview() {
	if !p.scanned {
		panic("filesetwriter: Preview() called before Scan()")
	}

	entries := buildPreviewEntries(p.results)

	// Show zip extractions first.
	for _, ze := range p.zipExtractions {
		displayDir := strings.TrimSuffix(ze.Prefix, "/") + "/"
		log.Info().Msgf("  %s%s", styles.RenderTechnical(displayDir),
			styles.RenderSuccess(fmt.Sprintf(" (%d new files)", ze.count)))
	}

	for _, e := range entries {
		if e.isGroup {
			displayDir := filepath.ToSlash(e.dir) + "/"
			log.Info().Msgf("  %s%s", styles.RenderTechnical(displayDir),
				styles.RenderSuccess(fmt.Sprintf(" (%d new files)", e.count)))
			continue
		}

		r := e.result
		badge := ""
		switch r.Action {
		case ActionCreate:
			badge = styles.RenderSuccess(" (new)")
		case ActionOverwrite:
			badge = styles.RenderAttention(" (overwrite)")
		case ActionSkip:
			badge = styles.RenderMuted(" (skip, exists)")
		case ActionRename:
			badge = styles.RenderAttention(fmt.Sprintf(" (write as %s, original exists)", filepath.Base(r.WritePath)))
		case ActionUpdate:
			if r.File.Message != "" {
				badge = styles.RenderSuccess(fmt.Sprintf(" (%s)", r.File.Message))
			} else {
				badge = styles.RenderSuccess(" (update)")
			}
		}

		readOnlyBadge := ""
		if r.ReadOnly && r.Action != ActionSkip {
			readOnlyBadge = styles.RenderWarning(" [read-only]")
		}

		displayPath := r.WritePath
		if r.Action == ActionSkip {
			displayPath = r.File.Path
		}
		displayPath = filepath.ToSlash(displayPath)

		log.Info().Msgf("  %s%s%s", styles.RenderTechnical(displayPath), badge, readOnlyBadge)
	}
}

// Execute writes all planned files to disk and extracts any zip archives.
// On failure, the error includes details about which files were already written.
// Use Written() to retrieve the list of successfully written paths.
func (p *Plan) Execute() error {
	if !p.scanned {
		panic("filesetwriter: Execute() called before Scan()")
	}

	p.written = nil

	// Extract zip archives first.
	for _, ze := range p.zipExtractions {
		if err := p.executeZipExtraction(ze); err != nil {
			return err
		}
	}

	for _, r := range p.results {
		if r.Action == ActionSkip {
			log.Info().Msgf("  %s", styles.RenderMuted(fmt.Sprintf("Skipped %s (already exists)", r.File.Path)))
			continue
		}

		dir := filepath.Dir(r.WritePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return p.wrapWriteError(err, fmt.Sprintf("Failed to create directory %s", dir))
		}

		if err := os.WriteFile(r.WritePath, r.File.Content, r.File.Perm); err != nil {
			return p.wrapWriteError(err, fmt.Sprintf("Failed to write file %s", r.WritePath))
		}

		p.written = append(p.written, r.WritePath)

		switch r.Action {
		case ActionUpdate:
			log.Info().Msgf("  %s", styles.RenderMuted("Updated "+r.WritePath))
		default:
			log.Info().Msgf("  %s", styles.RenderMuted("Created "+r.WritePath))
		}
	}

	return nil
}

// executeZipExtraction extracts files from a zip archive with progress reporting.
func (p *Plan) executeZipExtraction(ze ZipExtraction) error {
	reader, err := zip.OpenReader(ze.ZipPath)
	if err != nil {
		return clierrors.Wrap(err, fmt.Sprintf("Failed to open zip archive %s", ze.ZipPath))
	}
	defer reader.Close()

	displayName := strings.TrimSuffix(ze.Prefix, "/")
	cleanDest := filepath.Clean(ze.DestDir)
	spinnerFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	start := time.Now()
	extracted := 0

	for _, file := range reader.File {
		if !strings.HasPrefix(file.Name, ze.Prefix) {
			continue
		}
		if file.FileInfo().IsDir() {
			continue
		}

		// Construct target path and guard against zip slip.
		targetPath := filepath.Join(ze.DestDir, file.Name)
		if !strings.HasPrefix(filepath.Clean(targetPath), cleanDest+string(filepath.Separator)) {
			return clierrors.Newf("Zip entry %q escapes destination directory", file.Name)
		}

		// Create parent directories.
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return p.wrapWriteError(err, fmt.Sprintf("Failed to create directory for %s", targetPath))
		}

		// Extract file.
		if err := extractZipFile(file, targetPath); err != nil {
			return p.wrapWriteError(err, fmt.Sprintf("Failed to extract %s", file.Name))
		}

		extracted++

		// Show animated progress in interactive mode only.
		if p.interactive {
			fmt.Fprintf(os.Stderr, "\r %s Extracting %s... %d/%d files",
				styles.RenderMuted(spinnerFrames[extracted%len(spinnerFrames)]), displayName, extracted, ze.count)
		}
	}

	elapsed := time.Since(start)

	if p.interactive {
		// Clear the progress line.
		fmt.Fprintf(os.Stderr, "\r\033[K")
	}
	log.Info().Msgf(" %s Extracted %s (%d files) %s",
		styles.RenderSuccess("✓"), displayName, extracted,
		styles.RenderMuted(fmt.Sprintf("[%.1fs]", elapsed.Seconds())))

	return nil
}

// extractZipFile extracts a single file from a zip archive to the target path.
func extractZipFile(file *zip.File, targetPath string) error {
	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	outFile, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	_, err = io.Copy(outFile, rc)
	return err
}

// Written returns the paths that were successfully written during Execute.
// Before a failure this is the partial list; on success it is all written paths.
func (p *Plan) Written() []string {
	return p.written
}

// wrapWriteError wraps a write error with details about previously written files.
func (p *Plan) wrapWriteError(err error, message string) error {
	cliErr := clierrors.Wrap(err, message).
		WithSuggestion("Check that you have write permissions to the output directory")
	if len(p.written) > 0 {
		details := make([]string, 0, len(p.written)+1)
		details = append(details, fmt.Sprintf("Successfully wrote %d file(s) before failure:", len(p.written)))
		for _, w := range p.written {
			details = append(details, fmt.Sprintf("  %s", w))
		}
		cliErr = cliErr.WithDetails(details...)
	}
	return cliErr
}

// isReadOnly returns true if the file's permission bits indicate it is read-only
// (owner write bit not set). On Windows, Go maps the read-only attribute to
// permission bits.
func isReadOnly(info os.FileInfo) bool {
	return info.Mode().Perm()&0200 == 0
}
