---
name: metaplay-develop-local-development
description: Run a Metaplay game server, LiveOps Dashboard, and BotClient locally for development — `metaplay dev server`, `metaplay dev dashboard`, `metaplay dev image`, plus the relationship to `metaplay build server` / `build dashboard` / `build image`. Use when the user asks to run the game locally, test on a local server, start the dashboard, or iterate without deploying. Cover the typical inner-loop, port layout, and when to test the real Docker image vs the raw .NET process.
---

# Local development loop

The daily inner loop for Metaplay project work: run the server, the LiveOps Dashboard, and a Unity client (or BotClient) on the developer's own machine, with fast feedback on code changes.

## The three pieces

A live local stack typically runs three processes:

| Piece | Command | What it serves |
|---|---|---|
| Game server (.NET) | `metaplay dev server` | TCP game traffic + admin API. Connect Unity clients and the dashboard to it. |
| LiveOps Dashboard (Vue) | `metaplay dev dashboard` | The admin UI at `http://localhost:5550` once the server is up. |
| BotClient | `metaplay dev botclient` | Synthetic clients for load testing or scripted scenarios. Optional. |

`metaplay dev server` is roughly `cd Backend/Server && dotnet run`. `metaplay dev dashboard` is the Vue.js dev server with hot reload. They're separate processes — run them in separate terminals (or background one of them).

The LiveOps Dashboard is **served by the game server** at `http://localhost:5550` when you run `metaplay dev server` — that uses the pre-built dashboard bundled into the project (`Backend/PrebuiltDashboard/` or built from `metaplay build dashboard`). You only need `metaplay dev dashboard` separately when you're actively editing dashboard code and want hot reload — it overlays the dev Vue server on top of the running game server.

## Inner-loop patterns

```bash
# Run the server. Press 'q' to stop. The dashboard is reachable at http://localhost:5550.
metaplay dev server

# Auto-restart on .cs changes (file watcher).
metaplay dev server --watch

# Pass extra args to dotnet run (after --).
metaplay dev server -- -LogLevel=Warning
metaplay dev server -- -ExitAfter=00:00:30
```

For dashboard development:

```bash
# In one terminal: the game server (which also serves the bundled dashboard).
metaplay dev server

# In another: Vue dev server with hot reload. Use the URL the command prints.
metaplay dev dashboard
```

For testing the real Docker image (the thing that actually ships to the cloud):

```bash
metaplay build image                       # Produces <projectId>:<timestamp>-<commit>, alias 'latest-local'.
metaplay dev image latest-local            # Run the just-built image in Docker.
```

Use `dev image` when:

- You suspect a difference between `dotnet run` behavior and the production image (e.g. config file resolution, missing assets).
- You're debugging Dockerfile changes.
- You want to test the integrated build right before pushing.

Stick with `dev server` for everything else — it's faster to iterate.

## Connecting a Unity client locally

The Unity client connects to whichever server URL is configured in its `MetaplayClient` setup. For local work, that's `localhost:9339` (the default game server port) and the bundled dashboard URL `http://localhost:5550`. If the client can't connect, the usual suspects are:

- The server isn't actually running — check `metaplay dev server` is up.
- A firewall blocked the port — `localhost` shouldn't but corporate setups vary.
- The Unity client is built with a non-local server URL (`*.metaplay.io`) — flip the connection config back to localhost.

## Validating changes

See the parent skill's "Validating changes locally" section: `metaplay build server` for a fast compile check, `dotnet test Backend/SharedCode.Tests` (and `Backend/Server.Tests` if present) for unit tests. The local dev stack itself is *not* a substitute for tests — desync bugs and other determinism-class issues won't always reproduce against a fresh server with one connected client.

## Dashboard build artifacts gotcha

If `metaplay dev dashboard` or `metaplay build dashboard` starts behaving strangely (stale assets, weird Node errors, dependency mismatches), run:

```bash
metaplay dev clean-dashboard-artifacts
```

That clears the Vite/pnpm caches under `Backend/Dashboard/`. Re-run the build/dev command afterward.

## Stopping cleanly

`metaplay dev server` listens for `q` on stdin and shuts down gracefully. Ctrl+C also works but may leave the .NET process briefly orphaned on Windows — the CLI handles this defensively but graceful is preferred when possible. `metaplay dev dashboard` and `metaplay dev image` exit on Ctrl+C.

## Error patterns

- **`.NET SDK not found / wrong version`:** `metaplay dev server` validates the .NET SDK on each run. Install per the error's suggested URL.
- **`port already in use`:** another local process holds the port. Find and kill it, or change the port via the relevant `Options.*.yaml`.
- **Dashboard build fails with `pnpm`/`node` complaints:** `metaplay dev clean-dashboard-artifacts`, then rebuild.
- **`MetaplaySDK/` not found:** the project hasn't been initialized. See `metaplay-develop/init-project`.
