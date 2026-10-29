---
name: metaplay-troubleshoot
description: Use when the `metaplay` CLI is missing or outdated, when any `metaplay …` command fails or returns unexpected output, or when the user needs to sign in to Metaplay.
---

- **Check CLI existence and version:** `metaplay version`.
- **`metaplay` command not found:** install per https://github.com/metaplay/cli#installation.
- **CLI suspected outdated or behaving weirdly:** `metaplay update cli`.
- **Signing in is required, `401`, or the session expired:** ask the user, then `metaplay auth login` if a browser can open on this machine. If not (SSH, container, cloud sandbox), run `metaplay auth login --no-browser`, give the user the URL, and finish with `metaplay auth login --code '<code>'` when they paste back the code. Details: `metaplay skills get metaplay-troubleshoot-sign-in`.
- **Bad usage, unknown flag, or wrong arguments:** check `metaplay <command> --help` (e.g. `metaplay deploy server --help`).
- **For more troubleshooting context:** `metaplay skills get metaplay-troubleshoot`.
- **General "how do I X in Metaplay" question:** `metaplay skills get metaplay-docs`.
