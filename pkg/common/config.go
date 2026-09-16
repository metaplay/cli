/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package common

import "os"

const DefaultPortalBaseURL = "https://portal.metaplay.dev"

// Environment variable to override the portal base URL.
const PortalBaseURLEnvVar = "METAPLAYCLI_PORTAL_BASEURL"

// Base URL of the Metaplay portal.
var PortalBaseURL = DefaultPortalBaseURL

func init() {
	// Allow overriding portalBaseURL with an environment variable (for testing purposes)
	// To test against local portal: set METAPLAYCLI_PORTAL_BASEURL=http://localhost:3000
	override := os.Getenv(PortalBaseURLEnvVar)
	if override != "" {
		PortalBaseURL = override
	}
}
