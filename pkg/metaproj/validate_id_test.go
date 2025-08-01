/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package metaproj

import (
	"testing"

	"github.com/metaplay/cli/pkg/portalapi"
)

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
			result := ValidateProjectID(test.input)
			didSucceed := result == nil
			if didSucceed != test.isValid {
				t.Errorf("For input '%s', expected %v but got %v", test.input, test.isValid, result)
			}
		})
	}
}

func TestValidateManagedEnvironmentID(t *testing.T) {
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
		// TODO: Numbers are allowed for now (because of legacy Idler environments)
		{"env-123", true},
		{"env-abc-123", true},
		{"123-456", true},
		{"abc-123-def", true},
		{"foo-bar-baz-123", true},

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
		// TODO: Numbers are allowed for now (because of legacy Idler environments)
		{"a1-b2-c3", true},          // alphanumeric parts
		{"abc-123", true},           // numbers
		{"123-456-789", true},       // all numbers
		{"a1-b2-c3-d4", true},       // 4 segments with numbers
		{"test-env-prod-001", true}, // numbers in name

		// Invalid cases: uppercase or mixed case
		{"abc-DEF", false},           // uppercase letters
		{"Abc-def", false},           // leading uppercase
		{"abc-Def", false},           // mixed case
		{"ABC-123", false},           // all uppercase
		{"test-ENV-prod-001", false}, // mixed case in 4-segment name
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			result := ValidateEnvironmentID(portalapi.HostingTypeMetaplayHosted, test.input)
			didSucceed := result == nil
			if didSucceed != test.isValid {
				t.Errorf("For input '%s', expected %v but got %v", test.input, test.isValid, result)
			}
		})
	}
}

func TestValidateSelfHostedEnvironmentID(t *testing.T) {
	tests := []struct {
		input   string
		isValid bool
	}{
		// Valid cases: 2-40 chars, lowercase alphanumeric and dashes, dashes only in the middle
		{"ab", true},
		{"abc", true},
		{"a-b", true},
		{"abc-def", true},
		{"a1b2c3", true},
		{"abc123", true},
		{"abc-def-123", true},
		{"a-b-c-d-e-f-g-h-i-j-k-l-m-n-o-p-q-r-s-t-u-v-w-x-y-z-1-2-3-4-5-6-7-8-9-0", false}, // too long (>40)
		{"a", false}, // too short
		{"abcdefghijklmnopqrstuvwxyz0123456789abcd", true}, // exactly 40 chars

		// Dashes only in the middle, no consecutive dashes
		{"-abc", false}, // cannot start with dash
		{"abc-", false}, // cannot end with dash
		{"a--b", false},
		{"a---b", false},
		{"a-b-c-d-e-f-g-h-i-j", true},

		// Invalid: uppercase
		{"Abcdef", false},
		{"ABCDEF", false},
		{"abcDef", false},
		{"abc-DEF", false},

		// Invalid: symbols
		{"abc_def", false},
		{"abc.def", false},
		{"abc+def", false},
		{"abc@def", false},
		{"abc#def", false},
		{"abc$def", false},
		{"abc!def", false},
		{"abc%def", false},
		{"abc^def", false},
		{"abc&def", false},
		{"abc*def", false},
		{"abc(def", false},
		{"abc)def", false},
		{"abc=def", false},
		{"abc~def", false},
		{"abc`def", false},
		{"abc[def", false},
		{"abc]def", false},
		{"abc{def", false},
		{"abc}def", false},
		{"abc|def", false},
		{"abc\\def", false},
		{"abc/def", false},
		{"abc:def", false},
		{"abc;def", false},
		{"abc'def", false},
		{"abc\"def", false},
		{"abc<def", false},
		{"abc>def", false},
		{"abc,def", false},
		{"abc?def", false},

		// Edge cases
		{"", false},
		{"-", false},
		{"--", false}, // still invalid
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			result := ValidateEnvironmentID(portalapi.HostingTypeSelfHosted, test.input)
			didSucceed := result == nil
			if didSucceed != test.isValid {
				t.Errorf("For input '%s', expected %v but got %v", test.input, test.isValid, result)
			}
		})
	}
}
