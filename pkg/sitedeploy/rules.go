/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

// Package sitedeploy uploads a static site, such as a published web client,
// into an environment's public bucket, which its CDN serves.
//
// S3 serves each object with the headers it was uploaded with and does no
// content negotiation, so each file is uploaded with the Content-Type,
// Content-Encoding and Cache-Control it is served with. A Config's files rules
// decide which source file becomes which object, such as app.css.br uploaded
// as app.css with Content-Encoding: br, and its headers rules set the objects'
// headers. A preset is a Config for one kind of build that a project's Config
// builds on.
package sitedeploy

import (
	"fmt"
	"mime"
	"path"
	"regexp"
	"strings"
)

// FileRule decides how the source files whose path in the site, with forward
// slashes, matches Pattern become objects in the bucket. A file follows the
// first rule that matches it, and a file no rule matches is uploaded as it is,
// under its own name.
type FileRule struct {
	Pattern *regexp.Regexp

	// StripSuffix is removed from the file's name to make the object's, so
	// app.css.br is uploaded as app.css. Empty keeps the name.
	StripSuffix string

	// ContentEncoding is the encoding the file's bytes are in, sent as the
	// object's Content-Encoding header.
	ContentEncoding string

	Exclude bool
}

// HeaderRule sets the headers of the objects whose name in the site matches
// Pattern. An object takes each header from the first rule that sets it.
type HeaderRule struct {
	Pattern      *regexp.Regexp
	ContentType  string
	CacheControl string
}

// Options decide how the files of a site are uploaded. Config.Options
// compiles them from a webClients entry of metaplay-project.yaml.
type Options struct {
	FileRules   []FileRule
	HeaderRules []HeaderRule

	// EntryPatterns name the objects a page loads first and that name the
	// rest, such as index.html. They are uploaded last, in the order given, so
	// a page never loads an entry point that names objects not yet uploaded.
	EntryPatterns []*regexp.Regexp
}

const (
	// DefaultCacheControl applies where no rule sets Cache-Control: the
	// browser and the CDN revalidate the file on each load, which costs a 304
	// while it is unchanged. The CDN's own default would cache it for a day.
	DefaultCacheControl = "no-cache"
)

// Glob compiles a glob over slash-separated paths: '*' and '?' match within
// one path segment, and '**' matches any number of segments.
func Glob(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, fmt.Errorf("empty glob pattern")
	}
	var sb strings.Builder
	sb.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case strings.HasPrefix(pattern[i:], "**/"):
			sb.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(pattern[i:], "**"):
			sb.WriteString(".*")
			i++
		case c == '*':
			sb.WriteString("[^/]*")
		case c == '?':
			sb.WriteString("[^/]")
		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	sb.WriteString("$")
	return regexp.Compile(sb.String())
}

// Content types of the files web builds are made of. The system's MIME table
// varies by machine and misses most of these, and a .wasm served as anything
// but application/wasm cannot be compiled while it streams in.
var contentTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".htm":         "text/html; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".json":        "application/json; charset=utf-8",
	".map":         "application/json; charset=utf-8",
	".webmanifest": "application/manifest+json; charset=utf-8",
	".txt":         "text/plain; charset=utf-8",
	".md":          "text/markdown; charset=utf-8",
	".xml":         "application/xml",
	".wasm":        "application/wasm",
	".dll":         "application/octet-stream",
	".pdb":         "application/octet-stream",
	".dat":         "application/octet-stream",
	".blat":        "application/octet-stream",
	".data":        "application/octet-stream",
	".unityweb":    "application/octet-stream",
	".bin":         "application/octet-stream",
	".mpa":         "application/octet-stream",
	".webp":        "image/webp",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".gif":         "image/gif",
	".avif":        "image/avif",
	".svg":         "image/svg+xml",
	".ico":         "image/x-icon",
	".woff2":       "font/woff2",
	".woff":        "font/woff",
	".ttf":         "font/ttf",
	".otf":         "font/otf",
	".mp3":         "audio/mpeg",
	".ogg":         "audio/ogg",
	".wav":         "audio/wav",
	".mp4":         "video/mp4",
	".webm":        "video/webm",
}

// contentTypeOf gives the content type a file is served with, from its name.
func contentTypeOf(sitePath string) string {
	ext := strings.ToLower(path.Ext(sitePath))
	if contentType, ok := contentTypes[ext]; ok {
		return contentType
	}
	if contentType := mime.TypeByExtension(ext); contentType != "" {
		return contentType
	}
	return "application/octet-stream"
}
