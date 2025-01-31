/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import "testing"

func TestValidateProjectID(t *testing.T) {
	tests := []struct {
		input   string
		isValid bool
	}{
		// Valid cases - 2-3 segments only
		{"env-abc", true},
		{"abc-def-ghi", true},
		{"a-b", true},
		{"a-b-c", true},
		{"abc-def", true},     // lowercase letters only
		{"foo-bar", true},     // simple lowercase
		{"xyz-abc-def", true}, // three segments lowercase

		// Invalid cases: numbers not allowed
		{"env-123", false},
		{"env-abc-123", false},
		{"123-abc", false},
		{"abc-123-def", false},

		// Invalid cases: wrong structure
		{"", false},
		{"env", false},
		{"env-123-456-789", false}, // 4 segments not allowed for projects
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

		// Invalid cases: numbers
		{"a1-b2-c3", false},    // alphanumeric parts
		{"abc-123", false},     // numbers
		{"123-456-789", false}, // all numbers

		// Invalid cases: uppercase or mixed case
		{"abc-DEF", false}, // uppercase letters
		{"Abc-def", false}, // leading uppercase
		{"abc-Def", false}, // mixed case
		{"ABC-123", false}, // all uppercase
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			result := validateProjectID(test.input)
			didSucceed := result == nil
			if didSucceed != test.isValid {
				t.Errorf("For input '%s', expected %v but got %v", test.input, test.isValid, result)
			}
		})
	}
}

func TestValidateEnvironmentID(t *testing.T) {
	tests := []struct {
		input   string
		isValid bool
	}{
		// Valid cases - 2-4 segments allowed
		{"env-abc", true},
		{"abc-def-ghi", true},
		{"foo-bar-baz-qux", true}, // 4 segments valid for environments
		{"a-b", true},
		{"a-b-c", true},
		{"a-b-c-d", true},
		{"abc-def", true},           // lowercase letters only
		{"xyz-abc-def", true},       // three segments lowercase
		{"foo-bar-baz-qux", true},   // four segments lowercase
		{"test-env-prod-dev", true}, // realistic 4-segment name

		// Invalid cases: numbers not allowed
		{"env-123", false},
		{"env-abc-123", false},
		{"123-456", false},
		{"abc-123-def", false},
		{"foo-bar-baz-123", false},

		// Invalid cases: wrong structure
		{"", false},
		{"e", false},
		{"e-a-b-c-d", false}, // 5 segments not allowed
		{"-", false},
		{"env-", false},
		{"-123", false},
		{"env-123-", false},
		{"--", false},
		{"env--123", false},

		// Invalid cases: invalid characters
		{"env-abc@", false},
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

		// Invalid cases: numbers
		{"a1-b2-c3", false},          // alphanumeric parts
		{"abc-123", false},           // numbers
		{"123-456-789", false},       // all numbers
		{"a1-b2-c3-d4", false},       // 4 segments with numbers
		{"test-env-prod-001", false}, // numbers in name

		// Invalid cases: uppercase or mixed case
		{"abc-DEF", false},           // uppercase letters
		{"Abc-def", false},           // leading uppercase
		{"abc-Def", false},           // mixed case
		{"ABC-123", false},           // all uppercase
		{"test-ENV-prod-001", false}, // mixed case in 4-segment name
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			result := validateEnvironmentID(test.input)
			didSucceed := result == nil
			if didSucceed != test.isValid {
				t.Errorf("For input '%s', expected %v but got %v", test.input, test.isValid, result)
			}
		})
	}
}
