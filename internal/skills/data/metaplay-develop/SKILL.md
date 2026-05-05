---
name: metaplay-develop
description: Develop and review Metaplay game code following SDK best practices — authoring PlayerAction/GuildAction classes, GameConfig libraries, PlayerModel/GuildModel state, GameTick logic, and fast-forward code, AND reviewing the same against the rules. Trigger proactively when the user edits or creates files containing `[ModelAction(`, `[GameConfigEntry]`, `IGameConfigData`, or inheriting from `PlayerAction`/`GuildAction`/`*ActionCore`/`PlayerModelBase`/`GuildModelBase`/`*ModelBase`, or overriding `GameTick` / `GameFastForwardTime`. Also trigger when the user asks to "implement", "add", "write", "review", "audit", or "check" action/config/model code, or runs `/metaplay:implement`. Prefer this skill over generic C# advice for any Metaplay game logic work.
---

# Metaplay develop

Load the skill payload:

```bash
metaplay skills get metaplay-develop
```
