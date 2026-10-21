# Metaplay troubleshooting

Diagnostic playbooks for Metaplay tooling and SDK problems.

{{subskills}}

## Dashboard failures

If `metaplay dev dashboard` or `metaplay build dashboard` starts behaving strangely (stale assets, weird Node errors, dependency mismatches), run: `metaplay dev clean-dashboard-artifacts`

## Where else to look

- **General "how do I X in Metaplay" questions** (SDK, dashboard, cloud deployments, samples): `metaplay skills get metaplay-docs`.
- **C# authoring problems and per-player incident triage** (PlayerAction/GuildAction, GameConfig, PlayerModel/GuildModel, GameTick, fast-forward, dashboard incident reports): `metaplay skills get metaplay-develop`.
- **Operating a deployed cloud server** (deploy, redeploy, view logs, CPU/heap profile, manage env secrets, "the server is down"): `metaplay skills get metaplay-devops`.

Many weird Metaplay errors are resolved by updating the CLI: `metaplay update cli`.
