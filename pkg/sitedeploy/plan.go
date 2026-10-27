/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package sitedeploy

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The top-level names in the public bucket that the game server and its
// clients own. A site deployed to the bucket root must not put files under
// them: the game server's own files would be replaced or shadowed.
var reservedTopLevelNames = []string{
	"GameConfig",                 // Game configs and localizations, served to clients.
	"Volatile",                   // Server status hint, served uncached.
	"Connectivity",               // Network diagnostics' CDN probe.
	"FacebookDataDeletionReport", // Facebook data deletion status pages.
	"Web3PseudoBucket",           // NFT metadata.
	"ClientBuilds",               // Client builds uploaded by the samples' CI.
}

// Object is a file of the site and how it is uploaded.
type Object struct {
	Key             string // Bucket key, including the prefix.
	Name            string // Name in the site, with forward slashes: the key without the prefix.
	LocalFile       string // File whose bytes are uploaded, such as app.css.br for app.css. Empty for a generated object.
	Data            []byte // Bytes of a generated object, which no file in the site holds.
	Size            int64
	MD5             string // Hex MD5 of the uploaded bytes.
	ContentType     string
	ContentEncoding string
	CacheControl    string
	UploadOrder     int  // When the object is uploaded, see ObjectRule.UploadOrder.
	Unchanged       bool // The last deploy uploaded the same bytes with the same headers.
}

// Plan is what deploying a site uploads, in order.
type Plan struct {
	Prefix      string
	ManifestKey string
	Objects     []Object
}

// UploadCount is how many objects the plan uploads, rather than leaves as
// they are.
func (p *Plan) UploadCount() int {
	count := 0
	for _, obj := range p.Objects {
		if !obj.Unchanged {
			count++
		}
	}
	return count
}

// NormalizePrefix checks a bucket key prefix and gives it a trailing slash.
// The empty prefix is the bucket root.
func NormalizePrefix(prefix string) (string, error) {
	if prefix == "" || prefix == "/" {
		return "", nil
	}
	if strings.HasPrefix(prefix, "/") {
		return "", fmt.Errorf("prefix '%s' must not start with '/'", prefix)
	}
	for _, segment := range strings.Split(strings.TrimSuffix(prefix, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("prefix '%s' has an empty, '.' or '..' segment", prefix)
		}
	}
	return strings.TrimSuffix(prefix, "/") + "/", nil
}

// validateObjectName checks a name in the site: relative, with no empty, '.'
// or '..' segments.
func validateObjectName(name string) error {
	if name == "" || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return fmt.Errorf("'%s' is not a relative file path", name)
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("'%s' has an empty, '.' or '..' segment", name)
		}
	}
	return nil
}

// ManifestKey is where the manifest of a site at prefix is kept.
func ManifestKey(prefix string) string {
	return prefix + manifestFileName
}

// BuildPlan decides how each file under siteDir is uploaded to prefix, which
// NormalizePrefix has checked, together with the generated objects, by name.
// A generated object takes the place of a file that would be uploaded under
// its name. Objects the previous deploy uploaded identically are marked
// unchanged.
func BuildPlan(siteDir, prefix string, opts Options, generated map[string][]byte, previous *Manifest) (*Plan, error) {
	files, err := listSiteFiles(siteDir)
	if err != nil {
		return nil, err
	}

	// Map the files to objects. Where two files map to the same object, as
	// app.css.br and app.css do, the file the earlier rule matched wins, and a
	// file no rule matched loses to any that a rule did.
	type source struct {
		sitePath string
		rule     int
		encoding string
	}
	sources := map[string]source{}
	sitePaths := make([]string, 0, len(files))
	for sitePath := range files {
		sitePaths = append(sitePaths, sitePath)
	}
	sort.Strings(sitePaths)
	for _, sitePath := range sitePaths {
		name, ruleNdx, encoding := sitePath, len(opts.FileRules), ""
		excluded := false
		for i, rule := range opts.FileRules {
			if !rule.Pattern.MatchString(sitePath) {
				continue
			}
			if rule.Exclude {
				excluded = true
				break
			}
			if rule.StripSuffix != "" {
				stripped, ok := strings.CutSuffix(sitePath, rule.StripSuffix)
				if !ok || stripped == "" || strings.HasSuffix(stripped, "/") {
					return nil, fmt.Errorf("files[%d] strips '%s' from '%s', which does not end with it after a file name", i, rule.StripSuffix, sitePath)
				}
				name = stripped
			}
			ruleNdx, encoding = i, rule.ContentEncoding
			break
		}
		if excluded {
			continue
		}
		if existing, ok := sources[name]; ok && existing.rule < ruleNdx {
			continue
		}
		sources[name] = source{sitePath: sitePath, rule: ruleNdx, encoding: encoding}
	}

	names := map[string]bool{}
	for name := range sources {
		names[name] = true
	}
	for name := range generated {
		if err := validateObjectName(name); err != nil {
			return nil, fmt.Errorf("invalid generated object name: %w", err)
		}
		delete(sources, name)
		names[name] = true
	}
	if names[manifestFileName] {
		return nil, fmt.Errorf("the site must not upload an object named '%s': the deploy keeps its manifest there", manifestFileName)
	}
	if err := checkReservedNames(prefix, names); err != nil {
		return nil, err
	}

	plan := &Plan{Prefix: prefix, ManifestKey: ManifestKey(prefix)}
	for name := range names {
		obj := Object{
			Key:          prefix + name,
			Name:         name,
			ContentType:  contentTypeOf(name),
			CacheControl: DefaultCacheControl,
		}
		if data, ok := generated[name]; ok {
			obj.Data = data
		} else {
			src := sources[name]
			obj.LocalFile = filepath.Join(siteDir, filepath.FromSlash(src.sitePath))
			obj.ContentEncoding = src.encoding
		}

		contentTypeSet, cacheControlSet, uploadOrderSet := false, false, false
		for _, rule := range opts.ObjectRules {
			if !rule.Pattern.MatchString(name) {
				continue
			}
			if rule.ContentType != "" && !contentTypeSet {
				obj.ContentType, contentTypeSet = rule.ContentType, true
			}
			if rule.CacheControl != "" && !cacheControlSet {
				obj.CacheControl, cacheControlSet = rule.CacheControl, true
			}
			if rule.UploadOrder != nil && !uploadOrderSet {
				obj.UploadOrder, uploadOrderSet = *rule.UploadOrder, true
			}
		}

		if obj.Data != nil {
			sum := md5.Sum(obj.Data)
			obj.Size, obj.MD5 = int64(len(obj.Data)), hex.EncodeToString(sum[:])
		} else {
			obj.Size, obj.MD5, err = hashFile(obj.LocalFile)
			if err != nil {
				return nil, err
			}
		}
		if previous != nil {
			if prev, ok := previous.Objects[obj.Key]; ok {
				obj.Unchanged = prev.MD5 == obj.MD5 && prev.Size == obj.Size &&
					prev.ContentType == obj.ContentType && prev.ContentEncoding == obj.ContentEncoding &&
					prev.CacheControl == obj.CacheControl
			}
		}
		plan.Objects = append(plan.Objects, obj)
	}

	sort.Slice(plan.Objects, func(i, j int) bool {
		a, b := plan.Objects[i], plan.Objects[j]
		if a.UploadOrder != b.UploadOrder {
			return a.UploadOrder < b.UploadOrder
		}
		return a.Key < b.Key
	})
	return plan, nil
}

// listSiteFiles gives the regular files under siteDir, by their path in the
// site.
func listSiteFiles(siteDir string) (map[string]struct{}, error) {
	info, err := os.Stat(siteDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read site directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", siteDir)
	}

	files := map[string]struct{}{}
	err = filepath.WalkDir(siteDir, func(filePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(siteDir, filePath)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list site files: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s has no files", siteDir)
	}
	return files, nil
}

// checkReservedNames refuses a site that would put objects under a name the
// game server owns.
func checkReservedNames[V any](prefix string, names map[string]V) error {
	var topLevelNames []string
	if prefix != "" {
		topLevelNames = []string{strings.SplitN(prefix, "/", 2)[0]}
	} else {
		seen := map[string]bool{}
		for objectName := range names {
			name := strings.SplitN(objectName, "/", 2)[0]
			if !seen[name] {
				seen[name] = true
				topLevelNames = append(topLevelNames, name)
			}
		}
	}

	var collisions []string
	for _, name := range topLevelNames {
		for _, reserved := range reservedTopLevelNames {
			if strings.EqualFold(name, reserved) {
				collisions = append(collisions, name)
			}
		}
	}
	if len(collisions) > 0 {
		sort.Strings(collisions)
		return fmt.Errorf("the site would write under %s, which the game server uses in the public bucket; deploy under another prefix",
			strings.Join(collisions, ", "))
	}
	return nil
}

func hashFile(filePath string) (int64, string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = file.Close() }()
	hash := md5.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return 0, "", fmt.Errorf("failed to read %s: %w", filePath, err)
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}
