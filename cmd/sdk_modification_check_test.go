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
