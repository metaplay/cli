/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package portalapi

import "testing"

func TestCanonicalizeSdkVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Basic padding
		{"36", "36.0.0"},
		{"36.1", "36.1.0"},
		{"36.1.0", "36.1.0"},

		// Already three segments
		{"1.2.3", "1.2.3"},
		{"100.200.300", "100.200.300"},

		// Prerelease
		{"36.1-beta", "36.1.0-beta"},
		{"36.1.0-rc.1", "36.1.0-rc.1"},

		// Metadata
		{"36.1.0+build123", "36.1.0+build123"},
		{"36.1.0-rc.1+build123", "36.1.0-rc.1+build123"},

		// Invalid input returned as-is
		{"not-a-version", "not-a-version"},
		{"", ""},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := CanonicalizeSdkVersion(tc.input)
			if result != tc.expected {
				t.Errorf("CanonicalizeSdkVersion(%q) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestIsMajorVersionOnly(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"34", true},
		{"0", true},
		{"100", true},
		{"34.1", false},
		{"34.1.0", false},
		{"abc", false},
		{"34a", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := IsMajorVersionOnly(tc.input)
			if result != tc.expected {
				t.Errorf("IsMajorVersionOnly(%q) = %v, expected %v", tc.input, result, tc.expected)
			}
		})
	}
}
