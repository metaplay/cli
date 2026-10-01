/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/rs/zerolog/log"

	"github.com/metaplay/cli/internal/syncutil"
)

// listingConcurrency is how many registry requests a listing has in flight.
const listingConcurrency = 10

// RepositoryImage is one image in an environment's repository: every tag that
// names the same bytes, and what the image says about itself.
//
// There is no push time. The registry protocol does not report when an image
// was pushed -- it is in no manifest, descriptor or header -- so the time an
// image has is when it was built, read from its config.
type RepositoryImage struct {
	Tags   []string `json:"tags"`
	Digest string   `json:"digest,omitempty"`
	// BuiltAt is when the image was built, from its config. Zero where the
	// image records none or could not be read.
	BuiltAt time.Time `json:"builtAt,omitzero"`
	// SizeBytes is the compressed size of the image's config and layers, as
	// the registry stores them, over every platform a multi-platform image
	// carries, counting a layer the platforms share once.
	SizeBytes  int64  `json:"sizeBytes,omitempty"`
	SdkVersion string `json:"sdkVersion,omitempty"`
	CommitID   string `json:"commitId,omitempty"`
	// Error says why the image, or the tag, could not be read. Empty for an
	// image that was.
	Error string `json:"error,omitempty"`
}

// Readable reports whether the image could be read, and so whether anything
// beyond its tags is known about it.
func (i RepositoryImage) Readable() bool {
	return i.Error == ""
}

// ListRepositoryImages lists every image in the repository, newest built first,
// through the registry protocol every registry speaks.
//
// Build time is only in an image's config, so every tag is read before
// anything is ordered: ordering by tag name instead would be right only for
// tags that happen to be timestamps. The cost is kept down by resolving each
// tag to its digest with a HEAD and reading each image once however many tags
// name it, over pooled connections sharing one token.
//
// A tag or an image that cannot be read is still listed, saying why. Only a
// repository whose tags cannot be listed at all is an error.
func ListRepositoryImages(ctx context.Context, repository *EnvironmentImageRepository) ([]RepositoryImage, error) {
	repo, err := name.NewRepository(repository.QualifiedRepository)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image repository '%s': %w", repository.QualifiedRepository, err)
	}
	puller, err := remote.NewPuller(remote.WithAuth(registryAuthenticator(repository.Credentials)), remote.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to create a registry client: %w", err)
	}

	tags, err := puller.List(ctx, repo)
	if isRemoteImageNotFound(err) {
		// A registry creates a repository on the first push to it, so one
		// nothing has been pushed to does not exist yet: that is empty, as it
		// is on a registry creating repositories up front. A registry that
		// creates them up front answers the same for a repository that is not
		// there at all, which listing cannot tell apart, so say what it saw.
		log.Debug().Msgf("The registry reports no repository '%s'; listing it as empty: %v", repository.QualifiedRepository, err)
		return []RepositoryImage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list the tags in '%s': %w", repository.QualifiedRepository, err)
	}

	tagged := syncutil.ParallelMap(tags, listingConcurrency, func(tag string) taggedDigest {
		descriptor, err := puller.Head(ctx, repo.Tag(tag))
		if err != nil {
			return taggedDigest{tag: tag, err: err}
		}
		return taggedDigest{tag: tag, digest: descriptor.Digest.String()}
	})

	var digests []string
	for _, t := range tagged {
		if t.err == nil && !slices.Contains(digests, t.digest) {
			digests = append(digests, t.digest)
		}
	}
	reads := syncutil.ParallelMap(digests, listingConcurrency, func(digest string) imageRead {
		facts, err := readImageFacts(ctx, puller, repo.Digest(digest))
		return imageRead{facts: facts, err: err}
	})
	byDigest := map[string]imageRead{}
	for i, digest := range digests {
		byDigest[digest] = reads[i]
	}
	return assembleImages(tagged, byDigest), nil
}

// registryAuthenticator presents credentials where there are any, and reads
// anonymously where there are none.
func registryAuthenticator(credentials *DockerCredentials) authn.Authenticator {
	if credentials == nil || (credentials.Username == "" && credentials.Password == "") {
		return authn.Anonymous
	}
	return authn.FromConfig(authn.AuthConfig{Username: credentials.Username, Password: credentials.Password})
}

// readImageFacts reads one image's manifest and config. A multi-platform image
// is sized over every platform and described by the one imagePlatforms picks.
func readImageFacts(ctx context.Context, puller *remote.Puller, ref name.Digest) (imageFacts, error) {
	descriptor, err := puller.Get(ctx, ref)
	if err != nil {
		return imageFacts{}, fmt.Errorf("failed to read the image's manifest: %w", err)
	}

	var manifests []*v1.Manifest
	var describing v1.Image
	if descriptor.MediaType.IsIndex() {
		index, err := descriptor.ImageIndex()
		if err != nil {
			return imageFacts{}, fmt.Errorf("failed to read the image's index: %w", err)
		}
		indexManifest, err := index.IndexManifest()
		if err != nil {
			return imageFacts{}, fmt.Errorf("failed to read the image's index: %w", err)
		}
		platforms, describedBy := imagePlatforms(indexManifest)
		if len(platforms) == 0 {
			return imageFacts{}, errors.New("the index names no platform image")
		}
		for _, platform := range platforms {
			image, err := index.Image(platform.Digest)
			if err != nil {
				return imageFacts{}, fmt.Errorf("failed to read the image for %s: %w", platformName(platform), err)
			}
			manifest, err := image.Manifest()
			if err != nil {
				return imageFacts{}, fmt.Errorf("failed to read the manifest for %s: %w", platformName(platform), err)
			}
			manifests = append(manifests, manifest)
			if platform.Digest == describedBy.Digest {
				describing = image
			}
		}
	} else {
		describing, err = descriptor.Image()
		if err != nil {
			return imageFacts{}, fmt.Errorf("failed to read the image: %w", err)
		}
		manifest, err := describing.Manifest()
		if err != nil {
			return imageFacts{}, fmt.Errorf("failed to read the image's manifest: %w", err)
		}
		manifests = append(manifests, manifest)
	}

	config, err := describing.ConfigFile()
	if err != nil {
		return imageFacts{}, fmt.Errorf("failed to read the image's config: %w", err)
	}
	return imageFacts{
		builtAt:    config.Created.Time,
		sizeBytes:  compressedSize(manifests...),
		sdkVersion: config.Config.Labels["io.metaplay.sdk_version"],
		commitID:   config.Config.Labels["io.metaplay.commit_id"],
	}, nil
}

// taggedDigest is what one tag resolved to: the digest of the image it names,
// or why that could not be read.
type taggedDigest struct {
	tag    string
	digest string
	err    error
}

// imageRead is what reading one image gave: its facts, or why it could not be
// read.
type imageRead struct {
	facts imageFacts
	err   error
}

// imageFacts is what reading one image's manifest and config says about it.
type imageFacts struct {
	builtAt    time.Time
	sizeBytes  int64
	sdkVersion string
	commitID   string
}

// assembleImages turns what each tag resolved to, and what reading each image
// said, into the images of a repository: tags naming the same digest grouped
// into one, newest built first.
//
// Nothing is dropped. After the images with a build time come those that
// record none, then images that could not be read, keeping their tags
// together, then tags that could not be resolved to an image at all, each on
// its own. Everything that could not be read says why.
func assembleImages(tags []taggedDigest, reads map[string]imageRead) []RepositoryImage {
	images := []RepositoryImage{}
	byDigest := map[string]int{}
	for _, tagged := range tags {
		if tagged.err != nil {
			images = append(images, RepositoryImage{Tags: []string{tagged.tag}, Error: tagged.err.Error()})
			continue
		}
		if index, seen := byDigest[tagged.digest]; seen {
			images[index].Tags = append(images[index].Tags, tagged.tag)
			continue
		}

		image := RepositoryImage{Tags: []string{tagged.tag}, Digest: tagged.digest}
		if read := reads[tagged.digest]; read.err != nil {
			image.Error = read.err.Error()
		} else {
			image.BuiltAt = read.facts.builtAt
			image.SizeBytes = read.facts.sizeBytes
			image.SdkVersion = read.facts.sdkVersion
			image.CommitID = read.facts.commitID
		}
		byDigest[tagged.digest] = len(images)
		images = append(images, image)
	}

	for i := range images {
		slices.Sort(images[i].Tags)
	}
	slices.SortStableFunc(images, func(a, b RepositoryImage) int {
		if byRank := cmp.Compare(listingRank(a), listingRank(b)); byRank != 0 {
			return byRank
		}
		if byBuilt := b.BuiltAt.Compare(a.BuiltAt); byBuilt != 0 {
			return byBuilt
		}
		return cmp.Compare(a.Tags[0], b.Tags[0])
	})
	return images
}

// The parts of a listing, in the order they are listed.
const (
	rankBuilt           = iota // images with a build time
	rankUndated                // images recording none
	rankUnreadableImage        // images that could not be read
	rankUnresolvedTag          // tags that could not be resolved to an image at all
)

// listingRank is which part of a listing an image belongs in.
func listingRank(image RepositoryImage) int {
	switch {
	case !image.Readable() && image.Digest == "":
		return rankUnresolvedTag
	case !image.Readable():
		return rankUnreadableImage
	case image.BuiltAt.IsZero():
		return rankUndated
	default:
		return rankBuilt
	}
}

// platformName is how a platform image is named in an error.
func platformName(platform v1.Descriptor) string {
	if platform.Platform == nil {
		return platform.Digest.String()
	}
	return platform.Platform.String()
}

// compressedSize is the size a registry stores for images with these
// manifests: every config and layer as pushed, which for a gzipped layer is its
// compressed size. A client-side sum, since no manifest or index carries a
// total. For a multi-platform image it covers every platform the repository
// holds, and a blob two platforms share is stored once, so it counts once.
func compressedSize(manifests ...*v1.Manifest) int64 {
	var size int64
	counted := map[v1.Hash]bool{}
	count := func(blob v1.Descriptor) {
		if !counted[blob.Digest] {
			counted[blob.Digest] = true
			size += blob.Size
		}
	}
	for _, manifest := range manifests {
		count(manifest.Config)
		for _, layer := range manifest.Layers {
			count(layer)
		}
	}
	return size
}

// imagePlatforms is the platform images a multi-platform image's index names,
// and the one that describes the image: linux/amd64, which is what every other
// command reads, or the first listed where there is none. describing is the
// zero descriptor when the index names no platform image at all.
//
// An attestation buildx adds to an index names the platform unknown/unknown,
// and is not a platform.
func imagePlatforms(index *v1.IndexManifest) (platforms []v1.Descriptor, describing v1.Descriptor) {
	for _, manifest := range index.Manifests {
		if manifest.Platform != nil && manifest.Platform.OS == "unknown" {
			continue
		}
		platforms = append(platforms, manifest)
	}
	for _, platform := range platforms {
		if platform.Platform != nil && platform.Platform.OS == "linux" && platform.Platform.Architecture == "amd64" {
			return platforms, platform
		}
	}
	if len(platforms) > 0 {
		describing = platforms[0]
	}
	return platforms, describing
}
