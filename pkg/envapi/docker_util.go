/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/daemon"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	version "github.com/hashicorp/go-version"
	"github.com/rs/zerolog/log"
)

// Metadata about a Metaplay docker image.
type MetaplayImageInfo struct {
	ImageID     string        // Docker image ID
	Name        string        // Image name (generally project ID for local projects), empty for remote projects.
	RepoTag     string        // Eg, 'lovely-wombats-build:12345678'.
	Tag         string        // Image tag (eg, Git hash).
	ProjectID   string        // Project human ID (label io.metaplay.project_id).
	SdkVersion  string        // Metaplay SDK version (label io.metaplay.sdk_version).
	CommitID    string        // Commit ID, e.g., git hash (label io.metaplay.commit_id).
	BuildNumber string        // Build number (label io.metaplay.build_number).
	ConfigFile  v1.ConfigFile // Docker metadata.
}

func newMetaplayImageInfo(imageID, repoTag, tag string, configFile v1.ConfigFile) (*MetaplayImageInfo, error) {
	// Extract required labels
	projectID, ok := configFile.Config.Labels["io.metaplay.project_id"]
	if !ok {
		return nil, fmt.Errorf("missing required label: io.metaplay.project_id")
	}

	sdkVersion, ok := configFile.Config.Labels["io.metaplay.sdk_version"]
	if !ok {
		return nil, fmt.Errorf("missing required label: io.metaplay.sdk_version")
	}

	commitID, ok := configFile.Config.Labels["io.metaplay.commit_id"]
	if !ok {
		return nil, fmt.Errorf("missing required label: io.metaplay.commit_id")
	}

	buildNumber, ok := configFile.Config.Labels["io.metaplay.build_number"]
	if !ok {
		return nil, fmt.Errorf("missing required label: io.metaplay.build_number")
	}

	// Create and return the MetaplayImageInfo
	return &MetaplayImageInfo{
		ImageID:     imageID,
		Name:        projectID, // Use projectID as name for local images
		RepoTag:     repoTag,
		Tag:         tag,
		ProjectID:   projectID,
		SdkVersion:  sdkVersion,
		CommitID:    commitID,
		BuildNumber: buildNumber,
		ConfigFile:  configFile,
	}, nil
}

// ReadLocalDockerImageMetadata retrieves metadata from a local Docker image.
func ReadLocalDockerImageMetadata(imageRef string) (*v1.ConfigFile, error) {
	// Parse the image reference (name + tag or digest)
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse local docker image reference: %w", err)
	}

	// Load the image from the local Docker daemon
	img, err := daemon.Image(ref)
	if err != nil {
		return nil, fmt.Errorf("failed to get local docker image: %w", err)
	}

	// Fetch the image configuration blob
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("failed to get local docker image config file: %w", err)
	}

	return cfg, nil
}

// FetchRemoteDockerImageMetadata retrieves the labels of an image in a remote Docker registry.
func FetchRemoteDockerImageMetadata(creds *DockerCredentials, imageRef string) (*v1.ConfigFile, error) {
	// Create a registry authenticator using the provided credentials
	authenticator := authn.FromConfig(authn.AuthConfig{
		Username: creds.Username,
		Password: creds.Password,
	})

	// Parse the image reference (name + tag or digest)
	ref, err := name.ParseReference(imageRef, name.WithDefaultRegistry(creds.RegistryURL))
	if err != nil {
		return nil, fmt.Errorf("failed to parse remote docker image reference: %w", err)
	}

	// Retrieve the image manifest and associated metadata
	desc, err := remote.Get(ref, remote.WithAuth(authenticator))
	if err != nil {
		return nil, fmt.Errorf("failed to get remote docker image descriptor: %w", err)
	}

	// Fetch the image configuration blob
	img, err := desc.Image()
	if err != nil {
		return nil, fmt.Errorf("failed to get remote docker image from descriptor: %w", err)
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("failed to get remote docker image config file: %w", err)
	}

	// Return the labels from the configuration
	return cfg, nil
}

// ReadLocalDockerImagesByProjectID retrieves metadata for all local Docker images
// that have the 'io.metaplay.project_id' label matching the provided projectID.
// The images are returned in a timestamp order, latest first (highest timestamp first).
func ReadLocalDockerImagesByProjectID(projectID string) ([]MetaplayImageInfo, error) {
	log.Debug().Msgf("Reading local docker images for project ID: %s", projectID)

	// Create a new Docker client, enabling API version negotiation
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	// Negotiate API version -- the WithAPIVersionNegotiation above doesn't do this regardless of the name
	cli.NegotiateAPIVersion(context.Background())

	// Check Docker daemon connectivity and API version compatibility
	ping, err := cli.Ping(context.Background())
	if err != nil {
		// Check if the error is due to daemon not running or inaccessible
		if client.IsErrConnectionFailed(err) {
			return nil, fmt.Errorf("cannot connect to the Docker daemon. Is the docker daemon running and accessible?")
		}
		return nil, fmt.Errorf("failed to ping Docker daemon: %w", err)
	}

	// Compare daemon API version (max supported) with the client's negotiated version.
	// The client attempts to use its version, which must be <= the daemon's max version.
	clientAPIVersion := cli.ClientVersion()
	daemonMaxAPIVersion := ping.APIVersion

	clientVsn, errClient := version.NewVersion(clientAPIVersion)
	daemonMaxVsn, errDaemon := version.NewVersion(daemonMaxAPIVersion)

	if errClient != nil {
		log.Warn().Msgf("Failed to parse Docker client API version '%v', skipping version compatibility check", clientAPIVersion)
	} else if errDaemon != nil {
		log.Warn().Msgf("Failed to parse Docker daemon API version '%v', skipping version compatibility check", daemonMaxAPIVersion)
	} else {
		// Proper semantic version comparison
		if clientVsn.GreaterThan(daemonMaxVsn) {
			return nil, fmt.Errorf("docker daemon API version %s is too old. This CLI requires the daemon to support at least API version %s. Please update your Docker daemon", daemonMaxAPIVersion, clientAPIVersion)
		}
	}

	// Create filter for the project ID label
	filterArgs := filters.NewArgs()
	filterArgs.Add("label", fmt.Sprintf("io.metaplay.project_id=%s", projectID))

	// List all images from the local Docker daemon with the filter
	images, err := cli.ImageList(context.Background(), image.ListOptions{
		All:     false,
		Filters: filterArgs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list local docker images: %w", err)
	}

	var matchingImages []MetaplayImageInfo

	// Iterate through all images
	for _, img := range images {
		// For each image ID, create a reference and fetch metadata
		// log.Info().Msgf("Repo tags: %#v", img.RepoTags)
		for _, repoTag := range img.RepoTags {
			if repoTag == "" {
				continue
			}

			ref, err := name.ParseReference(repoTag)
			if err != nil {
				continue
			}

			// Get the image configuration using go-containerregistry
			containerImg, err := daemon.Image(ref)
			if err != nil {
				continue
			}

			cfg, err := containerImg.ConfigFile()
			if err != nil {
				continue
			}

			// Skip any remote references.
			if strings.Contains(repoTag, "/") {
				continue
			}

			// Convert ConfigFile to MetaplayImageInfo
			// log.Info().Msgf("REF: context=%s, identifier=%s, name=%s", ref.Context(), ref.Identifier(), ref.Name())
			imageInfo, err := newMetaplayImageInfo(img.ID, repoTag, ref.Identifier(), *cfg)
			if err != nil {
				continue // Skip images with missing required labels
			}

			matchingImages = append(matchingImages, *imageInfo)
			// break // Found a match for this image, no need to check other tags
		}
	}

	// Sort latest image first
	slices.SortFunc(matchingImages, func(a, b MetaplayImageInfo) int {
		// First sort by image time, latest first
		if imgCmp := a.ConfigFile.Created.Time.Compare(b.ConfigFile.Created.Time); imgCmp != 0 {
			return -imgCmp
		}
		if a.ImageID == b.ImageID {
			// Within the same image, sort by tag largest first. The intention is that tags commonly represent a
			// timestamp in a sortable format. Sort the latest tag first.
			if tagCmp := strings.Compare(a.Tag, b.Tag); tagCmp != 0 {
				return -tagCmp
			}
		}
		return 0
	})

	return matchingImages, nil
}
