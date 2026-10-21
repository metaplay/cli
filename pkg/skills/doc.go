/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

// Package skills implements the loading, addressing, installation, and
// removal of agent "skills" — markdown documents (with YAML frontmatter)
// that AI coding harnesses such as Claude Code, Cursor, Codex, and others
// load to learn how to work with a particular SDK or codebase.
//
// # The wrapper / payload model
//
// At install time only a thin SKILL.md "wrapper" lands on disk. The wrapper
// carries the same frontmatter (name, description) as the canonical skill
// plus two stamps that make safe re-installs and removals possible:
//
//   - managed-by: metaplay-cli — the literal value identifying us as the
//     owner of the file. Anything without this exact value is treated as
//     user-authored and never overwritten or removed.
//   - metaplay-cli-version: <semver> — the version of the tool that wrote
//     the file. Re-installing with an older version is a no-op; re-installing
//     with the same/newer version overwrites. --force bypasses the gate.
//
// The wrapper body is a tiny pointer (e.g. "metaplay skills get <name>") —
// the canonical content stays embedded in the binary and is fetched on
// demand. This keeps user-repo diffs small and lets sub-skills update
// when the binary updates without any extra sync step.
//
// # Bundled skills
//
// This package ships its own skill payload via go:embed. Call
// EmbeddedFS to obtain it as an fs.FS, e.g.:
//
//	loaded, err := skills.LoadAll(skills.EmbeddedFS())
//
// Library consumers wanting custom data can pass any fs.FS to LoadAll
// (for instance an os.DirFS rooted at their own data tree, or a synthetic
// testing/fstest.MapFS in tests).
//
// # Levels of API
//
// Two layers are exposed, depending on how much wiring you need:
//
//   - Low-level: Install / Remove take a fully-resolved options struct
//     and produce action records. Use these when you've already worked
//     out the scope, root directory, and target list yourself.
//
//   - High-level orchestrators: RunInstall / RunRemove take a Request that
//     can leave scope and targets unspecified, in which case they consult
//     a Prompter (interactive UI) or fall back to detection / defaults.
//
// CLI front-ends typically use the high-level orchestrators; programmatic
// callers driving fully-specified inputs may prefer the lower-level
// Install / Remove directly.
//
// # Tests and tempdirs
//
// Both the high-level requests take ProjectDir / UserDir as caller-supplied
// strings rather than calling os.UserHomeDir / os.Getwd themselves, so
// tests can drive user-scope flows under a t.TempDir() without touching
// the real $HOME. See run_install_test.go for examples.
package skills
