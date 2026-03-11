/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package filesetwriter

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestNewPlanIsEmpty(t *testing.T) {
	p := NewPlan(false)
	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}
	if got := p.FilesToWrite(); got != 0 {
		t.Fatalf("expected 0 files to write, got %d", got)
	}
	if got := len(p.Results()); got != 0 {
		t.Fatalf("expected 0 results, got %d", got)
	}
}

func TestAddCreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	p := NewPlan(false)
	p.Add(path, []byte("hello"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	results := p.Results()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Action != ActionCreate {
		t.Fatalf("expected ActionCreate, got %d", r.Action)
	}
	if r.Exists {
		t.Fatal("expected Exists=false for new file")
	}
	if r.WritePath != path {
		t.Fatalf("expected WritePath=%s, got %s", path, r.WritePath)
	}
	if p.FilesToWrite() != 1 {
		t.Fatalf("expected 1 file to write, got %d", p.FilesToWrite())
	}
}

func TestAddOverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")
	os.WriteFile(path, []byte("old"), 0644)

	p := NewPlan(false)
	p.Add(path, []byte("new"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	r := p.Results()[0]
	if r.Action != ActionOverwrite {
		t.Fatalf("expected ActionOverwrite, got %d", r.Action)
	}
	if !r.Exists {
		t.Fatal("expected Exists=true")
	}
	if r.WritePath != path {
		t.Fatalf("expected WritePath=%s, got %s", path, r.WritePath)
	}
}

func TestAddSkipExistingSkipsWhenPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")
	os.WriteFile(path, []byte("keep"), 0644)

	p := NewPlan(false)
	p.AddSkipExisting(path, []byte("new"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	r := p.Results()[0]
	if r.Action != ActionSkip {
		t.Fatalf("expected ActionSkip, got %d", r.Action)
	}
	if r.WritePath != "" {
		t.Fatalf("expected empty WritePath for skip, got %s", r.WritePath)
	}
	if p.FilesToWrite() != 0 {
		t.Fatalf("expected 0 files to write, got %d", p.FilesToWrite())
	}
}

func TestAddSkipExistingCreatesWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	p := NewPlan(false)
	p.AddSkipExisting(path, []byte("content"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	r := p.Results()[0]
	if r.Action != ActionCreate {
		t.Fatalf("expected ActionCreate, got %d", r.Action)
	}
	if p.FilesToWrite() != 1 {
		t.Fatalf("expected 1 file to write, got %d", p.FilesToWrite())
	}
}

func TestAddWithRenameUsesAlternateWhenExists(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "primary.txt")
	alternate := filepath.Join(dir, "alternate.txt")
	os.WriteFile(primary, []byte("original"), 0644)

	p := NewPlan(false)
	p.AddWithRename(primary, alternate, []byte("new"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	r := p.Results()[0]
	if r.Action != ActionRename {
		t.Fatalf("expected ActionRename, got %d", r.Action)
	}
	if r.WritePath != alternate {
		t.Fatalf("expected WritePath=%s, got %s", alternate, r.WritePath)
	}
	if p.FilesToWrite() != 1 {
		t.Fatalf("expected 1 file to write, got %d", p.FilesToWrite())
	}
}

func TestAddWithRenameCreatesPrimaryWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "primary.txt")
	alternate := filepath.Join(dir, "alternate.txt")

	p := NewPlan(false)
	p.AddWithRename(primary, alternate, []byte("content"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	r := p.Results()[0]
	if r.Action != ActionCreate {
		t.Fatalf("expected ActionCreate, got %d", r.Action)
	}
	if r.WritePath != primary {
		t.Fatalf("expected WritePath=%s, got %s", primary, r.WritePath)
	}
}

func TestReadOnlyDetection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "readonly.txt")
	os.WriteFile(path, []byte("locked"), 0444)

	p := NewPlan(false)
	p.Add(path, []byte("new"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	r := p.Results()[0]
	if !r.ReadOnly {
		t.Fatal("expected ReadOnly=true for 0444 file")
	}
	if !p.HasReadOnlyFiles() {
		t.Fatal("expected HasReadOnlyFiles()=true")
	}
}

func TestReadOnlySkippedFileNotReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "readonly.txt")
	os.WriteFile(path, []byte("locked"), 0444)

	p := NewPlan(false)
	p.AddSkipExisting(path, []byte("new"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	if p.HasReadOnlyFiles() {
		t.Fatal("expected HasReadOnlyFiles()=false for skipped read-only file")
	}
}

func TestReadOnlyAlternatePath(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "primary.txt")
	alternate := filepath.Join(dir, "alternate.txt")
	os.WriteFile(primary, []byte("original"), 0644)
	os.WriteFile(alternate, []byte("locked"), 0444)

	p := NewPlan(false)
	p.AddWithRename(primary, alternate, []byte("new"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	r := p.Results()[0]
	if !r.ReadOnly {
		t.Fatal("expected ReadOnly=true for read-only alternate path")
	}
}

func TestExecuteCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	path1 := filepath.Join(dir, "sub", "file1.txt")
	path2 := filepath.Join(dir, "file2.txt")

	p := NewPlan(false)
	p.Add(path1, []byte("content1"), 0644)
	p.Add(path2, []byte("content2"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := p.Execute(); err != nil {
		t.Fatal(err)
	}

	// Verify files were written.
	data1, err := os.ReadFile(path1)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path1, err)
	}
	if string(data1) != "content1" {
		t.Fatalf("expected content1, got %s", data1)
	}
	data2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path2, err)
	}
	if string(data2) != "content2" {
		t.Fatalf("expected content2, got %s", data2)
	}

	// Verify Written() list.
	written := p.Written()
	if len(written) != 2 {
		t.Fatalf("expected 2 written paths, got %d", len(written))
	}
}

func TestExecuteOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("old"), 0644)

	p := NewPlan(false)
	p.Add(path, []byte("new"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := p.Execute(); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Fatalf("expected new, got %s", data)
	}
}

func TestExecuteSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("keep"), 0644)

	p := NewPlan(false)
	p.AddSkipExisting(path, []byte("ignored"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := p.Execute(); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "keep" {
		t.Fatalf("expected keep, got %s", data)
	}
	if len(p.Written()) != 0 {
		t.Fatalf("expected 0 written, got %d", len(p.Written()))
	}
}

func TestExecuteRenames(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "primary.txt")
	alternate := filepath.Join(dir, "alternate.txt")
	os.WriteFile(primary, []byte("original"), 0644)

	p := NewPlan(false)
	p.AddWithRename(primary, alternate, []byte("new"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := p.Execute(); err != nil {
		t.Fatal(err)
	}

	// Primary should be untouched.
	data, _ := os.ReadFile(primary)
	if string(data) != "original" {
		t.Fatalf("expected primary to be untouched, got %s", data)
	}
	// Alternate should have new content.
	data, _ = os.ReadFile(alternate)
	if string(data) != "new" {
		t.Fatalf("expected new in alternate, got %s", data)
	}
	if len(p.Written()) != 1 || p.Written()[0] != alternate {
		t.Fatalf("expected Written=[%s], got %v", alternate, p.Written())
	}
}

func TestExecuteTracksPartialWrites(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.txt")
	// Place a regular file where MkdirAll expects a directory, forcing a failure.
	blocker := filepath.Join(dir, "blocker")
	os.WriteFile(blocker, []byte("I'm a file"), 0644)
	bad := filepath.Join(blocker, "sub", "bad.txt")

	p := NewPlan(false)
	p.Add(good, []byte("ok"), 0644)
	p.Add(bad, []byte("fail"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}
	err := p.Execute()
	if err == nil {
		t.Fatal("expected error from Execute")
	}

	written := p.Written()
	if len(written) != 1 || written[0] != good {
		t.Fatalf("expected Written=[%s], got %v", good, written)
	}
}

func TestChainingAPI(t *testing.T) {
	dir := t.TempDir()

	p := NewPlan(false)
	p.Add(filepath.Join(dir, "a.txt"), []byte("a"), 0644).
		Add(filepath.Join(dir, "b.txt"), []byte("b"), 0644).
		AddSkipExisting(filepath.Join(dir, "c.txt"), []byte("c"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}
	if got := len(p.Results()); got != 3 {
		t.Fatalf("expected 3 results, got %d", got)
	}
}

func TestMultiplePoliciesMixed(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.txt")
	os.WriteFile(existing, []byte("old"), 0644)

	newFile := filepath.Join(dir, "new.txt")
	skipFile := filepath.Join(dir, "existing.txt")
	overwriteFile := filepath.Join(dir, "existing.txt")

	// Use separate paths for each to avoid ambiguity; reuse existing for skip+overwrite.
	overwrite := filepath.Join(dir, "overwrite.txt")
	os.WriteFile(overwrite, []byte("old2"), 0644)

	p := NewPlan(false)
	p.Add(newFile, []byte("new"), 0644)
	p.AddSkipExisting(skipFile, []byte("skip"), 0644)
	p.Add(overwrite, []byte("replaced"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	results := p.Results()
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// new.txt -> ActionCreate (doesn't exist, skipFile path == existing.txt)
	if results[0].Action != ActionCreate {
		t.Fatalf("expected ActionCreate for new file, got %d", results[0].Action)
	}
	// existing.txt with Skip -> ActionSkip
	if results[1].Action != ActionSkip {
		t.Fatalf("expected ActionSkip, got %d", results[1].Action)
	}
	// overwrite.txt with Overwrite -> ActionOverwrite
	if results[2].Action != ActionOverwrite {
		t.Fatalf("expected ActionOverwrite, got %d", results[2].Action)
	}

	_ = overwriteFile // used in skip test above
}

func TestWrittenEmptyBeforeExecute(t *testing.T) {
	p := NewPlan(false)
	if got := p.Written(); got != nil {
		t.Fatalf("expected nil Written before Execute, got %v", got)
	}
}

func TestExecuteCreatesSubdirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "file.txt")

	p := NewPlan(false)
	p.Add(path, []byte("deep"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := p.Execute(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read deeply nested file: %v", err)
	}
	if string(data) != "deep" {
		t.Fatalf("expected deep, got %s", data)
	}
}

// --- Preview collapsing tests ---

func makeResult(path string, action FileAction) FileResult {
	return FileResult{
		File:      PlannedFile{Path: path},
		Action:    action,
		WritePath: path,
	}
}

func TestPreviewCollapsesAllNewDirectory(t *testing.T) {
	results := []FileResult{
		makeResult(filepath.Join("Backend", "a.txt"), ActionCreate),
		makeResult(filepath.Join("Backend", "b.txt"), ActionCreate),
		makeResult(filepath.Join("Backend", "sub", "c.txt"), ActionCreate),
	}
	entries := buildPreviewEntries(results)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !entries[0].isGroup {
		t.Fatal("expected group entry")
	}
	if entries[0].dir != "Backend" {
		t.Fatalf("expected dir=Backend, got %s", entries[0].dir)
	}
	if entries[0].count != 3 {
		t.Fatalf("expected count=3, got %d", entries[0].count)
	}
}

func TestPreviewDoesNotCollapseRootFiles(t *testing.T) {
	results := []FileResult{
		makeResult("a.txt", ActionCreate),
		makeResult("b.txt", ActionCreate),
	}
	entries := buildPreviewEntries(results)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for i, e := range entries {
		if e.isGroup {
			t.Fatalf("entry %d should not be a group", i)
		}
	}
}

func TestPreviewMixedDirectoryNotCollapsed(t *testing.T) {
	results := []FileResult{
		makeResult(filepath.Join("dir", "a.txt"), ActionCreate),
		makeResult(filepath.Join("dir", "b.txt"), ActionOverwrite),
	}
	entries := buildPreviewEntries(results)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for i, e := range entries {
		if e.isGroup {
			t.Fatalf("entry %d should not be a group", i)
		}
	}
}

func TestPreviewSubdirCollapsesWhenParentTainted(t *testing.T) {
	skipResult := FileResult{
		File:      PlannedFile{Path: filepath.Join("Assets", "Shared.meta")},
		Action:    ActionSkip,
		WritePath: "",
	}
	results := []FileResult{
		skipResult,
		makeResult(filepath.Join("Assets", "Shared", "a.cs"), ActionCreate),
		makeResult(filepath.Join("Assets", "Shared", "b.cs"), ActionCreate),
	}
	entries := buildPreviewEntries(results)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].isGroup {
		t.Fatal("first entry should be individual skip")
	}
	if !entries[1].isGroup {
		t.Fatal("second entry should be a group")
	}
	if entries[1].dir != filepath.Join("Assets", "Shared") {
		t.Fatalf("expected dir=%s, got %s", filepath.Join("Assets", "Shared"), entries[1].dir)
	}
	if entries[1].count != 2 {
		t.Fatalf("expected count=2, got %d", entries[1].count)
	}
}

func TestPreviewSingleFileGroupShownIndividually(t *testing.T) {
	results := []FileResult{
		makeResult(filepath.Join("dir", "only.txt"), ActionCreate),
	}
	entries := buildPreviewEntries(results)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].isGroup {
		t.Fatal("single-file group should be shown individually")
	}
}

func TestPreviewMultipleGroupsAndIndividuals(t *testing.T) {
	results := []FileResult{
		makeResult("config.yaml", ActionCreate),
		makeResult(filepath.Join("Backend", "a.go"), ActionCreate),
		makeResult(filepath.Join("Backend", "b.go"), ActionCreate),
		makeResult(filepath.Join("Assets", "x.meta"), ActionOverwrite),
		makeResult(filepath.Join("Assets", "Sub", "c.cs"), ActionCreate),
		makeResult(filepath.Join("Assets", "Sub", "d.cs"), ActionCreate),
	}
	entries := buildPreviewEntries(results)

	// Expected: config.yaml (individual), Backend/ (group of 2),
	//           Assets/x.meta (individual), Assets/Sub/ (group of 2)
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}
	if entries[0].isGroup {
		t.Fatal("config.yaml should be individual")
	}
	if !entries[1].isGroup || entries[1].dir != "Backend" || entries[1].count != 2 {
		t.Fatalf("expected Backend group of 2, got %+v", entries[1])
	}
	if entries[2].isGroup {
		t.Fatal("Assets/x.meta should be individual")
	}
	if !entries[3].isGroup || entries[3].dir != filepath.Join("Assets", "Sub") || entries[3].count != 2 {
		t.Fatalf("expected Assets/Sub group of 2, got %+v", entries[3])
	}
}

// --- Update operation tests ---

func TestAddUpdateWhenFileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	os.WriteFile(path, []byte("old"), 0644)

	p := NewPlan(false)
	p.AddUpdate(path, []byte("updated"), 0644, "added reference")

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	r := p.Results()[0]
	if r.Action != ActionUpdate {
		t.Fatalf("expected ActionUpdate, got %d", r.Action)
	}
	if !r.Exists {
		t.Fatal("expected Exists=true")
	}
	if r.WritePath != path {
		t.Fatalf("expected WritePath=%s, got %s", path, r.WritePath)
	}
	if r.File.Message != "added reference" {
		t.Fatalf("expected Message='added reference', got %q", r.File.Message)
	}
}

func TestAddUpdateWhenFileAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	p := NewPlan(false)
	p.AddUpdate(path, []byte("new"), 0644, "initial")

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	r := p.Results()[0]
	if r.Action != ActionCreate {
		t.Fatalf("expected ActionCreate when file absent, got %d", r.Action)
	}
}

func TestExecuteUpdatesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	os.WriteFile(path, []byte("old content"), 0644)

	p := NewPlan(false)
	p.AddUpdate(path, []byte("new content"), 0644, "updated field")

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := p.Execute(); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "new content" {
		t.Fatalf("expected 'new content', got %s", data)
	}

	written := p.Written()
	if len(written) != 1 || written[0] != path {
		t.Fatalf("expected Written=[%s], got %v", path, written)
	}
}

func TestPreviewUpdateNotCollapsed(t *testing.T) {
	// Update entries should be treated as non-create (taint their directory).
	results := []FileResult{
		makeResult(filepath.Join("dir", "a.txt"), ActionCreate),
		{
			File:      PlannedFile{Path: filepath.Join("dir", "b.json"), Message: "updated"},
			Action:    ActionUpdate,
			WritePath: filepath.Join("dir", "b.json"),
		},
	}
	entries := buildPreviewEntries(results)
	// Both should be shown individually since Update taints the directory.
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for i, e := range entries {
		if e.isGroup {
			t.Fatalf("entry %d should not be a group", i)
		}
	}
}

// --- Zip extraction tests ---

// createTestZip creates a small zip file for testing, returning the path.
func createTestZip(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	zipPath := filepath.Join(dir, "test.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)

	for name, content := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, err = fw.Write([]byte(content))
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return zipPath
}

func TestAddZipExtractionScanCounts(t *testing.T) {
	dir := t.TempDir()
	zipPath := createTestZip(t, dir, map[string]string{
		"MetaplaySDK/a.txt":     "a",
		"MetaplaySDK/sub/b.txt": "b",
		"MetaplaySDK/c.txt":     "c",
		"Other/d.txt":           "d",
	})

	p := NewPlan(false)
	p.AddZipExtraction(zipPath, "MetaplaySDK/", dir)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	// Should count 3 files matching prefix (not the Other/ one).
	if p.FilesToWrite() != 3 {
		t.Fatalf("expected 3 files to write, got %d", p.FilesToWrite())
	}
}

func TestAddZipExtractionExecute(t *testing.T) {
	dir := t.TempDir()
	destDir := filepath.Join(dir, "output")
	os.MkdirAll(destDir, 0755)

	zipPath := createTestZip(t, dir, map[string]string{
		"MetaplaySDK/hello.txt":     "hello world",
		"MetaplaySDK/sub/nested.go": "package main",
	})

	p := NewPlan(false)
	p.AddZipExtraction(zipPath, "MetaplaySDK/", destDir)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := p.Execute(); err != nil {
		t.Fatal(err)
	}

	// Verify files were extracted.
	data, err := os.ReadFile(filepath.Join(destDir, "MetaplaySDK", "hello.txt"))
	if err != nil {
		t.Fatalf("failed to read extracted file: %v", err)
	}
	if string(data) != "hello world" {
		t.Fatalf("expected 'hello world', got %s", data)
	}

	data, err = os.ReadFile(filepath.Join(destDir, "MetaplaySDK", "sub", "nested.go"))
	if err != nil {
		t.Fatalf("failed to read nested extracted file: %v", err)
	}
	if string(data) != "package main" {
		t.Fatalf("expected 'package main', got %s", data)
	}
}

func TestAddZipExtractionWithPrefix(t *testing.T) {
	dir := t.TempDir()
	destDir := filepath.Join(dir, "output")
	os.MkdirAll(destDir, 0755)

	zipPath := createTestZip(t, dir, map[string]string{
		"MetaplaySDK/included.txt":    "yes",
		"MetaplaySamples/excluded.txt": "no",
		"other.txt":                    "no",
	})

	p := NewPlan(false)
	p.AddZipExtraction(zipPath, "MetaplaySDK/", destDir)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := p.Execute(); err != nil {
		t.Fatal(err)
	}

	// Included file should exist.
	if _, err := os.Stat(filepath.Join(destDir, "MetaplaySDK", "included.txt")); err != nil {
		t.Fatal("expected MetaplaySDK/included.txt to be extracted")
	}

	// Excluded files should not exist.
	if _, err := os.Stat(filepath.Join(destDir, "MetaplaySamples", "excluded.txt")); err == nil {
		t.Fatal("MetaplaySamples/ should not have been extracted")
	}
	if _, err := os.Stat(filepath.Join(destDir, "other.txt")); err == nil {
		t.Fatal("other.txt should not have been extracted")
	}
}

// --- SetConflictPolicy tests ---

func TestSetConflictPolicyOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("old"), 0644)

	p := NewPlan(false)
	p.AddSkipExisting(path, []byte("new"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}
	if p.Results()[0].Action != ActionSkip {
		t.Fatalf("expected ActionSkip before policy change, got %d", p.Results()[0].Action)
	}

	p.SetConflictPolicy(Overwrite, "")
	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	r := p.Results()[0]
	if r.Action != ActionOverwrite {
		t.Fatalf("expected ActionOverwrite after policy change, got %d", r.Action)
	}
	if r.WritePath != path {
		t.Fatalf("expected WritePath=%s, got %s", path, r.WritePath)
	}
}

func TestSetConflictPolicySkip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("old"), 0644)

	p := NewPlan(false)
	p.Add(path, []byte("new"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}
	if p.Results()[0].Action != ActionOverwrite {
		t.Fatalf("expected ActionOverwrite before policy change, got %d", p.Results()[0].Action)
	}

	p.SetConflictPolicy(Skip, "")
	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	r := p.Results()[0]
	if r.Action != ActionSkip {
		t.Fatalf("expected ActionSkip after policy change, got %d", r.Action)
	}
	if p.FilesToWrite() != 0 {
		t.Fatalf("expected 0 files to write, got %d", p.FilesToWrite())
	}
}

func TestSetConflictPolicyRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("old"), 0644)

	p := NewPlan(false)
	p.Add(path, []byte("new"), 0644)

	p.SetConflictPolicy(Rename, ".new")
	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	r := p.Results()[0]
	if r.Action != ActionRename {
		t.Fatalf("expected ActionRename, got %d", r.Action)
	}
	expectedAlt := path + ".new"
	if r.WritePath != expectedAlt {
		t.Fatalf("expected WritePath=%s, got %s", expectedAlt, r.WritePath)
	}
	if r.File.AlternatePath != expectedAlt {
		t.Fatalf("expected AlternatePath=%s, got %s", expectedAlt, r.File.AlternatePath)
	}
}

func TestSetConflictPolicyNewFilesUnaffected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	p := NewPlan(false)
	p.Add(path, []byte("content"), 0644)

	// Change to Skip — but since the file doesn't exist, it should still be ActionCreate.
	p.SetConflictPolicy(Skip, "")
	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	r := p.Results()[0]
	if r.Action != ActionCreate {
		t.Fatalf("expected ActionCreate for non-existing file regardless of policy, got %d", r.Action)
	}
	if p.FilesToWrite() != 1 {
		t.Fatalf("expected 1 file to write, got %d", p.FilesToWrite())
	}
}

// --- Unchanged detection tests ---

func TestUnchangedFileDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	content := []byte("same content")
	os.WriteFile(path, content, 0644)

	p := NewPlan(false)
	p.Add(path, content, 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	r := p.Results()[0]
	if r.Action != ActionUnchanged {
		t.Fatalf("expected ActionUnchanged, got %d", r.Action)
	}
	if r.WritePath != "" {
		t.Fatalf("expected empty WritePath for unchanged, got %s", r.WritePath)
	}
	if p.FilesToWrite() != 0 {
		t.Fatalf("expected 0 files to write, got %d", p.FilesToWrite())
	}
	if p.HasConflicts() {
		t.Fatal("expected HasConflicts()=false for unchanged file")
	}
}

func TestChangedFileIsConflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("old content"), 0644)

	p := NewPlan(false)
	p.Add(path, []byte("new content"), 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	r := p.Results()[0]
	if r.Action != ActionOverwrite {
		t.Fatalf("expected ActionOverwrite for changed file, got %d", r.Action)
	}
	if !p.HasConflicts() {
		t.Fatal("expected HasConflicts()=true for changed file")
	}
}

func TestUnchangedFileNotWritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	content := []byte("keep this")
	os.WriteFile(path, content, 0644)

	p := NewPlan(false)
	p.Add(path, content, 0644)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := p.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(p.Written()) != 0 {
		t.Fatalf("expected 0 written for unchanged file, got %d", len(p.Written()))
	}
}

func TestFilesToWriteIncludesZipFiles(t *testing.T) {
	dir := t.TempDir()
	zipPath := createTestZip(t, dir, map[string]string{
		"SDK/a.txt": "a",
		"SDK/b.txt": "b",
	})

	p := NewPlan(false)
	p.Add(filepath.Join(dir, "regular.txt"), []byte("r"), 0644)
	p.AddZipExtraction(zipPath, "SDK/", dir)

	if err := p.Scan(); err != nil {
		t.Fatal(err)
	}

	// 1 regular file + 2 zip files = 3
	if got := p.FilesToWrite(); got != 3 {
		t.Fatalf("expected 3 files to write, got %d", got)
	}
}
