/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestLLMDocsSearchOptsPrepare(t *testing.T) {
	tests := []struct {
		name         string
		flagKeywords string
		wantKeywords []string
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
			name:         "no keywords sends the query alone",
			flagKeywords: "",
			wantKeywords: nil,
		},
		{
			name:         "only separators sends the query alone",
			flagKeywords: " , , ",
			wantKeywords: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := &llmDocsSearchOpts{flagKeywords: tc.flagKeywords}
			if err := o.Prepare(nil, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(o.keywords, tc.wantKeywords) {
				t.Errorf("keywords = %v, want %v", o.keywords, tc.wantKeywords)
			}
		})
	}
}

func TestMissingKeywordsError(t *testing.T) {
	t.Run("older service rejecting a search without keywords", func(t *testing.T) {
		cliErr := missingKeywordsError(status.Error(codes.InvalidArgument, "missing required parameter: keywords"))
		if cliErr == nil {
			t.Fatal("expected an error, got nil")
		}
		if !strings.Contains(cliErr.Suggestion, "--keywords") {
			t.Errorf("suggestion does not mention --keywords: %q", cliErr.Suggestion)
		}
		if len(cliErr.Details) != 1 || cliErr.Details[0] != "missing required parameter: keywords" {
			t.Errorf("expected the service message in details, got %v", cliErr.Details)
		}
	})

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"other invalid argument", status.Error(codes.InvalidArgument, "missing required parameter: query")},
		{"other status code", status.Error(codes.Unavailable, "keywords service down")},
		{"not a gRPC error", errors.New("missing required parameter: keywords")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if cliErr := missingKeywordsError(tc.err); cliErr != nil {
				t.Errorf("expected nil, got %v", cliErr)
			}
		})
	}
}
