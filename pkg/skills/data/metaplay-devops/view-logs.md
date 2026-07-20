---
name: metaplay-devops-view-logs
description: Stream and filter game server logs from a Metaplay cloud environment via `metaplay debug logs`. Covers time windows (`--since`, `--since-time`), live tailing (`-f`), per-pod filtering (`--pod`), and combining with grep / jq for triage. Use when the user asks to view logs, tail logs, check what the server said, or investigate a specific error window.
---

# View server logs

`metaplay debug logs` is the only way to pull server logs through the CLI. It aggregates across all running game server pods in an environment unless `--pod` is set.

## Common patterns

```bash
# Everything available (the default).
metaplay debug logs <env>

# Tail live until Ctrl+C.
metaplay debug logs <env> -f

# Last 15 minutes only (use this most of the time).
metaplay debug logs <env> --since=15m

# Last 3 hours.
metaplay debug logs <env> --since=3h

# From a precise instant (UTC, RFC 3339). Useful when a dashboard incident
# gave you `OccurredAt`.
metaplay debug logs <env> --since-time=2026-04-12T15:04:05Z

# Single pod only (when one pod is misbehaving).
metaplay debug logs <env> --pod service-0
```

`--since` accepts standard Go duration syntax: `30s`, `15m`, `3h`. The two `--since*` flags are mutually exclusive; pick whichever shape the question takes.

## Pod naming

Pod names follow `<role>-<index>` — e.g. `service-0`, `entity-0`, `all-0`. Discover the running pod set via `metaplay debug server-status <env>`.

## Filtering and triage

The CLI emits plain text lines. Pipe into the shell tools that fit the question:

```bash
# Errors only in the last hour.
metaplay debug logs <env> --since=1h | grep -E "ERR|Exception"

# Watch live for a specific pattern.
metaplay debug logs <env> -f | grep --line-buffered NullReferenceException

# Save a window to a file before the live tail wraps.
metaplay debug logs <env> --since=30m > logs-snapshot.txt
```

If the log lines look like JSON, pipe through `jq -R 'fromjson?'` to drop non-JSON noise and then filter by field. Output format depends on how the game server is configured to log.

## Picking the right window

- **You're chasing a known incident timestamp:** `--since-time=<exact>` is precise; use it.
- **Deploy just finished and looks broken:** `--since=10m` covers the deploy window.
- **Crash-looping pod:** `--pod <name> --since=15m` gets that pod's last few startup attempts.
- **No idea, just want to see what's going on:** `--since=15m -f` shows recent history then tails live.

Avoid omitting `--since` on a busy env — the default is *all available logs* and the volume can be large.

## Limitations

- The CLI streams what Kubernetes retained — logs older than the cluster's retention window (typically a few days) are not available here. For longer-horizon investigations, point the user at the environment's Grafana at `<admin-hostname>/grafana` (e.g. `https://tiny-squids-admin.p1.metaplay.io/grafana`). The admin hostname is shown by `metaplay get environment-info <env>`.
- `metaplay debug logs` cannot directly grep server-side. Filter on the local end (`| grep`).
- If logs stop unexpectedly during `-f`, the pod likely restarted. Rerun, or pull `--since=<small>` to confirm.

## Error patterns

- **`401` / auth:** `metaplay auth login`.
- **`403`:** user lacks `api.logs.view` for the env.
- **`no pods found`:** no game server is deployed — `metaplay deploy server` first.
- **`pod not found`:** `--pod` value doesn't match any running pod; check `metaplay debug server-status <env>`.
