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

By default, the CLI signs in with Metaplay Auth and uses the managed Metaplay portal at `https://portal.metaplay.dev`. This environment variable points it at another portal:

| Variable | Description |
|---|---|
| `METAPLAYCLI_PORTAL_BASEURL` | Base URL of the portal to use instead of `https://portal.metaplay.dev`. The CLI prints the overridden URL at startup. |

Setting it to `http://portal.metaplay-dev.localhost` targets the local Tilt setup. The CLI then signs in with the Tilt setup's auth server instead of Metaplay Auth, and stores that session separately from the Metaplay Auth session. With any other URL, the CLI still signs in with Metaplay Auth.

```bash
export METAPLAYCLI_PORTAL_BASEURL=http://portal.metaplay-dev.localhost
metaplay auth login
```

Kubeconfigs generated with `metaplay get kubeconfig` run the CLI to fetch credentials whenever `kubectl` needs them, so run `kubectl` with the same variable set.

## LLM Docs Service

These environment variables point `metaplay llm-docs` at another instance of the service, such as one running locally:

| Variable | Description |
|---|---|
| `METAPLAYCLI_LLM_DOCS_ADDR` | gRPC address (`host:port`) to use instead of `llm-docs-grpc.platform.metaplay.dev:443`. Addresses on `localhost`, `127.0.0.1`, or `::1` use plaintext automatically. |
| `METAPLAYCLI_LLM_DOCS_INSECURE` | Set to `1` to use plaintext for any address. The auth token is then never sent. |

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
