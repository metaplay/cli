/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/rs/zerolog/log"

	"github.com/metaplay/cli/internal/syncutil"
)

// The phases a listing through ECR runs before reading images, in order.
const (
	PhaseListingImages    = "Listing images"
	PhaseReadingManifests = "Reading manifests"
)

// ECR's limits on what one call answers.
const (
	ecrDescribeImagesPageSize = 1000
	ecrBatchGetImageSize      = 100
)

// ecrManifestMediaTypes are the manifests a listing asks ECR for, which are
// those it can read. ECR converts a manifest to one of these where it can and
// it is stored as another.
var ecrManifestMediaTypes = []string{
	string(types.DockerManifestSchema2),
	string(types.DockerManifestList),
	string(types.OCIManifestSchema1),
	string(types.OCIImageIndex),
}

// ecrImageAPI is the part of ECR's control plane a listing asks.
type ecrImageAPI interface {
	DescribeImages(ctx context.Context, input *ecr.DescribeImagesInput, optFns ...func(*ecr.Options)) (*ecr.DescribeImagesOutput, error)
	BatchGetImage(ctx context.Context, input *ecr.BatchGetImageInput, optFns ...func(*ecr.Options)) (*ecr.BatchGetImageOutput, error)
}

// ecrRepository is an ECR repository as its control plane names it, and a
// client allowed to ask about it.
type ecrRepository struct {
	client ecrImageAPI
	name   string
}

// listECRImages lists the repository's images with their tags and manifests
// from ECR, a page and a batch at a time, and reads each image's config
// through the registry, so that what is listed is exactly what the registry
// protocol lists. Where ECR will not list the images, the listing fails, for
// the caller to list through the registry protocol instead. Where it lists
// them but withholds their manifests, those are read through the registry.
func listECRImages(ctx context.Context, repository *ecrRepository, registry *registryImageSource, options ListingOptions) ([]RepositoryImage, error) {
	tagged, err := describeECRImages(ctx, repository, options)
	if err != nil {
		return nil, err
	}

	manifests, err := getECRImageManifests(ctx, repository, uniqueDigests(tagged), options)
	if err != nil {
		if ctx.Err() != nil {
			// Cancelled rather than refused, so the registry would fail the
			// same way.
			return nil, ctx.Err()
		}
		log.Debug().Msgf("Could not get manifests from ECR repository '%s'; reading them through the registry protocol: %v", repository.name, err)
		options.finishPhase(err)
		manifests = map[string]ecrManifest{}
	}
	return readImages(ctx, tagged, &ecrImageSource{registryImageSource: registry, manifests: manifests}, options)
}

// getECRImageManifests fetches the manifests of the images with these
// digests, and of their platforms' images where an image is multi-platform,
// since reading it reads those too. Only where ECR returns none of the
// images' manifests at all is it an error; a manifest it withholds is left
// out, for reading through the registry.
func getECRImageManifests(ctx context.Context, repository *ecrRepository, digests []string, options ListingOptions) (map[string]ecrManifest, error) {
	reading := options.startPhase(PhaseReadingManifests, len(digests))
	manifests, err := getECRManifests(ctx, repository, digests, reading, options)
	if err != nil {
		return nil, err
	}

	// Platform images are untagged, so were not asked for yet. Platforms are
	// shared, as by images rebuilt for one platform only, so each is asked
	// for once.
	var platformDigests []string
	asked := map[string]bool{}
	for _, digest := range digests {
		asked[digest] = true
	}
	for _, digest := range digests {
		image, ok := manifests[digest]
		if !ok || !image.mediaType.IsIndex() {
			continue
		}
		index, err := v1.ParseIndexManifest(bytes.NewReader(image.raw))
		if err != nil {
			continue // Reading the image reports this.
		}
		platforms, _ := imagePlatforms(index)
		for _, platform := range platforms {
			if digest := platform.Digest.String(); !asked[digest] {
				asked[digest] = true
				platformDigests = append(platformDigests, digest)
			}
		}
	}
	reading.addTotal(len(platformDigests))
	platformManifests, err := getECRManifests(ctx, repository, platformDigests, reading, options)
	if err != nil {
		// The images' own manifests came back, so their platforms' are read
		// through the registry rather than giving up on all of them.
		log.Debug().Msgf("Could not get platform manifests from ECR repository '%s'; reading them through the registry protocol: %v", repository.name, err)
		return manifests, nil
	}
	maps.Copy(manifests, platformManifests)
	return manifests, nil
}

// describeECRImages lists every tagged image in the repository, as a tag per
// digest, each image's tags together. Untagged images are not asked for: the
// registry protocol, which lists tags, never sees them either.
func describeECRImages(ctx context.Context, repository *ecrRepository, options ListingOptions) ([]taggedDigest, error) {
	var tagged []taggedDigest
	var listing *phaseProgress
	var nextToken *string
	for {
		output, err := repository.client.DescribeImages(ctx, &ecr.DescribeImagesInput{
			RepositoryName: aws.String(repository.name),
			Filter:         &ecrtypes.DescribeImagesFilter{TagStatus: ecrtypes.TagStatusTagged},
			MaxResults:     aws.Int32(ecrDescribeImagesPageSize),
			NextToken:      nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to describe the images in ECR repository '%s': %w", repository.name, err)
		}
		// Started once ECR has answered, so a listing ECR refuses outright
		// shows no phase for it.
		if listing == nil {
			listing = options.startPhase(PhaseListingImages, 0)
		}
		for _, detail := range output.ImageDetails {
			digest := aws.ToString(detail.ImageDigest)
			for _, tag := range detail.ImageTags {
				tagged = append(tagged, taggedDigest{tag: tag, digest: digest})
			}
		}
		listing.add(len(output.ImageDetails))
		if output.NextToken == nil {
			return tagged, nil
		}
		nextToken = output.NextToken
	}
}

// ecrManifest is a manifest as ECR returned it, checked against its digest.
type ecrManifest struct {
	raw       []byte
	mediaType types.MediaType
}

// getECRManifests fetches the manifests with these digests, as many to a call
// as ECR allows. A manifest ECR withholds, or a call it fails past its
// retries, leaves those manifests out, for reading through the registry. Only
// every call failing is an error, as for a credential without the permission.
func getECRManifests(ctx context.Context, repository *ecrRepository, digests []string, reading *phaseProgress, options ListingOptions) (map[string]ecrManifest, error) {
	batches := slices.Collect(slices.Chunk(digests, ecrBatchGetImageSize))
	type batchResult struct {
		manifests map[string]ecrManifest
		err       error
	}
	results := syncutil.ParallelMap(batches, options.concurrency(), func(batch []string) batchResult {
		manifests, err := batchGetECRManifests(ctx, repository, batch)
		reading.add(len(batch))
		return batchResult{manifests, err}
	})

	manifests := map[string]ecrManifest{}
	var failed []error
	for _, result := range results {
		if result.err != nil {
			log.Debug().Msgf("%v; reading those manifests through the registry protocol", result.err)
			failed = append(failed, result.err)
			continue
		}
		maps.Copy(manifests, result.manifests)
	}
	if len(batches) > 0 && len(failed) == len(batches) {
		return nil, failed[0]
	}
	return manifests, nil
}

// batchGetECRManifests fetches one call's worth of manifests. A manifest is
// checked against the digest it was asked for, as the registry protocol
// checks one it serves. One that does not match, that comes without its media
// type, or that ECR reports it cannot return, is left out.
func batchGetECRManifests(ctx context.Context, repository *ecrRepository, digests []string) (map[string]ecrManifest, error) {
	ids := make([]ecrtypes.ImageIdentifier, len(digests))
	for i, digest := range digests {
		ids[i] = ecrtypes.ImageIdentifier{ImageDigest: aws.String(digest)}
	}
	output, err := repository.client.BatchGetImage(ctx, &ecr.BatchGetImageInput{
		RepositoryName:     aws.String(repository.name),
		ImageIds:           ids,
		AcceptedMediaTypes: ecrManifestMediaTypes,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get manifests from ECR repository '%s': %w", repository.name, err)
	}

	manifests := map[string]ecrManifest{}
	for _, image := range output.Images {
		if image.ImageId == nil {
			continue
		}
		digest := aws.ToString(image.ImageId.ImageDigest)
		raw := []byte(aws.ToString(image.ImageManifest))
		hash, _, err := v1.SHA256(bytes.NewReader(raw))
		if err != nil || hash.String() != digest {
			log.Debug().Msgf("ECR returned a manifest for %s that does not match its digest; reading it through the registry protocol", digest)
			continue
		}
		mediaType := types.MediaType(aws.ToString(image.ImageManifestMediaType))
		if mediaType == "" {
			// Whether it is an index is told by its media type alone, so one
			// ECR does not name is read through the registry, which does.
			log.Debug().Msgf("ECR returned a manifest for %s without its media type; reading it through the registry protocol", digest)
			continue
		}
		manifests[digest] = ecrManifest{raw: raw, mediaType: mediaType}
	}
	for _, failure := range output.Failures {
		if failure.ImageId != nil {
			log.Debug().Msgf("ECR could not return the manifest for %s (%s: %s); reading it through the registry protocol",
				aws.ToString(failure.ImageId.ImageDigest), failure.FailureCode, aws.ToString(failure.FailureReason))
		}
	}
	return manifests, nil
}

// ecrImageSource serves the manifests ECR already returned, and reads the
// rest through the registry.
type ecrImageSource struct {
	*registryImageSource
	manifests map[string]ecrManifest
}

// manifest serves the manifest ECR returned, and reads one it did not
// through the registry.
func (s *ecrImageSource) manifest(ctx context.Context, digest string) ([]byte, types.MediaType, error) {
	if image, ok := s.manifests[digest]; ok {
		return image.raw, image.mediaType, nil
	}
	return s.registryImageSource.manifest(ctx, digest)
}
