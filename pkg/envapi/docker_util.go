/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/versions"
	"github.com/rs/zerolog/log"
)

// The labels a Metaplay server image carries about itself.
const (
	labelProjectID   = "io.metaplay.project_id"
	labelSdkVersion  = "io.metaplay.sdk_version"
	labelCommitID    = "io.metaplay.commit_id"
	labelBuildNumber = "io.metaplay.build_number"
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
	projectID, ok := labels[labelProjectID]
	if !ok {
		return nil, fmt.Errorf("missing required label '%s' in image %s (tag %s)", labelProjectID, imageID, repoTag)
	}

	sdkVersion, ok := labels[labelSdkVersion]
	if !ok {
		return nil, fmt.Errorf("missing required label '%s' in image %s (tag %s)", labelSdkVersion, imageID, repoTag)
	}

	commitID, ok := labels[labelCommitID]
	if !ok {
		return nil, fmt.Errorf("missing required label '%s' in image %s (tag %s)", labelCommitID, imageID, repoTag)
	}

	buildNumber, ok := labels[labelBuildNumber]
	if !ok {
		return nil, fmt.Errorf("missing required label '%s' in image %s (tag %s)", labelBuildNumber, imageID, repoTag)
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

// dockerResponseTimeout bounds how long WaitForDocker waits for Docker to answer. A paused or
// still-starting Docker Desktop accepts connections on its socket before its engine answers, and
// without a bound a daemon that never answers hangs the command. Docker Desktop can take tens of
// seconds to start its engine, and CI runners can be slow to start the daemon, so it is generous.
const dockerResponseTimeout = 60 * time.Second

// WaitForDocker reports through onWaiting after dockerSlowAfter, and then every
// dockerStillWaitingEvery, so a slow start doesn't look like a hang in a terminal or a CI log.
const (
	dockerSlowAfter         = 3 * time.Second
	dockerStillWaitingEvery = 10 * time.Second
)

// WaitForDocker runs check, which talks to Docker, with a deadline of dockerResponseTimeout. While
// check runs, it reports progress messages through onWaiting, and it makes no further calls to
// onWaiting after it returns. If the deadline passes, it returns a *clierrors.CLIError saying
// Docker did not respond. Otherwise it returns check's error as is. Callers tell the timeout apart
// from check's own errors by that type, so check must not return a *clierrors.CLIError.
//
// Inside a task runner task, pass the task output's AppendLine as onWaiting: logging directly would
// garble the display. Outside one, pass LogWaiting.
func WaitForDocker(ctx context.Context, onWaiting func(message string), check func(ctx context.Context) error) error {
	checkCtx, cancel := context.WithTimeout(ctx, dockerResponseTimeout)
	defer cancel()

	// Run check on its own goroutine, and report from this one: onWaiting then can't run after return.
	checkDone := make(chan error, 1)
	go func() { checkDone <- check(checkCtx) }()

	next := time.NewTimer(dockerSlowAfter)
	defer next.Stop()
	message := "Waiting for Docker to respond..."
	for {
		select {
		case err := <-checkDone:
			if err != nil && ctx.Err() == nil && errors.Is(checkCtx.Err(), context.DeadlineExceeded) {
				return clierrors.Wrapf(err, "Docker did not respond within %d seconds", int(dockerResponseTimeout.Seconds())).
					WithSuggestion("Make sure Docker is running and not paused, then try again")
			}
			return err
		case <-next.C:
			onWaiting(message)
			message = "Still waiting for Docker to respond..."
			next.Reset(dockerStillWaitingEvery)
		}
	}
}

// NewDockerClient creates a Docker client with a verified connection to the daemon at DOCKER_HOST,
// or at the default socket. On macOS, when DOCKER_HOST is unset and the default socket is not
// reachable, it tries the Docker Desktop socket in the user's home directory.
//
// onWaiting receives progress messages while the daemon is slow to answer; see WaitForDocker.
func NewDockerClient(ctx context.Context, onWaiting func(message string)) (*client.Client, error) {
	dockerClient, err := connectDocker(ctx, onWaiting, client.FromEnv)
	if client.IsErrConnectionFailed(err) && runtime.GOOS == "darwin" && os.Getenv(client.EnvOverrideHost) == "" {
		if homeDir, homeErr := os.UserHomeDir(); homeErr == nil {
			host := "unix://" + filepath.Join(homeDir, ".docker", "run", "docker.sock")
			log.Debug().Msgf("Docker connection with default settings failed, trying Docker Desktop socket %s", host)
			dockerClient, err = connectDocker(ctx, onWaiting, client.FromEnv, client.WithHost(host))
		}
	}
	if err == nil {
		return dockerClient, nil
	}

	if _, ok := errors.AsType[*clierrors.CLIError](err); ok || ctx.Err() != nil {
		return nil, err // Already explained, or cancelled (runCommand reports cancellation)
	}
	dockerSuggestion := "Start Docker Desktop"
	switch runtime.GOOS {
	case "darwin":
		dockerSuggestion = "Start Docker Desktop, or run 'open -a Docker'"
	case "linux":
		dockerSuggestion = "Start Docker with 'sudo systemctl start docker', or ensure your user is in the 'docker' group"
	case "windows":
		dockerSuggestion = "Start Docker Desktop from the Start menu"
	}
	return nil, clierrors.Wrap(err, "Cannot connect to Docker").
		WithSuggestion(dockerSuggestion)
}

// connectDocker creates a client with opts, then pings the daemon to verify it answers and to
// negotiate the API version.
func connectDocker(ctx context.Context, onWaiting func(message string), opts ...client.Opt) (*client.Client, error) {
	dockerClient, err := client.New(opts...)
	if err != nil {
		return nil, clierrors.Wrap(err, "Invalid Docker client configuration").
			WithSuggestion("Check the DOCKER_HOST, DOCKER_API_VERSION, DOCKER_CERT_PATH and DOCKER_TLS_VERIFY environment variables")
	}

	var ping client.PingResult
	err = WaitForDocker(ctx, onWaiting, func(ctx context.Context) error {
		var pingErr error
		ping, pingErr = dockerClient.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true})
		return pingErr
	})
	if err != nil {
		_ = dockerClient.Close()
		// Ping returns the daemon's API version along with the negotiation error. Check the version
		// itself: the client reports other failures, such as any HTTP 400, as the same error type.
		if ping.APIVersion != "" && versions.LessThan(ping.APIVersion, client.MinAPIVersion) {
			return nil, clierrors.Wrapf(err, "Docker API version %s is not supported", ping.APIVersion).
				WithSuggestion(fmt.Sprintf("Upgrade Docker to a version that supports API %s or newer", client.MinAPIVersion))
		}
		return nil, err
	}
	return dockerClient, nil
}

// LogWaiting reports a WaitForDocker progress message outside a task runner task.
func LogWaiting(message string) { log.Info().Msg(message) }

// ReadLocalDockerImageMetadata retrieves metadata from a local Docker image.
func ReadLocalDockerImageMetadata(ctx context.Context, imageRefString string) (*MetaplayImageInfo, error) {
	dockerClient, err := NewDockerClient(ctx, LogWaiting)
	if err != nil {
		return nil, err // Pass up the detailed error from NewDockerClient
	}
	defer func() { _ = dockerClient.Close() }()

	// Parse the image reference string (e.g., "myimage:latest" or "image-id")
	// This is needed for imageRef.Identifier() when calling newMetaplayImageInfoFromInspect.
	parsedRef, err := name.ParseReference(imageRefString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse local docker image reference '%s': %w", imageRefString, err)
	}

	// Inspect the image using the Docker SDK
	imageInspect, err := dockerClient.ImageInspect(ctx, imageRefString)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect local docker image '%s': %w", imageRefString, err)
	}

	// Use the helper function to convert inspect data to MetaplayImageInfo
	// Pass imageInspect.ID as imageID, imageRefString as repoTag, and parsedRef for its Identifier method.
	return newMetaplayImageInfoFromInspect(imageInspect.ID, imageRefString, parsedRef, imageInspect.InspectResponse)
}

// RemoteDockerImageDigests holds the two content digests that can identify a remote image. Which
// one matches a local image's reported ID depends on the local Docker daemon's image store:
// the legacy store reports the config digest as the image ID, while the containerd store reports
// the manifest digest. Both are preserved across push/pull, so comparing a local ID against both
// detects an identical already-pushed image regardless of the store backend.
type RemoteDockerImageDigests struct {
	ConfigDigest   string // Digest of the image config blob (the legacy image-store ID).
	ManifestDigest string // Digest of the image manifest (the containerd image-store ID).
}

// FetchRemoteDockerImageDigests returns the config and manifest digests of the image at the given
// reference in a remote Docker registry.
//
// The 'exists' return value is false (with a nil error) when no image exists at the reference.
func FetchRemoteDockerImageDigests(creds *DockerCredentials, imageRef string) (digests *RemoteDockerImageDigests, exists bool, err error) {
	log.Debug().Msgf("Fetch digests of remote container image: %s", imageRef)
	if imageRef == "" {
		return nil, false, fmt.Errorf("empty image reference")
	}

	// Create a registry authenticator using the provided credentials.
	authenticator := authn.FromConfig(authn.AuthConfig{
		Username: creds.Username,
		Password: creds.Password,
	})

	// Parse the image reference (name + tag or digest).
	ref, err := name.ParseReference(imageRef, name.WithDefaultRegistry(creds.RegistryURL))
	if err != nil {
		return nil, false, fmt.Errorf("failed to parse remote docker image reference '%s': %w", imageRef, err)
	}

	// Fetch the image descriptor. A 'not found' response means the tag is free.
	desc, err := remote.Get(ref, remote.WithAuth(authenticator))
	if err != nil {
		if isRemoteImageNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to query remote docker image '%s': %w", imageRef, err)
	}

	// Resolve the image and read its config digest. The descriptor digest is the manifest digest.
	img, err := desc.Image()
	if err != nil {
		return nil, false, fmt.Errorf("failed to get remote docker image from descriptor '%s': %w", imageRef, err)
	}
	cfgName, err := img.ConfigName()
	if err != nil {
		return nil, false, fmt.Errorf("failed to get config digest for remote docker image '%s': %w", imageRef, err)
	}

	return &RemoteDockerImageDigests{
		ConfigDigest:   cfgName.String(),
		ManifestDigest: desc.Digest.String(),
	}, true, nil
}

// isRemoteImageNotFound reports whether err from a remote registry query indicates that no image
// exists at the requested reference (as opposed to a transport/auth/other failure).
func isRemoteImageNotFound(err error) bool {
	if transportErr, ok := errors.AsType[*transport.Error](err); ok {
		for _, code := range transportErr.Errors {
			if code.Code == transport.ManifestUnknownErrorCode || code.Code == transport.NameUnknownErrorCode {
				return true
			}
		}
		if transportErr.StatusCode == http.StatusNotFound {
			return true
		}
	}
	return false
}

// FetchRemoteDockerImageMetadata retrieves the labels of an image in a remote Docker registry.
//
// creds is nil for a registry the caller holds no credential for, which is read
// anonymously. EnvironmentImageRepository.FetchImageMetadata answers which it is.
func FetchRemoteDockerImageMetadata(creds *DockerCredentials, imageRef string) (*MetaplayImageInfo, error) {
	log.Debug().Msgf("Fetch image metadata for a remote container image: %s", imageRef)
	if imageRef == "" {
		return nil, fmt.Errorf("empty image reference")
	}

	// Create a registry authenticator using the provided credentials, if any.
	authenticator := authn.Anonymous
	var parseOptions []name.Option
	if creds != nil {
		authenticator = authn.FromConfig(authn.AuthConfig{
			Username: creds.Username,
			Password: creds.Password,
		})
		parseOptions = append(parseOptions, name.WithDefaultRegistry(creds.RegistryURL))
	}

	// Parse the image reference (name + tag or digest)
	ref, err := name.ParseReference(imageRef, parseOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to parse remote docker image reference '%s': %w", imageRef, err)
	}

	// Retrieve the image manifest and associated metadata
	desc, err := remote.Get(ref, remote.WithAuth(authenticator))
	if err != nil {
		return nil, fmt.Errorf("failed to get remote docker image descriptor '%s': %w", imageRef, err)
	}

	// Fetch the image configuration blob
	img, err := desc.Image()
	if err != nil {
		return nil, fmt.Errorf("failed to get remote docker image from descriptor '%s': %w", imageRef, err)
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("failed to get remote docker image config file '%s': %w", imageRef, err)
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
func ReadLocalDockerImagesByProjectID(ctx context.Context, projectID string) ([]MetaplayImageInfo, error) {
	log.Debug().Msgf("Reading local docker images for project ID: %s", projectID)

	dockerClient, err := NewDockerClient(ctx, LogWaiting)
	if err != nil {
		return nil, err // Pass up the detailed error from NewDockerClient
	}
	defer func() { _ = dockerClient.Close() }()

	// List all images from the local Docker daemon with the project ID label.
	images, err := dockerClient.ImageList(ctx, client.ImageListOptions{
		Filters: make(client.Filters).Add("label", fmt.Sprintf("%s=%s", labelProjectID, projectID)),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list local docker images: %w", err)
	}

	// Parse the image information for all local images matching this project.
	var matchingImages []MetaplayImageInfo
	for _, img := range images.Items {
		var imageInspect *client.ImageInspectResult // Inspected on the first usable tag, shared by all tags
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
			if imageInspect == nil {
				result, err := dockerClient.ImageInspect(ctx, img.ID)
				if err != nil {
					// Stop on cancellation, instead of returning a partial list.
					if ctx.Err() != nil {
						return nil, fmt.Errorf("failed to inspect local docker image %s: %w", img.ID, err)
					}
					log.Warn().Err(err).Msgf("Failed to inspect image %s (for repoTag %s), skipping", img.ID, repoTag)
					break
				}
				imageInspect = &result
			}

			imageInfo, err := newMetaplayImageInfoFromInspect(img.ID, repoTag, imageRef, imageInspect.InspectResponse)
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
