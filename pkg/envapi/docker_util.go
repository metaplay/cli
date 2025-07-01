/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	version "github.com/hashicorp/go-version"
	"github.com/rs/zerolog/log"
)

// Metadata about a Metaplay docker image.
type MetaplayImageInfo struct {
	ImageID      string    // Docker image ID
	Name         string    // Image name (generally project ID for local projects), empty for remote projects.
	RepoTag      string    // Eg, 'lovely-wombats-build:12345678'.
	Tag          string    // Image tag (eg, Git hash).
	ProjectID    string    // Project human ID (label io.metaplay.project_id).
	SdkVersion   string    // Metaplay SDK version (label io.metaplay.sdk_version).
	CommitID     string    // Commit ID, e.g., git hash (label io.metaplay.commit_id).
	BuildNumber  string    // Build number (label io.metaplay.build_number).
	CreatedTime  time.Time // Image creation timestamp.
	OS           string    // OS the image is built for (e.g., "linux") - can be added if needed elsewhere
	Architecture string    // Architecture the image is built for (e.g., "amd64") - can be added if needed elsewhere
}

func newMetaplayImageInfo(imageID, repoTag, tag string, labels map[string]string, createdTime time.Time, os string, architecture string) (*MetaplayImageInfo, error) {
	// Extract required labels for a valid Metaplay server image.
	projectID, ok := labels["io.metaplay.project_id"]
	if !ok {
		return nil, fmt.Errorf("missing required label 'io.metaplay.project_id' in image %s (tag %s)", imageID, repoTag)
	}

	sdkVersion, ok := labels["io.metaplay.sdk_version"]
	if !ok {
		return nil, fmt.Errorf("missing required label 'io.metaplay.sdk_version' in image %s (tag %s)", imageID, repoTag)
	}

	commitID, ok := labels["io.metaplay.commit_id"]
	if !ok {
		return nil, fmt.Errorf("missing required label 'io.metaplay.commit_id' in image %s (tag %s)", imageID, repoTag)
	}

	buildNumber, ok := labels["io.metaplay.build_number"]
	if !ok {
		return nil, fmt.Errorf("missing required label 'io.metaplay.build_number' in image %s (tag %s)", imageID, repoTag)
	}

	// Create and return the MetaplayImageInfo
	return &MetaplayImageInfo{
		ImageID:      imageID,
		Name:         projectID, // Use projectID as name for local images
		RepoTag:      repoTag,
		Tag:          tag,
		ProjectID:    projectID,
		SdkVersion:   sdkVersion,
		CommitID:     commitID,
		BuildNumber:  buildNumber,
		CreatedTime:  createdTime,
		OS:           os,
		Architecture: architecture,
	}, nil
}

// newDockerClient creates a new Docker client with a verified connection.
// It first tries the default connection mechanism (via environment variables or default socket).
// If that fails on macOS, it attempts to connect to the Docker Desktop socket as a fallback.
func NewDockerClient() (*client.Client, error) {
	// Try creating a client from environment variables (respects DOCKER_HOST).
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client from environment: %w", err)
	}

	// Ping the daemon to verify connectivity.
	_, err = dockerClient.Ping(context.Background())
	if err == nil {
		dockerClient.NegotiateAPIVersion(context.Background())
		return dockerClient, nil // Success
	}

	// If connection failed, decide if we should try a fallback.
	if client.IsErrConnectionFailed(err) && runtime.GOOS == "darwin" {
		log.Debug().Msg("Docker connection with default settings failed, trying Docker Desktop socket path as a fallback...")
		dockerClient.Close() // Close the failed client.

		homeDir, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return nil, fmt.Errorf("cannot find home directory to build fallback Docker socket path: %w", homeErr)
		}
		socketPath := filepath.Join(homeDir, ".docker", "run", "docker.sock")
		host := "unix://" + socketPath

		// Try again with the fallback host.
		dockerClient, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation(), client.WithHost(host))
		if err != nil {
			return nil, fmt.Errorf("failed to create Docker client with fallback host '%s': %w", host, err)
		}

		// Ping again to verify the fallback connection.
		_, err = dockerClient.Ping(context.Background())
		if err != nil {
			dockerClient.Close()
			return nil, fmt.Errorf("cannot connect to the Docker daemon. Is the docker daemon running and accessible? Fallback connection also failed: %w", err)
		}

		dockerClient.NegotiateAPIVersion(context.Background())
		return dockerClient, nil // Success with fallback
	}

	// For non-connection errors, or non-macOS systems, return the original error.
	return nil, fmt.Errorf("cannot connect to the Docker daemon. Is the docker daemon running and accessible? Original error: %w", err)
}

// ReadLocalDockerImageMetadata retrieves metadata from a local Docker image.
func ReadLocalDockerImageMetadata(imageRefString string) (*MetaplayImageInfo, error) {
	// Create a new Docker client
	dockerClient, err := NewDockerClient()
	if err != nil {
		return nil, err // Pass up the detailed error from NewDockerClient
	}
	defer dockerClient.Close()

	// Parse the image reference string (e.g., "myimage:latest" or "image-id")
	// This is needed for imageRef.Identifier() when calling newMetaplayImageInfoFromInspect.
	parsedRef, err := name.ParseReference(imageRefString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse local docker image reference '%s': %w", imageRefString, err)
	}

	// Inspect the image using the Docker SDK
	imageInspect, err := dockerClient.ImageInspect(context.Background(), imageRefString)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect local docker image '%s': %w", imageRefString, err)
	}

	// Use the helper function to convert inspect data to MetaplayImageInfo
	// Pass imageInspect.ID as imageID, imageRefString as repoTag, and parsedRef for its Identifier method.
	return newMetaplayImageInfoFromInspect(imageInspect.ID, imageRefString, parsedRef, imageInspect)
}

// FetchRemoteDockerImageMetadata retrieves the labels of an image in a remote Docker registry.
func FetchRemoteDockerImageMetadata(creds *DockerCredentials, imageRef string) (*MetaplayImageInfo, error) {
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

	// Use the helper function to convert config file data to MetaplayImageInfo
	// ImageID can be obtained from the image's digest for uniqueness.
	digest, err := img.Digest()
	if err != nil {
		return nil, fmt.Errorf("failed to get image digest for %s: %w", imageRef, err)
	}
	imageID := digest.String()
	// The 'tag' for newMetaplayImageInfo is the specific identifier part of the reference (tag or digest).
	tag := ref.Identifier()

	return newMetaplayImageInfo(imageID, imageRef, tag, cfg.Config.Labels, cfg.Created.Time, cfg.OS, cfg.Architecture)
}

// newMetaplayImageInfoFromInspect creates a MetaplayImageInfo from an image inspect response.
func newMetaplayImageInfoFromInspect(imageID string, repoTag string, imageRef name.Reference, imageInspect image.InspectResponse) (*MetaplayImageInfo, error) {
	// Parse the image's creation time.
	createdTime, err := time.Parse(time.RFC3339Nano, imageInspect.Created)
	if err != nil {
		// Fallback parsing for non-Nano precision, Docker might not always include nano
		createdTime, err = time.Parse(time.RFC3339, imageInspect.Created)
		if err != nil {
			return nil, fmt.Errorf("failed to parse image created time '%s' for image %s: %w", imageInspect.Created, imageID, err)
		}
	}

	// Get the image's labels.
	if imageInspect.Config == nil {
		return nil, fmt.Errorf("image %s (repoTag %s) has nil Config from ImageInspect()", imageID, repoTag)
	}
	labels := imageInspect.Config.Labels
	if labels == nil {
		return nil, fmt.Errorf("image %s (repoTag %s) has nil labels from ImageInspect()", imageID, repoTag)
	}

	// Convert ImageInspect data to MetaplayImageInfo
	return newMetaplayImageInfo(
		imageID,
		repoTag,
		imageRef.Identifier(), // from name.ParseReference(repoTag)
		labels,                // from imageInspect.Config.Labels
		createdTime,           // from imageInspect.Created (parsed)
		imageInspect.Os,
		imageInspect.Architecture,
	)
}

// ReadLocalDockerImagesByProjectID retrieves metadata for all local Docker images
// that have the 'io.metaplay.project_id' label matching the provided projectID.
// The images are returned in a timestamp order, latest first (highest timestamp first).
func ReadLocalDockerImagesByProjectID(projectID string) ([]MetaplayImageInfo, error) {
	log.Debug().Msgf("Reading local docker images for project ID: %s", projectID)

	// Create a new Docker client using the helper function.
	dockerClient, err := NewDockerClient()
	if err != nil {
		return nil, err // Pass up the detailed error from NewDockerClient
	}
	defer dockerClient.Close()

	// Check Docker API version compatibility.
	// The ping is already done in NewDockerClient, but we need the ping struct for the API version.
	ping, err := dockerClient.Ping(context.Background())
	if err != nil {
		// This should ideally not happen as NewDockerClient is supposed to return a connected client.
		return nil, fmt.Errorf("failed to ping Docker daemon after successful connection: %w", err)
	}

	// Compare daemon API version (max supported) with the client's negotiated version.
	// The client attempts to use its version, which must be <= the daemon's max version.
	clientAPIVersion := dockerClient.ClientVersion()
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
	images, err := dockerClient.ImageList(context.Background(), image.ListOptions{
		All:     false,
		Filters: filterArgs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list local docker images: %w", err)
	}

	// Parse the image information for all local images matching this project.
	var matchingImages []MetaplayImageInfo
	for _, img := range images {
		// For each image ID, create a reference and fetch metadata
		// log.Info().Msgf("Repo tags: %#v", img.RepoTags)
		for _, repoTag := range img.RepoTags {
			if repoTag == "" {
				continue
			}

			// Skip tags that look like fully qualified names (e.g., 'docker.io/library/ubuntu:latest' or 'customregistry/myimage:tag').
			// We only want to handle simple/local tags (e.g., 'myimage:latest').
			if strings.Contains(repoTag, "/") {
				continue
			}

			// Parse the image reference.
			imageRef, err := name.ParseReference(repoTag)
			if err != nil {
				continue
			}

			// Get the image configuration using Docker SDK
			imageInspect, err := dockerClient.ImageInspect(context.Background(), img.ID)
			if err != nil {
				log.Warn().Err(err).Msgf("Failed to inspect image %s (for repoTag %s), skipping", img.ID, repoTag)
				continue
			}

			imageInfo, err := newMetaplayImageInfoFromInspect(img.ID, repoTag, imageRef, imageInspect)
			if err != nil {
				// Log the error from newMetaplayImageInfoFromInspect, which might indicate parsing/validation failures.
				log.Debug().Err(err).Msgf("Skipping image %s (repoTag %s) as it could not be processed: %v", img.ID, repoTag, err)
				continue
			}
			matchingImages = append(matchingImages, *imageInfo)
		}
	}

	// Sort latest-built image first
	slices.SortFunc(matchingImages, func(a, b MetaplayImageInfo) int {
		// First sort by image time, with latest-built image as the first item in the result
		if imgCmp := a.CreatedTime.Compare(b.CreatedTime); imgCmp != 0 {
			return -imgCmp // Note: Compare returns -1 if a < b, 0 if a == b, 1 if a > b. For descending (latest first), we want -1 if a > b.
		}

		// If the CreatedTime was the same, sort by tag (largest first). When the timestamp is exactly the
		// same, it's often the same image tagged multiple times. Tags commonly represent a timestamp in a
		// sortable format, and we want to sort the latest tag first. In the rare case it is NOT the same
		// image tagged multiple times, with the same reasoning we want the latest tag sorted first.
		return -strings.Compare(a.Tag, b.Tag)
	})

	return matchingImages, nil
}
