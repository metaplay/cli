---
name: metaplay-devops
description: Diagnose and respond to Metaplay game server issues in cloud environments — production incidents, pod health, performance investigations, log/metrics triage, CPU profiles, heap dumps, database access, config rollbacks, and per-player incident report triage. Use whenever the user asks "why is my server down / slow / crashing / rejecting logins", mentions CrashLoopBackoff, OOM, EntityAsk errors, desyncs at scale, latency spikes, a bad config publish, or anything involving `metaplay debug`, `metaplay deploy`, Grafana, Loki, or kubectl against a live environment. Also trigger when the user pastes a dashboard incident URL (e.g. `*-admin.p1.metaplay.io/.../incidentReports/...`), asks about "the latest incidents" / "recent incidents", or names an environment (Metaplay-hosted hostname or env id from `metaplay-project.yaml`) together with a production-sounding symptom. Prefer this skill over general troubleshooting advice for any live-ops or cloud-deployment question.
---

# Metaplay devops

This is a thin wrapper. The operational playbook is served by the Metaplay CLI — load it before acting on any production environment:

```bash
metaplay skills get metaplay-devops/main
```

For per-player crash, desync, or network incident reports (admin-API URLs, "what are the latest incidents?" workflow), load:

```bash
metaplay skills get metaplay-devops/incident-analysis
```
