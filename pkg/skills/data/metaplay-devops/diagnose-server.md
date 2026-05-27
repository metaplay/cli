---
name: metaplay-devops-diagnose-server
description: Diagnose a misbehaving Metaplay game server in a cloud environment — server is down, deploy failed health checks, latency spiked, OOM, or pods crash-looping. Routes the user from symptom to the right tool — `debug server-status` for health, `debug logs` (see `view-logs` sub-skill) for what the server said, `debug shell` for in-pod inspection, `debug database` for DB shell, and the profile sub-skills for performance/memory issues.
---

# Diagnose a running cloud server

Whole-server triage. For a single player's incident report from the dashboard, use `metaplay-develop/incident-analysis` instead.

## Symptom → first tool

| Symptom | First call |
|---|---|
| Deploy reported health-check failures | `metaplay debug server-status <env>` to re-run and read the failure detail; then `metaplay debug logs <env>` for the actual error. |
| Site is down or unreachable | `metaplay debug server-status <env>` — tells you which check failed (pod readiness vs DNS vs admin endpoint vs client endpoint). |
| Server up but throwing errors | `metaplay debug logs <env> --since=15m` then filter for `ERR`/`Exception`. See `metaplay-devops/view-logs`. |
| Latency spike, server slow | `metaplay debug collect-cpu-profile <env>` — see `metaplay-devops/cpu-profiling`. |
| OOM, server memory growing | `metaplay debug collect-heap-dump <env>` — see `metaplay-devops/memory-profiling`. |
| Pod crash-looping or stuck | `metaplay debug server-status` to confirm, then `metaplay debug logs <env> --pod <name>` for the dying pod's output. |
| Suspect data issue (corrupt entity, missing row) | `metaplay debug database <env>` — read-only by default; pass `--read-write` deliberately. |
| Need to poke around inside a pod | `metaplay debug shell <env> [pod]` — ephemeral debug container attached to the game server container. |

## Health checks (what `server-status` actually checks)

`metaplay debug server-status <env>` runs the same checks as `metaplay deploy server`:

1. All expected pods are present, healthy, and ready.
2. Client-facing domain resolves correctly.
3. Game server responds to client traffic.
4. Admin-facing domain resolves correctly.
5. Admin endpoint returns success.

Failure in (1) is a Kubernetes / image / config issue. Failures in (2)/(4) are DNS or load-balancer issues, not game-server bugs. Failures in (3)/(5) usually mean the pod is up but the process itself is broken — go to logs.

## Pods and the shard layout

`metaplay debug logs` and other debug commands target the game server pods. Pod names follow `<role>-<index>` (e.g. `service-0`, `entity-0`, `all-0` for single-pod deployments). When a command optionally takes a pod argument, omitting it triggers an interactive picker if multiple pods are running.

```bash
metaplay debug shell <env>             # Pick interactively (or implicit if only one pod).
metaplay debug shell <env> service-0   # Direct target.
```

Inside the debug shell, the host filesystem of the game server container is available, plus the standard diagnostic tools from the `metaplay/diagnostics:latest` image (`dotnet-trace`, `dotnet-gcdump`, `dotnet-dump`, `jq`, `mariadb`, etc.).

## Database access

```bash
metaplay debug database <env>              # Read-only replica, first shard, interactive.
metaplay debug database <env> 1            # Different shard.
metaplay debug database <env> --read-write # Deliberately read-write; ask the user before running.
metaplay debug database <env> --query "SELECT COUNT(*) FROM Players"
metaplay debug database <env> --query-file ./q.sql --output result.txt
```

Use `--query` for non-interactive one-shots; pass mariadb client flags after `--` (e.g. `-- -N -B --raw` for binary-safe extraction).

## Reading what the dashboard says

For server-wide observability data the admin API exposes (active connections, incident statistics, etc.):

```bash
metaplay debug admin-request <env> GET <api-path>
```

Examples in `metaplay-develop/incident-analysis` (it uses `admin-request` to fetch per-player incidents).

## Closing the loop

Once a symptom is narrowed down to a code-level bug (stack trace in logs, hot method in CPU profile, leak path in heap dump), hand off to `metaplay-develop` — the rule sub-skills (`review-actions`, `review-configs`, `review-models`) cover the kinds of mistakes that produce desyncs, leaks, and exceptions.

## Error patterns

- **`401` / auth:** `metaplay auth login`.
- **`403` / permission denied:** user lacks the relevant per-env permission (e.g. `api.logs.view`, `api.shell.access`, `api.deployments.read`).
- **`env not found`:** name doesn't match `metaplay-project.yaml`.
- **`no pods found`:** the env has no running game server. `metaplay deploy server` first; see `metaplay-devops/deploy-server`.
