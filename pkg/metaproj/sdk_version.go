/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package metaproj

import (
	"github.com/hashicorp/go-version"
)

// Represents MetaplaySDK/version.yaml.
type MetaplayVersionMetadata struct {
	SdkVersion                   *version.Version `yaml:"sdkVersion"`
	DefaultDotnetRuntimeVersion  string           `yaml:"defaultDotnetRuntimeVersion"`
	DefaultServerChartVersion    *version.Version `yaml:"defaultServerChartVersion"`
	DefaultBotClientChartVersion *version.Version `yaml:"defaultBotClientChartVersion"`
	MinInfraVersion              *version.Version `yaml:"minInfraVersion"`
	MinServerChartVersion        *version.Version `yaml:"minServerChartVersion"`
	MinBotClientChartVersion     *version.Version `yaml:"minBotClientChartVersion"`
	MinDotnetSdkVersion          *version.Version `yaml:"minDotnetSdkVersion"` // Minimum .NET SDK version required to build projects.
	RecommendedNodeVersion       *version.Version `yaml:"nodeVersion"`
	RecommendedPnpmVersion       *version.Version `yaml:"pnpmVersion"`
}
