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
- **`cmd/`** - All CLI commands using Cobra. Commands implement the `CommandOptions` interface with `Prepare()` and `Run()` methods.
- **`pkg/`** - Core business logic:
  - `auth/` - Authentication and session management
  - `envapi/` - Environment API (Kubernetes, Docker, secrets)
  - `metaproj/` - Project configuration (`metaplay-project.yaml` handling)
  - `portalapi/` - Metaplay Portal API client
  - `styles/` - Terminal output styling
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

### Interactive Mode
The CLI auto-detects CI environments and disables interactive mode when:
- No terminal is available
- `--verbose` flag is set
- CI environment variables are present (`CI`, `GITHUB_ACTIONS`, etc.)
