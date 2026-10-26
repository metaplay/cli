/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package sitedeploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeSite creates the files of a site, by path, under a temporary directory.
func writeSite(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for sitePath, content := range files {
		filePath := filepath.Join(root, filepath.FromSlash(sitePath))
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// A Blazor publish in miniature: compressible files have .br and .gz
// siblings, the framework files but two are fingerprinted, and images are
// left uncompressed.
func blazorSite(t *testing.T) string {
	return writeSite(t, map[string]string{
		"index.html":                                  "<html>",
		"index.html.br":                               "br:<html>",
		"index.html.gz":                               "gz:<html>",
		"app.css":                                     "body{}",
		"app.css.br":                                  "br:body{}",
		"app.css.gz":                                  "gz:body{}",
		"art/den.webp":                                "RIFF",
		"_framework/dotnet.js":                        "dotnet",
		"_framework/dotnet.js.br":                     "br:dotnet",
		"_framework/blazor.webassembly.js":            "blazor",
		"_framework/blazor.webassembly.js.br":         "br:blazor",
		"_framework/Client.su7785ecxb.wasm":           "wasm",
		"_framework/Client.su7785ecxb.wasm.br":        "br:wasm",
		"_framework/dotnet.native.befq3iek54.wasm":    "native",
		"_framework/dotnet.native.befq3iek54.wasm.gz": "gz:native",
	})
}

// testSdkRootDir stands in for the Metaplay SDK, with a copy of its Blazor
// preset, relative to the package directory the tests run in.
const testSdkRootDir = "testdata/sdk"

// options compiles a webClients entry as the deploy command does, with the
// package directory as the project's.
func options(t *testing.T, config Config) Options {
	t.Helper()
	resolved, err := config.Resolve(".", testSdkRootDir)
	if err != nil {
		t.Fatal(err)
	}
	opts, err := resolved.Options()
	if err != nil {
		t.Fatal(err)
	}
	return opts
}

func blazorPlan(t *testing.T, siteDir, prefix string, previous *Manifest) *Plan {
	t.Helper()
	plan, err := BuildPlan(siteDir, prefix, options(t, Config{Preset: "blazor"}), nil, previous)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func objectByKey(t *testing.T, plan *Plan, key string) Object {
	t.Helper()
	for _, obj := range plan.Objects {
		if obj.Key == key {
			return obj
		}
	}
	t.Fatalf("plan has no object %s", key)
	return Object{}
}

func planKeys(plan *Plan) []string {
	keys := make([]string, len(plan.Objects))
	for i, obj := range plan.Objects {
		keys[i] = obj.Key
	}
	return keys
}

func TestGlob(t *testing.T) {
	cases := []struct {
		glob  string
		path  string
		match bool
	}{
		{"*.css", "app.css", true},
		{"*.css", "css/app.css", false},
		{"**/*.css", "app.css", true},
		{"**/*.css", "css/deep/app.css", true},
		{"art/**", "art/cards/x.webp", true},
		{"art/**", "artwork/x.webp", false},
		{"_framework/dotnet.?s", "_framework/dotnet.js", true},
		{"a+b.txt", "a+b.txt", true},
		{"a+b.txt", "aab.txt", false},
	}
	for _, c := range cases {
		pattern, err := Glob(c.glob)
		if err != nil {
			t.Fatalf("Glob(%q): %v", c.glob, err)
		}
		if got := pattern.MatchString(c.path); got != c.match {
			t.Errorf("Glob(%q) matching %q = %v, want %v", c.glob, c.path, got, c.match)
		}
	}
}

func TestNormalizePrefix(t *testing.T) {
	for input, want := range map[string]string{"": "", "/": "", "Client": "Client/", "Client/": "Client/", "a/b": "a/b/"} {
		got, err := NormalizePrefix(input)
		if err != nil || got != want {
			t.Errorf("NormalizePrefix(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"/Client", "a//b", "../x", "a/./b"} {
		if _, err := NormalizePrefix(input); err == nil {
			t.Errorf("NormalizePrefix(%q) succeeded, want an error", input)
		}
	}
}

// The CDN cannot pick an encoding per request, so each file with a Brotli
// sibling is uploaded as the Brotli bytes under its own name, and the
// siblings are not uploaded as files of their own.
func TestBlazorPlan_UploadsBrotliBytesUnderTheOriginalNames(t *testing.T) {
	siteDir := blazorSite(t)
	plan := blazorPlan(t, siteDir, "", nil)

	for _, key := range planKeys(plan) {
		if strings.HasSuffix(key, ".br") || strings.HasSuffix(key, ".gz") {
			t.Errorf("plan uploads the pre-compressed sibling %s as a file of its own", key)
		}
	}

	css := objectByKey(t, plan, "app.css")
	if css.ContentEncoding != "br" || css.LocalFile != filepath.Join(siteDir, "app.css.br") {
		t.Errorf("app.css uploads %s with encoding %q, want the .br sibling with encoding br", css.LocalFile, css.ContentEncoding)
	}
	if css.ContentType != "text/css; charset=utf-8" {
		t.Errorf("app.css content type = %q, want the type of the uncompressed file", css.ContentType)
	}

	// Without a Brotli sibling a file goes up as it is, even with a gzip one.
	native := objectByKey(t, plan, "_framework/dotnet.native.befq3iek54.wasm")
	if native.ContentEncoding != "" || native.LocalFile != filepath.Join(siteDir, "_framework", "dotnet.native.befq3iek54.wasm") {
		t.Errorf("dotnet.native.wasm uploads %s with encoding %q, want the file itself uncompressed", native.LocalFile, native.ContentEncoding)
	}
	if native.ContentType != "application/wasm" {
		t.Errorf(".wasm content type = %q, want application/wasm", native.ContentType)
	}
}

// Fingerprinted framework files are new names in each build and can be cached
// forever; anything served by a name that outlives a build is revalidated.
func TestBlazorPlan_CachesOnlyFingerprintedFilesForever(t *testing.T) {
	plan := blazorPlan(t, blazorSite(t), "", nil)
	for _, obj := range plan.Objects {
		fingerprinted := strings.Contains(obj.Key, "su7785ecxb") || strings.Contains(obj.Key, "befq3iek54")
		want := DefaultCacheControl
		if fingerprinted {
			want = "public, max-age=31536000, immutable"
		}
		if obj.CacheControl != want {
			t.Errorf("%s Cache-Control = %q, want %q", obj.Key, obj.CacheControl, want)
		}
	}
}

// The new build's fingerprinted files go up first, the files overwritten in
// place next, and the entry points last, index.html the very last: a page
// loaded mid-deploy finds every file its entry points name.
func TestBlazorPlan_OrdersEntryPointsLast(t *testing.T) {
	plan := blazorPlan(t, blazorSite(t), "", nil)
	want := []string{
		"_framework/Client.su7785ecxb.wasm",
		"_framework/dotnet.native.befq3iek54.wasm",
		"app.css",
		"art/den.webp",
		"_framework/dotnet.js",
		"_framework/blazor.webassembly.js",
		"index.html",
	}
	if got := planKeys(plan); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("upload order = %v, want %v", got, want)
	}
}

func TestBuildPlan_PrefixesKeysAndManifest(t *testing.T) {
	plan := blazorPlan(t, blazorSite(t), "Client/", nil)
	objectByKey(t, plan, "Client/index.html")
	if plan.ManifestKey != "Client/.metaplay-site.json" {
		t.Errorf("manifest key = %q, want Client/.metaplay-site.json", plan.ManifestKey)
	}
}

// At the bucket root a site shares the top level with the game server's own
// files, and must not write under the names it uses.
func TestBuildPlan_RefusesTheGameServersNames(t *testing.T) {
	opts := Options{}

	siteDir := writeSite(t, map[string]string{"index.html": "<html>", "GameConfig/x.json": "{}"})
	if _, err := BuildPlan(siteDir, "", opts, nil, nil); err == nil || !strings.Contains(err.Error(), "GameConfig") {
		t.Errorf("BuildPlan of a site with GameConfig/ = %v, want an error naming it", err)
	}
	siteDir = writeSite(t, map[string]string{"index.html": "<html>"})
	if _, err := BuildPlan(siteDir, "volatile/", opts, nil, nil); err == nil {
		t.Errorf("BuildPlan under prefix volatile/ succeeded, want an error")
	}

	// The names only matter at the top level.
	siteDir = writeSite(t, map[string]string{"index.html": "<html>", "art/GameConfig/x.webp": "RIFF"})
	if _, err := BuildPlan(siteDir, "", opts, nil, nil); err != nil {
		t.Errorf("BuildPlan of a site with art/GameConfig/ = %v, want success", err)
	}
	if _, err := BuildPlan(writeSite(t, map[string]string{"index.html": "<html>"}), "Client/GameConfig/", opts, nil, nil); err != nil {
		t.Errorf("BuildPlan under prefix Client/GameConfig/ = %v, want success", err)
	}
}

// A project's headers rules go before its preset's, so they set the headers of
// the objects they match and leave the rest to the preset. They match the
// object's name, after the files rules renamed it.
func TestConfig_ProjectRulesOverrideThePresetForTheFilesTheyMatch(t *testing.T) {
	opts := options(t, Config{
		Preset: "blazor",
		Headers: []HeaderRuleConfig{
			{Match: "_framework/Client.*.wasm", CacheControl: "max-age=60"},
			{Match: "art/**", ContentType: "image/x-test"},
		},
	})
	plan, err := BuildPlan(blazorSite(t), "", opts, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := objectByKey(t, plan, "_framework/Client.su7785ecxb.wasm").CacheControl; got != "max-age=60" {
		t.Errorf("Cache-Control = %q, want the project's max-age=60 over the preset's", got)
	}
	if got := objectByKey(t, plan, "_framework/dotnet.native.befq3iek54.wasm").CacheControl; !strings.Contains(got, "immutable") {
		t.Errorf("Cache-Control = %q, want the preset's for a file the project's rules do not match", got)
	}
	if got := objectByKey(t, plan, "art/den.webp").ContentType; got != "image/x-test" {
		t.Errorf("Content-Type = %q, want the project's image/x-test", got)
	}
}

// A project's files rules go before the preset's, so it can, for example,
// leave out the Brotli copies; its entryFiles replace the preset's.
func TestConfig_ProjectSettingsGoBeforeThePresets(t *testing.T) {
	opts := options(t, Config{
		Preset:     "blazor",
		Files:      []FileRuleConfig{{Match: "**/*.br", Exclude: true}},
		EntryFiles: []string{"index.html"},
	})
	plan, err := BuildPlan(blazorSite(t), "", opts, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if obj := objectByKey(t, plan, "app.css"); obj.ContentEncoding != "" {
		t.Errorf("app.css Content-Encoding = %q, want none", obj.ContentEncoding)
	}
	for _, key := range planKeys(plan) {
		if strings.HasSuffix(key, ".br") || strings.HasSuffix(key, ".gz") {
			t.Errorf("plan uploads %s, want the project's and the preset's exclusions to leave out the compressed copies", key)
		}
	}
	keys := planKeys(plan)
	if keys[len(keys)-1] != "index.html" || objectByKey(t, plan, "_framework/dotnet.js").Phase != phaseContent {
		t.Errorf("upload order = %v, want only index.html as an entry point", keys)
	}
}

// The preset names the endpoints file the client reads, so a project leaves it
// out; one that sets it replaces the preset's.
func TestConfig_EndpointsFileComesFromThePreset(t *testing.T) {
	for _, tc := range []struct{ entry, want string }{
		{entry: "", want: "metaplay-endpoints.json"},
		{entry: "endpoints.json", want: "endpoints.json"},
	} {
		resolved, err := (&Config{Preset: "blazor", EndpointsFile: tc.entry}).Resolve(".", testSdkRootDir)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.EndpointsFile != tc.want {
			t.Errorf("entry's endpointsFile %q: resolved %q, want %q", tc.entry, resolved.EndpointsFile, tc.want)
		}
	}
}

// A project's own preset file is the same as the SDK's preset by name.
func TestConfig_PresetFileIsRelativeToTheProject(t *testing.T) {
	fromFile, err := (&Config{PresetFile: "sdk/WebClientPresets/blazor.yaml"}).Resolve("testdata", "no-sdk")
	if err != nil {
		t.Fatal(err)
	}
	byName, err := (&Config{Preset: "blazor"}).Resolve("no-project", testSdkRootDir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromFile.Files, byName.Files) || !reflect.DeepEqual(fromFile.EntryFiles, byName.EntryFiles) || fromFile.EndpointsFile != byName.EndpointsFile {
		t.Errorf("presetFile resolved to %+v, want the same settings as preset 'blazor', %+v", fromFile, byName)
	}
}

func TestConfig_RefusesInvalidSettings(t *testing.T) {
	absolutePreset, err := filepath.Abs("testdata/sdk/WebClientPresets/blazor.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]Config{
		"both presets":          {Preset: "blazor", PresetFile: "preset.yaml"},
		"preset path as name":   {Preset: "WebClientPresets/blazor.yaml"},
		"preset in parent":      {Preset: "../blazor"},
		"absolute presetFile":   {PresetFile: absolutePreset},
		"two patterns":          {Headers: []HeaderRuleConfig{{Match: "*", MatchRegex: ".*", CacheControl: "no-store"}}},
		"no pattern":            {Headers: []HeaderRuleConfig{{CacheControl: "no-store"}}},
		"header with no effect": {Headers: []HeaderRuleConfig{{Match: "*"}}},
		"file with no effect":   {Files: []FileRuleConfig{{Match: "*"}}},
		"excluded and renamed":  {Files: []FileRuleConfig{{Match: "*.br", StripSuffix: ".br", Exclude: true}}},
		"invalid regex":         {Headers: []HeaderRuleConfig{{MatchRegex: "(", CacheControl: "no-store"}}},
		"absolute prefix":       {Prefix: "/Client"},
		"empty entry file glob": {EntryFiles: []string{""}},
		"absolute endpoints":    {EndpointsFile: "/metaplay-endpoints.json"},
		"endpoints in parent":   {EndpointsFile: "../metaplay-endpoints.json"},
	}
	for name, config := range cases {
		if err := config.Validate(); err == nil {
			t.Errorf("%s: Validate succeeded, want an error", name)
		}
	}
}

// The preset file is read when the config is resolved, not when it is
// validated, so a project loads without it.
func TestConfig_ReadsThePresetOnlyWhenResolved(t *testing.T) {
	for _, config := range []Config{{Preset: "missing"}, {PresetFile: "testdata/missing.yaml"}} {
		if err := config.Validate(); err != nil {
			t.Errorf("%+v: Validate = %v, want success for a preset that is not read yet", config, err)
		}
		if _, err := config.Resolve(".", testSdkRootDir); err == nil {
			t.Errorf("%+v: Resolve succeeded with a missing preset, want an error", config)
		}
	}

	invalid := filepath.Join(t.TempDir(), "preset.yaml")
	if err := os.WriteFile(invalid, []byte("prefix: Client/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Config{PresetFile: filepath.Base(invalid)}).Resolve(filepath.Dir(invalid), testSdkRootDir); err == nil {
		t.Error("Resolve succeeded with a preset that sets prefix, want an error")
	}
}

// A build that names its compressed files itself, as Unity WebGL does with
// Build/game.wasm.br, keeps the name: the file rule sets only the encoding,
// and a header rule gives the type of what is inside.
func TestBuildPlan_CompressedFilesCanKeepTheirNames(t *testing.T) {
	siteDir := writeSite(t, map[string]string{"index.html": "<html>", "Build/game.wasm.br": "br:wasm"})
	opts := options(t, Config{
		Files:   []FileRuleConfig{{Match: "Build/*.br", ContentEncoding: "br"}},
		Headers: []HeaderRuleConfig{{Match: "Build/*.wasm.br", ContentType: "application/wasm"}},
	})
	plan, err := BuildPlan(siteDir, "", opts, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	obj := objectByKey(t, plan, "Build/game.wasm.br")
	if obj.ContentEncoding != "br" || obj.ContentType != "application/wasm" {
		t.Errorf("Build/game.wasm.br = %q, %q; want br, application/wasm", obj.ContentEncoding, obj.ContentType)
	}
}

func TestBuildPlan_RefusesAStripSuffixThatDoesNotFit(t *testing.T) {
	siteDir := writeSite(t, map[string]string{"app.css": "body{}"})
	opts := options(t, Config{Files: []FileRuleConfig{{Match: "*.css", StripSuffix: ".br"}}})
	if _, err := BuildPlan(siteDir, "", opts, nil, nil); err == nil {
		t.Errorf("BuildPlan succeeded, want an error for a file without the suffix to strip")
	}
}

// A generated object, such as the endpoints file, goes up with the site's
// files and takes the place of a file under its name: the deploy knows the
// environment, and a file in the build does not.
func TestBuildPlan_GeneratedObjectsReplaceFilesOfTheirName(t *testing.T) {
	siteDir := writeSite(t, map[string]string{"index.html": "<html>", "metaplay-endpoints.json": "{\"serverHost\":\"localhost\"}"})
	generated := map[string][]byte{"metaplay-endpoints.json": []byte(`{"serverHost":"nimbly.p1.metaplay.io"}`)}

	plan, err := BuildPlan(siteDir, "", Options{}, generated, nil)
	if err != nil {
		t.Fatal(err)
	}
	obj := objectByKey(t, plan, "metaplay-endpoints.json")
	if string(obj.Data) != `{"serverHost":"nimbly.p1.metaplay.io"}` || obj.LocalFile != "" {
		t.Errorf("endpoints file = %q from %q, want the generated bytes", obj.Data, obj.LocalFile)
	}
	if obj.ContentType != "application/json; charset=utf-8" || obj.CacheControl != DefaultCacheControl || obj.Phase != phaseContent {
		t.Errorf("endpoints file = %+v, want JSON, revalidated, uploaded before the entry points", obj)
	}

	// Generated objects are planned like files: unchanged when the last deploy
	// uploaded the same bytes, and kept out of the game server's names.
	store := newFakeStore()
	if _, err := Deploy(context.Background(), store, "webclient", plan, nil, time.Unix(1000, 0).UTC(), 1, nil); err != nil {
		t.Fatal(err)
	}
	if got := string(store.objects["metaplay-endpoints.json"]); got != `{"serverHost":"nimbly.p1.metaplay.io"}` {
		t.Errorf("uploaded endpoints file = %q", got)
	}
	previous, _ := ReadManifest(context.Background(), store, "")
	again, err := BuildPlan(siteDir, "", Options{}, generated, previous)
	if err != nil {
		t.Fatal(err)
	}
	if again.UploadCount() != 0 {
		t.Errorf("redeploy uploads %d objects, want none", again.UploadCount())
	}
	if _, err := BuildPlan(siteDir, "", Options{}, map[string][]byte{"GameConfig/x.json": []byte("{}")}, nil); err == nil {
		t.Errorf("BuildPlan with a generated GameConfig/x.json succeeded, want an error")
	}
}

// fakeStore records the order of uploads. Each upload gets a new tag, as an
// object store's tag changes when an object is written.
type fakeStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	headers map[string]PutRequest
	tags    map[string]string
	puts    int
	order   []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{objects: map[string][]byte{}, headers: map[string]PutRequest{}, tags: map[string]string{}}
}

func (s *fakeStore) Head(ctx context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tag, ok := s.tags[key]
	if !ok {
		return "", ErrNotFound
	}
	return tag, nil
}

func (s *fakeStore) Get(ctx context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return data, nil
}

func (s *fakeStore) Put(ctx context.Context, req PutRequest) (string, error) {
	data, err := io.ReadAll(req.Body)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts++
	tag := fmt.Sprintf("tag-%d", s.puts)
	s.objects[req.Key] = data
	s.tags[req.Key] = tag
	req.Body = nil
	s.headers[req.Key] = req
	s.order = append(s.order, req.Key)
	return tag, nil
}

// An interrupted deploy leaves objects in the bucket that the manifest does
// not describe. Redeploying the previous version, the manifest says those are
// unchanged, so they are checked against the bucket and uploaded again, as is
// an object removed outside a deploy.
func TestVerifyUnchanged_UploadsWhatTheBucketNoLongerHoldsAsRecorded(t *testing.T) {
	store := newFakeStore()
	siteDir := blazorSite(t)
	first := blazorPlan(t, siteDir, "", nil)
	if _, err := Deploy(context.Background(), store, "webclient", first, nil, time.Unix(1000, 0).UTC(), 4, nil); err != nil {
		t.Fatal(err)
	}
	previous, err := ReadManifest(context.Background(), store, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := previous.Objects["app.css"].StoreTag; got == "" || got != store.tags["app.css"] {
		t.Fatalf("manifest records app.css with tag %q, want the store's %q", got, store.tags["app.css"])
	}

	// The next deploy got as far as app.css before it was stopped, and
	// art/den.webp was removed by hand.
	if _, err := store.Put(context.Background(), PutRequest{Key: "app.css", Body: strings.NewReader("br:body{color:red}")}); err != nil {
		t.Fatal(err)
	}
	delete(store.tags, "art/den.webp")

	rollback := blazorPlan(t, siteDir, "", previous)
	if rollback.UploadCount() != 0 {
		t.Fatalf("plan of the same site uploads %d files before the check, want none", rollback.UploadCount())
	}
	stale, err := VerifyUnchanged(context.Background(), store, rollback, previous, 4)
	if err != nil {
		t.Fatal(err)
	}
	if stale != 2 || rollback.UploadCount() != 2 || objectByKey(t, rollback, "app.css").Unchanged || objectByKey(t, rollback, "art/den.webp").Unchanged {
		t.Errorf("check found %d stale files, plan uploads %d; want app.css and art/den.webp", stale, rollback.UploadCount())
	}

	store.order = nil
	manifest, err := Deploy(context.Background(), store, "webclient", rollback, previous, time.Unix(2000, 0).UTC(), 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(store.objects["app.css"]); got != "br:body{}" {
		t.Errorf("app.css after the rollback = %q, want the first deploy's bytes", got)
	}
	if manifest.Objects["app.css"].StoreTag != store.tags["app.css"] || manifest.Objects["index.html"].StoreTag != previous.Objects["index.html"].StoreTag {
		t.Errorf("manifest tags = %+v, want the new upload's for app.css and the first deploy's for index.html", manifest.Objects)
	}
}

func TestDeploy_UploadsInPlanOrderThenTheManifest(t *testing.T) {
	store := newFakeStore()
	plan := blazorPlan(t, blazorSite(t), "", nil)
	if _, err := Deploy(context.Background(), store, "webclient", plan, nil, time.Unix(1000, 0).UTC(), 1, nil); err != nil {
		t.Fatal(err)
	}

	want := append(planKeys(plan), ".metaplay-site.json")
	if strings.Join(store.order, ",") != strings.Join(want, ",") {
		t.Errorf("upload order = %v, want %v", store.order, want)
	}
	if got := string(store.objects["app.css"]); got != "br:body{}" {
		t.Errorf("app.css body = %q, want the Brotli sibling's bytes", got)
	}
	if got := store.headers["app.css"].ContentEncoding; got != "br" {
		t.Errorf("app.css Content-Encoding = %q, want br", got)
	}

	// The manifest is what tells the portal a web client is deployed here,
	// which one and when.
	manifest, err := ReadManifest(context.Background(), store, "")
	if err != nil || manifest == nil {
		t.Fatalf("ReadManifest = %v, %v; want the deploy's manifest", manifest, err)
	}
	if manifest.Name != "webclient" || !manifest.DeployedAt.Equal(time.Unix(1000, 0)) {
		t.Errorf("manifest names %q deployed at %v, want webclient at the deploy's time", manifest.Name, manifest.DeployedAt)
	}
}

// A deploy uploads only the files that changed since the last one, including
// files whose headers changed, and keeps the last deploys' objects on record:
// it deletes nothing, so they are still in the bucket.
func TestDeploy_SkipsUnchangedFilesAndKeepsEarlierObjectsOnRecord(t *testing.T) {
	store := newFakeStore()
	siteDir := blazorSite(t)
	first := blazorPlan(t, siteDir, "", nil)
	if _, err := Deploy(context.Background(), store, "webclient", first, nil, time.Unix(1000, 0).UTC(), 4, nil); err != nil {
		t.Fatal(err)
	}

	// The next build has a new fingerprinted assembly and a changed stylesheet.
	if err := os.Rename(filepath.Join(siteDir, "_framework", "Client.su7785ecxb.wasm"), filepath.Join(siteDir, "_framework", "Client.newprint00.wasm")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(siteDir, "_framework", "Client.su7785ecxb.wasm.br"), filepath.Join(siteDir, "_framework", "Client.newprint00.wasm.br")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(siteDir, "app.css.br"), []byte("br:body{color:red}"), 0o644); err != nil {
		t.Fatal(err)
	}

	previous, err := ReadManifest(context.Background(), store, "")
	if err != nil || previous == nil {
		t.Fatalf("ReadManifest = %v, %v; want the first deploy's manifest", previous, err)
	}
	second := blazorPlan(t, siteDir, "", previous)
	store.order = nil
	manifest, err := Deploy(context.Background(), store, "webclient", second, previous, time.Unix(2000, 0).UTC(), 4, nil)
	if err != nil {
		t.Fatal(err)
	}

	uploaded := strings.Join(store.order, ",")
	if uploaded != "_framework/Client.newprint00.wasm,app.css,.metaplay-site.json" {
		t.Errorf("second deploy uploaded %s, want only the new assembly, the changed stylesheet and the manifest", uploaded)
	}
	old, ok := manifest.Objects["_framework/Client.su7785ecxb.wasm"]
	if !ok || !old.DeployedAt.Equal(time.Unix(1000, 0)) {
		t.Errorf("manifest has the old assembly as %+v, %v; want it on record from the first deploy", old, ok)
	}
	if got := manifest.Objects["index.html"].DeployedAt; !got.Equal(time.Unix(2000, 0)) {
		t.Errorf("index.html deployed at %v, want the second deploy's time", got)
	}

	// Changed headers on the same bytes are an upload too.
	opts := options(t, Config{Preset: "blazor", Headers: []HeaderRuleConfig{{Match: "art/**", CacheControl: "max-age=60"}}})
	third, err := BuildPlan(siteDir, "", opts, nil, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if third.UploadCount() != 1 || objectByKey(t, third, "art/den.webp").Unchanged {
		t.Errorf("plan with a new Cache-Control for art uploads %d files, want only art/den.webp", third.UploadCount())
	}
}
