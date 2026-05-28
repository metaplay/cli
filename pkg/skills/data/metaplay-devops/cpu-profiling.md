---
name: metaplay-devops-cpu-profiling
description: Collect a CPU profile from a running Metaplay game server pod with `metaplay debug collect-cpu-profile` (a `dotnet-trace` wrapper). Covers when to profile (latency spike, high CPU), output formats (`nettrace`, `speedscope`, `chromium`), durations, and how to read the result. Use when the user asks to profile, capture a trace, or diagnose a CPU/latency hotspot.
---

# CPU profiling

When to reach for this: server CPU is pegged, request latency is high, a known hot path is suspected, or you need quantitative evidence before optimizing. Don't pre-emptively profile — collect on a real symptom.

## Collecting

```bash
# Default: 30-second trace, picks the pod automatically (or prompts if multiple).
metaplay debug collect-cpu-profile <env>

# Specific pod.
metaplay debug collect-cpu-profile <env> service-0

# Longer trace for a bursty symptom (default 30s).
metaplay debug collect-cpu-profile <env> --duration 60

# Speedscope format for browser viewing.
metaplay debug collect-cpu-profile <env> --format speedscope

# Chromium format (Chrome devtools, Perfetto).
metaplay debug collect-cpu-profile <env> --format chromium

# Custom output path.
metaplay debug collect-cpu-profile <env> -o /tmp/server-hotpath.nettrace

# Pass extra args through to dotnet-trace (after `--`).
metaplay debug collect-cpu-profile <env> -- --providers Microsoft-Windows-DotNETRuntime:4:4
```

The command runs `dotnet-trace` inside an ephemeral debug container attached to the pod, then copies the trace back to the local machine. Health probes are temporarily patched to always succeed during the trace, so the kubelet doesn't restart the pod mid-collection.

## Picking a format

| Format | View with | Best for |
|---|---|---|
| `nettrace` (default) | Visual Studio, PerfView, `dotnet-trace report` | Deep inspection on Windows; the source-of-truth format. |
| `speedscope` | https://speedscope.app | Quick flamegraph in the browser. Cross-platform, zero install. |
| `chromium` | Chrome devtools Performance tab, Perfetto | Timeline view with concurrency lanes. |

Default to `speedscope` when sharing with the user — it's the fastest "look at this hot stack" loop. Use `nettrace` if the user is on Windows and using PerfView.

## How long to capture

- **Steady-state high CPU:** 30s (default) is plenty.
- **Intermittent / bursty:** 60–120s, and time it to coincide with the symptom (e.g. start the trace right before a known traffic spike).
- **One specific slow request:** trigger the request just after starting the trace; 30s is still usually enough.

Longer captures produce much bigger files and slow down the post-processing. Don't go past a couple of minutes without a reason.

## Reading the result

A flamegraph in speedscope is the canonical first view: wide bars at the top of the stack = the methods burning CPU. Cross-reference hot frames against:

- `metaplay-develop-review-actions` rule **PS1** (performance on frequently-invoked actions).
- `metaplay-develop-review-models` rules **GT1**/**GT3** (GameTick performance, per-tick work).
- The `metaplay-develop` rule **D3** (avoid `SortedSet`/`SortedDictionary`) — sorted-collection hot frames are a common false positive that's actually a real issue.

If the hot path is inside SDK code (e.g. serialization, scheduler), it's usually a downstream symptom of a userland mistake — back up the stack until you find the userland frame that called it.

## Safety notes

- Tracing has measurable overhead while running (~5–10% CPU plus disk). Coordinate with the user before tracing a production env at peak hours.
- The health-probe patch is temporary: it's reverted when the trace finishes or the debug container exits. If the CLI crashes mid-collection, restart the affected pod (or wait for the next deploy) to restore the original probe behavior.
- Trace files are not sensitive by themselves but include method names from your game code — treat them as you would source code.

## Error patterns

- **`401`/`403`:** auth or `api.shell.access` permission missing.
- **`no pods found`:** no game server deployed.
- **Trace fails immediately:** the pod may be unhealthy in a way `dotnet-trace` can't attach. `metaplay debug server-status <env>` and `metaplay debug logs <env>` first.
- **Out-of-disk inside the debug container:** drop `--duration`, or use a different `--format` that yields smaller files.
