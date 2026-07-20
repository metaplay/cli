---
name: metaplay-devops-deploy-server
description: Deploy a Metaplay game server image to a cloud environment — the build → push → deploy chain, with rollback (`remove server`), post-deploy verification (`debug server-status`, `get server-info`), and image discovery (`image list`). Use when the user asks to deploy, redeploy, ship, roll out, or roll back a server to a cloud environment.
---

# Deploy server

Get a built game server image running in a cloud environment, and verify it actually came up.

## The canonical chain

```bash
# 1. Build the Docker image locally.
metaplay build image mygame:<tag>

# 2. Deploy: implicitly pushes the local image if the tag isn't in the
#    registry yet, then runs Helm and the post-deploy health checks.
metaplay deploy server <env> mygame:<tag>
```

`deploy server` is the high-level driver: with `mygame:<tag>` it pushes-then-deploys; with just `<tag>` it assumes the image is already in the env's registry and skips the push. The push can also be done explicitly:

```bash
metaplay image push <env> mygame:<tag>
metaplay deploy server <env> <tag>
```

Use the explicit form when build and deploy happen on different machines (CI builds and pushes; another step deploys).

## Picking a tag

- **Manual local build:** if you ran `metaplay build image` without a tag, the resulting image is uniquely tagged `<projectId>:YYYYMMDD-HHMMSS-<commit>` (timestamp only if no commit is available). `build image` prints this tag — pass it to `metaplay deploy server <env> <tag>` to ship that build.
- **CI:** always pass an explicit, unique tag (e.g. `<commit>-<build-number>`) so the deployed artifact is traceable.
- **Already-pushed image:** `metaplay image list <env>` shows the 20 most-recent images in the env's registry. Pass `--limit=0` for the full list.

Always deploy a unique tag. Never reuse a tag (e.g. `latest`, `latest-local`, or a bare commit SHA — you can build the same commit more than once) for a cloud deploy; they won't work. Use the unique `<timestamp>-<commit>` tag from `build image`, or a `<commit>-<build-number>` tag in CI.

## Verification

`deploy server` runs the same health checks as `debug server-status` at the end:

- All expected pods are present, healthy, and ready.
- Client-facing domain resolves and the game server responds.
- Admin domain resolves and the admin endpoint returns success.

If those pass, the deploy is functionally up. For richer detail:

```bash
metaplay get server-info <env>           # Helm release, image, replicas, etc.
metaplay debug server-status <env>       # Re-run the health checks any time.
```

If verification fails, jump to `metaplay-devops-diagnose-server` (and `metaplay-devops-view-logs` for the actual error).

## Rolling back

```bash
metaplay deploy server <env> <previous-tag>
```

Pass the previous good tag. There is no separate "rollback" command — redeploying the prior image is the rollback. If the failed deploy left the environment broken in a way redeploy can't fix:

```bash
metaplay remove server <env>             # Tear down the Helm release.
metaplay deploy server <env> <good-tag>  # Redeploy fresh.
```

`remove server` is **destructive**: it removes the Helm release and brings the env down. Never run it against a production env unless that is the intent — confirm with the user first.

## Helm overrides

`deploy server` runs the `metaplay-gameserver` Helm chart. Pass extra args after `--` to forward them to Helm, or use these flags to override:

- `-f Backend/Deployments/<env>-server.yaml` — values file (the convention is one per env).
- `--helm-chart-version=0.7.0` / `--helm-chart-repo=…` — pin or override the chart.
- `--local-chart-path=/path/to/chart` — develop against a local chart copy.
- `--helm-release-name=…` — non-default release name (rarely needed).
- `--dry-run` — render the manifests without applying.

If the user is fighting Helm-level issues, `--dry-run` plus the relevant override flag is usually the fastest signal.

## Error patterns

- **`401` / authentication:** session expired — `metaplay auth login`.
- **`403` / permission denied:** user lacks the `api.deployments.write` permission for that environment.
- **Image not found in registry:** the tag wasn't pushed. Either pass `mygame:<tag>` to `deploy server` (auto-push), or `metaplay image push <env> mygame:<tag>` first.
- **Health checks fail after deploy:** the deploy itself probably succeeded but the server didn't come up cleanly. Go to `metaplay-devops-diagnose-server`.
- **Helm-level error (chart not found, release exists, etc.):** rerun with `--dry-run` to inspect what's being applied; the error message names the offending resource.
