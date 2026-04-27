# Player incident report analysis

Diagnose an individual player's crash, desync, or network incident from a Metaplay game environment's admin API. Use when the user pastes a dashboard incident URL, asks about "the latest incidents" or "recent incidents", or wants a specific player incident report triaged. For broader production symptoms (pods down, CPU spike, login drop) run the scenario playbooks in `metaplay skills get metaplay-devops` instead — those target the cluster, not one player.

## Environment selection

Same rule as every other `metaplay debug` command (see `metaplay skills get metaplay-devops`):

- Read `metaplay-project.yaml` for configured environments.
- If the user pasted a dashboard URL like `https://<env-id>-admin.p1.metaplay.io/players/Player:X/incidentReports/Y`, match the hostname prefix against `metaplay-project.yaml` to recover the env name.
- If multiple envs and no URL, ask the user which to target before running anything.

## Fetching incident data

### Case A — URL given for a specific incident

Dashboard URLs follow `https://<host>/players/<PlayerId>/incidentReports/<IncidentId>`. Parse the path, then:

```bash
metaplay debug admin-request <env> GET api/players/<PlayerId>/incidentReport/<IncidentId>
```

### Case B — no specific incident named

Show the user the recent-incident catalog and let them pick.

1. Fetch last-24h statistics (grouped by fingerprint, with counts):
   ```bash
   metaplay debug admin-request <env> GET api/incidentReports/statistics
   ```
   Response is `List<PlayerIncidentStatistics>` with:
   - `Fingerprint` — MD5 of Type/SubType/Reason (use to fetch specific incidents)
   - `Type` — e.g. `TerminatedConnection`, `UnhandledException`, `ChecksumMismatch`
   - `SubType` — more specific classification
   - `Reason` — human-readable description
   - `Count`, `CountIsLimitedByQuerySize`

2. Display the top 15, sorted by `Count` descending:
   ```
    #  | Count | Type                    | Reason
   ----|-------|-------------------------|------------------------------------------
    1  |   847 | TerminatedConnection    | Session terminated: ConnectionTimeout
    2  |   523 | UnhandledException      | NullReferenceException in PlayerActor
    3  |   156 | ChecksumMismatch        | Desync detected after PlayerBuyItem
   ```
   Include `SubType` in parens after `Type` when it adds clarity.

3. Ask the user to pick a number. Grab that entry's `Fingerprint`.

4. Fetch one sample instance via the fingerprint:
   ```bash
   metaplay debug admin-request <env> GET api/incidentReports/<Fingerprint>/1
   ```
   Response is `List<PlayerIncidentHeader>` — take the first entry's `IncidentId` and `PlayerId`.

5. Fetch full details using the same URL form as Case A.

## Incident data surface

The full response is `IncidentDashboardInfo`:

| Field | Contents |
|---|---|
| `ErrorInfo` | `ErrorType`, `ErrorMessage`, `StackTrace` |
| `ClientLogEntries` | Client logs leading up to the incident |
| `ClientSystemInfo`, `ClientPlatformInfo` | Device, platform, OS |
| `ApplicationInfo` | App version, build info |
| `NetworkInfo`, `NetworkReport` | Connection state, latency, packet loss |
| `GameConfigInfo` | Active game config version on the client |
| `PlayerModelDiffReport` | Desyncs: field-level diff between client and server state |
| `ExtraInfo` | Custom game-specific payload |
| `OccurredAt` | Incident timestamp |

## Classify, then diagnose by type

Identify the incident type from `ErrorInfo.ErrorType` (for exceptions) or `Type` from the statistics row. Then apply the matching heuristics below.

### Desync / `ChecksumMismatch`

Client and server state diverged.

- **Look at `PlayerModelDiffReport` field by field.** The specific members that differ point directly at the buggy code path. Note both the field name and the mismatched values.
- **Cross-reference `ClientLogEntries`** for the action executed right before the checksum mismatch — that action's `Execute` is the likely culprit.
- **Common causes:**
  - Non-deterministic action or model code — `System.Random`, `DateTime.Now`, raw `Dictionary<,>`/`HashSet<>` iteration. The `metaplay-develop` skill's rules `D1`, `D3`, `MS1`, `MS4` cover these in detail.
  - Missing `[Transient]` attribute on a field that shouldn't be serialized but was.
  - Config version mismatch — compare the client's `GameConfigInfo` version against what the server was running.
  - Floating-point math in game logic (see `metaplay-develop` `D2`/`MS2`).

### `UnhandledException`

Exception thrown during game logic execution.

- **`ErrorInfo.ErrorType` + first frames of `ErrorInfo.StackTrace`** identify the failing method.
- **`ApplicationInfo.Version`** — does the exception repro on the latest build, or is the client stale?
- **Common causes:** null dereference in model/config access, invalid state assumption, config lookup for a removed key, serialization issue (missing `[MetaSerializable]`/`[MetaMember]` somewhere).

### Network issue

Connection problems, timeouts.

- **`NetworkInfo` / `NetworkReport`** — look for disconnect reason, RTT, packet loss pattern.
- Usually client-side (bad network, backgrounded app) rather than server bug, but check server logs for the same timestamp via `metaplay debug logs <env>` just in case.

### Session termination

Unexpected disconnect.

- **`SubType` / `Reason`** — typical values: `ConnectionTimeout`, `AuthenticationError`, server-initiated kick.
- Cross-reference with server-side logs for the incident timestamp: `metaplay debug logs <env> --since-time=<OccurredAt>`.

### Custom incident

Game-specific incident type — inspect `ExtraInfo` for the custom payload and follow the reason field.

## Report the diagnosis

Structure the response as:

1. **Summary.** One-paragraph diagnosis of what likely happened.
2. **Root cause analysis.** Most-likely causes ranked by probability, each with evidence (specific stack trace line, diff field, log entry, version mismatch).
3. **Recommended actions.** Concrete code changes or config adjustments to investigate, plus a repro or verification path.
4. **Related documentation.** Payload-relative paths to relevant SDK docs (readable via `metaplay llm-docs read <path>`) and similar sample patterns. Defer to the `metaplay-docs` skill for deeper SDK lookups.

## Error handling

- **401/authentication**: user needs `metaplay auth login`.
- **Environment not found**: verify the env name matches one in `metaplay-project.yaml`.
- **403 permission denied**: user lacks `api.incidents.view` for this environment.
- **404 not found**: the incident was purged or the ID is wrong.
- **CLI/API error**: suggest checking the dashboard directly at the env's admin URL.
