/*
 * Copyright Metaplay. All rights reserved.
 */
package common

import "os"

const DefaultPortalBaseURL = "https://portal.metaplay.dev"

// Base URL of the Metaplay portal.
var PortalBaseURL = DefaultPortalBaseURL

func init() {
	// Allow overriding portalBaseURL with an environment variable (for testing purposes)
	// To test against local portal: set METAPLAYCLI_PORTAL_BASEURL=http://localhost:3000
	override := os.Getenv("METAPLAYCLI_PORTAL_BASEURL")
	if override != "" {
		PortalBaseURL = override
	}
}
