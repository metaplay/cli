---
name: metaplay-develop-init-project
description: Integrate the Metaplay SDK into an existing Unity project — `metaplay init project` (downloads SDK + scaffolds Backend/, sample scene, metaplay-project.yaml). Use when the user wants to set up Metaplay for the first time in a project or integrate the SDK into a Unity game.
---

# Initialize a Metaplay project

## `metaplay init project`

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

# Pin an SDK version (latest is the default).
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

## After `init project`

Typical next steps:

1. Open the Unity project; the SDK scaffold and HelloWorld scene are now part of the asset tree.
2. Run the server locally: `metaplay dev server` (see `metaplay-develop-local-development`).
3. Build the dashboard if customizing it: `metaplay init dashboard` (see `metaplay-develop-init-dashboard`).
4. Look at the generated `metaplay-project.yaml` — it lists the environments the project can deploy to.

## Common mistakes

- **Running `init project` over an existing Metaplay project**: don't — it tries to re-extract the SDK and may overwrite scaffolding.
- **`init project` on a fresh empty directory**: requires an existing Unity project. Create the Unity project first, then run `init project` from its root (or pass `--unity-project=<path>`).
- **Not authenticated**: `init project` fails without `metaplay auth login`. The error message tells you.
- **SDK T&Cs not accepted**: the portal will redirect; pass `--auto-agree` once the user has read them.

## Related

- After init: `metaplay-develop-local-development` to run the stack, and `metaplay-develop-code-review` once code starts being written (its rule checklist applies at write time as well as review time).
- Updating the SDK in an already-initialized project: `metaplay-develop-update-sdk`.
- Custom LiveOps Dashboard scaffolding: `metaplay-develop-init-dashboard`.
