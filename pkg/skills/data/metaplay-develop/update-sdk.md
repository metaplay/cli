---
name: metaplay-develop-update-sdk
description: Upgrade the Metaplay SDK in a project to a newer version — discovers the current version, picks a target, runs `metaplay update sdk`, then walks the release-notes migration guide entry by entry. Use when the user asks to upgrade, update, or bump the SDK (e.g. "upgrade to R36", "bump SDK", "update Metaplay SDK"), or when work depends on a feature only available in a newer release. The flow is interactive — confirm version choices and migration ambiguities with the user. Load alongside the parent `metaplay-develop` page.
---

# SDK upgrade

A guided workflow for moving a project from its current Metaplay SDK release to a newer one. The CLI handles the file swap; this skill is about choosing the right target, applying the per-release migration guide, and verifying the result.

## Up-front warning

Before doing anything, tell the user:

- **This is an experimental workflow.** They must manually validate every change, especially the migrations applied from the release notes.
- **Any local edits inside `MetaplaySDK/` will be replaced.** The `metaplay update sdk` command writes a `metaplay-sdk-modifications.patch` for re-applying local SDK edits, but conflicts may need hand-resolution. Confirm the project is under version control.

## Phase 1 — Determine current and target versions

1. **Read the current SDK version.** From `MetaplaySDK/version.yaml`, extract the `version` field (format `major.minor`, e.g. `34.0`). Report it to the user.

2. **List available versions:**
   ```bash
   metaplay get sdk-versions
   ```

3. **Ask the user which target they want** (`AskUserQuestion`):
   - Latest minor within the current major (e.g. 34.1 → 34.3), or
   - Latest minor within the next major (e.g. 34.3 → 35.2).

4. **Validate the upgrade path.** Only one major version step is allowed at a time. If the user wants 33.x → 35.x, tell them to upgrade to 34.x first. Minor bumps within the same major are always fine.

## Phase 2 — Confirm the plan

Present the plan and `AskUserQuestion` for confirmation:

- Current version: X.Y
- Target version: A.B
- Type: Major upgrade / Minor upgrade
- For major upgrades: name the release notes that will be applied (e.g. `release-notes/release-35.md`).

## Phase 3 — Run the update

```bash
metaplay update sdk --to-version=<target_major>.<target_minor> --yes
```

Wait for the command to finish. If it reports unresolved patch hunks (local SDK modifications that didn't apply cleanly), stop and surface them to the user — they need to resolve those before continuing.

## Phase 4 — Apply the migration guide (major upgrades only)

Skip this phase for minor bumps.

1. **Read the release notes:**
   ```bash
   metaplay llm-docs read release-notes/release-<target_major>.md
   ```

2. **Parse the migration sections.** Look for:
   - `## Migration Guide for All Customers`
   - `## Migration Guide for Self-Service Customers`

   Each `::: details` block inside these sections is one migration task. Extract its title and contents.

3. **Track tasks.** Create a task list for the migration entries.

4. **Apply each entry in order:**
   - Mark the current task `in_progress`.
   - Read the migration text fully before touching files.
   - **If the migration is ambiguous, offers multiple options, or you're not sure it applies**, ask the user via `AskUserQuestion` — present the options clearly and wait for the answer. Do not guess.
   - Apply the changes.
   - Mark the task `completed` and move on.

5. **Skip non-applicable entries.** Some migrations are conditional (e.g. "For Games With Customized LiveOps Dashboard"). If the project doesn't use the feature, skip the entry; if you can't tell from the code, ask the user.

## Phase 5 — Verify

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

## Phase 6 — Report

Summarize:

- Previous version → new version
- Migration entries applied (and any skipped, with reason)
- Build and test status
- Manual follow-ups the user still owes — typical examples: Google Sheets schema edits, Helm chart redeploys, dashboard custom code, infrastructure config changes flagged by the release notes
- Recommend committing the result (do **not** commit yourself unless asked — see the parent skill's workflow).

If anything failed, point the user at the failing entry and the specific file/line.

## Error handling

- **`metaplay get sdk-versions` 401/403:** user needs `metaplay auth login`.
- **`version.yaml` missing:** the project isn't a Metaplay SDK project, or `SdkRootDir` in `metaplay-project.yaml` points elsewhere — check before assuming a bug.
- **Patch hunks rejected by `metaplay update sdk`:** stop and hand the conflicts to the user; do not try to silently force-apply.
- **Release notes file not found via `llm-docs read`:** the target major may not have a published release-notes page yet — confirm the version is real with `metaplay get sdk-versions` and surface the gap rather than fabricating a migration list.
