package version

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIsDevBuild(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"dev", true},
		{"1.2.3", false},
		{"1.2.3-dev.5", false},
	}
	for _, tt := range tests {
		AppVersion = tt.version
		if got := IsDevBuild(); got != tt.want {
			t.Errorf("IsDevBuild() with version %q = %v, want %v", tt.version, got, tt.want)
		}
	}
}

func TestIsPrerelease(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"dev", false},
		{"1.2.3", false},
		{"1.2.3-dev.5", true},
		{"0.1.0-dev.1", true},
		{"1.2.3-beta.1", false},
	}
	for _, tt := range tests {
		AppVersion = tt.version
		if got := IsPrerelease(); got != tt.want {
			t.Errorf("IsPrerelease() with version %q = %v, want %v", tt.version, got, tt.want)
		}
	}
}

func TestRenderUpdateBanner(t *testing.T) {
	tests := []struct {
		name           string
		currentVersion string
		latestVersion  string
	}{
		{"short versions", "1.0.0", "1.1.0"},
		{"long prerelease versions", "1.2.3-dev.100", "1.2.4-dev.200"},
		{"asymmetric lengths", "0.1.0", "10.20.30-dev.999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := renderUpdateBanner(tt.currentVersion, tt.latestVersion)

			if len(lines) != 6 {
				t.Fatalf("expected 6 lines, got %d", len(lines))
			}

			// Strip ANSI escape codes to measure visual width.
			stripANSI := func(s string) string {
				var out strings.Builder
				inEscape := false
				for _, r := range s {
					if r == '\x1b' {
						inEscape = true
						continue
					}
					if inEscape {
						if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
							inEscape = false
						}
						continue
					}
					out.WriteRune(r)
				}
				return out.String()
			}

			// All lines should have the same visual width.
			widths := make([]int, len(lines))
			for i, line := range lines {
				widths[i] = utf8.RuneCountInString(stripANSI(line))
			}
			for i := 1; i < len(widths); i++ {
				if widths[i] != widths[0] {
					t.Errorf("line %d width %d != line 0 width %d\n  line 0: %s\n  line %d: %s",
						i, widths[i], widths[0], stripANSI(lines[0]), i, stripANSI(lines[i]))
				}
			}

			// First and last lines should be borders.
			first := stripANSI(lines[0])
			if !strings.HasPrefix(first, "╭") || !strings.HasSuffix(first, "╮") {
				t.Errorf("first line should be top border, got: %s", first)
			}
			last := stripANSI(lines[5])
			if !strings.HasPrefix(last, "╰") || !strings.HasSuffix(last, "╯") {
				t.Errorf("last line should be bottom border, got: %s", last)
			}

			// Content lines should contain the version strings.
			content := stripANSI(lines[2])
			if !strings.Contains(content, tt.currentVersion) || !strings.Contains(content, tt.latestVersion) {
				t.Errorf("update line should contain both versions, got: %s", content)
			}
		})
	}
}
