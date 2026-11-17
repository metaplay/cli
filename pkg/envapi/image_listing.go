/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/types"
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
	PhaseListingTags    = "Listing tags"
	PhaseResolvingTags  = "Resolving tags"
	PhaseRetryingTags   = "Retrying failed tags"
	PhaseReadingImages  = "Reading images"
	PhaseRetryingImages = "Retrying failed images"
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

// addTotal counts n more parts to the phase, found as it runs.
func (p *phaseProgress) addTotal(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.total += n
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

// finishPhase ends the phase in progress as failed, for a listing that
// carries on another way.
func (o ListingOptions) finishPhase(err error) {
	if o.Progress != nil {
		o.Progress.Finish(err)
	}
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
// Where the repository is in ECR and can be asked about through ECR's control
// plane, its tags and manifests come from there instead, which takes a handful
// of calls rather than a request per tag and per image. What is listed is the
// same either way; only configs are always read through the registry.
//
// A tag or an image that cannot be read is still listed, saying why, after
// one more try where another might succeed. Only a repository whose tags
// cannot be listed at all, or a listing that was cancelled, is an error.
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
	registry := &registryImageSource{puller: puller, repo: repo}

	if repository.ecr != nil {
		images, err := listECRImages(ctx, repository.ecr, registry, options)
		if err == nil {
			return images, nil
		}
		if ctx.Err() != nil {
			// Cancelled rather than refused, so the registry protocol would
			// fail the same way.
			return nil, ctx.Err()
		}
		// A credential that reaches the registry need not be allowed ECR's
		// control plane, and the registry protocol lists the same images.
		log.Debug().Msgf("Could not list '%s' through ECR; listing it through the registry protocol: %v", repository.QualifiedRepository, err)
		options.finishPhase(err)
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

	resolve := func(tag string) taggedDigest {
		descriptor, err := puller.Head(ctx, repo.Tag(tag))
		if err != nil {
			return taggedDigest{tag: tag, err: err}
		}
		return taggedDigest{tag: tag, digest: descriptor.Digest.String()}
	}
	resolving := options.startPhase(PhaseResolvingTags, len(tags))
	tagged := syncutil.ParallelMap(tags, concurrency, func(tag string) taggedDigest {
		resolved := resolve(tag)
		resolving.add(1)
		return resolved
	})
	if err := retryFailed(ctx, options, PhaseRetryingTags, tags, tagged, taggedDigest.failure, resolve); err != nil {
		return nil, err
	}

	return readImages(ctx, tagged, registry, options)
}

// readImages reads each image the tags name once, however many tags name it,
// and assembles the listing. It fails only where the listing was cancelled.
func readImages(ctx context.Context, tagged []taggedDigest, source imageSource, options ListingOptions) ([]RepositoryImage, error) {
	digests := uniqueDigests(tagged)
	read := func(digest string) imageRead {
		described, err := readImage(ctx, source, digest)
		return imageRead{described: described, err: err}
	}
	reading := options.startPhase(PhaseReadingImages, len(digests))
	reads := syncutil.ParallelMap(digests, options.concurrency(), func(digest string) imageRead {
		described := read(digest)
		reading.add(1)
		return described
	})
	if err := retryFailed(ctx, options, PhaseRetryingImages, digests, reads, imageRead.failure, read); err != nil {
		return nil, err
	}
	readsByDigest := map[string]imageRead{}
	for i, digest := range digests {
		readsByDigest[digest] = reads[i]
	}
	return assembleImages(tagged, readsByDigest), nil
}

// listingRetryConcurrency is how many failed reads a listing tries again at
// once.
const listingRetryConcurrency = 4

// retryFailed tries each item whose result failed once more, where another
// try might succeed, and keeps what the second try gave. Under the load of a
// listing a registry can fail a read past its client's own retries, by
// throttling it, timing it out or dropping its connection, and then serve it
// once the load has passed. So the second tries come after the rest, a few at
// a time. A second try failing the same way says the registry has not
// recovered, and the rest are left as they failed rather than waited on.
//
// It fails only where the listing was cancelled: every read failed for that
// reason, and there is no listing to show.
func retryFailed[In, Out any](ctx context.Context, options ListingOptions, phase string, items []In, results []Out, failure func(Out) error, try func(In) Out) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var failed []int
	for i, result := range results {
		if err := failure(result); err != nil && worthRetrying(err) {
			failed = append(failed, i)
		}
	}
	if len(failed) == 0 {
		return nil
	}

	log.Debug().Msgf("%s: trying %d failed reads again", phase, len(failed))
	retrying := options.startPhase(phase, len(failed))
	var givenUp atomic.Bool
	retried := syncutil.ParallelMap(failed, min(listingRetryConcurrency, options.concurrency()), func(i int) Out {
		defer retrying.add(1)
		if givenUp.Load() {
			return results[i]
		}
		result := try(items[i])
		if err := failure(result); err != nil && worthRetrying(err) {
			givenUp.Store(true)
		}
		return result
	})
	for j, i := range failed {
		results[i] = retried[j]
	}
	return ctx.Err()
}

// worthRetrying reports whether a read that failed might succeed if tried
// again: the registry throttled it, timed it out or failed itself, or the
// connection broke. A read the registry refused, or one whose answer could not
// be used, fails the same way every time.
func worthRetrying(err error) bool {
	if registryErr, ok := errors.AsType[*transport.Error](err); ok {
		code := registryErr.StatusCode
		return code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= http.StatusInternalServerError
	}
	if _, ok := errors.AsType[net.Error](err); ok {
		return true
	}
	return errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET)
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

// uniqueDigests is each image the tags resolved to, once, in the order the
// tags first name them.
func uniqueDigests(tagged []taggedDigest) []string {
	var digests []string
	seen := map[string]bool{}
	for _, t := range tagged {
		if t.err == nil && !seen[t.digest] {
			seen[t.digest] = true
			digests = append(digests, t.digest)
		}
	}
	return digests
}

// imageSource is where describing an image reads its parts from: manifests,
// by digest, and configs.
type imageSource interface {
	manifest(ctx context.Context, digest string) ([]byte, types.MediaType, error)
	config(ctx context.Context, descriptor v1.Descriptor) (*v1.ConfigFile, error)
}

// registryImageSource reads an image's parts through the registry protocol.
type registryImageSource struct {
	puller *remote.Puller
	repo   name.Repository
}

// manifest fetches the manifest with this digest, checked against it.
func (s *registryImageSource) manifest(ctx context.Context, digest string) ([]byte, types.MediaType, error) {
	descriptor, err := s.puller.Get(ctx, s.repo.Digest(digest))
	if err != nil {
		return nil, "", err
	}
	return descriptor.Manifest, descriptor.MediaType, nil
}

// config fetches and parses the config blob a manifest names, checked against
// the digest and size the manifest gives it.
func (s *registryImageSource) config(ctx context.Context, descriptor v1.Descriptor) (*v1.ConfigFile, error) {
	layer, err := s.puller.Layer(ctx, s.repo.Digest(descriptor.Digest.String()))
	if err != nil {
		return nil, err
	}
	blob, err := layer.Compressed()
	if err != nil {
		return nil, err
	}
	defer func() { _ = blob.Close() }()
	// The blob is checked against its digest only once it is read to its end,
	// and parsing stops at the config's closing brace, so it is read whole
	// first, and no further than the size it should be.
	raw, err := io.ReadAll(io.LimitReader(blob, descriptor.Size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != descriptor.Size {
		return nil, fmt.Errorf("the registry served a config of another size than the %d bytes its manifest gives", descriptor.Size)
	}
	return v1.ParseConfigFile(bytes.NewReader(raw))
}

// readImage reads what one image says about itself, from its manifest and
// config: when it was built, its size, and its labels. Its tags and digest are
// the listing's to fill in. A multi-platform image is sized over every
// platform and described by the one imagePlatforms picks.
func readImage(ctx context.Context, source imageSource, digest string) (RepositoryImage, error) {
	raw, mediaType, err := source.manifest(ctx, digest)
	if err != nil {
		return RepositoryImage{}, fmt.Errorf("failed to fetch the image's manifest: %w", err)
	}

	var manifests []*v1.Manifest
	var describing *v1.Manifest
	if mediaType.IsIndex() {
		index, err := v1.ParseIndexManifest(bytes.NewReader(raw))
		if err != nil {
			return RepositoryImage{}, fmt.Errorf("failed to parse the image's index: %w", err)
		}
		platforms, describedBy := imagePlatforms(index)
		if len(platforms) == 0 {
			return RepositoryImage{}, errors.New("the index names no platform image")
		}
		for _, platform := range platforms {
			raw, mediaType, err := source.manifest(ctx, platform.Digest.String())
			if err != nil {
				return RepositoryImage{}, fmt.Errorf("failed to fetch the image for %s: %w", platformName(platform), err)
			}
			manifest, err := parseImageManifest(raw, mediaType)
			if err != nil {
				return RepositoryImage{}, fmt.Errorf("failed to parse the manifest for %s: %w", platformName(platform), err)
			}
			manifests = append(manifests, manifest)
			if platform.Digest == describedBy.Digest {
				describing = manifest
			}
		}
	} else {
		manifest, err := parseImageManifest(raw, mediaType)
		if err != nil {
			return RepositoryImage{}, fmt.Errorf("failed to parse the image's manifest: %w", err)
		}
		manifests = append(manifests, manifest)
		describing = manifest
	}

	config, err := source.config(ctx, describing.Config)
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

// parseImageManifest parses a single-platform image's manifest. Any JSON
// parses as a manifest, so one of another kind -- a schema 1 manifest, which
// predates configs, or a type the listing does not know -- parses as a
// manifest naming no config. That is reported by its media type, since the
// config it lacks is not what is wrong with it.
func parseImageManifest(raw []byte, mediaType types.MediaType) (*v1.Manifest, error) {
	manifest, err := v1.ParseManifest(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if manifest.Config.Digest == (v1.Hash{}) {
		return nil, fmt.Errorf("unsupported manifest media type %q, which names no config", mediaType)
	}
	return manifest, nil
}

// taggedDigest is what one tag resolved to: the digest of the image it names,
// or why that could not be read.
type taggedDigest struct {
	tag    string
	digest string
	err    error
}

func (t taggedDigest) failure() error { return t.err }

// imageRead is what reading one image gave: what the image says about itself,
// or why it could not be read.
type imageRead struct {
	described RepositoryImage
	err       error
}

func (r imageRead) failure() error { return r.err }

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
