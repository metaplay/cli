# AGENTS.md

Guidelines for AI coding agents working in this Go CLI repository.

## Build, Test, and Lint Commands

```bash
# Build
make                    # Build CLI (outputs to dist/metaplay[.exe])

# Test
make test               # Run all unit tests
go test -run TestName ./pkg/metaproj    # Run single test by name
go test -run TestValidateProjectID ./pkg/metaproj  # Example: specific test
go test -v ./pkg/metaproj/...           # Verbose output for a package

# Before committing (CI enforces this)
go mod tidy             # Required - CI fails if this causes changes

# Development - run without building
go run . -p ../MyProject debug shell
go run . --help
```

## Project Architecture

This is a Go CLI for managing Metaplay projects and cloud environments.

### Entry Point
`main.go` -> `cmd.Execute()` -> `cmd/root.go`

### Package Structure
- **`cmd/`** - CLI commands (Cobra). Each command implements `CommandOptions` interface.
- **`pkg/`** - Core business logic:
  - `auth/` - Authentication and session management
  - `envapi/` - Environment API (Kubernetes, Docker, secrets)
  - `metaproj/` - Project config (`metaplay-project.yaml`)
  - `portalapi/` - Metaplay Portal API client
  - `helmutil/` - Helm chart utilities
  - `styles/` - Terminal output styling
- **`internal/`** - Internal packages:
  - `tui/` - Terminal UI (dialogs, task runners)
  - `version/` - Version management

### Command Pattern
Commands implement the `CommandOptions` interface:
```go
type CommandOptions interface {
    Prepare(cmd *cobra.Command, args []string) error
    Run(cmd *cobra.Command) error
}
```

## Code Style Guidelines

### File Header
Every `.go` file must start with the copyright header:
```go
/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
```

### Import Organization
Imports are grouped in this order, separated by blank lines:
1. Standard library
2. External dependencies
3. Internal packages (`github.com/metaplay/cli/...`)

```go
import (
    "fmt"
    "os"
    "strings"

    "github.com/spf13/cobra"
    "github.com/rs/zerolog/log"

    "github.com/metaplay/cli/internal/tui"
    "github.com/metaplay/cli/pkg/styles"
)
```

### Naming Conventions
- **Types**: PascalCase (`MetaplayProject`, `TaskRunner`)
- **Exported functions/methods**: PascalCase (`LoadProjectConfigFile`)
- **Unexported functions/methods**: camelCase (`validateProjectDir`)
- **Constants**: PascalCase for exported, camelCase for unexported
- **Command option structs**: `<action><resource>Opts` (e.g., `deployGameServerOpts`)
- **Flag variables**: `flag<Name>` prefix (e.g., `flagVerbose`, `flagHelmChartVersion`)
- **Argument variables**: `arg<Name>` prefix (e.g., `argEnvironment`)

### Error Handling
- Return errors with context using `fmt.Errorf`:
```go
return fmt.Errorf("failed to read file: %w", err)
```
- Use `log.Panic().Msgf()` only for programming errors (unreachable states)
- Use `log.Error().Msgf()` for user-facing errors, then return/exit
- Check errors immediately after function calls

### Logging
Use zerolog (`github.com/rs/zerolog/log`):
```go
log.Debug().Msgf("Processing %s", filename)
log.Info().Msgf("Deployment complete")
log.Warn().Msgf("Config not found, using defaults")
log.Error().Msgf("Failed to connect: %v", err)
```

### Struct Tags
Use YAML tags for config structs:
```go
type Config struct {
    ProjectID string `yaml:"projectId"`
    SdkRootDir string `yaml:"sdkRootDir"`
}
```

### Comments
- Use `// Comment` for single-line comments
- Use `/* ... */` block style only for file headers
- Document exported functions with a comment starting with the function name
- Use `// \todo` for TODO items (backslash style)

### Testing
- Test files: `*_test.go` in the same package
- Test function names: `Test<FunctionName>` or `Test<FunctionName>_<Scenario>`
- Use table-driven tests for multiple cases:
```go
func TestValidateProjectID(t *testing.T) {
    tests := []struct {
        input   string
        isValid bool
    }{
        {"valid-project", true},
        {"", false},
    }
    for _, test := range tests {
        t.Run(test.input, func(t *testing.T) {
            result := ValidateProjectID(test.input)
            // assertions
        })
    }
}
```

### Terminal Output Styling
Use the `pkg/styles` package for consistent output:
```go
styles.RenderTitle("Deploy Server")      // Blue, bold
styles.RenderSuccess("Done")             // Green
styles.RenderError("Failed")             // Red
styles.RenderTechnical("env-name")       // Blue (for IDs, values)
styles.RenderMuted("optional info")      // Gray
styles.RenderPrompt("metaplay deploy")   // Orange, bold (commands)
```

### Command Structure
Commands follow this pattern in `cmd/`:
```go
func init() {
    o := myCommandOpts{}

    cmd := &cobra.Command{
        Use:   "mycommand ARGS",
        Short: "Brief description",
        Long:  renderLong(&o, `Detailed description...`),
        Run:   runCommand(&o),
    }

    parentCmd.AddCommand(cmd)
    cmd.Flags().StringVar(&o.flagName, "flag-name", "", "Description")
}

func (o *myCommandOpts) Prepare(cmd *cobra.Command, args []string) error {
    // Validate inputs, resolve paths
    return nil
}

func (o *myCommandOpts) Run(cmd *cobra.Command) error {
    // Execute command logic
    return nil
}
```

### Global Flags
- `-p, --project` - Path to project directory
- `-v, --verbose` - Enable verbose logging
- `--color yes|no|auto` - Color output control

### Key Dependencies
- `github.com/spf13/cobra` - CLI framework
- `github.com/rs/zerolog` - Structured logging
- `github.com/charmbracelet/bubbletea` - Interactive TUI
- `github.com/charmbracelet/lipgloss` - Terminal styling
- `helm.sh/helm/v3` - Kubernetes Helm operations
- `k8s.io/client-go` - Kubernetes client
