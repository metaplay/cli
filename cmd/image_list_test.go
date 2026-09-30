/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"reflect"
	"strings"
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
	images := []envapi.RepositoryImage{
		{Tags: []string{"a"}}, {Tags: []string{"b"}}, {Tags: []string{"c"}},
		{Tags: []string{"broken"}, Error: "manifest unknown"},
	}

	footers := strings.Join(imageListFooters(images, 2), "\n")
	if !strings.Contains(footers, "Showing 2 of 4 images") {
		t.Errorf("footers = %q, want them to say how many are shown", footers)
	}
	if !strings.Contains(footers, "1 image could not be read. Use --format=json --limit=0 to see why.") {
		t.Errorf("footers = %q, want them to count what could not be read", footers)
	}

	if footers := imageListFooters(images[:3], 0); len(footers) != 0 {
		t.Errorf("footers = %q, want none when everything is shown and readable", footers)
	}
}
