---
name: metaplay-develop-game-logic
description: Implementation playbook for Metaplay game logic — entity actions (PlayerAction, GuildAction, custom entity actions), GameConfig classes (libraries and globals), and entity models (PlayerModel, GuildModel, custom entity models). Covers feature-shape planning, write-time design patterns per area, SDK code templates for game configs, the deterministic type conventions that apply across all three areas, and the local validation commands. Load before writing game logic; after implementation, load `metaplay-develop-code-review` to verify the changes against the full rule checklist.
---

# Implementing game logic

Most feature work boils down to deciding where each piece of the feature lives within the SDK's primitives — state on the player model (or a guild / custom entity model), tick logic that advances state over time, actions that mutate state on player input, game configs for designer-tunable data — then writing the code that fits each primitive's contract.

## Before writing code

1. **Pin down the feature shape.** What state is involved, who owns it (single player, guild, server), what mutations are possible, and what's designer-tunable? The right SDK primitive (model member vs. action vs. config vs. server entity) follows directly from these answers — sketch them before touching files.
2. **Read what exists.** Grep the project for similar features and the SDK markers they use (`[ModelAction(`, `[GameConfigEntry]`, `EntityActor`, `MetaplayClient`). Local patterns are the most reliable guide to project conventions.
3. **Consult docs for unfamiliar primitives.** Use `metaplay-docs` for SDK API and concept questions rather than guessing — the SDK has constraints (determinism, serialization, fast-forward) that aren't obvious from type signatures alone.
4. **Apply the design patterns below.** They cover the SDK contracts most likely to bite. After implementation, verify against `metaplay-develop-code-review` for the full rule checklist.

## Actions

- **Transactional by default** — an action should spend the cost and grant the reward in the same `Execute`. E.g. `PlayerPurchaseItem` deducts gold and adds the item in one atomic step. Never split cost and reward into separate actions — a hacked client will skip the cost action.
- **Resource-granting-only actions are debug-only** — an action that adds resources without spending anything (e.g. `PlayerGainGoldDebug`) must carry `[DevelopmentOnlyAction]` so it cannot execute in production.
- **State-only features don't need actions** — if a feature only holds model state and has no behavior, skip the action entirely. Debug actions may still be useful for testing.
- **Server-authoritative data comes from the model or config, not from client parameters** — prices, reward amounts, and quantities must be looked up from `GameConfig` or model state inside `Execute`. Client-supplied parameters are untrusted.
- **Validate before the `if (commit)` block, mutate inside it** — the pre-commit phase is pure validation and computed values. All state changes go inside `if (commit) { ... }`.

## GameConfigs

- **Don't change the data source of an existing config** — if a library is already backed by CSV, Google Sheets, or C# code, keep it on that source unless explicitly asked to migrate.
- **New libraries and globals default to C# code** — use the templates below. CSV or Google Sheets are explicit opt-ins.
- **Google Sheets edits are out of scope** — if a change requires modifying a Google Sheet, tell the user and hand them the data in a table for copy-paste.
- **Pick the right container type:**
  - `GameConfigLibrary<TKey, TInfo>` — items referred to by id (troops, items, quests, levels).
  - `GameConfigKeyValue<T>` ("global config") — singleton config data that isn't referred to by id, including small "nameless" arrays.

### GameConfigLibrary in C#

```csharp
public class GameConfigContent
{
    public static readonly GameConfigLibrary<TKey, TInfo> SomeLibrary =
        GameConfigLibrary<TKey, TInfo>.CreateSolo(new TInfo[] {
            new TInfo(...),
            new TInfo(...),
            // ...
        });
}

public class SharedGameConfig : SharedGameConfigBase
{
    // No attribute — values come from source code.
    public GameConfigLibrary<TKey, TInfo> SomeLibrary { get; private set; } = GameConfigContent.SomeLibrary;
}
```

### GameConfigKeyValue in C#

```csharp
public class GlobalConfig : GameConfigKeyValue<GlobalConfig>
{
    [MetaMember(id)] public int SomeValue { get; private set; } = 123;
    [MetaMember(id)] public int[] SomeArray { get; private set; } = new int[] { /* values */ };
}

public class SharedGameConfig : SharedGameConfigBase
{
    // No attribute — values come from source code.
    public GlobalConfig Global { get; private set; } = new GlobalConfig();
}
```

## Models

- **Deterministic types throughout** — see "Deterministic data types" below.
- **Split state into sub-models** — group related state (inventory, quests, energy, wallet) into sub-model classes annotated with `[MetaMember]`. The rules below apply to sub-models the same as to the root.
- **Event-driven, not polled** — `GameTick` should not scan state each tick to see if anything needs doing; store "next event time" values and only act when `CurrentTime >= nextEventTime`.
- **Fast-forward computes analytically** — `GameFastForwardTime` must produce the same result as running `GameTick` for the elapsed duration, but without iterating tick-by-tick. Entities can be offline for weeks.
- **Only actions and `GameTick` mutate model state** — never mutate from API endpoints, session handlers, or listeners.
- **Bound growing collections** — any collection that appends over a player's lifetime (history logs, completed quests) needs a pruning strategy.

## Deterministic data types

These apply to all game logic — model state, action `Execute` methods, and `GameConfig` items alike:

- **Collections** — `MetaDictionary<,>` instead of `Dictionary<,>`; `OrderedSet<>` instead of `HashSet<>`. Avoid `SortedSet<>` / `SortedDictionary<,>`; they work but perform poorly.
- **Numbers** — `F32` (16.16) or `F64` (32.32) fixed-point instead of `float` / `double`; floating-point math diverges across platforms and causes desyncs.
- **Time** — `MetaTime` for timestamps, `MetaDuration` for time spans. Inside actions and `GameTick`, read the model's `CurrentTime`, never `DateTime.Now` or `Environment.TickCount`.
- **Randomness** — `RandomPCG` (the SDK's deterministic PRNG) instead of `System.Random` or any LINQ `Shuffle()`.
- **Strings & identifiers** — `StringId<>` for typed identifiers; invariant-culture variants (`ToLowerInvariant()`, `CultureInfo.InvariantCulture`) for any locale-sensitive operation.

## Validating changes locally

Two commands cover the routine local validation loop for code changes:

- `metaplay build server` — compiles the .NET game server. The fastest signal that a change compiles cleanly.
- `dotnet test Backend/SharedCode.Tests` (and `dotnet test Backend/Server.Tests` if present) — runs the project's own unit tests. These test projects are optional; not every project has them.

These are the canonical paths; `Backend/SharedCode.Tests` and `Backend/Server.Tests` are the conventional locations for user-authored unit tests in a Metaplay project.

For running the full local stack (server + dashboard + Unity client) as part of the inner-loop, see the `metaplay-develop-local-development` sub-skill.

## After implementation: review

Load `metaplay-develop-code-review` and walk through the rule checklist for each touched area (Actions, GameConfigs, Models). The discovery patterns there also help confirm that no related class — sub-model, listener interface, validation hook — was missed.
