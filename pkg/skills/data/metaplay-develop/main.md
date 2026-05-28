# Metaplay develop

Day-to-day work on a Metaplay SDK project: designing and implementing new features, refactoring and debugging existing code, running the stack locally, setting up a new project or a custom LiveOps Dashboard, triaging per-player incidents, and upgrading the SDK to a newer release. A Metaplay project blends server-side game logic, a Unity client, designer-tunable game configs, and a LiveOps Dashboard — most features touch more than one of these layers, and the SDK has strong opinions about how state, logic, and configuration are structured.

This skill is about *how to work* in a Metaplay project. For SDK API references, concepts, and "how do I…" questions, pair it with the `metaplay-docs` skill, which is about *what the SDK provides*.

## Implementing a new feature

The primary workflow this skill is built for. Most feature work boils down to deciding where each piece of the feature lives within the SDK's primitives, then writing the code that fits each primitive's contract. Common building blocks: state on the player model (or a guild / custom entity model), tick logic that advances state over time, actions that mutate state on player input, game configs for designer-tunable data, server-side backend systems for shared logic, and Unity client code that drives the UI from the model.

Before writing code:

1. **Pin down the feature shape.** What state is involved, who owns it (single player, guild, server), what mutations are possible, and what's designer-tunable? The right SDK primitive (model member vs. action vs. config vs. server entity) follows directly from these answers — sketch them before touching files.
2. **Read what exists.** Grep the project for similar features and the SDK markers they use (`[ModelAction(`, `[GameConfigEntry]`, `EntityActor`, `MetaplayClient`). Local patterns are the most reliable guide to project conventions.
3. **Consult docs for unfamiliar primitives.** Use `metaplay-docs` for SDK API and concept questions rather than guessing — the SDK has constraints (determinism, serialization, fast-forward) that aren't obvious from type signatures alone.
4. **Load the `code-review` sub-skill before generating code.** When a piece of the feature lands in the actions, configs, or models areas, load `code-review` *first*. Its rule checklist applies at write time, not just review time — catching a determinism or commit-discipline violation while writing is much cheaper than fixing it after.

## Validating changes locally

Two commands cover the routine local validation loop for code changes:

- `metaplay build server` — compiles the .NET game server. The fastest signal that a change compiles cleanly.
- `dotnet test Backend/SharedCode.Tests` (and `dotnet test Backend/Server.Tests` if present) — runs the project's own unit tests. These test projects are optional; not every project has them.

These are the canonical paths; `Backend/SharedCode.Tests` and `Backend/Server.Tests` are the conventional locations for user-authored unit tests in a Metaplay project.

For running the full local stack (server + dashboard + Unity client) as part of the inner-loop, see the `local-development` sub-skill below.

{{subskills}}

`code-review` is the rule checklist for the actions, configs, and models areas — load it both at write time (to catch determinism or commit-discipline mistakes while writing) and at review time. The other sub-skills are workflow playbooks; load whichever fits the task.

For deploy/logs/profiling/secrets work against a *cloud* environment (not local), use the sibling `metaplay-devops` skill instead.

## When NOT to use this skill

- Non-Metaplay C# code — use standard practice.
- SDK API or "how do I…" questions — use `metaplay-docs`.
- Deploying, viewing cloud logs, profiling a deployed server, or managing per-env secrets — use `metaplay-devops`.
- CLI tool problems (`metaplay` not found, outdated, weird output) — use `metaplay-troubleshoot`.
