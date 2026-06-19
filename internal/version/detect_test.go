/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package version

import (
	"runtime"
	"strings"
	"testing"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		candidate string
		current   string
		want      bool
	}{
		{"1.1.0", "1.0.0", true},
		{"1.0.0", "1.0.0", false},
		{"1.0.0", "1.1.0", false},
		// Prerelease ordering.
		{"1.11.1-dev.12", "1.11.1-dev.5", true},
		{"1.11.1-dev.5", "1.11.1-dev.12", false},
		// A GA release is newer than any prerelease of the same base version.
		{"1.11.1", "1.11.1-dev.12", true},
		{"1.11.1-dev.12", "1.11.1", false},
		// Malformed versions are never treated as an update.
		{"not-a-version", "1.0.0", false},
		{"1.0.0", "not-a-version", false},
	}
	for _, tt := range tests {
		if got := IsNewer(tt.candidate, tt.current); got != tt.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.candidate, tt.current, got, tt.want)
		}
	}
}

func TestParseTagFromLocation(t *testing.T) {
	tests := []struct {
		location string
		want     string
		wantErr  bool
	}{
		{"https://github.com/metaplay/cli/releases/tag/1.11.0", "1.11.0", false},
		{"https://github.com/metaplay/cli/releases/tag/v1.11.0", "1.11.0", false},
		{"https://github.com/metaplay/cli/releases/tag/1.11.1-dev.12", "1.11.1-dev.12", false},
		// No actual release (redirect points back to /releases/latest).
		{"https://github.com/metaplay/cli/releases/latest", "", true},
		// No release published at all (GitHub redirects to .../releases, no /tag/ segment).
		{"https://github.com/metaplay/cli/releases", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		got, err := parseTagFromLocation(tt.location)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseTagFromLocation(%q) error = %v, wantErr %v", tt.location, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("parseTagFromLocation(%q) = %q, want %q", tt.location, got, tt.want)
		}
	}
}

func TestParseLatestDevFromAtom(t *testing.T) {
	// Entries are intentionally NOT in version order to verify we pick by semver, not position.
	feed := `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <title>1.11.1-dev.9</title>
    <link rel="alternate" type="text/html" href="https://github.com/metaplay/cli/releases/tag/1.11.1-dev.9"/>
  </entry>
  <entry>
    <title>1.11.1-dev.12</title>
    <link rel="alternate" type="text/html" href="https://github.com/metaplay/cli/releases/tag/1.11.1-dev.12"/>
  </entry>
  <entry>
    <title>1.11.1-dev.10</title>
    <link rel="alternate" type="text/html" href="https://github.com/metaplay/cli/releases/tag/1.11.1-dev.10"/>
  </entry>
</feed>`

	got, err := parseLatestDevFromAtom([]byte(feed))
	if err != nil {
		t.Fatalf("parseLatestDevFromAtom() unexpected error: %v", err)
	}
	if got != "1.11.1-dev.12" {
		t.Errorf("parseLatestDevFromAtom() = %q, want %q", got, "1.11.1-dev.12")
	}
}

func TestParseLatestDevFromAtom_NoDevReleases(t *testing.T) {
	// A feed containing only GA releases should yield no dev version.
	feed := `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <title>1.11.0</title>
    <link rel="alternate" type="text/html" href="https://github.com/metaplay/cli/releases/tag/1.11.0"/>
  </entry>
</feed>`

	if _, err := parseLatestDevFromAtom([]byte(feed)); err == nil {
		t.Error("parseLatestDevFromAtom() expected an error for a GA-only feed, got nil")
	}
}

func TestAssetURL(t *testing.T) {
	url := assetURL("1.11.0")

	if !strings.HasPrefix(url, "https://github.com/metaplay/cli/releases/download/1.11.0/MetaplayCLI_") {
		t.Errorf("assetURL() has unexpected prefix: %s", url)
	}

	// The archive extension must match what goreleaser produces for this platform.
	wantExt := ".tar.gz"
	if runtime.GOOS == "windows" {
		wantExt = ".zip"
	}
	if !strings.HasSuffix(url, wantExt) {
		t.Errorf("assetURL() = %s, want suffix %s for GOOS=%s", url, wantExt, runtime.GOOS)
	}

	// Sanity-check the OS/arch tokens for the platforms goreleaser builds.
	switch runtime.GOOS {
	case "linux":
		if !strings.Contains(url, "_Linux_") {
			t.Errorf("assetURL() = %s, expected _Linux_ token", url)
		}
	case "windows":
		if !strings.Contains(url, "_Windows_") {
			t.Errorf("assetURL() = %s, expected _Windows_ token", url)
		}
	case "darwin":
		if !strings.Contains(url, "_Darwin_") {
			t.Errorf("assetURL() = %s, expected _Darwin_ token", url)
		}
	}
}
