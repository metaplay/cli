/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"dev", "dev", 0},
		{"dev", "1.0.0", -1},
		{"1.0.0", "dev", 1},
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.2.0", "1.0.0", 1},
		{"2.0.0", "1.99.99", 1},
		{"v1.0.0", "1.0.0", 0},
		{"1.0.0-rc1", "1.0.0", -1},
	}
	for _, c := range cases {
		got, err := CompareVersions(c.a, c.b)
		if err != nil {
			t.Errorf("CompareVersions(%q, %q): unexpected error %v", c.a, c.b, err)
			continue
		}
		if got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCompareVersions_MalformedReturnsError(t *testing.T) {
	if _, err := CompareVersions("garbage", "1.0.0"); err == nil {
		t.Errorf("expected error for malformed version")
	}
	if _, err := CompareVersions("1.0.0", "also-bad"); err == nil {
		t.Errorf("expected error for malformed version")
	}
}
