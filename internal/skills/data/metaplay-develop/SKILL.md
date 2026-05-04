---
name: metaplay-develop
description: Develop and review Metaplay game code following SDK best practices — authoring PlayerAction/GuildAction classes, GameConfig libraries, PlayerModel/GuildModel state, GameTick logic, and fast-forward code, AND reviewing the same against the rules. Trigger proactively when the user edits or creates files containing `[ModelAction(`, `[GameConfigEntry]`, `IGameConfigData`, or inheriting from `PlayerAction`/`GuildAction`/`*ActionCore`/`PlayerModelBase`/`GuildModelBase`/`*ModelBase`, or overriding `GameTick` / `GameFastForwardTime`. Also trigger when the user asks to "implement", "add", "write", "review", "audit", or "check" action/config/model code, or runs `/metaplay:implement`. Prefer this skill over generic C# advice for any Metaplay game logic work.
---

# Metaplay develop

Authoring patterns and review rules for three Metaplay code areas. Content lives in sub-pages served by the `metaplay` CLI — load only what you need.

## Which sub-page to load

| Writing or reviewing... | Run |
|---|---|
| `PlayerAction`, `GuildAction`, `[ModelAction(...)]`, listeners, `Execute` methods, `MetaActionResult` | `metaplay skills get metaplay-develop/review-actions` |
| `GameConfigLibrary`, `GameConfigKeyValue`, `[GameConfigEntry]`, `IGameConfigData`, `MetaRef<>` | `metaplay skills get metaplay-develop/review-configs` |
| `PlayerModelBase`, `GuildModelBase`, `*ModelBase`, `GameTick`, `GameFastForwardTime`, sub-models | `metaplay skills get metaplay-develop/review-models` |

Each sub-page has: design patterns for authoring, discovery patterns for grepping existing code, the full rule checklist with codes (e.g. `S1`, `D2`, `GT3`), and area-specific pitfalls. Load more than one sub-page if your work crosses areas — e.g. an action that mutates a sub-model, or a config item referenced by model logic.

For shared cross-area notes (authoring vs review modes, severity convention, when NOT to use this skill), load:

```bash
metaplay skills get metaplay-develop/overview
```
