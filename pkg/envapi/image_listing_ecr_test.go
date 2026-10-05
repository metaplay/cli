/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// fakeECR answers ECR's control-plane calls the way ECR does, from what is in
// a registry: images with their tags, a page at a time, and manifests by
// digest, at most 100 to a call. An image pushed only by digest is untagged,
// which ECR lists unless told not to.
type fakeECR struct {
	repository string
	untagged   []string
	// pageSize caps a page below what was asked for, as ECR may, so paging is
	// exercised by a repository of a handful of images.
	pageSize int
	// failWith fails every call, as a credential without the permission does.
	failWith error
	// failBatchesWith fails only the calls for manifests.
	failBatchesWith error
	// failBatchesOfAtMost fails the calls for this many manifests or fewer,
	// as calls throttled past their retries do, while the larger calls
	// succeed.
	failBatchesOfAtMost int
	// withhold names images whose manifests ECR reports it cannot return.
	withhold map[string]bool
	// failDescribeAfter fails the call for this page of images and any after
	// it, where it is more than zero, as a call throttled partway does.
	failDescribeAfter int

	mu       sync.Mutex
	describe int
	batches  int
}

func (f *fakeECR) DescribeImages(ctx context.Context, input *ecr.DescribeImagesInput, _ ...func(*ecr.Options)) (*ecr.DescribeImagesOutput, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	f.mu.Lock()
	f.describe++
	page := f.describe
	f.mu.Unlock()
	if f.failDescribeAfter > 0 && page >= f.failDescribeAfter {
		return nil, errors.New("ThrottlingException: rate exceeded")
	}

	repo, err := name.NewRepository(f.repository)
	if err != nil {
		return nil, err
	}
	tags, err := remote.List(repo, remote.WithContext(ctx), asFake)
	if err != nil {
		return nil, err
	}
	tagsByDigest := map[string][]string{}
	var digests []string
	for _, tag := range tags {
		descriptor, err := remote.Head(repo.Tag(tag), remote.WithContext(ctx), asFake)
		if err != nil {
			return nil, err
		}
		digest := descriptor.Digest.String()
		if _, seen := tagsByDigest[digest]; !seen {
			digests = append(digests, digest)
		}
		tagsByDigest[digest] = append(tagsByDigest[digest], tag)
	}
	if input.Filter == nil || input.Filter.TagStatus != ecrtypes.TagStatusTagged {
		digests = append(digests, f.untagged...)
	}
	slices.Sort(digests)

	start := 0
	if input.NextToken != nil {
		start, err = strconv.Atoi(*input.NextToken)
		if err != nil {
			return nil, errors.New("InvalidParameterException: invalid next token")
		}
	}
	size := f.pageSize
	if input.MaxResults != nil && int(*input.MaxResults) < size {
		size = int(*input.MaxResults)
	}
	end := min(start+size, len(digests))

	output := &ecr.DescribeImagesOutput{}
	for _, digest := range digests[start:end] {
		output.ImageDetails = append(output.ImageDetails, ecrtypes.ImageDetail{
			ImageDigest: aws.String(digest),
			ImageTags:   tagsByDigest[digest],
		})
	}
	if end < len(digests) {
		output.NextToken = aws.String(strconv.Itoa(end))
	}
	return output, nil
}

func (f *fakeECR) BatchGetImage(ctx context.Context, input *ecr.BatchGetImageInput, _ ...func(*ecr.Options)) (*ecr.BatchGetImageOutput, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	if f.failBatchesWith != nil {
		return nil, f.failBatchesWith
	}
	if len(input.ImageIds) > 100 {
		return nil, fmt.Errorf("InvalidParameterException: %d image IDs, at most 100", len(input.ImageIds))
	}
	asked := map[string]bool{}
	for _, id := range input.ImageIds {
		if asked[aws.ToString(id.ImageDigest)] {
			return nil, fmt.Errorf("InvalidParameterException: image %s asked for twice", aws.ToString(id.ImageDigest))
		}
		asked[aws.ToString(id.ImageDigest)] = true
	}
	f.mu.Lock()
	f.batches++
	f.mu.Unlock()
	if len(input.ImageIds) <= f.failBatchesOfAtMost {
		return nil, errors.New("ThrottlingException: rate exceeded")
	}

	output := &ecr.BatchGetImageOutput{}
	for _, id := range input.ImageIds {
		if f.withhold[aws.ToString(id.ImageDigest)] {
			output.Failures = append(output.Failures, ecrtypes.ImageFailure{
				ImageId:       &id,
				FailureCode:   ecrtypes.ImageFailureCodeKmsError,
				FailureReason: aws.String("The KMS key could not be used"),
			})
			continue
		}
		ref, err := name.NewDigest(f.repository + "@" + aws.ToString(id.ImageDigest))
		if err != nil {
			return nil, err
		}
		descriptor, err := remote.Get(ref, remote.WithContext(ctx), asFake)
		if err != nil {
			output.Failures = append(output.Failures, ecrtypes.ImageFailure{
				ImageId:       &id,
				FailureCode:   ecrtypes.ImageFailureCodeImageNotFound,
				FailureReason: aws.String("Requested image not found"),
			})
			continue
		}
		if !slices.Contains(input.AcceptedMediaTypes, string(descriptor.MediaType)) {
			return nil, fmt.Errorf("manifest %s is a %s, which the call does not accept", ref, descriptor.MediaType)
		}
		output.Images = append(output.Images, ecrtypes.Image{
			ImageId:                &ecrtypes.ImageIdentifier{ImageDigest: id.ImageDigest},
			ImageManifest:          aws.String(string(descriptor.Manifest)),
			ImageManifestMediaType: aws.String(string(descriptor.MediaType)),
		})
	}
	return output, nil
}

// fakeECRHeader marks the requests the fake ECR makes to read the registry it
// answers for, which are ECR's own doing rather than the listing's.
const fakeECRHeader = "X-Fake-ECR"

// asFake marks a request as the fake ECR's.
var asFake = remote.WithTransport(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(fakeECRHeader, "1")
	return remote.DefaultTransport.RoundTrip(r)
}))

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// requestLog records which requests a registry served the listing, leaving
// out those the fake ECR made.
type requestLog struct {
	mu       sync.Mutex
	requests []string
}

func (l *requestLog) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(fakeECRHeader) == "" {
			l.mu.Lock()
			l.requests = append(l.requests, r.Method+" "+r.URL.Path)
			l.mu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
}

// served is how many of the requests served contain fragment.
func (l *requestLog) served(fragment string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	count := 0
	for _, request := range l.requests {
		if strings.Contains(request, fragment) {
			count++
		}
	}
	return count
}

func (l *requestLog) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requests = nil
}

// ecrTestRepository is a registry holding images of every shape a listing
// meets -- an image with two tags, a multi-platform image, an untagged image,
// and enough more that ECR returns their manifests over more than one call --
// and a fake ECR answering for it.
func ecrTestRepository(t *testing.T) (*EnvironmentImageRepository, *fakeECR, *requestLog) {
	t.Helper()
	requests := &requestLog{}
	repository := testRegistryBehind(t, requests.wrap) + "/lovely-wombats-build-nimbly/gameserver"
	write := func(tag string, image v1.Image) {
		t.Helper()
		ref, err := name.NewTag(repository + ":" + tag)
		if err != nil {
			t.Fatal(err)
		}
		if err := remote.Write(ref, image); err != nil {
			t.Fatal(err)
		}
	}

	release := builtImage(t, builtOn("2026-09-30"), "39.0.0", "abc123")
	write("20260930-120000", release)
	write("release", release)

	// Two multi-platform images sharing their amd64 image, as a rebuild of
	// one platform only makes.
	amd64 := builtImage(t, builtOn("2026-06-01"), "38.0.0", "def456")
	for i, tag := range []string{"hotfix", "hotfix-arm64-rebuilt"} {
		index := mutate.AppendManifests(empty.Index,
			mutate.IndexAddendum{Add: builtImage(t, builtOn("2026-06-02").AddDate(0, 0, i), "38.0.0", ""), Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "arm64"}}},
			mutate.IndexAddendum{Add: amd64, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "amd64"}}},
		)
		ref, err := name.NewTag(repository + ":" + tag)
		if err != nil {
			t.Fatal(err)
		}
		if err := remote.WriteIndex(ref, index); err != nil {
			t.Fatal(err)
		}
	}

	for i := range 101 {
		image, err := random.Image(64, 1)
		if err != nil {
			t.Fatal(err)
		}
		write(fmt.Sprintf("build-%03d", i), image)
	}

	untagged := builtImage(t, builtOn("2026-01-01"), "37.0.0", "")
	untaggedDigest, err := untagged.Digest()
	if err != nil {
		t.Fatal(err)
	}
	untaggedRef, err := name.NewDigest(repository + "@" + untaggedDigest.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(untaggedRef, untagged); err != nil {
		t.Fatal(err)
	}

	fake := &fakeECR{repository: repository, untagged: []string{untaggedDigest.String()}, pageSize: 40}
	return &EnvironmentImageRepository{
		QualifiedRepository: repository,
		Credentials:         &DockerCredentials{},
	}, fake, requests
}

// ECR answers what a repository holds -- its images, their tags and their
// manifests -- in a handful of calls, where the registry protocol takes a
// request per tag and per image. Where the listing can ask ECR, it does, and
// lists exactly what reading the same repository through the registry
// protocol lists: the same images, tags, columns and order.
func TestListRepositoryImages_ListsAnECRRepositoryAsTheRegistryProtocolDoes(t *testing.T) {
	repository, fake, requests := ecrTestRepository(t)

	throughRegistry, err := ListRepositoryImages(t.Context(), repository, ListingOptions{})
	if err != nil {
		t.Fatalf("listing through the registry protocol: %v", err)
	}

	requests.reset()
	repository.ecr = &ecrRepository{client: fake, name: "lovely-wombats-build-nimbly/gameserver"}
	throughECR, err := ListRepositoryImages(t.Context(), repository, ListingOptions{})
	if err != nil {
		t.Fatalf("listing through ECR: %v", err)
	}

	if len(throughECR) != len(throughRegistry) {
		t.Fatalf("listed %d images through ECR, %d through the registry protocol", len(throughECR), len(throughRegistry))
	}
	for i := range throughRegistry {
		throughRegistry[i].BuiltAt = throughRegistry[i].BuiltAt.UTC()
		throughECR[i].BuiltAt = throughECR[i].BuiltAt.UTC()
		if !reflect.DeepEqual(throughECR[i], throughRegistry[i]) {
			t.Errorf("image %d through ECR = %+v\nthrough the registry      %+v", i, throughECR[i], throughRegistry[i])
		}
	}

	// The tags and manifests came from ECR, a page and a batch at a time; only
	// the configs were read from the registry.
	if served := requests.served("/tags/list"); served != 0 {
		t.Errorf("the registry was asked for tags %d times", served)
	}
	if served := requests.served("/manifests/"); served != 0 {
		t.Errorf("the registry was asked for manifests %d times", served)
	}
	if fake.describe < 2 || fake.batches < 2 {
		t.Errorf("ECR was asked for %d pages of images and %d batches of manifests, want several of each", fake.describe, fake.batches)
	}
}

// A credential can reach the registry without being allowed ECR's control
// plane. Then the listing reads the repository through the registry protocol,
// as it would anywhere else, rather than failing.
func TestListRepositoryImages_ListsThroughTheRegistryWhereECRRefuses(t *testing.T) {
	repository, fake, _ := ecrTestRepository(t)

	throughRegistry, err := ListRepositoryImages(t.Context(), repository, ListingOptions{})
	if err != nil {
		t.Fatalf("listing through the registry protocol: %v", err)
	}

	fake.failWith = errors.New("AccessDeniedException: not authorized to perform ecr:DescribeImages")
	repository.ecr = &ecrRepository{client: fake, name: "lovely-wombats-build-nimbly/gameserver"}
	images, err := ListRepositoryImages(t.Context(), repository, ListingOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(tagsOf(images), tagsOf(throughRegistry)) {
		t.Errorf("listed %v, want %v", tagsOf(images), tagsOf(throughRegistry))
	}
}

// A credential can be allowed to list ECR's images but not to fetch their
// manifests from the control plane. The tags still come from ECR, and the
// manifests from the registry, which serves them to the same credential.
func TestListRepositoryImages_ReadsManifestsFromTheRegistryWhereECRWithholdsThem(t *testing.T) {
	repository, fake, requests := ecrTestRepository(t)

	throughRegistry, err := ListRepositoryImages(t.Context(), repository, ListingOptions{})
	if err != nil {
		t.Fatalf("listing through the registry protocol: %v", err)
	}

	requests.reset()
	fake.failBatchesWith = errors.New("AccessDeniedException: not authorized to perform ecr:BatchGetImage")
	repository.ecr = &ecrRepository{client: fake, name: "lovely-wombats-build-nimbly/gameserver"}
	progress := &recordedProgress{}
	images, err := ListRepositoryImages(t.Context(), repository, ListingOptions{Progress: progress})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(images) != len(throughRegistry) {
		t.Fatalf("listed %d images, want %d", len(images), len(throughRegistry))
	}
	for i := range throughRegistry {
		throughRegistry[i].BuiltAt = throughRegistry[i].BuiltAt.UTC()
		images[i].BuiltAt = images[i].BuiltAt.UTC()
		if !reflect.DeepEqual(images[i], throughRegistry[i]) {
			t.Errorf("image %d = %+v\nthrough the registry %+v", i, images[i], throughRegistry[i])
		}
	}
	if served := requests.served("/tags/list"); served != 0 {
		t.Errorf("the registry was asked for tags %d times, though ECR listed them", served)
	}
	if !reflect.DeepEqual(progress.failed, []string{PhaseReadingManifests}) {
		t.Errorf("phases ended as failed = %v, want only %q", progress.failed, PhaseReadingManifests)
	}
}

// ECR can fail partway, as when it throttles. The listing starts over through
// the registry protocol, and the phase it abandoned shows as failed rather
// than done, since what follows it starts from the beginning.
func TestListRepositoryImages_ShowsAnAbandonedECRPhaseAsFailed(t *testing.T) {
	repository, fake, _ := ecrTestRepository(t)
	fake.failDescribeAfter = 2
	repository.ecr = &ecrRepository{client: fake, name: "lovely-wombats-build-nimbly/gameserver"}

	progress := &recordedProgress{}
	images, err := ListRepositoryImages(t.Context(), repository, ListingOptions{Progress: progress})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(images) == 0 {
		t.Error("listed no images")
	}
	if !reflect.DeepEqual(progress.failed, []string{PhaseListingImages}) {
		t.Errorf("phases ended as failed = %v, want only %q", progress.failed, PhaseListingImages)
	}
}

// ECR can withhold some manifests while returning the rest: one image it
// reports it cannot return, or one call failing past its retries. Those few
// are read through the registry, and the rest stay as ECR returned them, so
// the listing is still exactly what the registry protocol lists, and nothing
// shows as failed.
func TestListRepositoryImages_ReadsOnlyTheManifestsECRWithholdsFromTheRegistry(t *testing.T) {
	repository, fake, requests := ecrTestRepository(t)

	throughRegistry, err := ListRepositoryImages(t.Context(), repository, ListingOptions{})
	if err != nil {
		t.Fatalf("listing through the registry protocol: %v", err)
	}

	requests.reset()
	fake.withhold = map[string]bool{throughRegistry[0].Digest: true}
	fake.failBatchesOfAtMost = 10
	repository.ecr = &ecrRepository{client: fake, name: "lovely-wombats-build-nimbly/gameserver"}
	progress := &recordedProgress{}
	images, err := ListRepositoryImages(t.Context(), repository, ListingOptions{Progress: progress})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(images) != len(throughRegistry) {
		t.Fatalf("listed %d images, want %d", len(images), len(throughRegistry))
	}
	for i := range throughRegistry {
		throughRegistry[i].BuiltAt = throughRegistry[i].BuiltAt.UTC()
		images[i].BuiltAt = images[i].BuiltAt.UTC()
		if !reflect.DeepEqual(images[i], throughRegistry[i]) {
			t.Errorf("image %d = %+v\nthrough the registry %+v", i, images[i], throughRegistry[i])
		}
	}
	// The 104 tagged images take a call for 100 manifests and a failed one
	// for 4. Only those 4, the withheld image, and at most the platform
	// images of the two multi-platform ones, read once per image as the
	// registry protocol reads them, come from the registry: not the 108 it
	// would read were the listing to give up on ECR's manifests altogether.
	if served := requests.served("/manifests/"); served == 0 || served > 9 {
		t.Errorf("the registry was asked for %d manifests, want only those ECR withheld", served)
	}
	if len(progress.failed) != 0 {
		t.Errorf("phases ended as failed = %v, want none", progress.failed)
	}
}
