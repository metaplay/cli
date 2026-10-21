/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package sitedeploy

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is how a site is uploaded, as a webClients entry of
// metaplay-project.yaml has it. A preset is a Config of the same form, which
// the entry builds on.
type Config struct {
	// Preset names the preset the config builds on.
	Preset string `yaml:"preset,omitempty"`

	// Prefix is a path under the environment's web client location to deploy
	// the site to, usually empty. A preset does not set it.
	Prefix string `yaml:"prefix,omitempty"`

	// Files decide how source files become objects, the first matching rule
	// each file.
	Files []FileRuleConfig `yaml:"files,omitempty"`

	// Headers set the objects' headers by their name, the first matching rule
	// each header.
	Headers []HeaderRuleConfig `yaml:"headers,omitempty"`

	// EntryFiles are globs of the objects uploaded last, in this order.
	EntryFiles []string `yaml:"entryFiles,omitempty"`

	// EndpointsFile names the object the deploy writes with the game server's
	// and the CDN's addresses, as far as the environment reports them, for the
	// client to read at startup. Empty writes none.
	EndpointsFile string `yaml:"endpointsFile,omitempty"`
}

// FileRuleConfig is a rule of a Config's files. It matches source files by a
// glob or a regular expression over their path in the site.
type FileRuleConfig struct {
	Match           string `yaml:"match,omitempty"`
	MatchRegex      string `yaml:"matchRegex,omitempty"`
	StripSuffix     string `yaml:"stripSuffix,omitempty"`
	ContentEncoding string `yaml:"contentEncoding,omitempty"`
	Exclude         bool   `yaml:"exclude,omitempty"`
}

// HeaderRuleConfig is a rule of a Config's headers. It matches objects by a
// glob or a regular expression over their name in the site.
type HeaderRuleConfig struct {
	Match        string `yaml:"match,omitempty"`
	MatchRegex   string `yaml:"matchRegex,omitempty"`
	ContentType  string `yaml:"contentType,omitempty"`
	CacheControl string `yaml:"cacheControl,omitempty"`
}

//go:embed presets/*.yaml
var presetFiles embed.FS

// PresetNames lists the presets a config can build on.
func PresetNames() []string {
	entries, err := fs.ReadDir(presetFiles, "presets")
	if err != nil {
		panic(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, strings.TrimSuffix(entry.Name(), ".yaml"))
	}
	sort.Strings(names)
	return names
}

// LoadPreset reads a preset.
func LoadPreset(name string) (*Config, error) {
	data, err := presetFiles.ReadFile("presets/" + name + ".yaml")
	if err != nil {
		return nil, fmt.Errorf("unknown preset '%s', expecting one of: %s", name, strings.Join(PresetNames(), ", "))
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var preset Config
	if err := decoder.Decode(&preset); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("failed to parse preset '%s': %w", name, err)
	}
	if preset.Preset != "" || preset.Prefix != "" {
		return nil, fmt.Errorf("preset '%s' must not set preset or prefix", name)
	}
	return &preset, nil
}

// Resolve gives the config with its preset applied: the config's files and
// headers rules go before the preset's, so they win for the files they match;
// its entryFiles and endpointsFile replace the preset's where set.
func (c *Config) Resolve() (*Config, error) {
	if c.Preset == "" {
		resolved := *c
		return &resolved, nil
	}
	preset, err := LoadPreset(c.Preset)
	if err != nil {
		return nil, err
	}
	resolved := *preset
	resolved.Preset = c.Preset
	resolved.Prefix = c.Prefix
	resolved.Files = append(append([]FileRuleConfig{}, c.Files...), preset.Files...)
	resolved.Headers = append(append([]HeaderRuleConfig{}, c.Headers...), preset.Headers...)
	if c.EntryFiles != nil {
		resolved.EntryFiles = c.EntryFiles
	}
	if c.EndpointsFile != "" {
		resolved.EndpointsFile = c.EndpointsFile
	}
	return &resolved, nil
}

// Options compiles a resolved config.
func (c *Config) Options() (Options, error) {
	opts := Options{}

	for i, ruleConfig := range c.Files {
		pattern, err := compilePattern(ruleConfig.Match, ruleConfig.MatchRegex)
		if err != nil {
			return Options{}, fmt.Errorf("invalid files[%d]: %w", i, err)
		}
		if ruleConfig.Exclude && (ruleConfig.StripSuffix != "" || ruleConfig.ContentEncoding != "") {
			return Options{}, fmt.Errorf("invalid files[%d]: an excluded file has no stripSuffix or contentEncoding", i)
		}
		if !ruleConfig.Exclude && ruleConfig.StripSuffix == "" && ruleConfig.ContentEncoding == "" {
			return Options{}, fmt.Errorf("invalid files[%d]: set stripSuffix, contentEncoding or exclude", i)
		}
		opts.FileRules = append(opts.FileRules, FileRule{
			Pattern:         pattern,
			StripSuffix:     ruleConfig.StripSuffix,
			ContentEncoding: ruleConfig.ContentEncoding,
			Exclude:         ruleConfig.Exclude,
		})
	}

	for i, ruleConfig := range c.Headers {
		pattern, err := compilePattern(ruleConfig.Match, ruleConfig.MatchRegex)
		if err != nil {
			return Options{}, fmt.Errorf("invalid headers[%d]: %w", i, err)
		}
		if ruleConfig.ContentType == "" && ruleConfig.CacheControl == "" {
			return Options{}, fmt.Errorf("invalid headers[%d]: set contentType or cacheControl", i)
		}
		opts.HeaderRules = append(opts.HeaderRules, HeaderRule{
			Pattern:      pattern,
			ContentType:  ruleConfig.ContentType,
			CacheControl: ruleConfig.CacheControl,
		})
	}

	for _, entry := range c.EntryFiles {
		pattern, err := Glob(entry)
		if err != nil {
			return Options{}, fmt.Errorf("invalid entryFiles glob '%s': %w", entry, err)
		}
		opts.EntryPatterns = append(opts.EntryPatterns, pattern)
	}
	return opts, nil
}

// compilePattern compiles a rule's glob or regular expression, of which it
// sets exactly one.
func compilePattern(match, matchRegex string) (*regexp.Regexp, error) {
	switch {
	case match != "" && matchRegex != "":
		return nil, fmt.Errorf("set only one of match and matchRegex")
	case match != "":
		return Glob(match)
	case matchRegex != "":
		return regexp.Compile(matchRegex)
	default:
		return nil, fmt.Errorf("set match or matchRegex")
	}
}

// Validate checks that the config resolves and compiles.
func (c *Config) Validate() error {
	if _, err := NormalizePrefix(c.Prefix); err != nil {
		return err
	}
	if c.EndpointsFile != "" {
		if err := validateObjectName(c.EndpointsFile); err != nil {
			return fmt.Errorf("invalid endpointsFile: %w", err)
		}
	}
	resolved, err := c.Resolve()
	if err != nil {
		return err
	}
	_, err = resolved.Options()
	return err
}
