# Metaplay troubleshooting

Diagnostic playbooks for Metaplay tooling and SDK problems.

## Where else to look

- **General "how do I X in Metaplay" questions** (SDK, dashboard, cloud deployments, samples): `metaplay skills get metaplay-docs`.
- **C# authoring problems and per-player incident triage** (PlayerAction/GuildAction, GameConfig, PlayerModel/GuildModel, GameTick, fast-forward, dashboard incident reports): `metaplay skills get metaplay-develop`.

## Keep the CLI current

Many "weird Metaplay error" reports are resolved by updating the CLI:

```bash
metaplay update cli
```

If the CLI itself does not run at all, follow the install paths in the `metaplay-troubleshoot` wrapper (`.claude/skills/metaplay-troubleshoot/SKILL.md` or `.agents/skills/metaplay-troubleshoot/SKILL.md`).
