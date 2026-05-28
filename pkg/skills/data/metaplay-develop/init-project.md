---
name: metaplay-develop-init-project
description: Integrate the Metaplay SDK into an existing Unity project — `metaplay init project` (downloads SDK + scaffolds Backend/, sample scene, metaplay-project.yaml) or `metaplay init project-config` (retrofit `metaplay-project.yaml` onto an existing Metaplay-flavored project). Use when the user wants to set up Metaplay for the first time in a project, integrate the SDK into a Unity game, or get an existing Metaplay project recognized by the CLI.
---

# Initialize a Metaplay project

Two distinct commands, two distinct situations. Pick first, then act.

## Which command — `init project` vs `init project-config`

| Situation | Command |
|---|---|
| **No `MetaplaySDK/` directory yet.** Greenfield: a Unity project exists, but Metaplay has never been added to it. | `metaplay init project` |
| **`MetaplaySDK/` already exists**, but `metaplay-project.yaml` is missing or stale (e.g. an older project predating the CLI's project-config format). | `metaplay init project-config` |

If you're not sure, look at the project directory: presence of `MetaplaySDK/` is the discriminator.

## `metaplay init project` — greenfield

Integrates the Metaplay SDK into an existing Unity project. The wizard:

1. Downloads the SDK from the Metaplay portal (requires `metaplay auth login` and accepted T&Cs in the portal).
2. Extracts the SDK to `MetaplaySDK/`.
3. Scaffolds:
   - `metaplay-project.yaml` (project config)
   - `<unity-project>/Assets/MetaplayHelloWorld` (sample scene)
   - `<unity-project>/Assets/SharedCode`
   - `<unity-project>/Assets/StreamingAssets/...`
   - `Backend/` (the server-side .NET projects)
4. Adds the Metaplay Client SDK reference to the Unity `package.json`.

```bash
# Interactive wizard.
metaplay init project

# Specify the portal project ID up front.
metaplay init project --project-id=fancy-gorgeous-bear

# Pin an SDK version (latest is the default; minimum supported is 34.0).
metaplay init project --sdk-version=34.0

# Use a locally downloaded SDK archive (offline / customized SDK).
metaplay init project --sdk-source=metaplay-sdk-release-34.0.zip

# Skip the sample scene (cleaner setup for teams that don't want HelloWorld).
metaplay init project --no-sample

# CI / scripted: auto-accept all prompts.
metaplay init project --auto-agree --yes
```

Prerequisites:

- The project ID exists in the Metaplay portal (created via portal UI or `metaplay` portal commands — outside this skill's scope).
- The user is logged in: `metaplay auth login`.
- The user has accepted SDK T&Cs in the portal (or pass `--auto-agree`).
- A Unity project exists in the working directory. If detection fails, pass `--unity-project=<path>` explicitly.

## `metaplay init project-config` — retrofit existing project

The project already has Metaplay code (an `MetaplaySDK/` directory, a `Backend/` directory, etc.), but the CLI doesn't recognize it because there's no `metaplay-project.yaml`. This command generates that file by auto-detecting paths.

Requires Metaplay SDK **32.0 or later** in the project.

What it auto-detects (override flags shown):

- Unity project location — `--unity-project`
- `MetaplaySDK/` — `--sdk-path`
- Backend directory — `--backend-path`
- Dashboard directory (if present) — `--dashboard-path`
- Shared code directory — `--shared-code-path`
- .NET runtime version — `--dotnet-version`

```bash
# Auto-detect and prompt for confirmation.
metaplay init project-config

# Specify the portal project ID.
metaplay init project-config --project-id=lovely-wombats-build

# Non-interactive (CI).
metaplay init project-config --yes
```

The command shows a summary of detected paths and asks for confirmation before writing the file. After running, `metaplay <command>` works from the project directory without `-p`.

## After `init project`

Typical next steps:

1. Open the Unity project; the SDK scaffold and HelloWorld scene are now part of the asset tree.
2. Run the server locally: `metaplay dev server` (see `metaplay-develop-local-development`).
3. Build the dashboard if customizing it: `metaplay init dashboard` (see `metaplay-develop-init-dashboard`).
4. Look at the generated `metaplay-project.yaml` — it lists the environments the project can deploy to.

## Common mistakes

- **Running `init project` over an existing Metaplay project**: don't — it tries to re-extract the SDK and may overwrite scaffolding. Use `init project-config` to register an existing project with the CLI.
- **`init project` on a fresh empty directory**: requires an existing Unity project. Create the Unity project first, then run `init project` from its root (or pass `--unity-project=<path>`).
- **Not authenticated**: both commands fail without `metaplay auth login`. The error message tells you.
- **SDK T&Cs not accepted**: the portal will redirect; pass `--auto-agree` once the user has read them.

## Related

- After init: `metaplay-develop-local-development` to run the stack, and the parent `metaplay-develop` page's rule sub-skills (`review-actions`, `review-configs`, `review-models`) once code starts being written.
- Updating the SDK in an already-initialized project: `metaplay-develop-update-sdk`.
- Custom LiveOps Dashboard scaffolding: `metaplay-develop-init-dashboard`.
