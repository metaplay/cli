/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitignoreMatching(t *testing.T) {
	// Create a temporary directory structure that mimics MetaplaySDK
	tmpDir, err := os.MkdirTemp("", "sdk-gitignore-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create .gitignore at root with patterns similar to MetaplaySDK
	gitignoreContent := `# Build results
bin/
obj/
__pycache__/

# Visual Studio
.vs/

# Node
node_modules/
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(gitignoreContent), 0644); err != nil {
		t.Fatalf("Failed to write .gitignore: %v", err)
	}

	// Create directory structure
	dirs := []string{
		"Backend/Attributes/bin/Debug/net9.0",
		"Backend/Attributes/obj/Debug/net9.0",
		"Backend/Server/bin/Release",
		"Backend/Server/obj/Release",
		"Backend/Server/src",
		"Frontend/node_modules/somepackage",
		"Frontend/src",
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(tmpDir, dir), 0755); err != nil {
			t.Fatalf("Failed to create dir %s: %v", dir, err)
		}
	}

	// Build the matcher
	matcher := buildGitignoreMatcherForDir(tmpDir)

	// Test cases
	tests := []struct {
		path     string
		isDir    bool
		expected bool
		desc     string
	}{
		// Direct bin/obj directories should be ignored
		{"bin", true, true, "root bin directory"},
		{"obj", true, true, "root obj directory"},

		// Nested bin/obj directories should be ignored
		{"Backend/Attributes/bin", true, true, "nested bin directory"},
		{"Backend/Attributes/obj", true, true, "nested obj directory"},
		{"Backend/Server/bin", true, true, "another nested bin directory"},

		// Files inside bin/obj should be ignored (via parent directory check)
		{"Backend/Attributes/bin/Debug/net9.0/Metaplay.Attributes.dll", false, true, "dll in bin"},
		{"Backend/Attributes/obj/Debug/net9.0/Metaplay.Attributes.dll", false, true, "dll in obj"},
		{"Backend/Attributes/bin/Debug/net9.0/Metaplay.Attributes.pdb", false, true, "pdb in bin"},

		// node_modules should be ignored
		{"Frontend/node_modules", true, true, "node_modules directory"},
		{"Frontend/node_modules/somepackage/index.js", false, true, "file in node_modules"},

		// Source files should NOT be ignored
		{"Backend/Server/src/Program.cs", false, false, "source file"},
		{"Frontend/src/index.ts", false, false, "frontend source file"},
		{"Backend/Attributes/Attributes.cs", false, false, "attribute source file"},

		// .vs directory should be ignored
		{".vs", true, true, ".vs directory"},
		{".vs/config/applicationhost.config", false, true, "file in .vs"},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			result := matcher.isIgnored(tc.path, tc.isDir)
			if result != tc.expected {
				t.Errorf("isIgnored(%q, isDir=%v) = %v, expected %v", tc.path, tc.isDir, result, tc.expected)
			}
		})
	}
}

func TestGitignoreNestedFiles(t *testing.T) {
	// Create a temporary directory with nested .gitignore files
	tmpDir, err := os.MkdirTemp("", "sdk-nested-gitignore-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Root .gitignore
	rootGitignore := `bin/
obj/
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(rootGitignore), 0644); err != nil {
		t.Fatalf("Failed to write root .gitignore: %v", err)
	}

	// Create Backend directory with its own .gitignore
	backendDir := filepath.Join(tmpDir, "Backend")
	if err := os.MkdirAll(backendDir, 0755); err != nil {
		t.Fatalf("Failed to create Backend dir: %v", err)
	}

	backendGitignore := `# Additional patterns for Backend
*.log
temp/
`
	if err := os.WriteFile(filepath.Join(backendDir, ".gitignore"), []byte(backendGitignore), 0644); err != nil {
		t.Fatalf("Failed to write Backend .gitignore: %v", err)
	}

	// Build the matcher
	matcher := buildGitignoreMatcherForDir(tmpDir)

	tests := []struct {
		path     string
		isDir    bool
		expected bool
		desc     string
	}{
		// Root patterns should apply everywhere
		{"bin", true, true, "root bin"},
		{"Backend/bin", true, true, "Backend bin"},
		{"Backend/Server/bin", true, true, "Backend/Server bin"},

		// Backend-specific patterns should only apply in Backend
		{"Backend/debug.log", false, true, "log file in Backend"},
		{"Backend/Server/error.log", false, true, "log file in Backend/Server"},
		{"debug.log", false, false, "log file at root (not matched)"},
		{"Frontend/debug.log", false, false, "log file in Frontend (not matched)"},

		{"Backend/temp", true, true, "temp dir in Backend"},
		{"temp", true, false, "temp dir at root (not matched)"},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			result := matcher.isIgnored(tc.path, tc.isDir)
			if result != tc.expected {
				t.Errorf("isIgnored(%q, isDir=%v) = %v, expected %v", tc.path, tc.isDir, result, tc.expected)
			}
		})
	}
}

func TestGitignoreEmptyDirectory(t *testing.T) {
	// Create a temporary directory with no .gitignore files
	tmpDir, err := os.MkdirTemp("", "sdk-no-gitignore-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	matcher := buildGitignoreMatcherForDir(tmpDir)

	// Nothing should be ignored
	tests := []struct {
		path  string
		isDir bool
	}{
		{"bin", true},
		{"obj", true},
		{"Backend/bin/file.dll", false},
	}

	for _, tc := range tests {
		if matcher.isIgnored(tc.path, tc.isDir) {
			t.Errorf("isIgnored(%q, isDir=%v) = true, expected false (no gitignore)", tc.path, tc.isDir)
		}
	}
}

// containsString checks if a string contains a substring
func containsString(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) && findSubstr(s, substr)
}

func findSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestGenerateUnifiedDiff_NewFile(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		newContent string
		wantHunk   string // expected hunk header pattern
	}{
		{
			name:       "single line new file",
			path:       "test.txt",
			newContent: "hello world\n",
			wantHunk:   "@@ -0,0 +1,1 @@",
		},
		{
			name:       "multi-line new file",
			path:       "test.txt",
			newContent: "line1\nline2\nline3\n",
			wantHunk:   "@@ -0,0 +1,3 @@",
		},
		{
			name:       "new file without trailing newline",
			path:       "test.txt",
			newContent: "no newline",
			wantHunk:   "@@ -0,0 +1,1 @@",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := generateUnifiedDiff(tc.path, nil, []byte(tc.newContent), true, false)

			// Check for git diff header
			if !containsString(result, "diff --git a/"+tc.path+" b/"+tc.path) {
				t.Errorf("missing diff header, got:\n%s", result)
			}

			// Check for new file marker
			if !containsString(result, "new file mode 100644") {
				t.Errorf("missing 'new file mode' marker, got:\n%s", result)
			}

			// Check for /dev/null in old file
			if !containsString(result, "--- /dev/null") {
				t.Errorf("missing '--- /dev/null', got:\n%s", result)
			}

			// Check hunk header format (must be -0,0 for new files)
			if !containsString(result, tc.wantHunk) {
				t.Errorf("expected hunk header %q, got:\n%s", tc.wantHunk, result)
			}
		})
	}
}

func TestGenerateUnifiedDiff_DeletedFile(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		oldContent string
		wantHunk   string
	}{
		{
			name:       "single line deleted file",
			path:       "test.txt",
			oldContent: "goodbye world\n",
			wantHunk:   "@@ -1,1 +0,0 @@",
		},
		{
			name:       "multi-line deleted file",
			path:       "test.txt",
			oldContent: "line1\nline2\nline3\n",
			wantHunk:   "@@ -1,3 +0,0 @@",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := generateUnifiedDiff(tc.path, []byte(tc.oldContent), nil, false, true)

			// Check for deleted file marker
			if !containsString(result, "deleted file mode 100644") {
				t.Errorf("missing 'deleted file mode' marker, got:\n%s", result)
			}

			// Check for /dev/null in new file
			if !containsString(result, "+++ /dev/null") {
				t.Errorf("missing '+++ /dev/null', got:\n%s", result)
			}

			// Check hunk header format (must be +0,0 for deleted files)
			if !containsString(result, tc.wantHunk) {
				t.Errorf("expected hunk header %q, got:\n%s", tc.wantHunk, result)
			}
		})
	}
}

func TestGenerateUnifiedDiff_ModifiedFile(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		oldContent string
		newContent string
		wantMinus  bool // expect - lines
		wantPlus   bool // expect + lines
	}{
		{
			name:       "simple modification",
			path:       "test.txt",
			oldContent: "old line\n",
			newContent: "new line\n",
			wantMinus:  true,
			wantPlus:   true,
		},
		{
			name:       "add line at end",
			path:       "test.txt",
			oldContent: "line1\n",
			newContent: "line1\nline2\n",
			wantMinus:  false,
			wantPlus:   true,
		},
		{
			name:       "remove line",
			path:       "test.txt",
			oldContent: "line1\nline2\n",
			newContent: "line1\n",
			wantMinus:  true,
			wantPlus:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := generateUnifiedDiff(tc.path, []byte(tc.oldContent), []byte(tc.newContent), false, false)

			// Should NOT have new/deleted file markers
			if containsString(result, "new file mode") {
				t.Errorf("should not have 'new file mode' for modification, got:\n%s", result)
			}
			if containsString(result, "deleted file mode") {
				t.Errorf("should not have 'deleted file mode' for modification, got:\n%s", result)
			}

			// Check for proper file headers (not /dev/null)
			if !containsString(result, "--- a/"+tc.path) {
				t.Errorf("missing '--- a/%s', got:\n%s", tc.path, result)
			}
			if !containsString(result, "+++ b/"+tc.path) {
				t.Errorf("missing '+++ b/%s', got:\n%s", tc.path, result)
			}

			// Check for minus/plus lines
			hasMinus := containsString(result, "\n-")
			hasPlus := containsString(result, "\n+")

			if tc.wantMinus && !hasMinus {
				t.Errorf("expected - lines in diff, got:\n%s", result)
			}
			if tc.wantPlus && !hasPlus {
				t.Errorf("expected + lines in diff, got:\n%s", result)
			}
		})
	}
}

func TestGenerateUnifiedDiff_BinaryFile(t *testing.T) {
	tests := []struct {
		name       string
		oldContent []byte
		newContent []byte
		isNew      bool
		isDeleted  bool
	}{
		{
			name:       "new binary file",
			oldContent: nil,
			newContent: []byte{0x00, 0x01, 0x02, 0x03},
			isNew:      true,
			isDeleted:  false,
		},
		{
			name:       "deleted binary file",
			oldContent: []byte{0x00, 0x01, 0x02, 0x03},
			newContent: nil,
			isNew:      false,
			isDeleted:  true,
		},
		{
			name:       "modified binary file",
			oldContent: []byte{0x00, 0x01},
			newContent: []byte{0x00, 0x02},
			isNew:      false,
			isDeleted:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := generateUnifiedDiff("file.bin", tc.oldContent, tc.newContent, tc.isNew, tc.isDeleted)

			// Binary files should return empty string (not included in patch)
			if result != "" {
				t.Errorf("expected empty string for binary file, got:\n%s", result)
			}
		})
	}
}

func TestGenerateUnifiedDiff_IdenticalContent(t *testing.T) {
	content := []byte("same content\n")
	result := generateUnifiedDiff("test.txt", content, content, false, false)

	// Identical content should produce no hunks
	if containsString(result, "@@") {
		t.Errorf("identical content should not produce hunks, got:\n%s", result)
	}
}

func TestGenerateUnifiedDiff_EmptyToContent(t *testing.T) {
	// Edge case: empty file (not nil, but zero bytes) to content
	result := generateUnifiedDiff("test.txt", []byte{}, []byte("new content\n"), false, false)

	// Should produce a valid diff
	if !containsString(result, "+new content") {
		t.Errorf("expected '+new content' line, got:\n%s", result)
	}
}

func TestGenerateUnifiedDiff_ContentToEmpty(t *testing.T) {
	// Edge case: content to empty file
	result := generateUnifiedDiff("test.txt", []byte("old content\n"), []byte{}, false, false)

	// Should produce a valid diff
	if !containsString(result, "-old content") {
		t.Errorf("expected '-old content' line, got:\n%s", result)
	}
}

// TestGenerateUnifiedDiff_LineIntegrity verifies that the diff output preserves
// line boundaries. The go-diff library's DiffCleanupSemantic can break line-level
// diffs by extracting character-level common prefixes/suffixes as separate Equal
// segments, splitting lines at arbitrary positions.
func TestGenerateUnifiedDiff_LineIntegrity(t *testing.T) {
	tests := []struct {
		name        string
		oldContent  string
		newContent  string
		wantLines   []string // lines that must appear in the diff output
		rejectLines []string // lines that must NOT appear (broken line boundaries)
	}{
		{
			name:       "common prefix between changed lines",
			oldContent: "first_old\nunchanged\nsecond_old\n",
			newContent: "first_new\nunchanged\nsecond_new\n",
			wantLines: []string{
				"-first_old\n",
				"+first_new\n",
				" unchanged\n",
				"-second_old\n",
				"+second_new\n",
			},
			rejectLines: []string{
				" first_\n", // common prefix must not be split into its own line
			},
		},
		{
			name:       "common suffix between changed lines",
			oldContent: "old_first\nunchanged\nold_second\n",
			newContent: "new_first\nunchanged\nnew_second\n",
			wantLines: []string{
				"-old_first\n",
				"+new_first\n",
				" unchanged\n",
				"-old_second\n",
				"+new_second\n",
			},
			rejectLines: []string{
				" _first\n",  // common suffix must not be split out
				" _second\n", // common suffix must not be split out
			},
		},
		{
			name:       "common prefix and suffix on single changed line",
			oldContent: "prefix_old_suffix\n",
			newContent: "prefix_new_suffix\n",
			wantLines: []string{
				"-prefix_old_suffix\n",
				"+prefix_new_suffix\n",
			},
			rejectLines: []string{
				" prefix_\n", // character-level prefix must not be split out
				" _suffix\n", // character-level suffix must not be split out
			},
		},
		{
			name:       "unchanged line not eliminated between changes",
			oldContent: "aaa\nkeep\nbbb\n",
			newContent: "xxx\nkeep\nyyy\n",
			wantLines: []string{
				"-aaa\n",
				"+xxx\n",
				" keep\n",
				"-bbb\n",
				"+yyy\n",
			},
			rejectLines: []string{
				"-keep\n", // unchanged line must not become a deletion
				"+keep\n", // unchanged line must not become an insertion
			},
		},
		{
			name:       "multiple unchanged lines preserved between changes",
			oldContent: "old1\na\nb\nc\nold2\n",
			newContent: "new1\na\nb\nc\nnew2\n",
			wantLines: []string{
				"-old1\n",
				"+new1\n",
				" a\n",
				" b\n",
				" c\n",
				"-old2\n",
				"+new2\n",
			},
		},
		{
			name:       "long common prefix does not break lines",
			oldContent: "shared_long_prefix_old\n",
			newContent: "shared_long_prefix_new\n",
			wantLines: []string{
				"-shared_long_prefix_old\n",
				"+shared_long_prefix_new\n",
			},
			rejectLines: []string{
				" shared_long_prefix_\n",
			},
		},
		{
			name:       "identical lines around multiple changes stay unchanged",
			oldContent: "header\nold_a\nmiddle\nold_b\nfooter\n",
			newContent: "header\nnew_a\nmiddle\nnew_b\nfooter\n",
			wantLines: []string{
				" header\n",
				"-old_a\n",
				"+new_a\n",
				" middle\n",
				"-old_b\n",
				"+new_b\n",
				" footer\n",
			},
			rejectLines: []string{
				"-header\n",
				"+header\n",
				"-middle\n",
				"+middle\n",
				"-footer\n",
				"+footer\n",
			},
		},
		{
			// When only one character differs in a line, the character-level common
			// prefix and suffix are maximally long. DiffCleanupMerge extracts both,
			// leaving just the single differing character as the edit — splitting the
			// line into three fragments.
			name:       "single character difference in line",
			oldContent: "abcXdef\n",
			newContent: "abcYdef\n",
			wantLines: []string{
				"-abcXdef\n",
				"+abcYdef\n",
			},
			rejectLines: []string{
				" abc\n",
				" def\n",
			},
		},
		{
			// When changed lines share a suffix (e.g., "_shared"), DiffCleanupMerge
			// extracts the character-level common suffix, creating a fragment that
			// doesn't correspond to any real line.
			name:       "common suffix crossing line boundary",
			oldContent: "aaa_shared\nunchanged\nbbb_shared\n",
			newContent: "xxx_shared\nunchanged\nyyy_shared\n",
			wantLines: []string{
				"-aaa_shared\n",
				"+xxx_shared\n",
				" unchanged\n",
				"-bbb_shared\n",
				"+yyy_shared\n",
			},
			rejectLines: []string{
				" _shared\n", // suffix must not be extracted across line boundary
			},
		},
		{
			// Multiple small equalities can all be eliminated, creating one large
			// concatenated block where character-level prefix/suffix extraction
			// has more material to work with.
			name:       "chain of small equalities between changes",
			oldContent: "test_aaa\nx\ntest_bbb\ny\ntest_ccc\n",
			newContent: "test_xxx\nx\ntest_yyy\ny\ntest_zzz\n",
			wantLines: []string{
				"-test_aaa\n",
				"+test_xxx\n",
				" x\n",
				"-test_bbb\n",
				"+test_yyy\n",
				" y\n",
				"-test_ccc\n",
				"+test_zzz\n",
			},
			rejectLines: []string{
				" test_\n", // common prefix must not be split out
				"-x\n",     // unchanged line must not become a deletion
				"+x\n",     // unchanged line must not become an insertion
				"-y\n",
				"+y\n",
			},
		},
		{
			// An equality exactly at the elimination threshold gets removed.
			// The threshold is: len(equality) <= max(edits_before) AND
			// len(equality) <= max(edits_after). With 9-char changes on each
			// side, a 9-char equality (including \n) is exactly at the boundary.
			name:       "equality at exact elimination threshold",
			oldContent: "changed1\nthreshld\nchanged2\n",
			newContent: "modified\nthreshld\nmodified\n",
			wantLines: []string{
				"-changed1\n",
				"+modified\n",
				" threshld\n",
				"-changed2\n",
			},
			rejectLines: []string{
				"-threshld\n",
				"+threshld\n",
			},
		},
		{
			// Lines differing only in case share long character-level prefixes
			// (since rune comparison is case-sensitive, the common prefix is up to
			// the first case change). This tests that cleanup doesn't break "Import"
			// into " " + "Import" when the prefix happens to be a space or similar.
			name:       "case-only difference in lines",
			oldContent: "ImportOld\nunchanged\nExportOld\n",
			newContent: "ImportNew\nunchanged\nExportNew\n",
			wantLines: []string{
				"-ImportOld\n",
				"+ImportNew\n",
				" unchanged\n",
				"-ExportOld\n",
				"+ExportNew\n",
			},
			rejectLines: []string{
				" Import\n",
				" Export\n",
			},
		},
		{
			// When lines share both a common prefix AND suffix, DiffCleanupMerge
			// can extract both ends, leaving just the differing middle as the edit.
			// With multiple such lines merged, this produces multiple fragments.
			name:       "prefix and suffix with eliminated equality between",
			oldContent: "cfg_old_value\nok\ncfg_old_value\n",
			newContent: "cfg_new_value\nok\ncfg_new_value\n",
			wantLines: []string{
				"-cfg_old_value\n",
				"+cfg_new_value\n",
				" ok\n",
			},
			rejectLines: []string{
				" cfg_\n",
				" _value\n",
				"-ok\n",
				"+ok\n",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := generateUnifiedDiff("test.txt", []byte(tc.oldContent), []byte(tc.newContent), false, false)

			for _, want := range tc.wantLines {
				if !containsString(result, want) {
					t.Errorf("expected diff to contain %q, got:\n%s", want, result)
				}
			}

			for _, reject := range tc.rejectLines {
				if containsString(result, reject) {
					t.Errorf("diff must not contain %q (broken line boundary), got:\n%s", reject, result)
				}
			}
		})
	}
}

func TestGenerateUnifiedDiff_HunkHeaders(t *testing.T) {
	tests := []struct {
		name       string
		oldContent string
		newContent string
		wantHeader string
	}{
		{
			name:       "single line replacement",
			oldContent: "old\n",
			newContent: "new\n",
			wantHeader: "@@ -1,1 +1,1 @@",
		},
		{
			name:       "change first of three lines",
			oldContent: "old\nb\nc\n",
			newContent: "new\nb\nc\n",
			wantHeader: "@@ -1,3 +1,3 @@", // 1 changed + 2 context
		},
		{
			name:       "insert line increases new count",
			oldContent: "a\nb\n",
			newContent: "a\nx\nb\n",
			wantHeader: "@@ -1,2 +1,3 @@",
		},
		{
			name:       "delete line decreases new count",
			oldContent: "a\nx\nb\n",
			newContent: "a\nb\n",
			wantHeader: "@@ -1,3 +1,2 @@",
		},
		{
			name:       "change in middle with context on both sides",
			oldContent: "a\nb\nc\nold\ne\nf\ng\n",
			newContent: "a\nb\nc\nnew\ne\nf\ng\n",
			wantHeader: "@@ -1,7 +1,7 @@", // 3 context before + 1 change + 3 context after
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := generateUnifiedDiff("test.txt", []byte(tc.oldContent), []byte(tc.newContent), false, false)
			if !containsString(result, tc.wantHeader) {
				t.Errorf("expected hunk header %q, got:\n%s", tc.wantHeader, result)
			}
		})
	}
}

func TestGenerateUnifiedDiff_MultipleHunks(t *testing.T) {
	// Build input with changes at line 1 and line 15, separated by 13 unchanged lines.
	// With 3 context lines: first hunk context ends at line 4, second starts at line 12.
	// Gap of 7 unchanged lines > 2*3=6, so hunks must be separate.
	var oldLines, newLines []string
	oldLines = append(oldLines, "old_first")
	newLines = append(newLines, "new_first")
	for i := range 13 {
		line := string(rune('a' + i))
		oldLines = append(oldLines, line)
		newLines = append(newLines, line)
	}
	oldLines = append(oldLines, "old_last")
	newLines = append(newLines, "new_last")

	oldContent := strings.Join(oldLines, "\n") + "\n"
	newContent := strings.Join(newLines, "\n") + "\n"

	result := generateUnifiedDiff("test.txt", []byte(oldContent), []byte(newContent), false, false)

	// Each @@ header contains "@@" twice, so 2 hunks = 4 occurrences
	hhCount := strings.Count(result, "@@")
	if hhCount != 4 {
		t.Errorf("expected 2 hunks (4 @@ markers), got %d in:\n%s", hhCount, result)
	}

	// Verify both changes appear
	if !containsString(result, "-old_first\n") {
		t.Errorf("missing -old_first in:\n%s", result)
	}
	if !containsString(result, "+new_first\n") {
		t.Errorf("missing +new_first in:\n%s", result)
	}
	if !containsString(result, "-old_last\n") {
		t.Errorf("missing -old_last in:\n%s", result)
	}
	if !containsString(result, "+new_last\n") {
		t.Errorf("missing +new_last in:\n%s", result)
	}
}
