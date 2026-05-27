/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestParseFrontmatter_Basic(t *testing.T) {
	input := []byte("---\nname: foo\ndescription: bar\n---\nbody line\n")
	fm, body, err := ParseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := fm.Name(); got != "foo" {
		t.Errorf("Name() = %q, want %q", got, "foo")
	}
	if got := fm.Description(); got != "bar" {
		t.Errorf("Description() = %q, want %q", got, "bar")
	}
	if string(body) != "body line\n" {
		t.Errorf("body = %q, want %q", body, "body line\n")
	}
}

func TestParseFrontmatter_NoBlock(t *testing.T) {
	_, _, err := ParseFrontmatter([]byte("just a body\n"))
	if !errors.Is(err, ErrNoFrontmatter) {
		t.Fatalf("expected ErrNoFrontmatter, got %v", err)
	}
}

func TestParseFrontmatter_Unterminated(t *testing.T) {
	_, _, err := ParseFrontmatter([]byte("---\nname: foo\nbody but no closing\n"))
	if !errors.Is(err, ErrUnterminatedFrontmatter) {
		t.Fatalf("expected ErrUnterminatedFrontmatter, got %v", err)
	}
}

func TestParseFrontmatter_CRLF(t *testing.T) {
	input := []byte("---\r\nname: foo\r\n---\r\nbody\r\n")
	fm, body, err := ParseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.Name() != "foo" {
		t.Errorf("Name() = %q", fm.Name())
	}
	if !bytes.Equal(body, []byte("body\r\n")) {
		t.Errorf("body = %q", body)
	}
}

func TestParseFrontmatter_BOM(t *testing.T) {
	input := []byte("\xef\xbb\xbf---\nname: foo\n---\nbody\n")
	fm, _, err := ParseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.Name() != "foo" {
		t.Errorf("Name() = %q", fm.Name())
	}
}

func TestFrontmatter_Hidden(t *testing.T) {
	input := []byte("---\nname: foo\nhidden: true\n---\n")
	fm, _, err := ParseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fm.Hidden() {
		t.Errorf("Hidden() = false, want true")
	}
}

func TestFrontmatter_RoundTripPreservesUnknownFields(t *testing.T) {
	input := []byte("---\nname: foo\ncustom: keepme\ndescription: bar\n---\nbody\n")
	fm, body, err := ParseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, err := fm.MarshalDocument(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "custom: keepme") {
		t.Errorf("round-trip dropped unknown field; got:\n%s", out)
	}
	// body preserved
	if !strings.HasSuffix(string(out), "body\n") {
		t.Errorf("body not preserved; got:\n%s", out)
	}
}

func TestFrontmatter_SetUpdatesInPlace(t *testing.T) {
	input := []byte("---\nname: foo\nversion: 1\n---\n")
	fm, _, err := ParseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fm.Set("version", 2)
	out, err := fm.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "version: 2") {
		t.Errorf("expected version: 2, got:\n%s", s)
	}
	if strings.Index(s, "name") > strings.Index(s, "version") {
		t.Errorf("Set should preserve key order, got:\n%s", s)
	}
}

func TestFrontmatter_SetAppendsNew(t *testing.T) {
	input := []byte("---\nname: foo\n---\n")
	fm, _, err := ParseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fm.Set("metaplay-cli-version", "1.2.3")
	fm.Set("managed-by", "metaplay-cli")
	out, _ := fm.Marshal()
	s := string(out)
	if !strings.Contains(s, "metaplay-cli-version: 1.2.3") {
		t.Errorf("missing injected field; got:\n%s", s)
	}
	if !strings.Contains(s, "managed-by: metaplay-cli") {
		t.Errorf("missing injected field; got:\n%s", s)
	}
}

func TestFrontmatter_Delete(t *testing.T) {
	input := []byte("---\nname: foo\nremoved: yes\n---\n")
	fm, _, err := ParseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fm.Delete("removed") {
		t.Errorf("Delete returned false")
	}
	if fm.Has("removed") {
		t.Errorf("key still present")
	}
}

func TestHasFrontmatter(t *testing.T) {
	cases := map[string]bool{
		"---\nname: foo\n---\n":   true,
		"\xef\xbb\xbf---\nx: 1\n": true,
		"# heading\n":             false,
		"":                        false,
	}
	for in, want := range cases {
		if got := HasFrontmatter([]byte(in)); got != want {
			t.Errorf("HasFrontmatter(%q) = %v, want %v", in, got, want)
		}
	}
}
