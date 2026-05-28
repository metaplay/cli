---
name: metaplay-devops-memory-profiling
description: Capture a heap dump from a running Metaplay game server pod with `metaplay debug collect-heap-dump` (wraps `dotnet-gcdump` or `dotnet-dump`). Covers when to dump (OOM, leak suspicion), the two modes (`gcdump` for managed heap only vs `dump` for full process), and how to read the result. Use when the user asks to diagnose memory growth, an OOM, or a suspected leak. **Intrusive — freezes the pod for seconds to minutes.**
---

# Memory profiling / heap dumps

When to reach for this: process memory is climbing without bound, the kubelet OOM-killed a pod, a specific entity type is suspected of leaking, or you need evidence for an unbounded-collection bug.

**This operation is intrusive.** Collecting a heap dump completely freezes the target process for the duration — seconds to minutes depending on heap size. Health probes are temporarily patched to keep the kubelet from killing the pod during the freeze. **Never run against a production env at peak load without coordinating with the user.**

## Collecting

```bash
# Default: gcdump (managed heap only), interactive pod pick.
metaplay debug collect-heap-dump <env>

# Specific pod.
metaplay debug collect-heap-dump <env> service-0

# Full process dump (much larger; includes native heap, threads, stacks).
metaplay debug collect-heap-dump <env> --mode=dump

# Custom output path. Use .gcdump extension for gcdump mode; no extension for dump mode.
metaplay debug collect-heap-dump <env> -o /tmp/leak.gcdump
metaplay debug collect-heap-dump <env> --mode=dump -o /tmp/core_250901_093000

# Skip the heap-size warning (only when you've already decided).
metaplay debug collect-heap-dump <env> --yes
```

## Two modes — which to pick

| Mode | What it captures | Size | When to use |
|---|---|---|---|
| `gcdump` (default) | Managed (.NET) heap only — object graph, types, sizes | Smaller, faster | Default first attempt for any managed-heap leak. Covers 90% of game-server memory questions. |
| `dump` | Full process: managed + native heap, threads, stacks | Much larger | When `gcdump` doesn't explain growth (native libraries leaking, large unmanaged buffers, stack overflow), or when you also want a stack trace from every thread. |

Start with `gcdump`. Only escalate to `dump` if managed-heap analysis didn't explain the growth.

## Reading the result

Open `.gcdump` files with:

- **PerfView** on Windows — the canonical viewer.
- **Visual Studio** ("Open Diagnostic File") — better integrated with the IDE.
- **`dotnet-gcdump` report** — text summary on any platform.

Open `.dump` files with:

- **`dotnet-dump analyze <file>`** — interactive REPL (`dumpheap -stat`, `gcroot`, etc.).
- **WinDbg** (Windows) with the SOS extension.
- **`lldb`** with the SOS plugin on Linux.

What to look for:

- **Object counts by type** sorted by retained size — the top entries name the leaking class.
- **`gcroot`** on a leaking instance — shows what's holding the reference (the actual leak path).
- **Unbounded collections** — a `List<T>` / `MetaDictionary<>` with millions of entries is almost always a missing prune. Cross-reference `metaplay-develop-code-review` rule **MI1**.

## Common leak shapes in Metaplay code

- **Unbounded model collections** that append over a player's lifetime (history logs, completed quest records). See `metaplay-develop-code-review` MI1.
- **Static caches** in server-side helpers that never evict. See `metaplay-develop-code-review` D5 (no global mutable state) — leaks here are also concurrency hazards.
- **Event-handler subscriptions** never unsubscribed when the entity actor stops.
- **Large game-config retained per session** when it should be shared — confirm with `metaplay debug shell` that there is only one config instance live.

## Safety notes — re-read before running

- **Freezes the pod.** Seconds for a small heap, minutes for a large one. Players connected to that pod will time out.
- **Health-probe patch:** restored when the dump finishes or the debug container exits. If the CLI crashes mid-dump, restart the pod (or wait for the next deploy).
- **`--mode=dump` files are large** — many GB for a server with a populated heap. Make sure the local disk has room.
- The `--yes` flag bypasses the size warning; only set it when you've already confirmed with the user.

## Error patterns

- **`401`/`403`:** auth or `api.shell.access` permission missing.
- **`no pods found`:** no game server deployed.
- **Dump fails or hangs:** the pod may be too far gone to attach. Pull `metaplay debug logs <env>` for the lead-up, and consider restarting and reproducing the leak with allocation tracking instead.
- **Heap dump shows nothing unexpected:** likely a native leak — escalate to `--mode=dump`.
