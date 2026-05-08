---
name: metaplay-troubleshoot
description: Recover when Metaplay tooling itself is failing — `metaplay` command missing, failing, or outdated. Trigger when any `metaplay …` command does not run or behaves unexpectedly.
---

# Metaplay troubleshooting

Load this skill when Metaplay tooling itself is misbehaving — the `metaplay` CLI is missing, won't run, prints unexpected errors, or appears outdated. The recovery steps below are self-contained so they work even when `metaplay` cannot be invoked.

## Step 1: Verify the CLI is installed and reasonably current

Run:

```bash
metaplay version
```

### If the command is not found

Follow the platform-specific installation instructions at https://github.com/metaplay/cli#installation, then re-run `metaplay version` to confirm.

### If the command is found but suspected outdated

Self-update:

```bash
metaplay update cli
```

## Step 2: Fetch the deeper troubleshooting payload

Once the CLI works, load the expanded troubleshooting content:

```bash
metaplay skills get metaplay-troubleshoot
```

## Step 3: For general "how do I X" Metaplay questions

```bash
metaplay skills get metaplay-docs
```
