/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envutil

import "os"

// IsCI reports whether the process is running in a CI environment
// by checking common CI provider environment variables.
func IsCI() bool {
	envVars := []string{
		"CI", "GITHUB_ACTIONS", "GITLAB_CI", "BITBUCKET_BUILD_NUMBER",
		"CIRCLECI", "TRAVIS", "APPVEYOR", "TEAMCITY_VERSION",
		"BUILDKITE", "HUDSON_URL", "JENKINS_URL", "BAMBOO_AGENT_HOME",
		"TFS_BUILD", "NETLIFY", "NOW_BUILDER",
	}
	for _, v := range envVars {
		if os.Getenv(v) != "" {
			return true
		}
	}
	return false
}
