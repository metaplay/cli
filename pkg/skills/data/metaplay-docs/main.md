# Metaplay docs

The `metaplay` CLI exposes a remote documentation service designed for AI coding agents. Treat it like a read-only filesystem covering the Metaplay SDK source, sample projects, the full docs site, blog posts, and the CLI reference — searchable from the shell.

All commands live under `metaplay llm-docs` and print plain text to stdout.

| Command | Use it to... |
|---|---|
| `search QUERY --keywords k1,k2,...` | Submit a natural-language question plus pre-extracted keywords and get back a catalog of relevant entry points to follow up on. |
| `read PATH` | Fetch a single file by path (e.g. `index.md`, `docs/cloud-deployments/getting-started`, `MetaplaySDK/version.yaml`). `.md` is auto-appended server-side when no extension is given. |
| `ripgrep PATTERN [flags]` | Full ripgrep against the payload. Supports `-i`, `-F`, `-n`, `-l`, `-c`, `-C`/`-B`/`-A`, `--multiline`, `--type`, `--glob`, `--path`. |
| `glob PATTERN [--path SUBDIR]` | List files matching a glob (e.g. `**/*.md`, `**/PlayerActorBase.cs`). |
| `info` | Show deployment metadata JSON (which SDK version the service currently ships, etc.). Useful when results look stale. |

## Payload layout

The remote payload is organized into top-level subtrees, each with its own `index.md`:

- `docs/` — full SDK documentation (markdown)
- `MetaplaySDK/` — SDK source: `Backend/`, `Client/`, `Frontend/`, `Plugins/`, plus `version.yaml`
- `samples/` — sample projects: `HelloWorld/`, `HelloNFT/`, `Idler/`, `Wordle/`, `orca/` (merge-2 game), `trashdash-sample/`
- `website/` — Metaplay blog posts and customer case studies
- `cli/` — `metaplay` CLI command reference

## Workflow

**Rule: `search` is always the first call.** Even if you think you already know the right file or symbol, run `search` first — it's the canonical entry point and returns the catalog you should use to decide what to `read`, `ripgrep`, or `glob` next. Those commands are follow-ups, not alternatives.

1. `search` — first call, every time.
2. `read` — open specific files `search` pointed at, or a per-subtree `index.md` to explore further.
3. `ripgrep` — hunt symbols, class references, or literal/regex matches. Scope aggressively with `--type cs` / `--type md` / `--path MetaplaySDK/Backend`; the payload is large.
4. `glob` — enumerate files when you want a list of paths rather than content.

Loop steps 2–4 as you narrow down.

## Calling notes

- `search` mechanics: pass the user's question verbatim as `QUERY`, and extract 3–7 informative keywords yourself. `--keywords` is comma-separated; quote the whole value if any keyword contains spaces: `--keywords "guild actor,members,social"`.
- Cite sources to the user as payload-relative paths (e.g. `docs/game-logic/player-actor.md`, `MetaplaySDK/Backend/Server/Player/PlayerActorBase.cs`). They can open any such path with `metaplay llm-docs read <path>`.
- `ripgrep` output may prefix matches with `/app/payload/` — strip that before quoting paths back to the user.
- These commands are marked `[preview]` and their output shape is not stable. Summarize rather than pasting raw output unless the user asked for it verbatim.
- Authentication: the CLI reuses the user's `metaplay auth` session if present. Unauthenticated calls still work for public content.
- `NotFound` usually means a bad path — run `metaplay llm-docs read index.md` for the root catalog. `Unavailable` means network/service trouble, not a bad query.
- To confirm which SDK release the payload reflects, run `metaplay llm-docs info` or `metaplay llm-docs read MetaplaySDK/version.yaml`. Flag version mismatches if the user's project pins a different release.

## Examples

```bash
# Broad feature question — user wants guilds with social + competitive aspects.
metaplay llm-docs search \
  "How do I add guilds to my game so players can join, chat, and compete on a shared leaderboard?" \
  --keywords "guilds,guild actor,membership,invites,chat,leaderboards,social,multiplayer"

# Live-ops / persistence question.
metaplay llm-docs search \
  "What's the recommended way to implement a daily reward with streak bonuses that survives server restarts?" \
  --keywords "daily reward,streak,PlayerModel,persistence,MetaTime,reset schedule,liveops,claim"

# Game config architecture question.
metaplay llm-docs search \
  "What's the best pattern for a game config with typed ids, references between entries, and build-time validation?" \
  --keywords "game config,StringId,MetaRef,ConfigKey,build-time validation,shared code,typed ids,deterministic"

# Persisted-state migration question — comes up whenever PlayerModel evolves.
metaplay llm-docs search \
  "I added a new field to PlayerModel and existing players fail to load — how do I write a schema migration so old saves keep working?" \
  --keywords "PlayerModel,schema version,entity migration,MigrateFromV1,MetaMember,persisted state,backward compatibility,serialization"

# Read a specific doc page.
metaplay llm-docs read docs/cloud-deployments/getting-started

# Find every class that extends EntityActor across the SDK sources.
metaplay llm-docs ripgrep "class\s+\w+\s*:\s*EntityActor" --multiline --type cs -n

# Count usages of a specific API in C# sources.
metaplay llm-docs ripgrep "PublishEvent" -c --type cs

# List only filenames that mention a symbol (cheap triage).
metaplay llm-docs ripgrep PlayerActorBase -l

# List all markdown docs under a subdirectory.
metaplay llm-docs glob "**/*.md" --path docs/cloud-deployments

# Find a file by name anywhere in the payload.
metaplay llm-docs glob "**/PlayerActorBase.cs"

# Explore a sample project.
metaplay llm-docs read samples/index.md
metaplay llm-docs glob "**/*.cs" --path samples/HelloWorld

# Check which SDK version the service currently ships.
metaplay llm-docs info
```

## When NOT to use this skill

- Generic programming questions (Go, C#, Unity, Docker) that aren't Metaplay-specific.
- When the user explicitly asked for a web search, an offline answer, or their own local notes.
