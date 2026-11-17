/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import "testing"

func TestLLMDocsPathFromArg(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want string
	}{
		{"payload path", "docs/game-logic/player-actor.md", "docs/game-logic/player-actor.md"},
		{"payload path without extension", "docs/cloud-deployments/getting-started", "docs/cloud-deployments/getting-started"},
		{"io html", "https://docs.metaplay.io/game-logic/player-actor.html", "docs/game-logic/player-actor.md"},
		{"dev html", "https://docs.metaplay.dev/game-logic/player-actor.html", "docs/game-logic/player-actor.md"},
		{"http scheme", "http://docs.metaplay.io/a/b.html", "docs/a/b.md"},
		{"uppercase scheme and host", "HTTPS://Docs.Metaplay.IO/a/b.html", "docs/a/b.md"},
		{"www host", "https://www.docs.metaplay.io/a/b.html", "docs/a/b.md"},
		{"fragment", "https://docs.metaplay.io/a/b.html#some-section", "docs/a/b.md"},
		{"query", "https://docs.metaplay.io/a/b.html?x=1", "docs/a/b.md"},
		{"clean url", "https://docs.metaplay.io/cloud-deployments/setup-ci-pipeline", "docs/cloud-deployments/setup-ci-pipeline.md"},
		{"trailing slash", "https://docs.metaplay.io/miscellaneous/sdk-updates/", "docs/miscellaneous/sdk-updates/index.md"},
		{"release notes", "https://docs.metaplay.dev/miscellaneous/sdk-updates/release-notes/release-36.html", "release-notes/release-36.md"},
		{"release notes index", "https://docs.metaplay.io/miscellaneous/sdk-updates/release-notes/", "release-notes/index.md"},
		{"site root", "https://docs.metaplay.io", "docs/index.md"},
		{"md url", "https://docs.metaplay.io/a/b.md", "docs/a/b.md"},
		{"uppercase html", "https://docs.metaplay.io/a/B.HTML", "docs/a/B.md"},
		{"uppercase md", "https://docs.metaplay.io/a/B.MD", "docs/a/B.md"},
		{"escaped path", "https://docs.metaplay.io/a/b%20c.html", "docs/a/b c.md"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := llmDocsPathFromArg(tc.arg)
			if err != nil {
				t.Fatalf("llmDocsPathFromArg(%q) returned error: %v", tc.arg, err)
			}
			if got != tc.want {
				t.Errorf("llmDocsPathFromArg(%q) = %q, want %q", tc.arg, got, tc.want)
			}
		})
	}
}

func TestLLMDocsPathFromArgRejectsOtherHosts(t *testing.T) {
	for _, arg := range []string{
		"https://example.com/a/b.html",
		"https://docs.metaplay.io.evil.com/a/b.html",
		"https://metaplay.io/a/b.html",
	} {
		if _, err := llmDocsPathFromArg(arg); err == nil {
			t.Errorf("llmDocsPathFromArg(%q) succeeded, want error", arg)
		}
	}
}
