/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"errors"
	"io"
	stdlog "log"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

func builtOn(day string) time.Time {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		panic(err)
	}
	return t
}

func tagsOf(images []RepositoryImage) [][]string {
	var tags [][]string
	for _, image := range images {
		tags = append(tags, image.Tags)
	}
	return tags
}

// A registry models images, and tags point at them. Tags naming the same bytes
// are one image, listed once with all of its tags, rather than rows that look
// like different images.
func TestAssembleImages_GroupsTagsNamingTheSameImage(t *testing.T) {
	images := assembleImages(
		[]taggedDigest{
			{tag: "20260930-120000", digest: "sha256:aaa"},
			{tag: "release", digest: "sha256:aaa"},
			{tag: "20260101-000000", digest: "sha256:bbb"},
		},
		map[string]imageRead{
			"sha256:aaa": {facts: imageFacts{builtAt: builtOn("2026-09-30"), sizeBytes: 300, sdkVersion: "39.0.0", commitID: "abc"}},
			"sha256:bbb": {facts: imageFacts{builtAt: builtOn("2026-01-01"), sizeBytes: 200}},
		})

	want := []RepositoryImage{
		{Tags: []string{"20260930-120000", "release"}, Digest: "sha256:aaa", BuiltAt: builtOn("2026-09-30"), SizeBytes: 300, SdkVersion: "39.0.0", CommitID: "abc"},
		{Tags: []string{"20260101-000000"}, Digest: "sha256:bbb", BuiltAt: builtOn("2026-01-01"), SizeBytes: 200},
	}
	if !reflect.DeepEqual(images, want) {
		t.Errorf("images = %+v\nwant     %+v", images, want)
	}
}

// Newest built first, whatever the tags are called. Tags this toolchain
// generates are timestamps and happen to sort the same way by name, but a
// hand-named tag does not, and it sorts by when its image was built.
func TestAssembleImages_OrdersNewestBuiltFirstWhateverTheTagIsCalled(t *testing.T) {
	images := assembleImages(
		[]taggedDigest{
			{tag: "20260101-000000", digest: "sha256:old"},
			{tag: "release-candidate", digest: "sha256:middle"},
			{tag: "20260930-120000", digest: "sha256:new"},
		},
		map[string]imageRead{
			"sha256:old":    {facts: imageFacts{builtAt: builtOn("2026-01-01")}},
			"sha256:middle": {facts: imageFacts{builtAt: builtOn("2026-06-01")}},
			"sha256:new":    {facts: imageFacts{builtAt: builtOn("2026-09-30")}},
		})

	want := [][]string{{"20260930-120000"}, {"release-candidate"}, {"20260101-000000"}}
	if got := tagsOf(images); !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// What cannot be read is still listed, after everything that could, saying why:
// a tag whose digest could not be read is a row of its own, and an image whose
// manifest or config could not be read keeps its tags together. An image with
// no build time recorded sorts after those with one.
func TestAssembleImages_ListsWhatCouldNotBeReadLast(t *testing.T) {
	images := assembleImages(
		[]taggedDigest{
			{tag: "unreadable-tag", err: errors.New("manifest unknown")},
			{tag: "broken-a", digest: "sha256:broken"},
			{tag: "broken-b", digest: "sha256:broken"},
			{tag: "undated", digest: "sha256:undated"},
			{tag: "20260930-120000", digest: "sha256:good"},
		},
		map[string]imageRead{
			"sha256:good":    {facts: imageFacts{builtAt: builtOn("2026-09-30")}},
			"sha256:undated": {},
			"sha256:broken":  {err: errors.New("config blob unknown")},
		})

	want := [][]string{{"20260930-120000"}, {"undated"}, {"broken-a", "broken-b"}, {"unreadable-tag"}}
	if got := tagsOf(images); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if images[0].Error != "" || images[1].Error != "" {
		t.Errorf("readable images carry an error: %+v", images[:2])
	}
	if images[2].Digest != "sha256:broken" || images[2].Error != "config blob unknown" {
		t.Errorf("unreadable image = %+v, want its digest and why", images[2])
	}
	if images[3].Digest != "" || images[3].Error != "manifest unknown" {
		t.Errorf("unreadable tag = %+v, want no digest and why", images[3])
	}
}

func descriptorOf(size int64) v1.Descriptor {
	return v1.Descriptor{Size: size}
}

// Size is what the registry stores: each config and layer as pushed, which for
// a gzipped layer is the compressed size. Summed over every platform of a
// multi-platform image, since the repository holds every one of them.
func TestCompressedSize_SumsConfigAndLayersOverEveryPlatform(t *testing.T) {
	amd64 := &v1.Manifest{Config: descriptorOf(100), Layers: []v1.Descriptor{descriptorOf(1000), descriptorOf(2000)}}
	arm64 := &v1.Manifest{Config: descriptorOf(90), Layers: []v1.Descriptor{descriptorOf(900), descriptorOf(1900)}}

	if got := compressedSize(amd64); got != 3100 {
		t.Errorf("single platform = %d, want 3100", got)
	}
	if got := compressedSize(amd64, arm64); got != 5990 {
		t.Errorf("two platforms = %d, want 5990", got)
	}
}

func platformDescriptor(os, architecture string) v1.Descriptor {
	return v1.Descriptor{
		MediaType: types.OCIManifestSchema1,
		Digest:    v1.Hash{Algorithm: "sha256", Hex: os + "-" + architecture},
		Platform:  &v1.Platform{OS: os, Architecture: architecture},
	}
}

// A multi-platform image is described by its linux/amd64 image, which is what
// every other command reads, or by the first platform listed where it has no
// amd64 one. An attestation buildx adds to the index is not a platform.
func TestImagePlatforms_DescribeAnIndexByItsAmd64Image(t *testing.T) {
	attestation := platformDescriptor("unknown", "unknown")

	for name, tc := range map[string]struct {
		manifests  []v1.Descriptor
		platforms  int
		describing string
	}{
		"amd64 listed second": {
			manifests:  []v1.Descriptor{platformDescriptor("linux", "arm64"), platformDescriptor("linux", "amd64"), attestation},
			platforms:  2,
			describing: "linux-amd64",
		},
		"no amd64": {
			manifests:  []v1.Descriptor{attestation, platformDescriptor("linux", "arm64"), platformDescriptor("linux", "arm")},
			platforms:  2,
			describing: "linux-arm64",
		},
	} {
		t.Run(name, func(t *testing.T) {
			platforms, describing := imagePlatforms(&v1.IndexManifest{Manifests: tc.manifests})
			if len(platforms) != tc.platforms {
				t.Errorf("platforms = %d, want %d: %+v", len(platforms), tc.platforms, platforms)
			}
			if describing.Digest.Hex != tc.describing {
				t.Errorf("described by %s, want %s", describing.Digest.Hex, tc.describing)
			}
		})
	}
}

// testRegistry is a registry speaking the distribution protocol, on a local
// server: the same protocol every registry an environment's images live in
// speaks, with no network beyond this process.
func testRegistry(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(registry.New(registry.Logger(stdlog.New(io.Discard, "", 0))))
	t.Cleanup(server.Close)
	return strings.TrimPrefix(server.URL, "http://")
}

// builtImage is an image built at builtAt, carrying the labels a Metaplay
// server image does.
func builtImage(t *testing.T, builtAt time.Time, sdkVersion, commitID string) v1.Image {
	t.Helper()
	image, err := random.Image(1024, 2)
	if err != nil {
		t.Fatal(err)
	}
	config, err := image.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	config = config.DeepCopy()
	config.Created = v1.Time{Time: builtAt}
	config.Config.Labels = map[string]string{
		"io.metaplay.sdk_version": sdkVersion,
		"io.metaplay.commit_id":   commitID,
	}
	image, err = mutate.ConfigFile(image, config)
	if err != nil {
		t.Fatal(err)
	}
	return image
}

func sizeOf(t *testing.T, image v1.Image) int64 {
	t.Helper()
	manifest, err := image.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	return compressedSize(manifest)
}

func digestOf(t *testing.T, digester interface{ Digest() (v1.Hash, error) }) string {
	t.Helper()
	digest, err := digester.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return digest.String()
}

// Everything the listing shows comes from the registry protocol itself: the
// tags, the image each one names, and each image's config. A multi-platform
// image is listed once, sized over every platform and described by its amd64
// image.
func TestListRepositoryImages_ReadsTheRepositoryThroughTheRegistryProtocol(t *testing.T) {
	repository := testRegistry(t) + "/lovely-wombats-build-nimbly/gameserver"
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

	single := builtImage(t, builtOn("2026-09-30"), "39.0.0", "abc123")
	write("20260930-120000", single)
	write("release", single)

	amd64 := builtImage(t, builtOn("2026-06-01"), "38.0.0", "def456")
	arm64 := builtImage(t, builtOn("2026-06-02"), "not-the-describing-platform", "")
	index := mutate.AppendManifests(empty.Index,
		mutate.IndexAddendum{Add: arm64, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "arm64"}}},
		mutate.IndexAddendum{Add: amd64, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "amd64"}}},
	)
	hotfix, err := name.NewTag(repository + ":hotfix")
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.WriteIndex(hotfix, index); err != nil {
		t.Fatal(err)
	}

	images, err := ListRepositoryImages(t.Context(), &EnvironmentImageRepository{
		QualifiedRepository: repository,
		Credentials:         &DockerCredentials{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []RepositoryImage{
		{
			Tags:       []string{"20260930-120000", "release"},
			Digest:     digestOf(t, single),
			BuiltAt:    builtOn("2026-09-30"),
			SizeBytes:  sizeOf(t, single),
			SdkVersion: "39.0.0",
			CommitID:   "abc123",
		},
		{
			Tags:       []string{"hotfix"},
			Digest:     digestOf(t, index),
			BuiltAt:    builtOn("2026-06-01"),
			SizeBytes:  sizeOf(t, amd64) + sizeOf(t, arm64),
			SdkVersion: "38.0.0",
			CommitID:   "def456",
		},
	}
	if len(images) != len(want) {
		t.Fatalf("images = %+v, want %d of them", images, len(want))
	}
	for i := range want {
		images[i].BuiltAt = images[i].BuiltAt.UTC()
		if !reflect.DeepEqual(images[i], want[i]) {
			t.Errorf("image %d = %+v\nwant      %+v", i, images[i], want[i])
		}
	}
}

// A registry creates a repository on the first push to it, so an environment
// nothing has been pushed to yet has no repository at all. That lists as empty,
// as it does on a registry that creates repositories up front, rather than as
// a failure to reach the registry.
func TestListRepositoryImages_ListsARepositoryNothingWasPushedToAsEmpty(t *testing.T) {
	images, err := ListRepositoryImages(t.Context(), &EnvironmentImageRepository{
		QualifiedRepository: testRegistry(t) + "/lovely-wombats-build-nimbly/gameserver",
		Credentials:         &DockerCredentials{},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if images == nil || len(images) != 0 {
		t.Errorf("images = %#v, want an empty list, which is also what --format=json prints as []", images)
	}
}
