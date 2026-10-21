/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/goccy/go-yaml"
)

// Frontmatter is the YAML block at the head of a SKILL.md file.
//
// It preserves key order so round-tripping (parse → mutate → marshal) does
// not reshuffle existing fields. Backed by goccy/go-yaml's MapSlice.
type Frontmatter struct {
	items yaml.MapSlice
}

// ErrNoFrontmatter is returned when the input does not begin with a
// `---` delimited YAML block.
var ErrNoFrontmatter = errors.New("no frontmatter block found")

// ErrUnterminatedFrontmatter is returned when an opening `---` is not
// followed by a closing `---`.
var ErrUnterminatedFrontmatter = errors.New("frontmatter block is not terminated")

// ParseFrontmatter splits a markdown document with a leading YAML
// frontmatter block into the parsed Frontmatter and the body bytes.
//
// The body returned does not include the closing `---` line nor the
// newline immediately following it; everything after that is preserved
// verbatim (including embedded blank lines).
func ParseFrontmatter(input []byte) (*Frontmatter, []byte, error) {
	yamlBytes, body, err := splitFrontmatter(input)
	if err != nil {
		return nil, nil, err
	}
	var items yaml.MapSlice
	if err := yaml.Unmarshal(yamlBytes, &items); err != nil {
		return nil, nil, fmt.Errorf("parse frontmatter YAML: %w", err)
	}
	return &Frontmatter{items: items}, body, nil
}

// HasFrontmatter reports whether input begins with a `---` delimiter.
func HasFrontmatter(input []byte) bool {
	rest := stripUTF8BOM(input)
	return bytes.HasPrefix(rest, []byte("---\n")) || bytes.HasPrefix(rest, []byte("---\r\n"))
}

// splitFrontmatter returns the YAML body bytes and the markdown body bytes.
func splitFrontmatter(input []byte) (yamlBytes, body []byte, err error) {
	rest := stripUTF8BOM(input)
	switch {
	case bytes.HasPrefix(rest, []byte("---\n")):
		rest = rest[len("---\n"):]
	case bytes.HasPrefix(rest, []byte("---\r\n")):
		rest = rest[len("---\r\n"):]
	default:
		return nil, nil, ErrNoFrontmatter
	}

	// Look for a line that is exactly `---` (possibly with a trailing CR).
	end := findClosingDelimiter(rest)
	if end.start < 0 {
		return nil, nil, ErrUnterminatedFrontmatter
	}
	yamlBytes = rest[:end.start]
	body = rest[end.afterNewline:]
	return yamlBytes, body, nil
}

type delimRange struct {
	start        int // first index of the `---` line in rest
	afterNewline int // index just past the trailing newline of that line
}

// findClosingDelimiter scans rest for the first line consisting solely of
// `---` (with optional trailing CR), returning its bounds. Returns -1
// (zero-value) start if not found; check the negative start.
func findClosingDelimiter(rest []byte) delimRange {
	lineStart := 0
	for lineStart <= len(rest) {
		nl := bytes.IndexByte(rest[lineStart:], '\n')
		var line []byte
		var afterNewline int
		if nl < 0 {
			line = rest[lineStart:]
			afterNewline = len(rest)
		} else {
			line = rest[lineStart : lineStart+nl]
			afterNewline = lineStart + nl + 1
		}
		// Trim a trailing CR so CRLF inputs match.
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if bytes.Equal(line, []byte("---")) {
			return delimRange{start: lineStart, afterNewline: afterNewline}
		}
		if nl < 0 {
			break
		}
		lineStart = afterNewline
	}
	return delimRange{start: -1}
}

func stripUTF8BOM(b []byte) []byte {
	const bom = "\xef\xbb\xbf"
	if bytes.HasPrefix(b, []byte(bom)) {
		return b[len(bom):]
	}
	return b
}

// Get returns the value associated with key, or (nil, false) if absent.
func (f *Frontmatter) Get(key string) (any, bool) {
	for _, it := range f.items {
		if k, ok := it.Key.(string); ok && k == key {
			return it.Value, true
		}
	}
	return nil, false
}

// GetString returns the string value for key, or "" if missing or not a
// string.
func (f *Frontmatter) GetString(key string) string {
	if v, ok := f.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// GetBool returns the bool value for key. YAML scalars like "true"/"false"
// also count.
func (f *Frontmatter) GetBool(key string) bool {
	v, ok := f.Get(key)
	if !ok {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true" || x == "yes"
	default:
		return false
	}
}

// Has reports whether key is present.
func (f *Frontmatter) Has(key string) bool {
	_, ok := f.Get(key)
	return ok
}

// Set assigns value to key. If the key is already present its position is
// preserved and only the value is replaced; otherwise the key is appended.
func (f *Frontmatter) Set(key string, value any) {
	for i, it := range f.items {
		if k, ok := it.Key.(string); ok && k == key {
			f.items[i].Value = value
			return
		}
	}
	f.items = append(f.items, yaml.MapItem{Key: key, Value: value})
}

// Delete removes the entry with the given key, if present. Returns true
// if a key was removed.
func (f *Frontmatter) Delete(key string) bool {
	for i, it := range f.items {
		if k, ok := it.Key.(string); ok && k == key {
			f.items = append(f.items[:i], f.items[i+1:]...)
			return true
		}
	}
	return false
}

// Name returns the `name` field, or "" if absent.
func (f *Frontmatter) Name() string { return f.GetString("name") }

// Description returns the `description` field, or "" if absent.
func (f *Frontmatter) Description() string { return f.GetString("description") }

// MaxDescriptionLength is the longest a SKILL.md frontmatter description
// may be before downstream AI-coding tools refuse to load the skill. The
// OpenAI Codex CLI rejects skills whose description exceeds 1024 chars;
// Claude Code emits a warning and silently skips loading. We hold to this
// limit at test time so embedded skills stay loadable across every host
// in the registry.
const MaxDescriptionLength = 1024

// Hidden returns the `hidden` boolean, false if absent.
func (f *Frontmatter) Hidden() bool { return f.GetBool("hidden") }

// ManagedBy returns the `managed-by` field, used to identify wrappers
// previously written by this CLI.
func (f *Frontmatter) ManagedBy() string { return f.GetString("managed-by") }

// CLIVersion returns the `metaplay-cli-version` stamp, used by the
// install version gate. Empty string for wrappers without a stamp.
func (f *Frontmatter) CLIVersion() string { return f.GetString("metaplay-cli-version") }

// Marshal renders the frontmatter as a `---\n...\n---\n` block. The
// trailing newline is included so callers can write `Marshal()+body`
// directly.
func (f *Frontmatter) Marshal() ([]byte, error) {
	body, err := yaml.Marshal(f.items)
	if err != nil {
		return nil, fmt.Errorf("marshal frontmatter: %w", err)
	}
	var out bytes.Buffer
	out.WriteString("---\n")
	out.Write(body)
	// goccy/go-yaml may or may not end with a newline; ensure exactly one.
	if !bytes.HasSuffix(body, []byte("\n")) {
		out.WriteByte('\n')
	}
	out.WriteString("---\n")
	return out.Bytes(), nil
}

// MarshalDocument returns Marshal(frontmatter) + body, the full SKILL.md
// representation suitable for writing to disk.
func (f *Frontmatter) MarshalDocument(body []byte) ([]byte, error) {
	fm, err := f.Marshal()
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(fm)+len(body))
	out = append(out, fm...)
	out = append(out, body...)
	return out, nil
}
