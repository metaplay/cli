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
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/rs/zerolog/log"

	"github.com/metaplay/cli/internal/syncutil"
)

// DefaultListingConcurrency is how many registry requests a listing has in
// flight unless told otherwise.
const DefaultListingConcurrency = 64

// ListingOptions tunes how a repository is listed.
type ListingOptions struct {
	// Concurrency is how many registry requests the listing has in flight at
	// once. Zero means DefaultListingConcurrency.
	Concurrency int
	// Progress, where given, is told how far the listing has got.
	Progress ListingProgress
}

// ListingProgress is told how far a listing has got.
type ListingProgress interface {
	// Update reports the phase the listing is in, how much of that phase is
	// done, and the phase's total, or zero where the total is not known until
	// the phase ends. Reports arrive one at a time, phase by phase, each
	// counting up.
	Update(phase string, done, total int)
	// Finish ends the phase in progress, as failed where err is not nil.
	Finish(err error)
}

// The phases of a listing, in the order they run.
const (
	PhaseListingTags   = "Listing tags"
	PhaseResolvingTags = "Resolving tags"
	PhaseReadingImages = "Reading images"
)

// phaseProgress counts one phase of a listing up as its parts finish, which
// they do in parallel, and reports each count in turn.
type phaseProgress struct {
	mu     sync.Mutex
	report ListingProgress
	phase  string
	done   int
	total  int
}

// startPhase reports that phase has begun, with nothing done of total.
func (o ListingOptions) startPhase(phase string, total int) *phaseProgress {
	p := &phaseProgress{report: o.Progress, phase: phase, total: total}
	p.add(0)
	return p
}

// add counts n more parts of the phase done.
func (p *phaseProgress) add(n int) {
	if p.report == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done += n
	p.report.Update(p.phase, p.done, p.total)
}

// concurrency is how many requests to have in flight.
func (o ListingOptions) concurrency() int {
	if o.Concurrency > 0 {
		return o.Concurrency
	}
	return DefaultListingConcurrency
}

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
func ListRepositoryImages(ctx context.Context, repository *EnvironmentImageRepository, options ListingOptions) ([]RepositoryImage, error) {
	concurrency := options.concurrency()
	repo, err := name.NewRepository(repository.QualifiedRepository)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image repository '%s': %w", repository.QualifiedRepository, err)
	}
	// The puller limits its own blob reads, which are how configs are read,
	// to a handful at a time unless told otherwise.
	puller, err := remote.NewPuller(
		remote.WithAuth(registryAuthenticator(repository.Credentials)),
		remote.WithContext(ctx),
		remote.WithJobs(concurrency),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create a registry client: %w", err)
	}

	tags, err := listTags(ctx, puller, repo, options.startPhase(PhaseListingTags, 0))
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

	resolving := options.startPhase(PhaseResolvingTags, len(tags))
	tagged := syncutil.ParallelMap(tags, concurrency, func(tag string) taggedDigest {
		descriptor, err := puller.Head(ctx, repo.Tag(tag))
		resolving.add(1)
		if err != nil {
			return taggedDigest{tag: tag, err: err}
		}
		return taggedDigest{tag: tag, digest: descriptor.Digest.String()}
	})

	var digests []string
	seen := map[string]bool{}
	for _, t := range tagged {
		if t.err == nil && !seen[t.digest] {
			seen[t.digest] = true
			digests = append(digests, t.digest)
		}
	}
	reading := options.startPhase(PhaseReadingImages, len(digests))
	reads := syncutil.ParallelMap(digests, concurrency, func(digest string) imageRead {
		described, err := readImage(ctx, puller, repo.Digest(digest))
		reading.add(1)
		return imageRead{described: described, err: err}
	})
	readsByDigest := map[string]imageRead{}
	for i, digest := range digests {
		readsByDigest[digest] = reads[i]
	}
	return assembleImages(tagged, readsByDigest), nil
}

// listTags lists every tag in the repository a page at a time, counting the
// tags listed so far, since a registry pages a long list and each page can
// take seconds.
func listTags(ctx context.Context, puller *remote.Puller, repo name.Repository, listing *phaseProgress) ([]string, error) {
	lister, err := puller.Lister(ctx, repo)
	if err != nil {
		return nil, err
	}
	var tags []string
	for lister.HasNext() {
		page, err := lister.Next(ctx)
		if err != nil {
			return nil, err
		}
		tags = append(tags, page.Tags...)
		listing.add(len(page.Tags))
	}
	return tags, nil
}

// registryAuthenticator presents credentials where there are any, and reads
// anonymously where there are none.
func registryAuthenticator(credentials *DockerCredentials) authn.Authenticator {
	if credentials == nil || (credentials.Username == "" && credentials.Password == "") {
		return authn.Anonymous
	}
	return authn.FromConfig(authn.AuthConfig{Username: credentials.Username, Password: credentials.Password})
}

// readImage reads what one image says about itself, from its manifest and
// config: when it was built, its size, and its labels. Its tags and digest are
// the listing's to fill in. A multi-platform image is sized over every
// platform and described by the one imagePlatforms picks.
func readImage(ctx context.Context, puller *remote.Puller, ref name.Digest) (RepositoryImage, error) {
	descriptor, err := puller.Get(ctx, ref)
	if err != nil {
		return RepositoryImage{}, fmt.Errorf("failed to fetch the image's manifest: %w", err)
	}

	var manifests []*v1.Manifest
	var describing v1.Image
	if descriptor.MediaType.IsIndex() {
		index, err := descriptor.ImageIndex()
		if err != nil {
			return RepositoryImage{}, fmt.Errorf("failed to open the image's index: %w", err)
		}
		indexManifest, err := index.IndexManifest()
		if err != nil {
			return RepositoryImage{}, fmt.Errorf("failed to parse the image's index: %w", err)
		}
		platforms, describedBy := imagePlatforms(indexManifest)
		if len(platforms) == 0 {
			return RepositoryImage{}, errors.New("the index names no platform image")
		}
		for _, platform := range platforms {
			image, err := index.Image(platform.Digest)
			if err != nil {
				return RepositoryImage{}, fmt.Errorf("failed to fetch the image for %s: %w", platformName(platform), err)
			}
			manifest, err := image.Manifest()
			if err != nil {
				return RepositoryImage{}, fmt.Errorf("failed to parse the manifest for %s: %w", platformName(platform), err)
			}
			manifests = append(manifests, manifest)
			if platform.Digest == describedBy.Digest {
				describing = image
			}
		}
	} else {
		describing, err = descriptor.Image()
		if err != nil {
			return RepositoryImage{}, fmt.Errorf("failed to open the image: %w", err)
		}
		manifest, err := describing.Manifest()
		if err != nil {
			return RepositoryImage{}, fmt.Errorf("failed to parse the image's manifest: %w", err)
		}
		manifests = append(manifests, manifest)
	}

	config, err := describing.ConfigFile()
	if err != nil {
		return RepositoryImage{}, fmt.Errorf("failed to read the image's config: %w", err)
	}
	return RepositoryImage{
		BuiltAt:    config.Created.Time,
		SizeBytes:  compressedSize(manifests...),
		SdkVersion: config.Config.Labels[labelSdkVersion],
		CommitID:   config.Config.Labels[labelCommitID],
	}, nil
}

// taggedDigest is what one tag resolved to: the digest of the image it names,
// or why that could not be read.
type taggedDigest struct {
	tag    string
	digest string
	err    error
}

// imageRead is what reading one image gave: what the image says about itself,
// or why it could not be read.
type imageRead struct {
	described RepositoryImage
	err       error
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
	positionByDigest := map[string]int{}
	for _, tagged := range tags {
		if tagged.err != nil {
			images = append(images, RepositoryImage{Tags: []string{tagged.tag}, Error: tagged.err.Error()})
			continue
		}
		if position, seen := positionByDigest[tagged.digest]; seen {
			images[position].Tags = append(images[position].Tags, tagged.tag)
			continue
		}

		read := reads[tagged.digest]
		image := read.described
		image.Tags = []string{tagged.tag}
		image.Digest = tagged.digest
		if read.err != nil {
			image.Error = read.err.Error()
		}
		positionByDigest[tagged.digest] = len(images)
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
// command reads, or the first listed where there is none. describedBy is the
// zero descriptor when the index names no platform image at all.
//
// An attestation buildx adds to an index names the platform unknown/unknown,
// and is not a platform.
func imagePlatforms(index *v1.IndexManifest) (platforms []v1.Descriptor, describedBy v1.Descriptor) {
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
		describedBy = platforms[0]
	}
	return platforms, describedBy
}
