---
name: metaplay-develop-code-review
description: Code review checklist for Metaplay projects, covering entity actions (PlayerAction, GuildAction, custom entity actions), GameConfig classes (libraries and globals), and entity models (PlayerModel, GuildModel, custom entity models). Includes the severity convention, scoping workflow, per-area design patterns, grep entry points, and the full rule checklists — actions (S/CM/D/PS/CO/AD), configs (TS/CD/V), and models (GT/FF/MS/MI). Load when reviewing existing Metaplay code, and load proactively when writing code in any of these areas — the rules apply at write time as well as review time.
---

# Code review

When the user asks to review, audit, or check Metaplay code:

1. **Scope.** Pin down what to review from the request — file paths, class names, commit ref, `changes in this PR`, or area-wide (if unspecified). If they named a focus ("security", "determinism", "validation", "immutability", "fast-forward", "performance"), limit to those rule categories.
2. **Discover.** Grep for the entry points listed in the relevant area section below. Traverse to sub-models, member types (reward structs, cost definitions), and partial class declarations. Verify completeness by counting attributes (`[ModelAction(`, `[GameConfigEntry]`, `[MetaMember]`) against discovered classes — investigate mismatches.
3. **Analyze.** For reviews spanning many files, cluster into small groups (a model + its sub-models, an actions file + its listeners) and launch one subagent per cluster in parallel. Give each subagent the file paths and the checklist from the relevant area.
4. **Consolidate.** Deduplicate findings, group by severity, report.

## Severity convention

- **Issues (must fix):** bugs, desyncs, cheating or security holes.
- **Warnings (should fix):** performance problems, patterns that break at scale, fragile designs.
- **Suggestions (consider):** style, naming, minor refinements.

For each finding include: the class name, `file:line`, the violated rule code (e.g. `D2`, `CD2`), and a one-line explanation of what's wrong and what to change. End with a summary — files reviewed, findings per severity, key concerns. If no issues are found, report a clean result.

## Actions

Authoring patterns and review rules for all entity action types — `PlayerAction`, `GuildAction`, and custom multiplayer entity actions.

### Design patterns

Apply these when writing new actions:

- **Transactional by default.** An action should spend the cost and grant the reward in the same `Execute`. E.g. `PlayerPurchaseItem` deducts gold and adds the item in one atomic step. Never split cost and reward into separate actions — a hacked client will skip the cost action. See S4.
- **Resource-granting-only actions are debug-only.** An action that adds resources without spending anything (e.g. `PlayerGainGoldDebug`) must carry `[DevelopmentOnlyAction]` so it cannot execute in production. See S1.
- **State-only features don't need actions.** If a feature only holds model state and has no behavior, skip the action entirely. Debug actions may still be useful for testing.
- **Server-authoritative data comes from the model or config, not from client parameters.** Prices, reward amounts, and quantities must be looked up from `GameConfig` or model state inside `Execute`. Client-supplied parameters are untrusted. See S2.
- **Validate before the `if (commit)` block, mutate inside it.** The pre-commit phase is pure validation and computed values. All state changes go inside `if (commit) { ... }`. See CM1, CO2.

### Discovery patterns

Grep for:

- **`[ModelAction(`** attribute — most reliable way to find all actions regardless of entity type.
- Player action base classes: `PlayerAction`, `PlayerActionCore`, `PlayerSynchronizedServerAction`, `PlayerSynchronizedServerActionCore`, `PlayerUnsynchronizedServerAction`, `PlayerUnsynchronizedServerActionCore`.
- Guild action base classes: `GuildAction`, `GuildActionCore`, `GuildClientAction`, `GuildClientActionCore`, `GuildServerAction`.
- Any other `*ActionCore` base classes for custom entity action types.
- Listener interfaces: `ModelServerListener` / `ModelClientListener` patterns (e.g. `IPlayerModelServerListener`, `IPlayerModelClientListener`, `IGuildModelServerListener`, `IGuildModelClientListener`).
- `MetaActionResult` and `ActionResult` definitions.

Traverse member types: action parameters or `Execute` methods may reference nested or shared types (reward definitions, resource cost structs). Discover these and apply the same determinism/collection rules.

Follow references: for each discovered action, scan for helper methods and utility classes it calls, and for partial class declarations in other files.

Verify completeness: count `[ModelAction(` attribute usages and compare against the number of discovered action classes. Investigate any discrepancy.

### Checklist

#### Security

- **S1 — No cheatable actions**: Actions that grant resources (gold, items, XP, etc.) without spending anything in return are cheatable. These must carry the `[DevelopmentOnlyAction]` attribute to prevent execution in production. Only transactional actions (spend + gain) should ship to production. See also S4.

- **S2 — No untrusted parameters**: Action parameters (data from the client) must not include values that should be server-authoritative: prices, reward amounts, resource quantities, or anything that directly affects inventory or economy. These values must come from GameConfig or model state and be looked up inside `Execute`.

- **S3 — Input validation before commit**: All input parameters must be validated before the `if (commit)` block. Hacked clients can send any action with arbitrary arguments — this validation is the first line of defense on the server. Return a descriptive `MetaActionResult` on validation failure. Specifically check:
  - Numeric ranges: quantities, amounts, counts (cannot sell a negative count to gain items, cannot specify zero or excessively large values).
  - Collection sizes: collections should have reasonable maximum sizes.
  - References: GameConfig references and other lookups should be validated for existence.

- **S4 — Costs must be paid in the same action**: An operation's cost (spending gold, consuming items) must be deducted in the same action that grants the reward. Never split payment and reward into separate actions — a hacked client can skip the payment and send only the reward action.

#### Commit discipline

- **CM1 — No state changes outside commit**: All model modifications must be inside the `if (commit) { ... }` block. No property assignments, mutating method calls, collection modifications, or other side effects outside this block.

- **CM2 — No model modifications from listeners**: `ServerListener` and `ClientListener` callback implementations — and any code they call — must not modify model state. Listeners are for external side effects. Trace the full call chain from each listener callback to verify no model state is mutated indirectly. Exception: `ServerListener`s may modify `[ServerOnly]` fields directly, since these are not synchronized to the client.

- **CM3 — Listeners must not execute actions synchronously**: Listener callbacks — and any code they call — must not synchronously execute another action. The action system is not re-entrant; the current action is still in progress when the listener fires. Further actions must be queued to run after the current action completes (e.g. enqueue a server action, or use `MetaplaySDK.RunOnMainThreadAsync()` on the client).

- **CM4 — No action execution from actions or GameTick**: Action `Execute` methods and `GameTick()` / `OnTick()` must not synchronously execute other actions. The execution pipeline is not re-entrant — triggering an action while one is executing (or during tick processing) causes undefined behavior. Enqueue further actions for later execution instead of calling them inline.

#### Determinism

- **D1 — Actions must be deterministic**: `Execute` methods must not use:
  - `System.Random` or any non-deterministic RNG — use `RandomPCG` (the SDK's deterministic PRNG) instead.
  - `DateTime.Now`, `DateTime.UtcNow`, `Environment.TickCount`, or any wall-clock time — use the model's `CurrentTime` (a `MetaTime`).
  - Global mutable state or static variables (see D5).
  - `Linq.Enumerable.Shuffle()` or other extension methods that use `System.Random` internally.
  - Locale-dependent operations: `ToString()` on numbers without `CultureInfo.InvariantCulture`, `ToLower()` / `ToUpper()` without a culture argument (use `ToLowerInvariant()` / `ToUpperInvariant()`), `string.Compare()` or `string.Format()` without invariant culture.

- **D2 — No floating point in action logic**: `Execute` methods and their helpers must not use `float` / `double` types or floating-point math — these diverge across platforms and cause desyncs. Use `F32` (16.16 fixed-point) or `F64` (32.32 fixed-point) instead.

- **D3 — Collection type restrictions**:
  - **Determinism**: must not use `Dictionary<,>` or `HashSet<>` — non-deterministic iteration order causes desyncs. Use `MetaDictionary<,>` and `OrderedSet<>`. The only exception is a field explicitly marked `[MetaAllowNondeterministicCollection]`.
  - **Performance**: avoid `SortedSet<>` and `SortedDictionary<,>` — they work but perform poorly. Prefer `OrderedSet<>` and `MetaDictionary<,>`.

- **D4 — Use MetaTime and MetaDuration**: Time-related values in action parameters and logic should use `MetaTime` (timestamps) and `MetaDuration` (time spans) instead of raw integers, `DateTime`, or `TimeSpan`.

- **D5 — No global mutable state**: Action code must not read or write global mutable state or static variables. Multiple players execute action code concurrently on the server, so global mutable state is both a thread-safety hazard and a determinism violation. Read-only globals and constants are fine.

#### Performance & style

- **PS1 — Performance on frequently invoked actions**: Actions that run often (per-tap, per-frame, per-tick) should avoid memory allocations, LINQ queries, and expensive lookups. Flag patterns that scale poorly at high call rates.

- **PS2 — Avoid extensive defensive coding**: Inside actions, the model and GameConfig are guaranteed to be present and valid. Avoid unnecessary null checks on the model, `GameConfig`, or config library lookups. Trust the framework guarantees.

- **PS3 — Logging discipline**:
  - Use the model's `Log.Debug()` sparingly for action logging.
  - `Information` level is only acceptable in infrequent actions for significant lifecycle events (e.g. level up, first purchase). Frequently invoked actions must not log at `Information` level in their normal code path — excessive log volume in production.
  - **Never use `DebugLog`** (the static helper). Temporary local testing only — must not appear in committed code.

- **PS4 — Be careful with unsynchronized server actions**: `PlayerUnsynchronizedServerAction` (and equivalents) may only modify `[NoChecksum]` fields or invoke callbacks. Often a synchronized server action or a regular client action is more appropriate. Unsynchronized server actions are for latency-sensitive operations that don't affect game logic directly.

#### Correctness

- **CO1 — Return early on validation failure**: After returning a failure `MetaActionResult`, the action must not fall through into the `if (commit)` block. Each validation check returns immediately on failure.

- **CO2 — No side effects in validation**: Code before the `if (commit)` block must not call methods that mutate state, enqueue other actions, or produce any side effects. The pre-commit phase is strictly for validation and computing values.

#### Action design

- **AD1 — Well-named MetaActionResults**: Custom `MetaActionResult` values should have descriptive names that help diagnose issues in logs and dashboards (e.g. `NotEnoughGold`, `ItemAlreadyOwned`, `InvalidTargetLevel`). Avoid generic names like `Error` or `Failed`.

- **AD2 — Don't use actions for internal logic**: Actions carry serialization, validation, and pipeline overhead. If the caller is already inside the model (another action's `Execute` or `GameTick()`), extract the shared logic into a plain helper method on the model and call it directly. Reserve actions for operations that originate from outside the model (client requests, server-initiated triggers).

## GameConfigs

Authoring patterns and review rules for GameConfig classes — `GameConfigLibrary`, `GameConfigKeyValue` (global configs), config item classes, and cross-library references (`MetaRef<>`).

### Design patterns

Apply these when adding or modifying configs:

- **Don't change the data source of an existing config.** If a library is already backed by CSV, Google Sheets, or C# code, keep it on that source unless explicitly asked to migrate.
- **New libraries and globals default to C# code.** Use the templates below. CSV or Google Sheets are explicit opt-ins.
- **Google Sheets edits are out of scope.** If a change requires modifying a Google Sheet, tell the user and hand them the data in a table for copy-paste.
- **Pick the right container type:**
  - `GameConfigLibrary<TKey, TInfo>` — items referred to by id (troops, items, quests, levels).
  - `GameConfigKeyValue<T>` ("global config") — singleton config data that isn't referred to by id, including small "nameless" arrays.

#### GameConfigLibrary in C#

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

#### GameConfigKeyValue in C#

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

### Discovery patterns

Grep for:

- Classes implementing `IGameConfigData`, or inheriting from `GameConfigLibrary`, `SharedGameConfigBase`, `GameConfigKeyValue`.
- **`[GameConfigEntry]`** attributes.

Traverse member types: config classes often contain nested or referenced types (e.g. a `RewardDef` member class, or `MetaRef<>` fields pointing to other config types). Discover these and include them in the review — the same rules apply.

Follow references: for each config class, scan for related validation logic:

- `IGameConfigPostLoad` interface and its `PostLoad()` method.
- `[MetaOnDeserialized]` methods.
- Custom build pipeline code.
- Types referenced via `MetaRef<>`.
- Partial class declarations in other files.

Verify completeness: count `[GameConfigEntry]` attribute usages and compare against the number of discovered config classes. Investigate any discrepancy.

### Checklist

#### Type safety

- **TS1 — Type safety for identifiers**: Config key types and reference fields should use `StringId<>` instead of raw `string`. Categorical values should use enums instead of `int` or `string`. Time-related fields should use `MetaTime` or `MetaDuration` instead of `int`, `long`, `string`, `DateTime`, or `TimeSpan`.

- **TS2 — No floating point**: Config items must not use `float` / `double` types or floating-point math — non-deterministic across platforms. Use `F32` (16.16 fixed-point) or `F64` (32.32 fixed-point) instead.

- **TS3 — Collection type restrictions**:
  - **Determinism**: must not use `Dictionary<,>` or `HashSet<>` — non-deterministic iteration order. Use `MetaDictionary<,>` and `OrderedSet<>`. The only exception is a field explicitly marked `[MetaAllowNondeterministicCollection]`.
  - **Performance**: avoid `SortedSet<>` and `SortedDictionary<,>` — they work but perform poorly. Prefer `OrderedSet<>` and `MetaDictionary<,>`.

#### Config design

- **CD1 — No data duplication**: The same data should not be stored in multiple members. Look for redundant fields that could be derived or referenced instead.

- **CD2 — Immutability**: Config item fields must have private setters (`{ get; private set; }`), never public setters. Collection properties should use `IReadOnlyList<>` instead of `List<>` or arrays to prevent accidental mutation of shared config data.

- **CD3 — No post-build mutation**: Code outside the config build pipeline (i.e. outside `PostLoad()`, `[MetaOnDeserialized]`, and custom build steps) must never mutate GameConfig instances. After configs are built, they are shared immutable data. Scan usages of config types in game logic, actions, and server code — any code that assigns to a config field or mutates a config object is a bug. Computing derived data inside `PostLoad()` / `[MetaOnDeserialized]` is fine.

- **CD4 — No unnecessary initialization at declaration**: Config item fields should generally not be initialized at declaration (e.g. `= new List<int>()`) — contents come from the config build pipeline, and defaults like empty lists usually don't make sense. Exception: `GameConfigKeyValue` (global config) fields may have meaningful defaults.

- **CD5 — ConfigKey pattern**: The `ConfigKey` property should be a get-only property that returns another member of the class (e.g. `public TroopKind ConfigKey => Kind;`). It should not have its own backing field or setter.

#### Validation

- **V1 — Build-time validation**: Check that config types are properly validated during the config build. Validation may be per-class or centralized — both are valid. Look for:
  - `IGameConfigPostLoad` interface with `PostLoad()` on individual config classes.
  - `[MetaOnDeserialized]` methods that validate data.
  - Centralized validation in `SharedGameConfig`, `ServerGameConfig`, or a dedicated validation helper.
  - Custom validation in the build pipeline.
  - Only flag missing validation if a complex config type has no validation anywhere (neither per-class nor centralized).

- **V2 — Cross-library validation**: When configs reference items from other libraries (via `MetaRef<>`), check that these cross-library references are validated at build time. Metaplay automatically validates `MetaRef<>` resolution, but custom business-logic checks (e.g. "every quest must reference a valid reward tier") should be present if applicable.

## Models

Authoring patterns and review rules for entity model types — `PlayerModel` (most common), `GuildModel`, and custom multiplayer entity models — with focus on state design, deterministic types, GameTick performance, fast-forward correctness, and sub-models.

### Design patterns

Apply these when writing model code:

- **Deterministic types throughout.** `MetaDictionary<,>` over `Dictionary<,>`, `OrderedSet<>` over `HashSet<>`, `MetaTime`/`MetaDuration` over `DateTime`/`TimeSpan`/raw integers, `F32`/`F64` over `float`/`double`. See MS1–MS3.
- **Split state into sub-models.** Group related state (inventory, quests, energy, wallet) into sub-model classes annotated with `[MetaMember]`. All rules apply to sub-models the same as to the root.
- **Event-driven, not polled.** GameTick should not scan state each tick to see if anything needs doing — store "next event time" values and only act when `CurrentTime >= nextEventTime`. See GT3.
- **Fast-forward computes analytically.** `GameFastForwardTime` must produce the same result as running GameTick for the elapsed duration, but without iterating tick-by-tick — entities can be offline for weeks. See FF1.
- **Only Actions and GameTick mutate model state.** Never mutate from API endpoints, session handlers, or listeners. See MI3.
- **Bound growing collections.** Any collection that appends over a player's lifetime (history logs, completed quests) needs a pruning strategy. See MI1.

### Discovery patterns

Grep for:

- Classes inheriting from `PlayerModelBase` — most common.
- Classes inheriting from `GuildModelBase`.
- Any other `*ModelBase` classes for custom multiplayer entity models.
- `PlayerModel` and `GuildModel` partial class declarations.
- `GameTick` method overrides — signature `GameTick(IChecksumContext`.
- Fast-forward method overrides — signature `GameFastForwardTime(MetaDuration`.
- Sub-model classes — types used as `[MetaMember]` fields within the main model (e.g. `InventoryModel`, `PlayerEnergyModel`, `WalletModel`).

Traverse sub-models recursively: apply the same rules to sub-models as to the root model.

Follow references: for each class, scan for helper methods, extension methods, and utility classes called from `GameTick` or `GameFastForwardTime`. Check for partial class declarations in other files.

Verify completeness: grep for `[MetaMember]` and `[MetaSerializable]` across the project and check for model-like classes not yet discovered. Investigate any that inherit from `*ModelBase` or are used as state fields in already-discovered models.

### Checklist

#### GameTick

- **GT1 — Performance in GameTick**: GameTick runs at a fixed rate (typically 10 Hz) and is called for every online entity. It must be lightweight. Flag:
  - Iteration over large collections (scanning all inventory items, all quests, etc.).
  - LINQ queries or complex computations executed every tick.
  - Memory allocations each tick (new objects, string formatting, list creation).
  - Work that could be triggered by events instead of polled every tick.
  - Any operation that scales with the size of model data.

- **GT2 — No DebugLog**: Never use the static `DebugLog` helper in model code — not in GameTick, fast-forward, or any helper methods. For temporary local testing only; must not appear in committed code. Use `Log.Debug()` on the model if logging is needed.

- **GT3 — Avoid unnecessary per-tick work**: If logic only needs to run when a condition changes (timer expires, threshold crossed), prefer checking a precomputed "next event time" rather than evaluating the full condition every tick. E.g. store the `MetaTime` when the next recharge completes, and only process it when `CurrentTime >= nextRechargeTime`.

- **GT4 — GameTick listeners must not execute actions synchronously**: If GameTick invokes listener callbacks, those callbacks — and any code they call — must not synchronously execute actions. The action system is not re-entrant. Actions must be queued to run after GameTick completes.

#### Fast-forward

- **FF1 — Fast-forward must not iterate**: `GameFastForwardTime` is called when an entity reconnects after being offline. It must not simulate the offline period tick-by-tick or loop proportional to elapsed time (e.g. `while (remaining > tickInterval) { ... }`). Such loops take extremely long for entities offline for days or weeks. Compute results analytically from the elapsed `MetaDuration`.

#### General model state

- **MS1 — Collection type restrictions**:
  - **Determinism**: must not use `Dictionary<,>` or `HashSet<>` — non-deterministic iteration order causes desyncs. Use `MetaDictionary<,>` and `OrderedSet<>`. The only exception is a field explicitly marked `[MetaAllowNondeterministicCollection]`.
  - **Performance**: avoid `SortedSet<>` and `SortedDictionary<,>` — they work but perform poorly. Prefer `OrderedSet<>` and `MetaDictionary<,>`.

- **MS2 — No floating point in model state or logic**: Model members must not use `float` / `double` types. Floating-point math produces different results across platforms, causing desyncs. Use `F32` / `F64` (fixed-point) instead. Also check methods called from GameTick and fast-forward for floating-point operations.

- **MS3 — Use MetaTime and MetaDuration**: Time-related fields should use `MetaTime` (timestamps) and `MetaDuration` (time spans) instead of raw integers, `DateTime`, or `TimeSpan`.

- **MS4 — Determinism in model methods**: All methods that run as part of the game simulation (GameTick, fast-forward, action execution) must be deterministic. Check for:
  - `System.Random` — use `RandomPCG` (the SDK's deterministic PRNG) instead.
  - `DateTime.Now`, `DateTime.UtcNow`, `Environment.TickCount` — use `CurrentTime` (a `MetaTime`).
  - `Linq.Enumerable.Shuffle()` or other extension methods that internally use `System.Random`.
  - Non-deterministic iteration over collections.
  - Locale-dependent operations: `ToString()` on numbers without `CultureInfo.InvariantCulture`, `ToLower()` / `ToUpper()` without a culture argument (use `ToLowerInvariant()` / `ToUpperInvariant()`), `string.Compare()` or `string.Format()` without invariant culture.
  - External or global state that may differ between client and server (see MS5).

- **MS5 — No global mutable state**: Model code (GameTick, fast-forward, and any methods they call) must not read or write global mutable state or static variables. On the server, multiple entities execute model code concurrently — global mutable state is both a thread-safety hazard and a determinism violation. Read-only globals and constants are fine.

#### Model invariants

- **MI1 — Unbounded collection growth**: Look for collections (lists, dictionaries) in the model that grow over the player's lifetime but are never pruned. E.g. a history log that appends forever, or completed quest records that accumulate indefinitely. Causes increasing memory usage and slower serialization over time.

- **MI2 — Correct [ServerOnly] and [NoChecksum] usage**: Fields marked `[ServerOnly]` are not sent to the client and must not be read in client-executed code paths. Fields marked `[NoChecksum]` are excluded from checksum validation — only use this for fields that are intentionally allowed to differ between client and server.

- **MI3 — Model state must only be modified in Actions and GameTick**: Model members — including all transitive state in sub-models — must only be modified from within Action `Execute` methods (inside `if (commit)`) or GameTick. Any other code path that mutates model state (actor event handlers, API endpoints, session logic) will cause desyncs or bypass the action system's guarantees.
