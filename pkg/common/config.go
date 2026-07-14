/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package common

import "os"

const DefaultPortalBaseURL = "https://portal.metaplay.dev"

// Base URL of the Metaplay portal.
var PortalBaseURL = DefaultPortalBaseURL

// StackApiBaseURLOverride, when non-empty, overrides the StackAPI base URL
// normally constructed from each environment's stack domain. Intended for
// local development against a StackAPI running on the developer's machine.
// Set by the METAPLAYCLI_STACKAPI_BASEURL environment variable at startup.
var StackApiBaseURLOverride string

func init() {
	// Allow overriding portalBaseURL with an environment variable (for testing purposes)
	// To test against local portal: set METAPLAYCLI_PORTAL_BASEURL=http://localhost:3000
	if override := os.Getenv("METAPLAYCLI_PORTAL_BASEURL"); override != "" {
		PortalBaseURL = override
	}

	// Allow overriding the StackAPI base URL with an environment variable
	// (for testing purposes). To test against a local StackAPI:
	// set METAPLAYCLI_STACKAPI_BASEURL=http://localhost:8080
	StackApiBaseURLOverride = os.Getenv("METAPLAYCLI_STACKAPI_BASEURL")
}
