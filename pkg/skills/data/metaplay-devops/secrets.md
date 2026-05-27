---
name: metaplay-devops-secrets
description: Manage per-environment Kubernetes user secrets via `metaplay secrets` — create, update, list, show, delete. Covers the `user-` name prefix requirement, the `kube-secret://<name>#<key>` runtime-options syntax for referencing values from server config, and `--from-literal` vs `--from-file` for input.
---

# Per-environment secrets

`metaplay secrets …` manages Kubernetes secrets in a cloud environment. Each secret is a name plus a set of key-value entries; the game server reads them at runtime via a special URL syntax in its `Options.*.yaml` files.

## Naming rule

Secret names **must start with `user-`** — e.g. `user-mysecret`, `user-stripe-keys`. The prefix exists to keep user-managed secrets out of the way of secrets the SDK and platform manage. The CLI rejects non-`user-` names.

## How the server reads a secret

In server runtime options (`Options.*.yaml`), reference a secret entry via the special URL form:

```
kube-secret://<secretName>#<entryKey>
```

Example: a secret `user-stripe-keys` with entry `apikey=sk_live_...` is referenced as `kube-secret://user-stripe-keys#apikey`. The game server resolves the URL at startup and substitutes the actual value.

## Commands

```bash
# Create a secret with one or more entries.
metaplay secrets create <env> user-mysecret \
  --from-literal=username=foobar \
  --from-literal=password=tops3cret

# Entry value from a file (each --from-file produces one entry whose key
# is the LHS of the '=' and value is the file contents).
metaplay secrets create <env> user-mysecret \
  --from-file=credentials=./credentials.json

# Idempotent create: replace if it exists. Useful for scripting and CI.
metaplay secrets create <env> user-mysecret \
  --from-literal=apikey=secret123 --overwrite

# Update — add new entries, replace existing entries, remove by key.
# At least one of --from-literal / --from-file / --remove is required.
metaplay secrets update <env> user-mysecret --from-literal=password=newvalue
metaplay secrets update <env> user-mysecret --from-file=cert=./cert.pem
metaplay secrets update <env> user-mysecret --remove=oldkey
metaplay secrets update <env> user-mysecret \
  --from-literal=password=new --remove=deprecated-key

# List — values censored by default.
metaplay secrets list <env>
metaplay secrets list <env> --show-values            # Text mode, values shown.
metaplay secrets list <env> --format=json            # JSON mode always includes values.

# Show one secret. Text mode is default; JSON mode includes all k8s metadata.
metaplay secrets show <env> user-mysecret
metaplay secrets show <env> user-mysecret --format=json

# Extract a single entry's raw value (JSON mode base64-encodes payloads):
metaplay secrets show <env> user-mysecret --format=json | jq -r .data.apikey | base64 -d

# Delete.
metaplay secrets delete <env> user-mysecret
```

## `--from-literal` vs `--from-file` — which to use

- **`--from-literal=key=value`** — value is the literal text. Fine for short strings (API keys, usernames, passwords). Be aware the value is on the command line and may appear in shell history.
- **`--from-file=key=path`** — value is the file's contents. Use for anything multi-line, binary, or sensitive enough that you don't want it in shell history (certs, JSON service-account files, RSA keys).

Each `--from-literal` / `--from-file` produces one entry. Pass the flag multiple times to add multiple entries to the same secret.

## Safety notes

- **Treat output as sensitive.** `--show-values` and `--format=json` reveal secret contents. Don't paste them into chat or commit them.
- **Update is non-destructive by default** — it adds/replaces only the entries you specify and leaves the rest untouched. Use `--remove` explicitly to delete an entry.
- **Create rejects existing names by default.** Use `--overwrite` only when you mean to wipe and rewrite the whole secret; you'll lose any entries not in the new payload.
- **Delete is immediate** — no soft delete or recovery. Confirm with the user before running.
- **Secrets are per-environment.** Creating a secret in `nimbly` does not propagate to other envs. If a secret needs to exist in dev, staging, and prod, run the create three times (or script it).

## After updating a secret

The game server reads secrets at startup. Changes take effect after the next pod restart:

```bash
# Trigger a restart by redeploying the same image.
metaplay deploy server <env> <current-tag>
```

If you don't redeploy, running pods continue using the old value.

## Error patterns

- **`401`/`403`:** auth or `api.secrets.write` permission missing.
- **`secret name must start with 'user-'`:** rename and retry.
- **`secret already exists`** (on create): pass `--overwrite` or use `update` instead.
- **`secret not found`** (on update/show/delete): name typo, or wrong env — `metaplay secrets list <env>` to confirm.
