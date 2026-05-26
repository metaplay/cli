/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"strings"
	"testing"
)

func TestRenderPnpmWorkspaceContent_WithAllowBuilds(t *testing.T) {
	got, err := renderPnpmWorkspaceContent(
		[]string{"MetaplaySDK/Frontend/*", "Backend/Dashboard"},
		[]string{"@parcel/watcher", "bootstrap-vue", "esbuild"},
	)
	if err != nil {
		t.Fatalf("renderPnpmWorkspaceContent returned error: %v", err)
	}

	out := string(got)

	wantContains := []string{
		"packages:",
		"- MetaplaySDK/Frontend/*",
		"- Backend/Dashboard",
		"allowBuilds:",
		"\"@parcel/watcher\": true",
		"bootstrap-vue: true",
		"esbuild: true",
	}
	for _, w := range wantContains {
		if !strings.Contains(out, w) {
			t.Errorf("expected output to contain %q, got:\n%s", w, out)
		}
	}
}

func TestRenderPnpmWorkspaceContent_WithoutAllowBuilds(t *testing.T) {
	got, err := renderPnpmWorkspaceContent(
		[]string{"MetaplaySDK/Frontend/*", "Backend/Dashboard"},
		nil,
	)
	if err != nil {
		t.Fatalf("renderPnpmWorkspaceContent returned error: %v", err)
	}

	out := string(got)

	if !strings.Contains(out, "packages:") {
		t.Errorf("expected 'packages:' in output, got:\n%s", out)
	}
	if strings.Contains(out, "allowBuilds") {
		t.Errorf("did not expect 'allowBuilds' in output, got:\n%s", out)
	}
}
