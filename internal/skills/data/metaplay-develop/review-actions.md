# Actions

Authoring patterns and review rules for all entity action types — `PlayerAction`, `GuildAction`, and custom multiplayer entity actions. See `metaplay skills get metaplay-develop` for the shared workflow and severity convention.

## Design patterns

Apply these when writing new actions:

- **Transactional by default.** An action should spend the cost and grant the reward in the same `Execute`. E.g. `PlayerPurchaseItem` deducts gold and adds the item in one atomic step. Never split cost and reward into separate actions — a hacked client will skip the cost action. See S4.
- **Resource-granting-only actions are debug-only.** An action that adds resources without spending anything (e.g. `PlayerGainGoldDebug`) must carry `[DevelopmentOnlyAction]` so it cannot execute in production. See S1.
- **State-only features don't need actions.** If a feature only holds model state and has no behavior, skip the action entirely. Debug actions may still be useful for testing.
- **Server-authoritative data comes from the model or config, not from client parameters.** Prices, reward amounts, and quantities must be looked up from `GameConfig` or model state inside `Execute`. Client-supplied parameters are untrusted. See S2.
- **Validate before the `if (commit)` block, mutate inside it.** The pre-commit phase is pure validation and computed values. All state changes go inside `if (commit) { ... }`. See CM1, CO2.

## Discovery patterns

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

## Checklist

### Security

- **S1 — No cheatable actions**: Actions that grant resources (gold, items, XP, etc.) without spending anything in return are cheatable. These must carry the `[DevelopmentOnlyAction]` attribute to prevent execution in production. Only transactional actions (spend + gain) should ship to production. See also S4.

- **S2 — No untrusted parameters**: Action parameters (data from the client) must not include values that should be server-authoritative: prices, reward amounts, resource quantities, or anything that directly affects inventory or economy. These values must come from GameConfig or model state and be looked up inside `Execute`.

- **S3 — Input validation before commit**: All input parameters must be validated before the `if (commit)` block. Hacked clients can send any action with arbitrary arguments — this validation is the first line of defense on the server. Return a descriptive `MetaActionResult` on validation failure. Specifically check:
  - Numeric ranges: quantities, amounts, counts (cannot sell a negative count to gain items, cannot specify zero or excessively large values).
  - Collection sizes: collections should have reasonable maximum sizes.
  - References: GameConfig references and other lookups should be validated for existence.

- **S4 — Costs must be paid in the same action**: An operation's cost (spending gold, consuming items) must be deducted in the same action that grants the reward. Never split payment and reward into separate actions — a hacked client can skip the payment and send only the reward action.

### Commit discipline

- **CM1 — No state changes outside commit**: All model modifications must be inside the `if (commit) { ... }` block. No property assignments, mutating method calls, collection modifications, or other side effects outside this block.

- **CM2 — No model modifications from listeners**: `ServerListener` and `ClientListener` callback implementations — and any code they call — must not modify model state. Listeners are for external side effects. Trace the full call chain from each listener callback to verify no model state is mutated indirectly. Exception: `ServerListener`s may modify `[ServerOnly]` fields directly, since these are not synchronized to the client.

- **CM3 — Listeners must not execute actions synchronously**: Listener callbacks — and any code they call — must not synchronously execute another action. The action system is not re-entrant; the current action is still in progress when the listener fires. Further actions must be queued to run after the current action completes (e.g. enqueue a server action, or use `MetaplaySDK.RunOnMainThreadAsync()` on the client).

- **CM4 — No action execution from actions or GameTick**: Action `Execute` methods and `GameTick()` / `OnTick()` must not synchronously execute other actions. The execution pipeline is not re-entrant — triggering an action while one is executing (or during tick processing) causes undefined behavior. Enqueue further actions for later execution instead of calling them inline.

### Determinism

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

### Performance & style

- **PS1 — Performance on frequently invoked actions**: Actions that run often (per-tap, per-frame, per-tick) should avoid memory allocations, LINQ queries, and expensive lookups. Flag patterns that scale poorly at high call rates.

- **PS2 — Avoid extensive defensive coding**: Inside actions, the model and GameConfig are guaranteed to be present and valid. Avoid unnecessary null checks on the model, `GameConfig`, or config library lookups. Trust the framework guarantees.

- **PS3 — Logging discipline**:
  - Use the model's `Log.Debug()` sparingly for action logging.
  - `Information` level is only acceptable in infrequent actions for significant lifecycle events (e.g. level up, first purchase). Frequently invoked actions must not log at `Information` level in their normal code path — excessive log volume in production.
  - **Never use `DebugLog`** (the static helper). Temporary local testing only — must not appear in committed code.

- **PS4 — Be careful with unsynchronized server actions**: `PlayerUnsynchronizedServerAction` (and equivalents) may only modify `[NoChecksum]` fields or invoke callbacks. Often a synchronized server action or a regular client action is more appropriate. Unsynchronized server actions are for latency-sensitive operations that don't affect game logic directly.

### Correctness

- **CO1 — Return early on validation failure**: After returning a failure `MetaActionResult`, the action must not fall through into the `if (commit)` block. Each validation check returns immediately on failure.

- **CO2 — No side effects in validation**: Code before the `if (commit)` block must not call methods that mutate state, enqueue other actions, or produce any side effects. The pre-commit phase is strictly for validation and computing values.

### Action design

- **AD1 — Well-named MetaActionResults**: Custom `MetaActionResult` values should have descriptive names that help diagnose issues in logs and dashboards (e.g. `NotEnoughGold`, `ItemAlreadyOwned`, `InvalidTargetLevel`). Avoid generic names like `Error` or `Failed`.

- **AD2 — Don't use actions for internal logic**: Actions carry serialization, validation, and pipeline overhead. If the caller is already inside the model (another action's `Execute` or `GameTick()`), extract the shared logic into a plain helper method on the model and call it directly. Reserve actions for operations that originate from outside the model (client requests, server-initiated triggers).
