---
name: metaplay-develop-init-dashboard
description: Scaffold a customizable LiveOps Dashboard in an existing Metaplay project — `metaplay init dashboard` populates `Backend/Dashboard/`, sets up the pnpm workspace and VS Code workspace, updates `metaplay-project.yaml`, and runs `pnpm install`. Use when the user wants to customize the LiveOps Dashboard UI, add dashboard pages, or move from the SDK's pre-built dashboard to a project-owned one.
---

# Initialize a custom LiveOps Dashboard

The Metaplay SDK ships with a pre-built LiveOps Dashboard, served by the game server out of the box. This skill is for the next step: scaffolding a *project-owned* dashboard so the team can add pages, branding, custom widgets, and game-specific tooling.

Run when:

- The user wants to customize the dashboard UI beyond what's possible with the SDK's pre-built version.
- The user wants to add new dashboard pages or widgets specific to this game.
- The user is following a tutorial that says "scaffold the dashboard".

Don't run when:

- The project already has `Backend/Dashboard/` populated — the command will refuse, or fight existing files. Check first.
- The user just wants to run the SDK dashboard locally — that's `metaplay dev server` (which serves the pre-built dashboard). No init needed.

## What it does

`metaplay init dashboard` performs four steps:

1. Populates a fresh Vue.js dashboard project under `Backend/Dashboard/` (extending the SDK's dashboard component library).
2. Initializes workspace files at the project root:
   - `pnpm-workspace.yaml` — pnpm workspace config so the dashboard shares dependencies with the SDK.
   - `Backend/dashboard.code-workspace` — VS Code workspace for backend + dashboard.
3. Updates `metaplay-project.yaml` to point at the new custom dashboard.
4. Runs `pnpm install` to generate `pnpm-lock.yaml`.

## Running

```bash
metaplay init dashboard
```

No flags; it's a one-shot setup. The command is idempotent only in the loose sense — re-running on an already-scaffolded project will fail or produce a mess. If you need to reset the dashboard scaffolding, delete `Backend/Dashboard/` and re-run.

## Prerequisites

- `metaplay-project.yaml` exists (the project is initialized — see `metaplay-develop-init-project` if not).
- Node.js and `pnpm` are installed and meet the version requirements the SDK declares. The command will fail with a clear error if not.
- No existing `Backend/Dashboard/` directory (or you accept it being clobbered).

## After scaffolding

Typical workflow:

```bash
# Run the dashboard with hot reload (one terminal).
metaplay dev dashboard

# Run the game server it talks to (another terminal). Press 'q' to stop.
metaplay dev server

# Build the dashboard before committing or shipping.
metaplay build dashboard
```

To bundle a pre-built dashboard into the repo (so people without Node/pnpm can still run the project locally):

```bash
metaplay build dashboard --output-prebuilt
# Commit Backend/PrebuiltDashboard/ to version control.
```

See `metaplay-develop-local-development` for the broader local-dev loop.

## Troubleshooting

If `pnpm install` or subsequent builds misbehave:

```bash
metaplay dev clean-dashboard-artifacts
```

This clears the Vite/pnpm caches under `Backend/Dashboard/`. Re-run `metaplay build dashboard` or `metaplay dev dashboard` afterward.

If the SDK has been updated and dashboard build starts failing, that's usually a peer-dependency drift — `metaplay update sdk` walks the SDK release-notes migration guide for changes that affect the dashboard. See `metaplay-develop-update-sdk`.

## Error patterns

- **`Backend/Dashboard already exists`:** the dashboard is already scaffolded. Either delete the directory and re-init (destructive — confirm with the user), or skip the init and go straight to `metaplay dev dashboard`.
- **`pnpm-workspace.yaml already exists`:** same situation; the user has already done this.
- **Node / pnpm version too old:** install the required versions; the SDK pins these and the command's error names the minimum.
- **`metaplay-project.yaml not found`:** run `metaplay init project` (or `metaplay init project-config`) first — see `metaplay-develop-init-project`.
