---
name: metaplay-develop-game-logic
description: Implementation playbook for Metaplay game logic, covering entity actions (PlayerAction, GuildAction, custom entity actions), GameConfig classes (libraries and globals), and entity models (PlayerModel, GuildModel, custom entity models). Covers feature-shape planning, write-time design patterns per area, SDK code templates for game configs, the deterministic type conventions that apply across all three areas, and the local validation commands. Load before writing game logic; after implementation, load `metaplay-develop-code-review` to verify the changes against the full rule checklist.
---

# Implementing game logic

Most feature work boils down to deciding where each piece of the feature lives within the SDK's primitives: state on the player model (or a guild / custom entity model), tick logic that advances state over time, actions that mutate state on player input, game configs for designer-tunable data. Then you write the code that fits each primitive's contract.

## Before writing code

1. **Pin down the feature shape.** What state is involved, who owns it (single player, guild, server), what mutations are possible, and what's designer-tunable? The right SDK primitive (model member vs. action vs. config vs. server entity) follows directly from these answers. Sketch them before touching files.
2. **Read what exists.** Grep the project for similar features and the SDK markers they use (`[ModelAction(`, `[GameConfigEntry]`, `EntityActor`, `MetaplayClient`). Local patterns are the most reliable guide to project conventions.
3. **Read the docs page for each SDK feature you use.** The SDK has constraints (determinism, serialization, fast-forward, model size) that aren't obvious from type signatures alone. Find pages with `metaplay llm-docs search "<question>" --keywords "k1,k2,k3"` and open them with `metaplay llm-docs read docs/<path>`; the rules below name their page. SDK doc comments link each type's guide as `<seealso href="https://docs.metaplay.io/<path>"/>`, which `read` also opens.
4. **Apply the design patterns below.** They cover the SDK contracts most likely to bite. After implementation, verify against `metaplay-develop-code-review` for the full rule checklist.

## Actions

- **Transactional by default.** An action should spend the cost and grant the reward in the same `Execute`. E.g. `PlayerPurchaseItem` deducts gold and adds the item in one atomic step. Never split cost and reward into separate actions. A hacked client will skip the cost action.
- **Resource-granting-only actions are debug-only.** An action that adds resources without spending anything (e.g. `PlayerGainGoldDebug`) must carry `[DevelopmentOnlyAction]` so it cannot execute in production.
- **State-only features don't need actions.** If a feature only holds model state and has no behavior, skip the action entirely. Debug actions may still be useful for testing.
- **Server-authoritative data comes from the model or config, not from client parameters.** Prices, reward amounts, and quantities must be looked up from `GameConfig` or model state inside `Execute`. Client-supplied parameters are untrusted.
- **Validate before the `if (commit)` block, mutate inside it.** The pre-commit phase is pure validation and computed values. All state changes go inside `if (commit) { ... }`.

## GameConfigs

- **Don't change the data source of an existing config.** If a library is already backed by CSV, Google Sheets, or C# code, keep it on that source unless explicitly asked to migrate.
- **New libraries and globals default to C# code.** Use the templates below. CSV or Google Sheets are explicit opt-ins.
- **Google Sheets edits are out of scope.** If a change requires modifying a Google Sheet, tell the user and hand them the data in a table for copy-paste.
- **Pick the right container type:**
  - `GameConfigLibrary<TKey, TInfo>`: items referred to by id (troops, items, quests, levels).
  - `GameConfigKeyValue<T>` ("global config"): singleton config data that isn't referred to by id, including small "nameless" arrays.
- **One config item refers to another with `MetaRef<TInfo>` or `MetaConfigId<TInfo>`**, not a string or a bare id. The config build then fails on a reference to a missing item. Docs: `docs/feature-cookbooks/game-configs/game-config-item-references.md`.
- **Validate designer-edited data at build time.** Override `BuildTimeValidate(GameConfigValidationResult validationResult)` on `SharedGameConfig`, call `base.BuildTimeValidate(validationResult)`, and report each bad row with `validationResult.Error(sheetName, configKey, message, columnHint)` (or `Warning`), so the build report names the sheet, row and column. Docs: `docs/feature-cookbooks/game-configs/deep-dive-custom-game-config-build-pipelines.md`.
- **Data derived only from config lives on the config.** Build lookups (offers by category, items by tag) in `protected override void OnLoaded()` on `SharedGameConfig` and read them through `GameConfig`. Keep them out of the model, even as `[Transient]`: that attribute only skips persistence, and when the active config changes the player actor clones the model through serialization against the new config, where a removed item fails to resolve. A static field keeps the config that was active when it was filled. Docs: `docs/game-logic/utilities/deep-dive-data-serialization.md`.

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
    // No attribute: values come from source code.
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
    // No attribute: values come from source code.
    public GlobalConfig Global { get; private set; } = new GlobalConfig();
}
```

## Models

- **Deterministic types throughout.** See "Deterministic data types" below.
- **Split state into sub-models.** Group related state (inventory, quests, energy, wallet) into sub-model classes annotated with `[MetaMember]`. The rules below apply to sub-models the same as to the root.
- **Event-driven, not polled.** `GameTick` should not scan state each tick to see if anything needs doing; store "next event time" values and only act when `CurrentTime >= nextEventTime`.
- **Fast-forward computes analytically.** `GameFastForwardTime` must produce the same result as running `GameTick` for the elapsed duration, but without iterating tick-by-tick. Entities can be offline for weeks.
- **Only actions and `GameTick` mutate model state.** Never mutate from API endpoints, session handlers, or listeners.
- **Persisted state outlives config items.** Designers remove items, and a persisted `MetaRef<TInfo>` to a removed item fails to deserialize unless its key gets an alias. For items that may go away, store the key type or `MetaConfigId<TInfo>`, resolve it at use with `TryGetValue` or `TryGetItem`, and decide what a missing item means (skip it or drop it). Docs: `docs/feature-cookbooks/game-configs/renaming-config-items.md`.
- **Keep the model small.** The actor loads the whole model, and the server sends all of it except `[ServerOnly]` members to the client at every login. The connection caps one message at 1 MB compressed by default, and an oversized player model is the usual way to hit it. `[ServerOnly]` keeps data off the client but not out of the persisted model.
- **A collection in the model needs a bound the player's own play sets.** Cap or prune it (recent history, active quests).
- **Data that grows with other players' activity or over the player's lifetime goes in a database table** (received gifts, messages, reactions, follower lists, full history). Use a custom table: a class implementing `IPersistedItem`, one row per item, written by the server and read on request with a limit through `MetaDatabase`. The model keeps at most counts or totals. A new table or column needs a migration: `dotnet ef migrations add <Name>` in `Backend/Server`. Docs: `docs/game-server-programming/how-to-guides/working-with-the-database.md`. The server side (handlers, queries) is in `metaplay-develop-server-logic`.

## Deterministic data types

These apply to all game logic: model state, action `Execute` methods, and `GameConfig` items alike:

- **Collections.** `MetaDictionary<,>` instead of `Dictionary<,>`; `OrderedSet<>` instead of `HashSet<>`. Avoid `SortedSet<>` / `SortedDictionary<,>`; they work but perform poorly.
- **Numbers.** `F32` (16.16) or `F64` (32.32) fixed-point instead of `float` / `double`; floating-point math diverges across platforms and causes desyncs.
- **Time.** `MetaTime` for timestamps, `MetaDuration` for time spans. Inside actions and `GameTick`, read the model's `CurrentTime`, never `DateTime.Now` or `Environment.TickCount`.
- **Randomness.** `RandomPCG` (the SDK's deterministic PRNG) instead of `System.Random` or any LINQ `Shuffle()`.
- **Strings & identifiers.** `StringId<>` for typed identifiers; invariant-culture variants (`ToLowerInvariant()`, `CultureInfo.InvariantCulture`) for any locale-sensitive operation.

## Validating changes locally

Two commands cover the routine local validation loop for code changes:

- `metaplay build server`: compiles the .NET game server. The fastest signal that a change compiles cleanly.
- `dotnet test Backend/SharedCode.Tests` (and `dotnet test Backend/Server.Tests` if present): runs the project's own unit tests. These test projects are optional; not every project has them.

These are the canonical paths; `Backend/SharedCode.Tests` and `Backend/Server.Tests` are the conventional locations for user-authored unit tests in a Metaplay project.

For running the full local stack (server + dashboard + Unity client) as part of the inner-loop, see the `metaplay-develop-local-development` sub-skill.

Shared code (models, actions, configs) runs on the client too. Tell the user when a change needs a client release alongside the server deploy.

## After implementation: review

Load `metaplay-develop-code-review` and walk through the rule checklist for each touched area (Actions, GameConfigs, Models). The discovery patterns there also help confirm that no related class was missed: sub-model, listener interface, validation hook.
