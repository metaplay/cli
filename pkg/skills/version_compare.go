/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
)

// DevVersion is the sentinel string used by dev (unstamped) CLI builds in
// place of a semver. It compares less than any valid semver — so a release
// CLI installing over a dev-stamped wrapper always wins, but a dev CLI
// installing over a release-stamped wrapper does not (the dev path is
// expected to bypass the gate via --force or IsDevBuild).
const DevVersion = "dev"

// CompareVersions returns -1 if a < b, 0 if equal, +1 if a > b.
//
// The DevVersion sentinel is treated specially:
//   - "dev" == "dev"
//   - "dev" < any semver
//   - any semver > "dev"
//
// Both arguments must be either "dev" or parseable by Masterminds/semver/v3
// (which accepts a leading "v"); otherwise an error is returned.
func CompareVersions(a, b string) (int, error) {
	if a == DevVersion && b == DevVersion {
		return 0, nil
	}
	if a == DevVersion {
		return -1, nil
	}
	if b == DevVersion {
		return 1, nil
	}
	av, err := semver.NewVersion(a)
	if err != nil {
		return 0, fmt.Errorf("parse version %q: %w", a, err)
	}
	bv, err := semver.NewVersion(b)
	if err != nil {
		return 0, fmt.Errorf("parse version %q: %w", b, err)
	}
	return av.Compare(bv), nil
}
