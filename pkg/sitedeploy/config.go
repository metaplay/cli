/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package sitedeploy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// SdkPresetDir is the directory of the Metaplay SDK's presets, relative to the
// SDK's root: preset 'blazor' is <sdkRootDir>/WebClientPresets/blazor.yaml.
const SdkPresetDir = "WebClientPresets"

var presetNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Config is how a site is uploaded, as a webClients entry of
// metaplay-project.yaml has it. A preset is a Config of the same form, which
// the entry builds on: one of the Metaplay SDK's, by name, or the project's own
// file.
type Config struct {
	// Preset is the name of the Metaplay SDK's preset the config builds on,
	// eg, 'blazor'. At most one of Preset and PresetFile is set.
	Preset string `yaml:"preset,omitempty"`

	// PresetFile is the path of a preset file the config builds on, relative
	// to metaplay-project.yaml.
	PresetFile string `yaml:"presetFile,omitempty"`

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

// LoadPreset reads the preset file at path.
func LoadPreset(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read preset: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var preset Config
	if err := decoder.Decode(&preset); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("failed to parse preset '%s': %w", path, err)
	}
	if preset.Preset != "" || preset.PresetFile != "" || preset.Prefix != "" {
		return nil, fmt.Errorf("preset '%s' must not set preset, presetFile or prefix", path)
	}
	return &preset, nil
}

// PresetPath is the path of the config's preset file, or empty when it has
// none: an SDK preset is under sdkRootDir, and a presetFile is relative to
// projectDir.
func (c *Config) PresetPath(projectDir, sdkRootDir string) string {
	switch {
	case c.Preset != "":
		return filepath.Join(sdkRootDir, SdkPresetDir, c.Preset+".yaml")
	case c.PresetFile != "":
		return filepath.Join(projectDir, filepath.FromSlash(c.PresetFile))
	default:
		return ""
	}
}

// Resolve gives the config with its preset applied: the config's files and
// headers rules go before the preset's, so they win for the files they match;
// its entryFiles and endpointsFile replace the preset's where set.
func (c *Config) Resolve(projectDir, sdkRootDir string) (*Config, error) {
	presetPath := c.PresetPath(projectDir, sdkRootDir)
	if presetPath == "" {
		resolved := *c
		return &resolved, nil
	}
	preset, err := LoadPreset(presetPath)
	if c.Preset != "" && errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("the Metaplay SDK has no preset '%s': %s does not exist", c.Preset, presetPath)
	}
	if err != nil {
		return nil, err
	}
	resolved := *preset
	resolved.Preset = c.Preset
	resolved.PresetFile = c.PresetFile
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

// Options compiles the config's rules.
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

// Validate checks the config without its preset, which is read only when the
// config is resolved.
func (c *Config) Validate() error {
	if c.Preset != "" && c.PresetFile != "" {
		return fmt.Errorf("set only one of preset and presetFile")
	}
	if c.Preset != "" && !presetNameRegex.MatchString(c.Preset) {
		return fmt.Errorf("invalid preset '%s': name one of the Metaplay SDK's presets, eg, 'blazor', or use presetFile for a file of the project's own", c.Preset)
	}
	if filepath.IsAbs(c.PresetFile) {
		return fmt.Errorf("presetFile '%s' is an absolute path: it must be relative to metaplay-project.yaml", c.PresetFile)
	}
	if _, err := NormalizePrefix(c.Prefix); err != nil {
		return err
	}
	if c.EndpointsFile != "" {
		if err := validateObjectName(c.EndpointsFile); err != nil {
			return fmt.Errorf("invalid endpointsFile: %w", err)
		}
	}
	_, err := c.Options()
	return err
}
