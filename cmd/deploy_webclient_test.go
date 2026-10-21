/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"errors"
	"testing"

	"github.com/metaplay/cli/pkg/envapi"
)

// The environment decides where a web client goes, so a project's
// configuration stays the same when the environment's hosting changes, and a
// CLI released before a stack names a web client location uses it once the
// stack does.

func TestResolveWebClientTarget_DefaultsToThePublicCDNRoot(t *testing.T) {
	deployment := &envapi.Deployment{
		S3BucketPublic: "nimbly.p1.metaplay.io-public",
		CdnS3Fqdn:      "nimbly-assets.p1.metaplay.io",
	}

	target, err := resolveWebClientTarget(deployment, "")
	if err != nil {
		t.Fatal(err)
	}
	want := webClientTarget{Bucket: "nimbly.p1.metaplay.io-public", Prefix: "", URL: "https://nimbly-assets.p1.metaplay.io/"}
	if *target != want {
		t.Errorf("target = %+v, want %+v", *target, want)
	}
}

func TestResolveWebClientTarget_UsesTheLocationTheEnvironmentNames(t *testing.T) {
	deployment := &envapi.Deployment{
		S3BucketPublic: "nimbly.p1.metaplay.io-public",
		CdnS3Fqdn:      "nimbly-assets.p1.metaplay.io",
		ClientURL:      "https://play.example.com",
		ClientS3Bucket: "nimbly-client",
		ClientS3Prefix: "Client",
	}

	target, err := resolveWebClientTarget(deployment, "")
	if err != nil {
		t.Fatal(err)
	}
	want := webClientTarget{Bucket: "nimbly-client", Prefix: "Client/", URL: "https://play.example.com/"}
	if *target != want {
		t.Errorf("target = %+v, want %+v", *target, want)
	}
}

// A location that names no bucket of its own is in the public bucket.
func TestResolveWebClientTarget_TheLocationDefaultsToThePublicBucket(t *testing.T) {
	deployment := &envapi.Deployment{
		S3BucketPublic: "nimbly.p1.metaplay.io-public",
		ClientURL:      "https://nimbly-client.p1.metaplay.io/",
		ClientS3Prefix: "Client/",
	}

	target, err := resolveWebClientTarget(deployment, "")
	if err != nil {
		t.Fatal(err)
	}
	if target.Bucket != "nimbly.p1.metaplay.io-public" || target.Prefix != "Client/" {
		t.Errorf("target = %+v, want Client/ in the public bucket", *target)
	}
}

// A web client's own prefix is a path under the location, whichever it is.
func TestResolveWebClientTarget_TheProjectsPrefixIsUnderTheLocation(t *testing.T) {
	onRoot := &envapi.Deployment{S3BucketPublic: "public", CdnS3Fqdn: "nimbly-assets.p1.metaplay.io"}
	target, err := resolveWebClientTarget(onRoot, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if target.Prefix != "staging/" || target.URL != "https://nimbly-assets.p1.metaplay.io/staging/" {
		t.Errorf("target = %+v, want staging/ under the CDN root", *target)
	}

	onLocation := &envapi.Deployment{S3BucketPublic: "public", ClientURL: "https://play.example.com/", ClientS3Prefix: "Client/"}
	target, err = resolveWebClientTarget(onLocation, "staging/")
	if err != nil {
		t.Fatal(err)
	}
	if target.Prefix != "Client/staging/" || target.URL != "https://play.example.com/staging/" {
		t.Errorf("target = %+v, want staging/ under the environment's location", *target)
	}
}

func TestResolveWebClientTarget_AnEnvironmentWithNoCDNHasNowhere(t *testing.T) {
	for name, deployment := range map[string]*envapi.Deployment{
		"edge":         {},
		"no CDN":       {S3BucketPublic: "public"},
		"no bucket":    {CdnS3Fqdn: "nimbly-assets.p1.metaplay.io"},
		"location URL": {ClientURL: "https://play.example.com/"},
	} {
		if _, err := resolveWebClientTarget(deployment, ""); !errors.Is(err, errNoWebClientHosting) {
			t.Errorf("%s: error = %v, want errNoWebClientHosting", name, err)
		}
	}
}
