# Models

Authoring patterns and review rules for all entity model types — `PlayerModel` (most common), `GuildModel`, and custom multiplayer entity models — with focus on state design, GameTick performance, and fast-forward correctness. See `metaplay skills get metaplay-develop` for the shared workflow and severity convention.

## Design patterns

Apply these when writing model code:

- **Deterministic types throughout.** `MetaDictionary<,>` over `Dictionary<,>`, `OrderedSet<>` over `HashSet<>`, `MetaTime`/`MetaDuration` over `DateTime`/`TimeSpan`/raw integers, `F32`/`F64` over `float`/`double`. See MS1–MS3.
- **Split state into sub-models.** Group related state (inventory, quests, energy, wallet) into sub-model classes annotated with `[MetaMember]`. All rules apply to sub-models the same as to the root.
- **Event-driven, not polled.** GameTick should not scan state each tick to see if anything needs doing — store "next event time" values and only act when `CurrentTime >= nextEventTime`. See GT3.
- **Fast-forward computes analytically.** `GameFastForwardTime` must produce the same result as running GameTick for the elapsed duration, but without iterating tick-by-tick — entities can be offline for weeks. See FF1.
- **Only Actions and GameTick mutate model state.** Never mutate from API endpoints, session handlers, or listeners. See MI3.
- **Bound growing collections.** Any collection that appends over a player's lifetime (history logs, completed quests) needs a pruning strategy. See MI1.

## Discovery patterns

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

## Checklist

### GameTick

- **GT1 — Performance in GameTick**: GameTick runs at a fixed rate (typically 10 Hz) and is called for every online entity. It must be lightweight. Flag:
  - Iteration over large collections (scanning all inventory items, all quests, etc.).
  - LINQ queries or complex computations executed every tick.
  - Memory allocations each tick (new objects, string formatting, list creation).
  - Work that could be triggered by events instead of polled every tick.
  - Any operation that scales with the size of model data.

- **GT2 — No DebugLog**: Never use the static `DebugLog` helper in model code — not in GameTick, fast-forward, or any helper methods. For temporary local testing only; must not appear in committed code. Use `Log.Debug()` on the model if logging is needed.

- **GT3 — Avoid unnecessary per-tick work**: If logic only needs to run when a condition changes (timer expires, threshold crossed), prefer checking a precomputed "next event time" rather than evaluating the full condition every tick. E.g. store the `MetaTime` when the next recharge completes, and only process it when `CurrentTime >= nextRechargeTime`.

- **GT4 — GameTick listeners must not execute actions synchronously**: If GameTick invokes listener callbacks, those callbacks — and any code they call — must not synchronously execute actions. The action system is not re-entrant. Actions must be queued to run after GameTick completes.

### Fast-forward

- **FF1 — Fast-forward must not iterate**: `GameFastForwardTime` is called when an entity reconnects after being offline. It must not simulate the offline period tick-by-tick or loop proportional to elapsed time (e.g. `while (remaining > tickInterval) { ... }`). Such loops take extremely long for entities offline for days or weeks. Compute results analytically from the elapsed `MetaDuration`.

### General model state

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

### Model invariants

- **MI1 — Unbounded collection growth**: Look for collections (lists, dictionaries) in the model that grow over the player's lifetime but are never pruned. E.g. a history log that appends forever, or completed quest records that accumulate indefinitely. Causes increasing memory usage and slower serialization over time.

- **MI2 — Correct [ServerOnly] and [NoChecksum] usage**: Fields marked `[ServerOnly]` are not sent to the client and must not be read in client-executed code paths. Fields marked `[NoChecksum]` are excluded from checksum validation — only use this for fields that are intentionally allowed to differ between client and server.

- **MI3 — Model state must only be modified in Actions and GameTick**: Model members — including all transitive state in sub-models — must only be modified from within Action `Execute` methods (inside `if (commit)`) or GameTick. Any other code path that mutates model state (actor event handlers, API endpoints, session logic) will cause desyncs or bypass the action system's guarantees.
