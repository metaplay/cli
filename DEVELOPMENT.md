# Metaplay CLI Development

This guide is for people who develop the Metaplay CLI, or who run it against a Metaplay platform other than the managed one, such as a local development setup or a CI test environment. None of this is needed for regular use of the CLI. For that, see the [README](README.md).

## Building and Testing

### Build Locally

There is a simple `Makefile` which produces the CLI binary as `dist/metaplay` (or `dist/metaplay.exe` on Windows):

```bash
cli$ make
```

Besides building, `make` runs the linter. The individual targets are `make build`, `make lint`, `make test`, and `make fix`, which runs `go mod tidy`, `go fix`, and the formatter, then builds.

You can add the `dist/` directory to your `PATH` to enable running the locally built CLI binary from any directory.

### Run Locally

While developing the CLI itself, it's often most convenient to run the CLI without building it. You can run it on a project with the `-p` flag, e.g.:

```bash
cli$ go run . -p ../MyProject debug shell
```

When working on Windows, you can avoid the network confirm dialog from being asked each with the following:

```bash
cli$ go build . && cli.exe auth login
```

### Unit Tests

To run all unit tests:

```bash
cli$ go test ./...
```

CI also fails the build if `golangci-lint` reports issues or if `go mod tidy` changes any files, so run `make lint` and `go mod tidy` before pushing.

### Platform Tests

Most of the testing of the CLI is done using Metaplay's internal platform tests. The CLI does very little in isolation so there's not much that can be tested without the surrounding components.

## Local Development Builds

A binary built from source without release version stamping (`make`, `go build`, or `go run`) reports its version as `dev`. Such a build behaves differently from a released one:

- It skips the check for a newer CLI version.
- It reads the agent skill content from `pkg/skills/data` in the source tree instead of the copy embedded in the binary, so skill edits take effect without rebuilding. If the source directory is not found, it falls back to the embedded copy.
- `metaplay skills install` overwrites the skill wrappers it manages regardless of their version stamp, as if `--force` was given.

## Published Development Builds

We continuously create development builds from the `metaplay/cli` repository `main` branch. These builds are tagged with a `-dev.N` suffix (e.g., `1.2.4-dev.1`) and published as draft releases. You can find the latest development build on the main [releases page](https://github.com/metaplay/cli/releases). The development builds are primarily intended for testing purposes and should generally not be used.

### Update Channels

The CLI has two update channels:

- **GA channel** — used by official releases (e.g., `1.2.3`). Shows an update banner when a newer GA release is available.
- **Prerelease channel** — prerelease builds (e.g., `1.2.3-dev.5`). Automatically updates to the latest prerelease on every run (except in CI environments).

To switch a GA build to the prerelease channel, run:

```bash
metaplay update cli --prerelease
```

This also works with locally built `dev` version to upgrade it to the prerelease channel.

## Running Against a Custom Platform

By default, the CLI signs in with Metaplay Auth and uses the managed Metaplay portal at `https://portal.metaplay.dev`. These environment variables point it at another Metaplay platform:

| Variable | Description |
|---|---|
| `METAPLAYCLI_PORTAL_BASEURL` | Base URL of the portal to use instead of `https://portal.metaplay.dev`. |
| `METAPLAYCLI_AUTH_PROVIDER_FILE` | Path to a YAML file describing the OAuth2 provider to sign in with instead of Metaplay Auth. See [Auth Provider File](#auth-provider-file). |

Set both to the same platform, because a portal does not accept tokens issued by another platform's auth server. The CLI prints the overridden values at startup, and warns when the provider file is set while the portal is still the default — the combination that sends a token to a platform it was not issued for.

### Auth Provider File

Example for the local Tilt setup:

```yaml
name: Metaplay Auth (tilt)
clientId: c16ea663-ced3-46c6-8f85-38c9681fe1f0
authEndpoint: https://auth.metaplay.localhost/oauth2/auth
tokenEndpoint: https://auth.metaplay.localhost/oauth2/token
revokeEndpoint: https://auth.metaplay.localhost/oauth2/revoke
userInfoEndpoint: https://portal.metaplay.localhost/api/external/userinfo
```

```bash
export METAPLAYCLI_PORTAL_BASEURL=https://portal.metaplay.localhost
export METAPLAYCLI_AUTH_PROVIDER_FILE=~/metaplay-tilt-auth.yaml
metaplay auth login
```

These endpoints are served over TLS with the platform's own CA, which must be in your system trust store before the CLI can reach them. Bringing the platform up installs it.

The file has the following fields:

| Field | Required | Description |
|---|---|---|
| `name` | Yes | Name of the provider. `Metaplay Auth` is reserved for the built-in provider. |
| `clientId` | Yes | OAuth2 client ID. |
| `authEndpoint` | Yes | OAuth2 authorization endpoint. |
| `tokenEndpoint` | Yes | OAuth2 token endpoint. |
| `revokeEndpoint` | Yes | OAuth2 token revocation endpoint. |
| `userInfoEndpoint` | Yes | Endpoint returning the signed-in user's information, usually the portal's `/api/external/userinfo`. |
| `scopes` | No | Space-separated OAuth2 scopes. Defaults to `openid profile email offline_access`. |
| `audience` | No | OAuth2 audience, if the provider requires one. |

The file is validated when the CLI loads it:

- Unknown fields are rejected, so a misspelled field name is an error.
- Endpoints must use `https`. Plain `http` is allowed only for loopback hosts: `localhost`, `*.localhost`, `127.0.0.1`, and `::1`.

The OAuth2 client must allow the redirect URIs `http://localhost:5000/callback` through `http://localhost:5004/callback`. The browser login uses the highest free port in that range.

While the variable is set:

- The provider from the file replaces Metaplay Auth as the default, and the provider name `metaplay` refers to it. The `auth` commands, and all commands that use the portal or target an environment, sign in with it.
- Environments whose `authProvider` in `metaplay-project.yaml` names a provider defined in the project keep using that provider.
- The session is stored separately from the Metaplay Auth session, so switching between platforms does not sign you out of either. To manage the Metaplay Auth session, unset both variables for that command, e.g., `METAPLAYCLI_AUTH_PROVIDER_FILE= METAPLAYCLI_PORTAL_BASEURL= metaplay auth logout` in bash.
- If you change the file's `clientId` or `tokenEndpoint` but keep its `name`, the stored session is rejected because it was issued by a different provider. Run `metaplay auth logout` to remove it.
- Kubeconfigs generated with `metaplay get kubeconfig` run the CLI to fetch credentials whenever `kubectl` needs them, so run `kubectl` with the same variables set.

## LLM Docs Service

These environment variables point `metaplay llm-docs` at another instance of the service, such as one running locally:

| Variable | Description |
|---|---|
| `METAPLAYCLI_LLM_DOCS_ADDR` | gRPC address (`host:port`) to use instead of `llm-docs-grpc.platform.metaplay.dev:443`. Addresses on `localhost`, `127.0.0.1`, or `::1` use plaintext automatically. |
| `METAPLAYCLI_LLM_DOCS_INSECURE` | Set to `1` to use plaintext for any address. The auth token is then never sent. |

When `METAPLAYCLI_AUTH_PROVIDER_FILE` is set, the session's token is sent only to a service named with `METAPLAYCLI_LLM_DOCS_ADDR`, never to the default Metaplay-hosted one.

## Debugging Aids

| Variable | Description |
|---|---|
| `METAPLAYCLI_DEBUG_COPY_RANDOM_FAIL` | Set to `1` to inject random failures into file downloads from debug containers, to test the retry and resume logic. Used by `metaplay debug collect-heap-dump` and `metaplay debug collect-cpu-profile`. |

## Publishing and CI

There are two types of releases published:

* Pre-release versions (with `-dev.X` suffix), done for each commit to `main`.
* Official releases (with no suffix), done for each version tag (e.g., `1.2.3`).

### Steps to Publish

1. Merge all relevant PRs into `main`.

2. Wait for the pre-release version (e.g., `v1.2.3-dev.4`) to get published.

3. Run the Metaplay internal [platform tests](https://github.com/metaplay/sdk/actions/workflows/platform-tests-new.yaml).

    The latest CLI pre-release version is also covered by the platform tests.

4. Tag the latest `main` (which was tested in the previous step) with the version number, e.g., `1.2.3` and push the tag.

    This triggers the release process, which publishes the official release. It takes about 30min to publish.
