# GameConfigs

Authoring patterns and review rules for GameConfig classes — `GameConfigLibrary`, `GameConfigKeyValue` (global configs), config item classes, and cross-library references. See `metaplay skills get metaplay-develop` for the shared workflow and severity convention.

## Design patterns

Apply these when adding or modifying configs:

- **Don't change the data source of an existing config.** If a library is already backed by CSV, Google Sheets, or C# code, keep it on that source unless explicitly asked to migrate.
- **New libraries and globals default to C# code.** Use the templates below. CSV or Google Sheets are explicit opt-ins.
- **Google Sheets edits are out of scope.** If a change requires modifying a Google Sheet, tell the user and hand them the data in a table for copy-paste.
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

## Discovery patterns

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

## Checklist

### Type safety

- **TS1 — Type safety for identifiers**: Config key types and reference fields should use `StringId<>` instead of raw `string`. Categorical values should use enums instead of `int` or `string`. Time-related fields should use `MetaTime` or `MetaDuration` instead of `int`, `long`, `string`, `DateTime`, or `TimeSpan`.

- **TS2 — No floating point**: Config items must not use `float` / `double` types or floating-point math — non-deterministic across platforms. Use `F32` (16.16 fixed-point) or `F64` (32.32 fixed-point) instead.

- **TS3 — Collection type restrictions**:
  - **Determinism**: must not use `Dictionary<,>` or `HashSet<>` — non-deterministic iteration order. Use `MetaDictionary<,>` and `OrderedSet<>`. The only exception is a field explicitly marked `[MetaAllowNondeterministicCollection]`.
  - **Performance**: avoid `SortedSet<>` and `SortedDictionary<,>` — they work but perform poorly. Prefer `OrderedSet<>` and `MetaDictionary<,>`.

### Config design

- **CD1 — No data duplication**: The same data should not be stored in multiple members. Look for redundant fields that could be derived or referenced instead.

- **CD2 — Immutability**: Config item fields must have private setters (`{ get; private set; }`), never public setters. Collection properties should use `IReadOnlyList<>` instead of `List<>` or arrays to prevent accidental mutation of shared config data.

- **CD3 — No post-build mutation**: Code outside the config build pipeline (i.e. outside `PostLoad()`, `[MetaOnDeserialized]`, and custom build steps) must never mutate GameConfig instances. After configs are built, they are shared immutable data. Scan usages of config types in game logic, actions, and server code — any code that assigns to a config field or mutates a config object is a bug. Computing derived data inside `PostLoad()` / `[MetaOnDeserialized]` is fine.

- **CD4 — No unnecessary initialization at declaration**: Config item fields should generally not be initialized at declaration (e.g. `= new List<int>()`) — contents come from the config build pipeline, and defaults like empty lists usually don't make sense. Exception: `GameConfigKeyValue` (global config) fields may have meaningful defaults.

- **CD5 — ConfigKey pattern**: The `ConfigKey` property should be a get-only property that returns another member of the class (e.g. `public TroopKind ConfigKey => Kind;`). It should not have its own backing field or setter.

### Validation

- **V1 — Build-time validation**: Check that config types are properly validated during the config build. Validation may be per-class or centralized — both are valid. Look for:
  - `IGameConfigPostLoad` interface with `PostLoad()` on individual config classes.
  - `[MetaOnDeserialized]` methods that validate data.
  - Centralized validation in `SharedGameConfig`, `ServerGameConfig`, or a dedicated validation helper.
  - Custom validation in the build pipeline.
  - Only flag missing validation if a complex config type has no validation anywhere (neither per-class nor centralized).

- **V2 — Cross-library validation**: When configs reference items from other libraries (via `MetaRef<>`), check that these cross-library references are validated at build time. Metaplay automatically validates `MetaRef<>` resolution, but custom business-logic checks (e.g. "every quest must reference a valid reward tier") should be present if applicable.
