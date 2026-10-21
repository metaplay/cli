/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"reflect"
	"testing"

	clierrors "github.com/metaplay/cli/internal/errors"
)

func TestLLMDocsSearchOptsPrepare(t *testing.T) {
	tests := []struct {
		name         string
		flagKeywords string
		wantKeywords []string
		wantErr      bool
	}{
		{
			name:         "single keyword",
			flagKeywords: "guilds",
			wantKeywords: []string{"guilds"},
		},
		{
			name:         "multiple keywords",
			flagKeywords: "dotnet,runtime,version",
			wantKeywords: []string{"dotnet", "runtime", "version"},
		},
		{
			name:         "surrounding whitespace is trimmed",
			flagKeywords: " a , b ,c ",
			wantKeywords: []string{"a", "b", "c"},
		},
		{
			name:         "empty entries are skipped",
			flagKeywords: "a,,b,   ,c",
			wantKeywords: []string{"a", "b", "c"},
		},
		{
			name:         "keyword with internal space is preserved",
			flagKeywords: "guild actor,members",
			wantKeywords: []string{"guild actor", "members"},
		},
		{
			name:         "empty string errors",
			flagKeywords: "",
			wantErr:      true,
		},
		{
			name:         "only separators errors",
			flagKeywords: " , , ",
			wantErr:      true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := &llmDocsSearchOpts{flagKeywords: tc.flagKeywords}
			err := o.Prepare(nil, nil)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !clierrors.IsUsageError(err) {
					t.Errorf("expected usage error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(o.keywords, tc.wantKeywords) {
				t.Errorf("keywords = %v, want %v", o.keywords, tc.wantKeywords)
			}
		})
	}
}
