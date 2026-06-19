/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package version

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/hashicorp/go-version"
)

const (
	// repoSlug is the owner/repo of the CLI on GitHub.
	repoSlug = "metaplay/cli"

	// latestReleaseURL is the non-rate-limited web redirect to the latest GA release.
	// Issuing a request returns a 302 to https://github.com/<repo>/releases/tag/<version>,
	// from which we parse the version. This avoids api.github.com, which is throttled to
	// 60 requests/hour/IP for unauthenticated requests.
	latestReleaseURL = "https://github.com/" + repoSlug + "/releases/latest"

	// releasesAtomURL is the non-rate-limited Atom feed of releases (including prereleases),
	// served from github.com (not api.github.com). Used to detect the latest dev/prerelease
	// version, which the GA redirect above intentionally skips.
	releasesAtomURL = "https://github.com/" + repoSlug + "/releases.atom"

	// downloadBaseURL is the CDN base for release assets. Downloads from here are not
	// subject to the api.github.com rate limit.
	downloadBaseURL = "https://github.com/" + repoSlug + "/releases/download"

	// httpTimeout bounds every version-detection/download request so that the per-command
	// background check can never hang a command for long when the network is slow or offline.
	httpTimeout = 5 * time.Second
)

// DetectLatest returns the latest available version. When prerelease is true it returns the
// latest dev/prerelease version (from the Atom feed); otherwise it returns the latest GA
// release (from the /releases/latest redirect). Neither path touches the throttled
// api.github.com endpoint.
func DetectLatest(ctx context.Context, prerelease bool) (string, error) {
	if prerelease {
		return detectLatestDev(ctx)
	}
	return detectLatestGA(ctx)
}

// detectLatestGA resolves the latest GA version via the github.com /releases/latest redirect.
func detectLatestGA(ctx context.Context) (string, error) {
	// Disable redirect following so we can read the Location header of the 302 ourselves.
	client := &http.Client{
		Timeout: httpTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, latestReleaseURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to query %s: %w", latestReleaseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusMovedPermanently {
		return "", fmt.Errorf("unexpected status %d from %s (expected a redirect to the latest release)", resp.StatusCode, latestReleaseURL)
	}

	location := resp.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf("no Location header in redirect from %s", latestReleaseURL)
	}
	return parseTagFromLocation(location)
}

// parseTagFromLocation extracts the version from a /releases/latest redirect target, which
// looks like https://github.com/metaplay/cli/releases/tag/1.11.0.
func parseTagFromLocation(location string) (string, error) {
	tag := strings.TrimPrefix(path.Base(location), "v")
	switch tag {
	case "", ".", "/", "latest":
		return "", fmt.Errorf("could not parse a version from redirect URL %q", location)
	}
	return tag, nil
}

// atomFeed is the minimal subset of the GitHub releases Atom feed that we parse.
type atomFeed struct {
	Entries []struct {
		Title string `xml:"title"`
		Link  struct {
			Href string `xml:"href,attr"`
		} `xml:"link"`
	} `xml:"entry"`
}

// detectLatestDev resolves the latest dev/prerelease version from the releases Atom feed.
// The feed lists releases (including prereleases) newest-first; we pick the highest dev
// version by semver rather than relying on ordering.
func detectLatestDev(ctx context.Context) (string, error) {
	client := &http.Client{Timeout: httpTimeout}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesAtomURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to query %s: %w", releasesAtomURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d from %s", resp.StatusCode, releasesAtomURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // feed is small; cap at 1 MiB
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", releasesAtomURL, err)
	}
	return parseLatestDevFromAtom(body)
}

// parseLatestDevFromAtom returns the highest dev/prerelease version found in a GitHub
// releases Atom feed. The feed lists releases newest-first, but we pick by semver rather
// than relying on ordering.
func parseLatestDevFromAtom(body []byte) (string, error) {
	var feed atomFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return "", fmt.Errorf("failed to parse the releases Atom feed: %w", err)
	}

	var best *version.Version
	var bestTag string
	for _, entry := range feed.Entries {
		// Prefer the tag from the link href; fall back to the title.
		tag := strings.TrimPrefix(path.Base(entry.Link.Href), "v")
		if tag == "" {
			tag = strings.TrimPrefix(entry.Title, "v")
		}
		if !strings.Contains(tag, "-dev.") {
			continue
		}
		v, err := version.NewVersion(tag)
		if err != nil {
			continue
		}
		if best == nil || v.GreaterThan(best) {
			best = v
			bestTag = tag
		}
	}

	if bestTag == "" {
		return "", fmt.Errorf("no dev release found in the releases Atom feed")
	}
	return bestTag, nil
}

// IsNewer reports whether candidate is a strictly newer version than current.
// Both are plain version strings (no leading 'v'). On parse failure it returns false,
// so a malformed version is never treated as an available update.
func IsNewer(candidate, current string) bool {
	c, err := version.NewVersion(candidate)
	if err != nil {
		return false
	}
	cur, err := version.NewVersion(current)
	if err != nil {
		return false
	}
	return c.GreaterThan(cur)
}

// assetURL builds the CDN download URL for the release archive of the given version,
// matching the goreleaser archive name templates in .goreleaser.yaml / .goreleaser-dev.yaml.
// Keep this in sync with those templates and with install.sh / install.ps1.
func assetURL(tag string) string {
	osTitle := map[string]string{
		"linux":   "Linux",
		"windows": "Windows",
		"darwin":  "Darwin",
	}[runtime.GOOS]

	arch := map[string]string{
		"amd64": "x86_64",
		"arm64": "arm64",
	}[runtime.GOARCH]

	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}

	name := fmt.Sprintf("MetaplayCLI_%s_%s.%s", osTitle, arch, ext)
	return fmt.Sprintf("%s/%s/%s", downloadBaseURL, tag, name)
}
