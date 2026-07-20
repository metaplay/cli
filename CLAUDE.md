# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
make              # Build CLI to dist/metaplay(.exe)
make test         # Run all unit tests
go test ./...     # Run all unit tests (alternative)
go test -run TestName ./pkg/metaproj  # Run single test
go mod tidy       # Required before commit (CI enforces no changes)
```

**Run without building** (useful during development):
```bash
go run . -p ../MyProject debug shell
```

## Architecture

This is a Go CLI tool for managing Metaplay game server projects. It uses the Cobra command framework.

### Entry Point
- `main.go` → `cmd.Execute()` → `cmd/root.go`

### Package Structure
- **`cmd/`** - All CLI commands using Cobra. Commands implement the `CommandOptions` interface with `Prepare()` and `Run()` methods. Use `UsePositionalArgs` for commands with positional arguments.
- **`pkg/`** - Core business logic:
  - `auth/` - Authentication and session management
  - `envapi/` - Environment API (Kubernetes, Docker, secrets)
  - `helmutil/` - Helm chart operations (install, upgrade, uninstall)
  - `kubeutil/` - Kubernetes utilities (debug containers, file copy, pod management)
  - `metaproj/` - Project configuration (`metaplay-project.yaml` handling)
  - `portalapi/` - Metaplay Portal API client
  - `styles/` - Terminal output styling
  - `testutil/` - Test utilities (background server, containers)
- **`internal/`** - Internal packages:
  - `tui/` - Terminal UI components (interactive dialogs, task runners)
  - `version/` - Version management and update checks

### Command Groups
Commands are organized into groups in `cmd/root.go`:
- **Core workflows**: `build`, `debug`, `deploy`, `dev`, `test`
- **Manage project**: `init`, `update`
- **Manage resources**: `database`, `get`, `image`, `secrets`, `remove`
- **Other**: `auth`, `version`

### Global Flags
- `-p, --project` - Path to project directory (where `metaplay-project.yaml` is located)
- `-v, --verbose` - Enable verbose logging (also `METAPLAYCLI_VERBOSE` env var)
- `--color yes|no|auto` - Color output control (also `METAPLAYCLI_COLOR` env var)

### Error Handling

Use `clierrors` (`internal/errors`) for all user-facing errors. **Never** use `log.Error()` + `os.Exit()` — return errors instead and let `runCommand()` in `cmd/root.go` handle display and exit codes.

```go
import clierrors "github.com/metaplay/cli/internal/errors"

// Runtime errors (exit code 1) — something failed during execution
return clierrors.New("Docker is not responding")
return clierrors.Wrap(err, "Failed to build image")
return clierrors.Newf("Project '%s' not found", name)

// Usage errors (exit code 2) — bad arguments/flags, shows usage help
return clierrors.NewUsageError("Missing required argument: ENVIRONMENT")
return clierrors.WrapUsageError(err, "Invalid --since-time format")

// Add actionable suggestions and context
return clierrors.New("No game server pods found").
    WithSuggestion("Deploy a game server first with 'metaplay deploy server'").
    WithDetails("Checked namespace: " + ns)

// Wrap with cause (shown dimmed in output)
return clierrors.Wrap(err, "Failed to connect to Docker").
    WithSuggestion("Make sure Docker Desktop is running")
```

**Rules:**
- `Prepare()` errors are typically usage errors (`NewUsageError`/`NewUsageErrorf`)
- `Run()` errors are typically runtime errors (`New`/`Wrap`/`Newf`)
- Always include a `WithSuggestion()` when the user can take a specific action to fix the issue
- Use `WithDetails()` for extra context (valid values, available options, etc.)
- Keep messages concise and capitalize the first word

### Interactive Mode
The CLI auto-detects CI environments and disables interactive mode when:
- No terminal is available
- `--verbose` flag is set
- CI environment variables are present (`CI`, `GITHUB_ACTIONS`, etc.)
