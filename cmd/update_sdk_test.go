/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/metaplay/cli/pkg/portalapi"
)

// makeSdkVersion creates a SdkVersionInfo for testing with the given version string.
func makeSdkVersion(ver string) portalapi.SdkVersionInfo {
	storagePath := "/path/to/sdk"
	return portalapi.SdkVersionInfo{
		ID:          "sdk-" + ver,
		Version:     ver,
		StoragePath: &storagePath,
	}
}

// makeSdkVersions creates a slice of SdkVersionInfo from version strings.
func makeSdkVersions(versions ...string) []portalapi.SdkVersionInfo {
	result := make([]portalapi.SdkVersionInfo, len(versions))
	for i, v := range versions {
		result[i] = makeSdkVersion(v)
	}
	return result
}

func TestFormatMajorMinor(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"34.1.0", "34.1"},
		{"35.2", "35.2"},
		{"1.0.0", "1.0"},
		{"100.200.300", "100.200"},
		{"1", "1.0"}, // single segment is padded by version library
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			v, err := version.NewVersion(tc.input)
			if err != nil {
				t.Fatalf("failed to parse version %s: %v", tc.input, err)
			}
			result := formatMajorMinor(v)
			if result != tc.expected {
				t.Errorf("formatMajorMinor(%s) = %s, expected %s", tc.input, result, tc.expected)
			}
		})
	}
}

func TestJoinWithCommaAnd(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected string
	}{
		{"empty", []string{}, ""},
		{"single", []string{"a"}, "a"},
		{"two items", []string{"a", "b"}, "a and b"},
		{"three items", []string{"a", "b", "c"}, "a, b, and c"},
		{"four items", []string{"a", "b", "c", "d"}, "a, b, c, and d"},
		{"five items", []string{"1", "2", "3", "4", "5"}, "1, 2, 3, 4, and 5"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := joinWithCommaAnd(tc.input)
			if result != tc.expected {
				t.Errorf("joinWithCommaAnd(%v) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestFormatMajorList(t *testing.T) {
	tests := []struct {
		name     string
		input    []int
		expected string
	}{
		{"empty", []int{}, ""},
		{"single", []int{35}, "35"},
		{"two", []int{35, 36}, "35 and 36"},
		{"three", []int{35, 36, 37}, "35, 36, and 37"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := formatMajorList(tc.input)
			if result != tc.expected {
				t.Errorf("formatMajorList(%v) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestGetUniqueMajorVersions(t *testing.T) {
	tests := []struct {
		name     string
		versions []portalapi.SdkVersionInfo
		expected []int
	}{
		{
			name:     "empty list",
			versions: []portalapi.SdkVersionInfo{},
			expected: nil,
		},
		{
			name:     "single version",
			versions: makeSdkVersions("35.1"),
			expected: []int{35},
		},
		{
			name:     "multiple versions same major",
			versions: makeSdkVersions("35.1", "35.2", "35.3"),
			expected: []int{35},
		},
		{
			name:     "multiple majors sorted ascending",
			versions: makeSdkVersions("37.0", "35.1", "36.2"),
			expected: []int{35, 36, 37},
		},
		{
			name:     "mixed versions with duplicates",
			versions: makeSdkVersions("35.1", "36.0", "35.2", "37.1", "36.1"),
			expected: []int{35, 36, 37},
		},
		{
			name: "invalid version is skipped",
			versions: append(makeSdkVersions("35.1", "36.0"), portalapi.SdkVersionInfo{
				ID:      "invalid",
				Version: "not-a-version",
			}),
			expected: []int{35, 36},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := getUniqueMajorVersions(tc.versions)
			if !intSliceEqual(result, tc.expected) {
				t.Errorf("getUniqueMajorVersions() = %v, expected %v", result, tc.expected)
			}
		})
	}
}

func TestSortVersionOptions(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "empty slice",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "single element",
			input:    []string{"35.1"},
			expected: []string{"35.1"},
		},
		{
			name:     "already sorted descending",
			input:    []string{"35.3", "35.2", "35.1"},
			expected: []string{"35.3", "35.2", "35.1"},
		},
		{
			name:     "needs sorting ascending to descending",
			input:    []string{"35.1", "35.2", "35.3"},
			expected: []string{"35.3", "35.2", "35.1"},
		},
		{
			name:     "mixed order",
			input:    []string{"35.2", "36.1", "35.1", "36.0"},
			expected: []string{"36.1", "36.0", "35.2", "35.1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			options := make([]sdkVersionOption, len(tc.input))
			for i, v := range tc.input {
				parsed, _ := version.NewVersion(v)
				options[i] = sdkVersionOption{
					version: &portalapi.SdkVersionInfo{Version: v},
					parsed:  parsed,
					label:   v,
				}
			}

			sortVersionOptions(options)

			result := make([]string, len(options))
			for i, opt := range options {
				result[i] = opt.label
			}

			if !stringSliceEqual(result, tc.expected) {
				t.Errorf("sortVersionOptions() = %v, expected %v", result, tc.expected)
			}
		})
	}
}

func TestSortVersionInfos(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "empty slice",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "single element",
			input:    []string{"35.1"},
			expected: []string{"35.1"},
		},
		{
			name:     "already sorted descending",
			input:    []string{"35.3", "35.2", "35.1"},
			expected: []string{"35.3", "35.2", "35.1"},
		},
		{
			name:     "needs sorting ascending to descending",
			input:    []string{"35.1", "35.2", "35.3"},
			expected: []string{"35.3", "35.2", "35.1"},
		},
		{
			name:     "mixed majors",
			input:    []string{"34.5", "36.1", "35.0"},
			expected: []string{"36.1", "35.0", "34.5"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			versions := makeSdkVersions(tc.input...)

			sortVersionInfos(versions)

			result := make([]string, len(versions))
			for i, v := range versions {
				result[i] = v.Version
			}

			if !stringSliceEqual(result, tc.expected) {
				t.Errorf("sortVersionInfos() = %v, expected %v", result, tc.expected)
			}
		})
	}
}

func TestFindLatestInMajor(t *testing.T) {
	tests := []struct {
		name     string
		versions []portalapi.SdkVersionInfo
		major    int
		expected string // empty string means nil expected
	}{
		{
			name:     "empty list",
			versions: []portalapi.SdkVersionInfo{},
			major:    35,
			expected: "",
		},
		{
			name:     "no matching major",
			versions: makeSdkVersions("34.1", "34.2", "36.0"),
			major:    35,
			expected: "",
		},
		{
			name:     "single match",
			versions: makeSdkVersions("34.1", "35.0", "36.0"),
			major:    35,
			expected: "35.0",
		},
		{
			name:     "multiple matches returns highest",
			versions: makeSdkVersions("35.1", "35.3", "35.2", "34.5"),
			major:    35,
			expected: "35.3",
		},
		{
			name:     "handles three-part versions",
			versions: makeSdkVersions("35.1.0", "35.1.5", "35.2.0"),
			major:    35,
			expected: "35.2.0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := findLatestInMajor(tc.versions, tc.major)
			if tc.expected == "" {
				if result != nil {
					t.Errorf("findLatestInMajor() = %v, expected nil", result.Version)
				}
			} else {
				if result == nil {
					t.Errorf("findLatestInMajor() = nil, expected %s", tc.expected)
				} else if result.Version != tc.expected {
					t.Errorf("findLatestInMajor() = %s, expected %s", result.Version, tc.expected)
				}
			}
		})
	}
}

func TestFindLatestMinorUpdate(t *testing.T) {
	tests := []struct {
		name           string
		versions       []portalapi.SdkVersionInfo
		currentMajor   int
		currentVersion string
		expected       string // empty string means nil expected
	}{
		{
			name:           "empty list",
			versions:       []portalapi.SdkVersionInfo{},
			currentMajor:   35,
			currentVersion: "35.0",
			expected:       "",
		},
		{
			name:           "no newer version in same major",
			versions:       makeSdkVersions("35.0", "35.1", "34.5"),
			currentMajor:   35,
			currentVersion: "35.1",
			expected:       "",
		},
		{
			name:           "single newer version",
			versions:       makeSdkVersions("35.0", "35.2", "34.5"),
			currentMajor:   35,
			currentVersion: "35.1",
			expected:       "35.2",
		},
		{
			name:           "multiple newer versions returns highest",
			versions:       makeSdkVersions("35.2", "35.4", "35.3", "34.9"),
			currentMajor:   35,
			currentVersion: "35.1",
			expected:       "35.4",
		},
		{
			name:           "ignores different major versions",
			versions:       makeSdkVersions("35.2", "36.5", "37.0"),
			currentMajor:   35,
			currentVersion: "35.1",
			expected:       "35.2",
		},
		{
			name:           "same version is not newer",
			versions:       makeSdkVersions("35.1"),
			currentMajor:   35,
			currentVersion: "35.1",
			expected:       "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			currentVersion, _ := version.NewVersion(tc.currentVersion)
			result := findLatestMinorUpdate(tc.versions, tc.currentMajor, currentVersion)
			if tc.expected == "" {
				if result != nil {
					t.Errorf("findLatestMinorUpdate() = %v, expected nil", result.Version)
				}
			} else {
				if result == nil {
					t.Errorf("findLatestMinorUpdate() = nil, expected %s", tc.expected)
				} else if result.Version != tc.expected {
					t.Errorf("findLatestMinorUpdate() = %s, expected %s", result.Version, tc.expected)
				}
			}
		})
	}
}

func TestFindLatestMajorUpdate(t *testing.T) {
	tests := []struct {
		name         string
		versions     []portalapi.SdkVersionInfo
		currentMajor int
		expected     string // empty string means nil expected
	}{
		{
			name:         "empty list",
			versions:     []portalapi.SdkVersionInfo{},
			currentMajor: 35,
			expected:     "",
		},
		{
			name:         "no higher major",
			versions:     makeSdkVersions("34.5", "35.2", "35.3"),
			currentMajor: 35,
			expected:     "",
		},
		{
			name:         "single higher major",
			versions:     makeSdkVersions("35.0", "36.1", "35.2"),
			currentMajor: 35,
			expected:     "36.1",
		},
		{
			name:         "multiple higher majors returns highest overall",
			versions:     makeSdkVersions("36.0", "37.2", "36.5", "38.1"),
			currentMajor: 35,
			expected:     "38.1",
		},
		{
			name:         "same major is not higher",
			versions:     makeSdkVersions("35.5", "35.9"),
			currentMajor: 35,
			expected:     "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := findLatestMajorUpdate(tc.versions, tc.currentMajor)
			if tc.expected == "" {
				if result != nil {
					t.Errorf("findLatestMajorUpdate() = %v, expected nil", result.Version)
				}
			} else {
				if result == nil {
					t.Errorf("findLatestMajorUpdate() = nil, expected %s", tc.expected)
				} else if result.Version != tc.expected {
					t.Errorf("findLatestMajorUpdate() = %s, expected %s", result.Version, tc.expected)
				}
			}
		})
	}
}

func TestFindLatestForMajor(t *testing.T) {
	tests := []struct {
		name     string
		versions []portalapi.SdkVersionInfo
		majorStr string
		expected string // empty string means nil expected
	}{
		{
			name:     "empty list",
			versions: []portalapi.SdkVersionInfo{},
			majorStr: "35",
			expected: "",
		},
		{
			name:     "major not found",
			versions: makeSdkVersions("34.1", "36.0"),
			majorStr: "35",
			expected: "",
		},
		{
			name:     "single match",
			versions: makeSdkVersions("34.1", "35.0", "36.0"),
			majorStr: "35",
			expected: "35.0",
		},
		{
			name:     "multiple matches returns highest",
			versions: makeSdkVersions("35.1", "35.3", "35.2"),
			majorStr: "35",
			expected: "35.3",
		},
		{
			name:     "handles string major input",
			versions: makeSdkVersions("100.1", "100.5", "100.3"),
			majorStr: "100",
			expected: "100.5",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := findLatestForMajor(tc.versions, tc.majorStr)
			if tc.expected == "" {
				if result != nil {
					t.Errorf("findLatestForMajor() = %v, expected nil", result.Version)
				}
			} else {
				if result == nil {
					t.Errorf("findLatestForMajor() = nil, expected %s", tc.expected)
				} else if result.Version != tc.expected {
					t.Errorf("findLatestForMajor() = %s, expected %s", result.Version, tc.expected)
				}
			}
		})
	}
}

func TestResolveTargetVersion(t *testing.T) {
	versions := makeSdkVersions("34.1", "34.2", "35.0", "35.1", "35.2", "36.0", "36.1")

	tests := []struct {
		name           string
		toVersion      string
		versions       []portalapi.SdkVersionInfo
		currentVersion string
		expected       string // empty string means error expected
		errorContains  string // substring expected in error message
	}{
		{
			name:           "exact version match",
			toVersion:      "35.1",
			versions:       versions,
			currentVersion: "34.2",
			expected:       "35.1",
		},
		{
			name:           "major-only resolves to latest in major",
			toVersion:      "35",
			versions:       versions,
			currentVersion: "34.2",
			expected:       "35.2",
		},
		{
			name:           "major-only with higher major",
			toVersion:      "36",
			versions:       versions,
			currentVersion: "35.0",
			expected:       "36.1",
		},
		{
			name:           "version not found",
			toVersion:      "99.0",
			versions:       versions,
			currentVersion: "35.0",
			expected:       "",
			errorContains:  "not found",
		},
		{
			name:           "major not found",
			toVersion:      "99",
			versions:       versions,
			currentVersion: "35.0",
			expected:       "",
			errorContains:  "no SDK version found",
		},
		{
			name:           "target not newer than current exact",
			toVersion:      "35.0",
			versions:       versions,
			currentVersion: "35.1",
			expected:       "",
			errorContains:  "not newer",
		},
		{
			name:           "target not newer than current major-only",
			toVersion:      "34",
			versions:       versions,
			currentVersion: "35.0",
			expected:       "",
			errorContains:  "not newer",
		},
		{
			name:           "same version is not newer",
			toVersion:      "35.1",
			versions:       versions,
			currentVersion: "35.1",
			expected:       "",
			errorContains:  "not newer",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			currentVersion, _ := version.NewVersion(tc.currentVersion)
			result, err := resolveTargetVersion(tc.toVersion, tc.versions, currentVersion)

			if tc.expected == "" {
				if err == nil {
					t.Errorf("resolveTargetVersion() expected error containing %q, got nil", tc.errorContains)
				} else if tc.errorContains != "" && !containsSubstring(err.Error(), tc.errorContains) {
					t.Errorf("resolveTargetVersion() error = %q, expected to contain %q", err.Error(), tc.errorContains)
				}
			} else {
				if err != nil {
					t.Errorf("resolveTargetVersion() unexpected error: %v", err)
				} else if result == nil {
					t.Errorf("resolveTargetVersion() = nil, expected %s", tc.expected)
				} else if result.Version != tc.expected {
					t.Errorf("resolveTargetVersion() = %s, expected %s", result.Version, tc.expected)
				}
			}
		})
	}
}

// Helper functions for test assertions

func intSliceEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
