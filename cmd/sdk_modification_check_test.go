/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os"
	"path/filepath"
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

func TestGenerateUnifiedDiff_NoNewlineAtEndOfFile(t *testing.T) {
	// When the input (either "old" or "new", or both) does not end in a newline,
	// the diff must contain the marker line "\ No newline at end of file" (without the quotes).
	// This marker appears in the diff immediately after a line that corresponds
	// to an input line that was missing a newline:
	// 1. If the "old" input is missing newline AND the last (newlineless) line was removed,
	//    then this marker appears just after the last deletion line in the diff.
	// 2. If the "new" input is missing newline AND the last (newlineless) line was added,
	//    then this marker appears just after the last addition line in the diff.
	// 3. If both inputs are missing newline AND neither of the above applies
	//    (i.e. the last (newlineless) line was unmodified),
	//    then this marker appears just after the last context line in the diff.
	// Note that the marker can appear twice, specifically in the case where
	// both 1. and 2. apply, i.e. both "old" and "new" are missing newline
	// and the last (newlineless) line was modified (old removed, new added).
	// In this case the marker appears both after the last deletion line
	// and after the last addition line.

	// Note: for simplicity of testing, we're comparing the outputs against exact references.
	// Technically this might be too strict, as generally there's no one single correct diff for given inputs.
	// Adjust the tests if this becomes a problem.

	tests := []struct {
		name     string
		old      string
		new      string
		expected string
	}{
		{
			name: "missing newline in new",
			old:  "hello\n",
			new:  "hello modified",
			expected: "diff --git a/test.txt b/test.txt\n" +
				"--- a/test.txt\n" +
				"+++ b/test.txt\n" +
				"@@ -1,1 +1,1 @@\n" +
				"-hello\n" +
				"+hello modified\n" +
				"\\ No newline at end of file\n",
		},
		{
			name: "missing newline in old",
			old:  "hello",
			new:  "hello modified\n",
			expected: "diff --git a/test.txt b/test.txt\n" +
				"--- a/test.txt\n" +
				"+++ b/test.txt\n" +
				"@@ -1,1 +1,1 @@\n" +
				"-hello\n" +
				"\\ No newline at end of file\n" +
				"+hello modified\n",
		},
		{
			name: "missing newline in both old and new",
			old:  "hello",
			new:  "hello modified",
			expected: "diff --git a/test.txt b/test.txt\n" +
				"--- a/test.txt\n" +
				"+++ b/test.txt\n" +
				"@@ -1,1 +1,1 @@\n" +
				"-hello\n" +
				"\\ No newline at end of file\n" +
				"+hello modified\n" +
				"\\ No newline at end of file\n",
		},
		{
			name: "missing newline in unmodified line",
			old:  "hello\ncommon",
			new:  "hello modified\ncommon",
			expected: "diff --git a/test.txt b/test.txt\n" +
				"--- a/test.txt\n" +
				"+++ b/test.txt\n" +
				"@@ -1,2 +1,2 @@\n" +
				"-hello\n" +
				"+hello modified\n" +
				" common\n" +
				"\\ No newline at end of file\n",
		},
		{
			name: "missing newline in both old and new; runs of multiple deleted/unmodified/added lines",
			old:  "hello\nworld\ncommon\nmore common\ngreetings\nagain",
			new:  "hello modified\nworld modified\ncommon\nmore common\ngreetings modified\nagain modified",
			expected: "diff --git a/test.txt b/test.txt\n" +
				"--- a/test.txt\n" +
				"+++ b/test.txt\n" +
				"@@ -1,6 +1,6 @@\n" +
				"-hello\n" +
				"-world\n" +
				"+hello modified\n" +
				"+world modified\n" +
				" common\n" +
				" more common\n" +
				"-greetings\n" +
				"-again\n" +
				"\\ No newline at end of file\n" +
				"+greetings modified\n" +
				"+again modified\n" +
				"\\ No newline at end of file\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := generateUnifiedDiff("test.txt", []byte(tc.old), []byte(tc.new), false, false)
			if result != tc.expected {
				t.Errorf("unexpected diff output\nexpected:\n%s\ngot:\n%s", tc.expected, result)
			}
		})
	}
}
