# Metaplay devops

Operating a Metaplay game server in a cloud environment: building and pushing images, deploying and rolling back, checking status, pulling logs, capturing CPU and heap profiles for diagnosis, and managing per-environment Kubernetes secrets.

This skill is about *what to run when something is wrong with — or about to change in — a deployed server*. For writing game code, use `metaplay-develop`. For per-player crash/desync reports surfaced in the dashboard, use `metaplay-develop/incident-analysis`. For the CLI itself misbehaving (missing, outdated, weird output), use `metaplay-troubleshoot`.

## Environments

Every command in this skill targets an **environment** — a deployed cloud stack identified by a short ID (e.g. `nimbly`) or a full slug (e.g. `lovely-wombats-build-nimbly`). The environment names available to the project are listed in `metaplay-project.yaml`. Most commands ask interactively if you omit the argument; pass the env explicitly when scripting.

Authentication: every command requires `metaplay auth login` first. A `401` means the session expired; `403` means the user lacks the relevant permission for that environment.

## Production safety — CRITICAL

Several commands in this skill are destructive, intrusive, or visible to real players: `metaplay deploy server` (changes what's running), `metaplay remove server` (tears down the deployment), `metaplay debug collect-heap-dump` (freezes the pod for seconds to minutes), `metaplay debug database --read-write`, `metaplay secrets create --overwrite` / `secrets update` / `secrets delete`, and any `--yes` / `--auto-agree` flag that bypasses interactive prompts.

**Before running any of these against an environment that looks like production, confirm with the user that the action is intended.** "Production" here means anything the user's real players hit — not the local dev stack, not a staging env the user has already named. If you can't tell whether an env is production from its name or `metaplay-project.yaml`, ask first. Match the scope of the action to what the user actually asked for: a request to "check the server" never authorizes a deploy, a redeploy, a database write, or a heap dump.

This rule applies to every sub-skill below. The sub-skills include command-specific safety notes (e.g. heap-dump freeze duration, `remove server` blast radius) but rely on this section for the overarching "confirm before acting on production" policy.

{{subskills}}

Each sub-skill is a focused playbook — load only what the current task needs:

- **`deploy-server`** — `build image` → `image push` → `deploy server`, with rollback and post-deploy verification.
- **`diagnose-server`** — symptom-to-tool routing for a misbehaving cloud deployment (server down, slow, OOM, crash-looping). Usually the right entry point when something is wrong but you don't know what yet.
- **`view-logs`** — `metaplay debug logs` with time windows, live tailing, per-pod filtering.
- **`cpu-profiling`** — `collect-cpu-profile` (a `dotnet-trace` wrapper) for latency and CPU-bound issues.
- **`memory-profiling`** — `collect-heap-dump` for OOMs and suspected leaks. Intrusive — re-read the production safety section.
- **`secrets`** — `metaplay secrets *` for per-environment Kubernetes user secrets.

Common pairings: `deploy-server` + `diagnose-server` (deploy, then verify the result); `diagnose-server` first, then `view-logs` or one of the profile sub-skills once a symptom is narrowed down.

## When NOT to use this skill

- Authoring or reviewing C# code, models, actions, or configs — use `metaplay-develop`.
- Per-player crash, desync, or network incident reports from the dashboard — use `metaplay-develop/incident-analysis`.
- The `metaplay` CLI itself is missing, outdated, or returns garbled output — use `metaplay-troubleshoot`.
- SDK API or concept questions ("how does X work?") — use `metaplay-docs`.
