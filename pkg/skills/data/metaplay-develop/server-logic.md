---
name: metaplay-develop-server-logic
description: Playbook for server-only code in a Metaplay project. Covers player actor message handlers and session start, custom entities and service actors, entity asks and background work, custom database tables and queries, database scan jobs over all players, LiveOps Dashboard API endpoints, public HTTP endpoints for third-party callbacks, and runtime options and secrets. Load before writing code under `Backend/Server`.
---

# Server-side code

Code under `Backend/Server` runs only on the server. The determinism rules for shared game logic do not bind it, but two other constraints do: an actor handles one message at a time, and every player shares one database. Each rule names the doc page it comes from; read it with `metaplay llm-docs read <path>` before using the feature.

## Actors and messages

- **The client talks to its own player actor.** A client request is a `MetaMessage` with `MessageDirection.ClientToServer` and `[MessageRoutingRuleOwnedPlayer]`, handled by a `[MessageHandler]` method in `PlayerActor`, which answers with `SendToClient`. Docs: `docs/feature-cookbooks/leagues/configuring-leagues.md` (the join request is a complete example).
- **Shared server entities get player data from the player actor.** A leaderboard, matchmaker or social service receives values the player actor reads from its validated model, sent when they change (a new best score, not every run). Values the client sends are untrusted, so the client never addresses these entities. Docs: `docs/game-server-programming/entities-and-actors/custom-entities.md`.
- **One copy of a service** is a custom entity with `ShardingStrategies.CreateSingletonService()` and `NodeSetPlacement.Service`. Docs: `docs/game-server-programming/entities-and-actors/introduction-to-entities-and-actors.md`.
- **A slow handler blocks the actor.** Every message queued behind an awaited HTTP call or long query waits. In a service that many players call, take the request as `EntityAsk<TReply>` in a `void` `[EntityAskHandler]`, start the work with `ContinueTaskOnActorContext(task, onSuccess, onFailure)`, and answer from the callbacks with `ask.Reply(...)` or `ask.Refuse(...)`. Change actor state only inside the callbacks. Docs: `docs/game-server-programming/entities-and-actors/deep-dive-entity-to-entity-communication.md` ("Long-Running EntityAsks").
- **Session start must finish within `SessionEntitySubscribeTimeout`** (10 s by default) or the login fails. Fetch what the client needs but login does not with `ContinueTaskOnActorContext`, and deliver it when it arrives, with `SendToClient` or a server action.
- **Async methods return `Task`.** An exception thrown from `async void` never reaches a caller.
- **A cache in a long-lived actor needs a size cap or expiry that evicts.**

## Database

- **Answer a request without reading every row or entity.** Query with `WHERE`, `ORDER BY` and `LIMIT` on an indexed column. To sort or filter entities by a value inside their payload (creation time, last activity), add an `[Index]`ed column to the persisted class (for example the project's `PersistedGuild`), set it when the entity is persisted, and add a migration. Dashboard queries use `QueryPriority.Low`, background scans `QueryPriority.Lowest`. Docs: `docs/game-server-programming/how-to-guides/working-with-the-database.md`.
- **Custom tables**: an `IPersistedItem` class with `[Table]`, `[Key]` and `[PartitionKey]`, read and written through `MetaDatabase`. Every new table, column or index needs `dotnet ef migrations add <Name>` in `Backend/Server`.
- **Work over all players runs as a database scan job**: a `DatabaseScanProcessor<PersistedPlayerBase>` and a `MaintenanceJobSpec`, registered with `[EntityMaintenanceJob]` on `PlayerConfig` and started from the dashboard. Scan jobs throttle themselves and survive restarts. Docs: `docs/game-server-programming/how-to-guides/database-scan-jobs.md`.

## HTTP endpoints and settings

- **Dashboard endpoints** are controllers deriving from `GameAdminApiController`, each endpoint with `[RequirePermission(...)]`. Docs: `docs/liveops-dashboard/how-to-guides/working-with-the-liveops-dashboard-http-api.md`.
- **Third-party callbacks** (ad rewards, offerwalls, payment webhooks) go to controllers deriving from `PublicWebApiController`. Any node can serve a request, so state that must hold across requests (processed transaction ids) lives in the player's model, through `ApiBridge.EnqueuePlayerServerActionAsync` and a server action, or in the database. Docs: `docs/game-server-programming/how-to-guides/public-web-endpoints.md`.
- **Settings are runtime options**: a class deriving from `RuntimeOptionsBase` with `[RuntimeOptions(...)]`, set in `Backend/Server/Config/Options.*.yaml`. A secret is referenced as `kube-secret://<name>#<key>` (see `metaplay-devops-secrets`). Docs: `docs/game-server-programming/how-to-guides/working-with-runtime-options.md`.
