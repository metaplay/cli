# Metaplay develop

Day-to-day work on a Metaplay SDK project: designing and implementing new features, refactoring and debugging existing code, running the stack locally, setting up a new project or a custom LiveOps Dashboard, triaging per-player incidents, and upgrading the SDK to a newer release. A Metaplay project blends server-side game logic, a Unity client, designer-tunable game configs, and a LiveOps Dashboard — most features touch more than one of these layers, and the SDK has strong opinions about how state, logic, and configuration are structured.

This skill is about *how to work* in a Metaplay project. For SDK API references, concepts, and "how do I…" questions, pair it with the `metaplay-docs` skill, which is about *what the SDK provides* and general guidance on how to use the SDK.

The skill itself is a dispatcher — pick the matching sub-skill below for the task at hand. Most feature work pairs `game-logic` (write-time playbook) with `code-review` (post-implementation verification).

{{subskills}}

`game-logic` and `code-review` are the everyday pair: load `game-logic` when implementing or modifying actions / game configs / entity models, then `code-review` to verify the change against the full rule checklist. The other sub-skills are workflow playbooks for narrower tasks — load whichever fits.

For deploy/logs/profiling/secrets work against a *cloud* environment (not local), use the sibling `metaplay-devops` skill instead.

## When NOT to use this skill

- Non-Metaplay C# code — use standard practice.
- Questions about Metaplay SDK or APIs, find sample code references, and other generic help — use `metaplay-docs`.
- Deploying, viewing cloud logs, profiling a deployed server, or managing per-env secrets — use `metaplay-devops`.
- CLI tool problems (`metaplay` not found, outdated, weird output) — use `metaplay-troubleshoot`.
