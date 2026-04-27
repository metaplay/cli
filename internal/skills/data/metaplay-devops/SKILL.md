---
name: metaplay-devops
description: Diagnose and respond to Metaplay game server issues in cloud environments — production incidents, pod health checks, performance investigations, log/metrics triage, CPU profiles, heap dumps, database access, config rollbacks, per-player incident report triage, and escalation to Metaplay support. Use whenever the user asks "why is my server down / slow / crashing / rejecting logins", mentions CrashLoopBackoff, OOM, EntityAsk errors, desyncs at scale, latency spikes, a bad config publish, or anything involving `metaplay debug`, `metaplay deploy`, Grafana, Loki, or kubectl against a live environment. Also trigger when the user pastes a dashboard incident URL (e.g. `https://*-admin.p1.metaplay.io/players/.../incidentReports/...`), asks about "the latest incidents" / "recent incidents", or wants a specific player's crash/desync report analyzed. Trigger proactively when the user names an environment (a Metaplay-hosted hostname like `*.p1.metaplay.io` or an env id from `metaplay-project.yaml`) together with any production-sounding symptom. Prefer this skill over general troubleshooting advice for any live-ops or cloud-deployment question.
---

# Metaplay devops

Operational playbook for diagnosing live Metaplay game server deployments. Covers the `metaplay debug` CLI, Grafana/Loki log patterns, a symptom-to-action table, and incident response scenarios.

All diagnostics route through the `metaplay` CLI from the project directory (or any subdirectory inside it). The CLI reads `metaplay-project.yaml` and figures out cluster/admin endpoints from the environment name or id. Every command accepts `--help` for full options.

## Guardrails — read before acting on production

Production operations destroy evidence or disrupt players if done in the wrong order. Apply these rules before running anything.

- **Do NOT restart pods before collecting a CPU profile or heap dump** — the restart clears the very state you need to diagnose the problem. Capture first, restart second.
- **`metaplay debug collect-heap-dump` freezes the server process for seconds to minutes.** Never run it on production during normal operation — it pauses gameplay for all connected players. Use it during a maintenance window, or reproduce on a dev/staging environment first. `--mode=dump` is strictly more intrusive than the default `gcdump`.
- **`metaplay debug database` defaults to read-only.** Never pass `--read-write` against production unless the user has explicitly authorized a specific query and you both understand its effect on live data.
- **`metaplay database reset` wipes the environment's database.** Never run it on production. Only run it on dev/staging after explicit confirmation.
- **Check `status.metaplay.io` first.** If Metaplay infrastructure is down, no diagnostic action on your side will fix it — wait, monitor, and notify stakeholders.
- **Do not suggest `kubectl exec` into the server container directly.** The game server image is distroless (no shell) and prefers ephemeral diagnostic containers via `metaplay debug shell`.
- **Data corruption suspected? Do not touch the database.** Escalate to Metaplay support (`support@metaplay.io`). Manual DB surgery without explicit playbook can make recovery impossible.
- **Destructive or shared-state commands require explicit user confirmation** in the current turn: `metaplay deploy server` (redeploy/rollback), `metaplay remove server`, `metaplay database reset|import-snapshot`, `--read-write` DB access, and `collect-heap-dump` on production.

## Environment selection

Every `metaplay debug ...` command takes an `ENVIRONMENT` argument (name or id).

1. Read `metaplay-project.yaml` from the project root to see configured environments — each has a `name` (used with the CLI) and an `adminApiUrl` / hostname.
2. If the user pasted a dashboard or Grafana URL like `https://<env-id>-admin.p1.metaplay.io/...`, match the hostname prefix against `metaplay-project.yaml` to recover the env name.
3. If there are multiple environments and the user hasn't named one, ask which to target before running anything.
4. The CLI accepts either the full id or a shorthand suffix (e.g. `nimbly` for `lovely-wombats-build-nimbly`), as long as it's unambiguous.

## `metaplay debug` CLI reference

| Command | Purpose | Notes |
|---|---|---|
| `metaplay debug server-status <env>` | Pod health, domain checks, admin endpoint reachability. Non-invasive. | First call in almost any investigation. |
| `metaplay debug logs <env>` | Recent server logs, all pods. | `--follow`/`-f` streams. `--pod <name>` filters to one pod. `--since=3h` / `--since-time=2024-12-27T15:04:05Z` for time ranges. Pipe into `grep` or `Select-String` to filter. |
| `metaplay debug admin-request <env> <METHOD> <PATH>` | Hit the game server admin API (the same API the dashboard uses). Example: `metaplay debug admin-request nimbly GET api/hello`. | Non-invasive for GETs. Supports `--body`, `--file`, `--content-type`, `-o` (binary download). Pipe JSON through `jq` for readability. |
| `metaplay debug collect-cpu-profile <env> [pod]` | 30-second CPU trace via `dotnet-trace`, copied to local disk as `profile-*.nettrace`. Health probes are auto-overridden for the duration. | `--duration 60`, `--format speedscope\|chromium\|nettrace`. Safe on production — low overhead. Analyze in Visual Studio or upload to Speedscope. |
| `metaplay debug collect-heap-dump <env> [pod]` | Heap dump via `dotnet-gcdump` (default) or `dotnet-dump` (`--mode=dump`). Copied to local disk. | **FREEZES THE PROCESS** for seconds–minutes. Never on production under normal conditions. Health probes are auto-overridden so Kubernetes doesn't kill the pod mid-dump. |
| `metaplay debug shell <env> [pod]` | Opens a Kubernetes ephemeral diagnostics container (`metaplay/diagnostics:latest`) targeting the server pod. | Distroless server image has no shell — this is the correct way to get one. Use only when you know which `dotnet-*` tool you're about to run. |
| `metaplay debug database <env> [shard]` | MariaDB/MySQL client against the environment's DB shard. | Read-only by default. `--query "<sql>"` or `--query-file <file>` for non-interactive use. `--read-write` connects to the writable replica — dangerous on production. Shards are 0-indexed. |

Helpers that aren't under `debug`:

| Command | Purpose |
|---|---|
| `metaplay get server-info <env>` | Static deployment info — server version, image tag, helm values summary. |
| `metaplay get kubeconfig <env> --type=dynamic --output=kubeconfig.yaml` | Generate a `kubeconfig` so `kubectl` can hit the cluster. `dynamic` re-fetches credentials via the CLI (recommended for humans); `static` embeds them (CI/CD). |
| `metaplay deploy server <env> <image>:<tag>` | Deploy or roll back to a specific image tag. **Destructive / visible to players** — confirm with the user. |
| `metaplay database export-snapshot <env> <file>` / `import-snapshot` | Snapshot small DBs (<1GB) for dev-to-dev moves. Not a production backup tool. |
| `metaplay database reset <env>` | Wipe all DB state. **Never on production.** |

For operations the CLI doesn't cover, fall back to `kubectl` with the generated kubeconfig:

```bash
metaplay get kubeconfig <env> --type=dynamic --output=kubeconfig.yaml
kubectl get pods --kubeconfig kubeconfig.yaml -l app=metaplay-server
kubectl describe pod <pod> --kubeconfig kubeconfig.yaml
```

## Symptom → diagnostic path

Quick lookup to skip straight to the right tool.

| Symptom | First check | Second check | Primary tool |
|---|---|---|---|
| CPU trending up unexpectedly | `game_entity_active` — is entity count growing? | Was there a recent deployment? | `metaplay debug collect-cpu-profile <env>` |
| Memory not returning to baseline | GC allocation rate per CCU changing? | Entity state size growth across releases? | `metaplay debug collect-heap-dump <env>` (heed the warning) |
| EntityAsk errors spiking | Which entity kind in logs? | Correlated with a specific player action? | `metaplay debug logs <env>` + Loki |
| Login rate sudden drop | `status.metaplay.io` | Auth-related errors in logs? | `metaplay debug logs <env>` |
| DB latency spike | Query count rate changed? | Specific entity kind over-reading/writing? | `metaplay debug server-status <env>` + Grafana `game_db_query_duration` |
| Pods in `CrashLoopBackoff` | `metaplay debug logs <env> --pod <pod>` | OOM kill? (`server-status`) | Logs → identify exception → rollback if recent deploy caused it |
| Dashboard 503 after a deploy | Server failing to start? (`logs` + search for `Launching application`) | Look for the first exception after startup | Roll back with `metaplay deploy server <env> <previous-tag>` |
| Errors began right after config publish | Was a config just published? | Client parse errors in logs? | **Roll back via Dashboard → Game Config → Publish Previous Version** |

## Log filtering — CLI

```bash
# Linux/macOS
metaplay debug logs <env> | grep "AuthenticationError"
metaplay debug logs <env> | grep -i "error"

# Windows PowerShell
metaplay debug logs <env> | Select-String -Pattern "AuthenticationError"
metaplay debug logs <env> | Select-String -Pattern "error" -Context 2,2
```

## Log filtering — Loki (Grafana "Explore" → Loki → Code mode)

Metaplay-hosted Grafana lives at `https://<env-id>-admin.p1.metaplay.io/grafana`. Open **Explore**, pick the Loki data source, switch the query editor from **Builder** to **Code**, and paste:

```logql
# All errors and warnings
{app="metaplay-server", loglevel=~"ERR|WRN"}

# EntityAsk errors only
{app="metaplay-server"} |= "EntityAsk" |= "Error"

# Errors from a specific entity kind (e.g. PlayerActor)
{app="metaplay-server", loglevel="ERR"} |= "PlayerActor"

# Authentication failures
{app="metaplay-server"} |= "AuthenticationError"

# Database exceptions
{app="metaplay-server"} |= "DatabaseException"

# Startup errors (useful when a deploy failed to come up)
{app="metaplay-server"} |= "Launching application"

# Regex match
{app="metaplay-server"} |~ "Timeout.*player"

# Scoped to a specific namespace (env id) — needed if the Loki source spans multiple envs
{app="metaplay-server", namespace="lovely-wombats-build-nimbly"}
```

Use the top-right time-range picker to focus on the incident window.

## Five key indicators to check first (Grafana)

When the user reports "something is wrong in production", scan these five before diving into any specific diagnostic:

1. **Login rate** — is it zero? Dropping fast?
2. **CPU per pod** — above 90% sustained?
3. **Error rate** — above ~50/min?
4. **EntityAsk error rate** — any sustained errors?
5. **Active connections** — sudden drop?

Then `metaplay debug server-status <env>` to correlate with pod state, and branch into the relevant scenario below.

## Incident response — scenario playbooks

### Scenario A — Pods in CrashLoopBackoff

- **Detection:** `metaplay debug server-status <env>` shows pods unhealthy; login rate is zero.
- **Do not restart.** Find out *why* first.
- **Diagnose:** `metaplay debug logs <env> --pod <pod-name>` → look for the exception type and stack trace. Classify as config error, code bug, or data corruption.
- **Resolve:**
  - Config or code regression → roll back with `metaplay deploy server <env> <image>:<previous-tag>`. (Previous image tag should be recorded in your CI; ask the user if you don't have it.)
  - Data corruption suspected → **escalate to Metaplay support immediately; do not attempt manual DB operations.**

### Scenario B — CPU at 100%, players being rejected

- **Detection:** CPU sustained above 90% for 5+ minutes; login rate dropping.
- **Do not restart pods** — you will destroy the diagnostic data.
- **Diagnose:** Capture a CPU profile (safe, low overhead):
  ```bash
  metaplay debug collect-cpu-profile <env>
  # or target a specific pod:
  metaplay debug collect-cpu-profile <env> <pod-name>
  ```
  Analyze the `.nettrace` in Visual Studio, or `--format speedscope` and upload to https://speedscope.app.
- **Mitigate while you investigate:** scale out if the environment supports it; for Metaplay-hosted environments contact support for emergency scaling.
- **Resolve:** identify the hotspot from the trace → fix → redeploy.

### Scenario C — Memory growth, OOM kills

- **Detection:** Working-set trending up over hours; pods eventually OOM-killed.
- **Before the OOM:** capture a heap dump *if you are authorized to freeze the process*. Preferably during a maintenance window.
  ```bash
  metaplay debug collect-heap-dump <env>                 # managed heap (faster, less intrusive)
  metaplay debug collect-heap-dump <env> --mode=dump     # full process dump (slower, more intrusive)
  ```
  On production under normal load, surface the warning to the user and get explicit go-ahead before running either.
- **Stopgap:** a pod restart flushes the leak temporarily (state persists in the DB, so data is safe). But the leak recurs until the root cause is fixed.
- **Resolve:** analyze the `.gcdump` in Visual Studio or PerfView; identify the dominant retained types and the reference chain (`gcroot` in `dotnet-dump analyze`).

### Scenario D — EntityAsk error spike

- **Detection:** EntityAsk error rate jumps in Grafana; players report action failures.
- **Diagnose:** `{app="metaplay-server"} |= "EntityAsk" |= "Error"` in Loki, last 30 min. Identify the entity kind involved and whether errors are concentrated on a single shard or spread across all shards.
- **Resolve:**
  - Single shard → the entity on that shard may be wedged; consider restarting *that specific pod only*.
  - All shards → likely a code bug triggered by specific game state. Cross-reference with recent action types in the logs.

### Scenario E — Database latency spike

- **Detection:** DB query p99 above 500ms in Grafana; EntityAsk latencies also climbing.
- **Do not restart pods.** For Metaplay-hosted environments, notify Metaplay support — the DB tier is managed by them.
- **Diagnose:** correlate latency with a specific entity kind or action type via logs.
- **Resolve:** Metaplay support investigates infrastructure/DB performance for Metaplay-hosted environments.

### Scenario F — Game config publish broke clients

- **Detection:** Error rate jumps the moment a new config goes live; client errors mention config parsing; new logins fail.
- **Immediate action:** **Roll back the config from the Dashboard — no server restart needed, takes effect in ~2 minutes.**
  - Dashboard → **Game Config** → select the current config → **Publish Previous Version**.
- This is the fastest rollback in the Metaplay system. Always try this first when an incident started after a config publish.

## Escalation policy

| Situation | Contact | How |
|---|---|---|
| Metaplay infrastructure outage (Metaplay-hosted) | Metaplay support | `support@metaplay.io`, or dedicated Slack on premium support |
| Database corruption / data recovery | Metaplay support | Same as above; do not attempt manual DB operations |
| Suspected SDK bug | Metaplay support ticket | File via portal; attach error logs, SDK version, and repro steps |
| Customer's own CI/CD broken | Customer's DevOps | Not a Metaplay issue |
| Account / billing | Account manager | Check internal contacts |

**Always check `status.metaplay.io` before escalating** — a known outage there means a ticket is less useful than watching the status page.

## Post-incident

After any production incident, preserve evidence and run a brief retro:

- Export relevant logs for the incident window: Grafana → Explore → Loki → Export.
- Screenshot the Grafana panels that showed the anomaly.
- Write a short timeline: first signal → detection → actions taken → resolution.
- 30-minute post-mortem: what happened, why, what change prevents recurrence.
- If a `.nettrace` or `.gcdump` was captured, archive it alongside the timeline — those files are ephemeral on the pod filesystem and will be gone if not copied off.

## Player incident report analysis

For diagnosing a single player's crash, desync, or network report from the admin API (dashboard URL, or the "what are the latest incidents?" workflow), run `metaplay skills get metaplay-devops/incident-analysis`. It covers URL parsing, `metaplay debug admin-request` endpoints for incident statistics and details, the `IncidentDashboardInfo` data surface, and per-type diagnostic heuristics (desync → `PlayerModelDiffReport` interpretation, unhandled exception → stack trace, etc.).

This is a different workflow from the scenario playbooks above — those target cluster-wide symptoms (pods down, CPU spike), incident analysis targets one player's specific report.

## Fetching deeper SDK context

This skill gives you the operational surface. For SDK-internal questions that come up during an investigation (e.g. "what does this log message mean?", "how is PlayerActor supposed to handle X?", "what's the Helm value for Y?"), defer to the `metaplay-docs` skill or the `metaplay:retriever` subagent — they query the same `metaplay llm-docs` payload that backs the SDK docs and source. Examples:

- `metaplay llm-docs read docs/cloud-deployments/troubleshooting`
- `metaplay llm-docs read docs/game-server-programming/how-to-guides/troubleshooting-servers`
- `metaplay llm-docs read docs/cloud-deployments/advanced-topics/configuring-a-deployment`

## When NOT to use this skill

- Local dev-loop issues (`metaplay dev server`, SQLite database, Unity Editor connection problems) — those are covered by the general troubleshooting docs, not by cloud diagnostics.
- Writing new game logic, configs, or player actions — use the `metaplay-develop` skill or `/metaplay:implement`.
- Questions about Metaplay Portal, billing, or account setup — escalate to the account manager.
