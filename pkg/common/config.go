package common

import "os"

// Base URL of the Metaplay portal.
var PortalBaseURL = "https://portal.metaplay.dev"

func init() {
	// Allow overriding portalBaseURL with an environment variable (for testing purposes)
	override := os.Getenv("METAPLAYCLI_PORTAL_BASEURL")
	if override != "" {
		PortalBaseURL = override
	}
}
