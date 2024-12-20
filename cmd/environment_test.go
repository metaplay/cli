package cmd

import (
	"errors"
	"reflect"
	"testing"
)

func TestIsValidEnvironmentId(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		// Valid cases
		{"env-123", true},
		{"env-123-abc", true},
		{"abc-def-ghi", true},
		{"123-456", true},
		{"a-b", true},
		{"a-b-c", true},

		// Invalid cases: wrong structure
		{"", false},
		{"env", false},
		{"env-123-456-789", false},
		{"-", false},
		{"env-", false},
		{"-123", false},
		{"env-123-", false},
		{"--", false},
		{"env--123", false},

		// Invalid cases: invalid characters
		{"env@123", false},
		{"env-123@abc", false},
		{"env-123$", false},
		{"env-!23", false},
		{"env-123#abc", false},
		{"env-abc_def", false},
		{"env-123.456", false},
		{"env-123+456", false},

		// Invalid cases: edge cases
		{"-a-b", false},          // starts with a dash
		{"a-b-", false},          // ends with a dash
		{"-a-b-c-", false},       // surrounded by dashes
		{"a--b-c", false},        // double dashes inside
		{"env--123--abc", false}, // multiple double dashes

		// Valid edge cases
		{"a1-b2-c3", true},    // alphanumeric parts
		{"abc-DEF", true},     // uppercase letters
		{"123-456-789", true}, // numeric parts only
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			result := isValidEnvironmentId(test.input)
			if result != test.expected {
				t.Errorf("For input '%s', expected %v but got %v", test.input, test.expected, result)
			}
		})
	}
}

func TestParseEnvironmentSlugs(t *testing.T) {
	tests := []struct {
		input       string
		expected    []string
		expectedErr error
	}{
		// Valid cases
		{"metaplay-idler-develop", []string{"metaplay", "idler", "develop"}, nil},
		{"abc-123-xyz", []string{"abc", "123", "xyz"}, nil},
		{"env-123-abc", []string{"env", "123", "abc"}, nil},
		{"a-b-c", []string{"a", "b", "c"}, nil},

		// Invalid cases: wrong structure
		{"metaplay-idler", nil, errors.New("the slugs must consist of exactly 3 dash-separated parts")},
		{"metaplay-idler-develop-extra", nil, errors.New("the slugs must consist of exactly 3 dash-separated parts")},
		{"", nil, errors.New("the slugs must consist of exactly 3 dash-separated parts")},
		{"metaplay--idler-develop", nil, errors.New("the slugs must consist of exactly 3 dash-separated parts")},
		{"-metaplay-idler-develop", nil, errors.New("the slugs must consist of exactly 3 dash-separated parts")},
		{"metaplay-idler-develop-", nil, errors.New("the slugs must consist of exactly 3 dash-separated parts")},

		// Invalid cases: invalid characters
		{"metaplay-idler-de@velop", nil, errors.New("the slugs can only contain alphanumeric characters")},
		{"metaplay-idler-dev elop", nil, errors.New("the slugs can only contain alphanumeric characters")},
		{"metaplay-idler-deve!lop", nil, errors.New("the slugs can only contain alphanumeric characters")},
		{"metaplay-idler-develop$", nil, errors.New("the slugs can only contain alphanumeric characters")},

		// Valid edge cases
		{"123-456-789", []string{"123", "456", "789"}, nil},
		{"abc-DEF-ghi", []string{"abc", "DEF", "ghi"}, nil},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			result, err := parseEnvironmentSlugs(test.input)

			// Compare expected and actual error
			if (err != nil || test.expectedErr != nil) && (err == nil || test.expectedErr == nil || err.Error() != test.expectedErr.Error()) {
				t.Errorf("For input '%s', expected error '%v' but got '%v'", test.input, test.expectedErr, err)
			}

			// Compare expected and actual result
			if !reflect.DeepEqual(result, test.expected) {
				t.Errorf("For input '%s', expected result '%v' but got '%v'", test.input, test.expected, result)
			}
		})
	}
}
