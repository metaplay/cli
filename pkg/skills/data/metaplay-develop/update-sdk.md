---
name: metaplay-develop-update-sdk
description: Upgrade the Metaplay SDK in a project to a newer version. Use when the user asks to upgrade, update, or bump the SDK (e.g. "upgrade to R36", "bump SDK", "update Metaplay SDK"), or when work depends on a feature only available in a newer release.
---

# SDK upgrade

A guided workflow for moving a project from its current Metaplay SDK release to a newer one. The CLI handles the file swap; this skill is about choosing the right target, applying the per-release migration guide, and verifying the result.

## Up-front notice

Tell the user: **this is a preview workflow** — behavior is stable enough for everyday use, but they should carefully review the changes made.

## Phase 0 — Verify source control

The SDK directory is wiped and reinstalled in Phase 2. Without a clean source-controlled tree, local SDK edits — especially binary files, which the modification-patch cannot capture — can be lost with no way to recover.

1. **Identify the source control system.** Ask the user if it's not obvious from the project.

   If the project has no source control at all, print a prominent warning:

   > ⚠️  **Project is not under source control.** Running this update will replace the SDK directory in place. Any local SDK edits will be lost with no way to recover.

   Then `AskUserQuestion` whether to abort and set up source control first (recommended) or proceed anyway.

2. **Verify the working tree is clean.** List any dirty paths in the project.

   If any are present, print a prominent warning listing them:

   > ⚠️  **Pending changes detected.** Commit, submit, or shelve them before continuing — local edits may be lost during the update, and dirty files will mix into the upgrade diff and make review harder.

   Then `AskUserQuestion`: commit/submit/shelve those changes first (recommended), proceed anyway, or abort.

## Phase 1 — Determine current and target versions

1. **Read the current SDK version.** Resolve the SDK root from `metaplay-project.yaml`'s `sdkRootDir` field (defaults to `MetaplaySDK/`), then read the `version` field from `version.yaml` inside that directory and report it to the user.

2. **List available versions:**

   ```bash
   metaplay get sdk-versions
   ```

3. **Ask the user which target they want** (`AskUserQuestion`):
   - Latest minor within the current major (e.g. 34.1 → 34.3), or
   - Latest minor within the next major (e.g. 34.3 → 35.2).
   - If only one upgrade is possible, confirm that with the user.
   - Note: Major versions cannot be skipped! If the user wants 33.x → 35.x, tell them to upgrade to 34.x first. Minor bumps within the same major are always fine.

## Phase 2 — Run the update

```bash
metaplay update sdk --to-version=<target_major>.<target_minor> --yes
```

- If the CLI surfaces a privacy-policy or terms-and-conditions prompt/error, the user hasn't accepted the contracts yet. Show the URLs to the user, `AskUserQuestion` whether they accept, and only after explicit consent re-run with `--auto-agree` added.

The command replaces the SDK directory in place. If local modifications were detected, it also writes `metaplay-sdk-modifications.patch` next to the project root — it does **not** apply the patch.

**If a patch file was written:**

1. Summarize what it captured and what it didn't:
   - List the text files now stored in `metaplay-sdk-modifications.patch`.
   - Call out any **binary SDK modifications** — the patch cannot represent them, so they are gone from the new SDK and the user must restore them from version control history.
   - If any non-SDK files in the project are dirty, remind the user those edits will land in the same commit as the upgrade.

2. **Preview the patch** from the SDK root, so you see which hunks will fail before writing anything:

   ```bash
   patch -p1 --dry-run < <path-to-metaplay-sdk-modifications.patch>
   ```

3. **Apply the patch** from the SDK root. Prefer `git apply --reject` if the project uses git (cleaner `.rej` output); fall back to `patch -p1` otherwise:

   ```bash
   git apply --reject <path-to-metaplay-sdk-modifications.patch>
   # or
   patch -p1 < <path-to-metaplay-sdk-modifications.patch>
   ```

   Hunks that don't apply cleanly land in `<file>.rej` next to the target file. This is expected when the new SDK has changed the same lines the user modified.

4. **Resolve `.rej` files.** Find each one (`git status` or a recursive search for `*.rej` under the SDK root), then for every one:
   - Read the `.rej` file and the target file together.
   - If the intent is unambiguous (e.g. a renamed symbol, a moved line, a trivially relocated block), apply the change manually and delete the `.rej` file.
   - If it's ambiguous, the surrounding code has been rewritten, or two changes genuinely conflict — stop and surface that specific `.rej` to the user via `AskUserQuestion`. Do not guess.

5. **Report what's left.** After your pass, list any `.rej` files you couldn't resolve and the binary files the patch couldn't capture. Those are the user's to handle.

## Phase 3 — Apply the migration guide (major upgrades only)

Skip this phase for minor bumps.

1. **Read the release notes:**

   ```bash
   metaplay llm-docs read release-notes/release-<target_major>.md
   ```

2. **Parse the migration sections.** Look for:
   - `## Migration Guide for All Customers`
   - `## Migration Guide for Self-Service Customers`

   Each `::: details` block inside these sections is one migration task. Extract its title and contents.

3. **Apply each entry in order**, tracking progress with a task list. Read the entry fully before touching files. If the migration is ambiguous, presents multiple options, or you're not sure it applies, ask the user via `AskUserQuestion` — do not guess.

4. **Skip non-applicable entries.** Some migrations are conditional (e.g. "For Games With Customized LiveOps Dashboard"). If the project doesn't use the feature, skip the entry; if you can't tell from the code, ask the user.

## Phase 4 — Verify

After all migrations are applied:

1. **Build the server:**

   ```bash
   metaplay build server
   ```

   Treat any compile error as a migration-step failure — find the entry that owns it.

2. **Run integration tests** if the project has them:

   ```bash
   metaplay test integration
   ```

## Phase 5 — Report and generate review guide

Two outputs: a short summary inline, and a project-specific review guide written to `metaplay-sdk-update-review.md` in the project root.

**Inline summary:**

- Previous version → new version
- Build and test status (pass/fail, with the failing entry and `file:line` if anything failed)
- Count of migration entries applied / skipped
- Pointer to the review guide file

**Review guide (`metaplay-sdk-update-review.md`):**

Generate it from what was actually done in this run — do not template generic SDK-upgrade advice. Structure:

1. **Header** — previous version, new version, date, and a one-line note that the user owns final validation.

2. **Per migration entry applied**, in the order applied:
   - Entry title (verbatim from the release notes section).
   - What the migration changed (1–2 sentences, in this project's terms).
   - **Files touched in this project** — concrete `path/to/file:line` references from the edits actually made. If no project files were touched (SDK-internal migration), say so.
   - **What to verify** — 1–3 specific things tied to the change: a behavior to exercise, an edge case, a value to spot-check, a save-game compatibility concern. Skip vague "test it works"; if you can't name something specific, say "no project-side verification needed" and explain why.
   - Any ambiguity the user resolved via `AskUserQuestion` during this entry — record the question and their answer.

3. **Per migration entry skipped** — title and the reason it was skipped (e.g. "project does not use custom dashboard"). The user can scan this to sanity-check the skip decisions.

4. **Patch reapply status** — whether `metaplay-sdk-modifications.patch` was applied, and a list of any `.rej` files still outstanding.

5. **Manual follow-ups the user still owes** — pulled from the release notes' non-code instructions: Google Sheets schema edits, Helm chart redeploys, dashboard custom code, infrastructure config changes, etc. Include only items the release notes actually flagged for this upgrade.

6. **Recommended next step** — "review this file, then commit". Do **not** commit yourself unless asked (see the parent skill's workflow).

## Error handling

- **`metaplay get sdk-versions` 401/403:** user needs `metaplay auth login`.
- **`version.yaml` missing:** the project isn't a Metaplay SDK project, or `sdkRootDir` in `metaplay-project.yaml` points elsewhere — check before assuming a bug.
- **Modification detection fails (portal lookup or download error):** stop and report the error; offer `--skip-patch` only if the user confirms they'll preserve SDK edits another way.
- **Patch hunks rejected when applying `metaplay-sdk-modifications.patch`:** stop and hand the `.rej` files to the user; do not try to silently force-apply.
- **Binary SDK files modified:** the patch cannot capture them and they will be lost. Ask the user to back them up before running the update.
- **Release notes file not found via `llm-docs read`:** the target major may not have a published release-notes page yet — confirm the version is real with `metaplay get sdk-versions` and surface the gap rather than fabricating a migration list.
