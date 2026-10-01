/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/metaplay/cli/pkg/envapi"
)

// The time column says what it measures. A registry cannot say when an image
// was pushed, so what is shown is when it was built, and headed so.
func TestImageListTable_HeadsTheTimeColumnAsBuildTime(t *testing.T) {
	header, _ := imageListTable(nil)

	if !reflect.DeepEqual(header, []string{"TAG", "SDK", "COMMIT", "BUILT", "SIZE"}) {
		t.Errorf("header = %v", header)
	}
}

// One row per image, its tags together, in the order the listing gave. An
// image that could not be read keeps its tags and leaves the rest blank.
func TestImageListTable_ShowsEachImageOnce(t *testing.T) {
	_, rows := imageListTable([]envapi.RepositoryImage{
		{
			Tags:       []string{"20260930-120000", "release"},
			BuiltAt:    time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC),
			SizeBytes:  512 * 1024 * 1024,
			SdkVersion: "39.0.0",
			CommitID:   "abcdef0123456789",
		},
		{Tags: []string{"unreadable"}, Error: "manifest unknown"},
	})

	want := [][]string{
		{"20260930-120000, release", "39.0.0", "abcdef012345", "2026-09-30 12:00", "512.0 MB"},
		{"unreadable", "", "", "", ""},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %q\nwant   %q", rows, want)
	}
}

// What the table does not show is said underneath it: images past the limit,
// and how many could not be read at all, counted over the whole repository
// since those sort last and are the first a limit cuts.
func TestImageListFooters_SayWhatTheTableLeavesOut(t *testing.T) {
	readable := []envapi.RepositoryImage{{Tags: []string{"a"}}, {Tags: []string{"b"}}, {Tags: []string{"c"}}}
	broken := envapi.RepositoryImage{Tags: []string{"broken"}, Error: "manifest unknown"}

	for scenario, tc := range map[string]struct {
		images []envapi.RepositoryImage
		limit  int
		want   []string
	}{
		"past the limit, one unreadable": {
			images: append(slices.Clone(readable), broken),
			limit:  2,
			want: []string{
				"Showing 2 of 4 images. Use --limit to see more.",
				"1 image could not be read. Use --format=json --limit=0 to see why.",
			},
		},
		"two unreadable": {
			images: append(slices.Clone(readable), broken, broken),
			limit:  0,
			want:   []string{"2 images could not be read. Use --format=json --limit=0 to see why."},
		},
		"everything shown and readable": {
			images: readable,
			limit:  0,
			want:   nil,
		},
	} {
		t.Run(scenario, func(t *testing.T) {
			if footers := imageListFooters(tc.images, tc.limit); !reflect.DeepEqual(footers, tc.want) {
				t.Errorf("footers = %q, want %q", footers, tc.want)
			}
		})
	}
}
