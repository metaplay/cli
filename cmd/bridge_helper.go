/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
)

// resolveDotnetCommand resolves the dotnet executable for the Unreal bridge build:
// 'dotnet' on PATH, or $DOTNET_ROOT/dotnet when only that is installed (same lookup
// order as the SDK's metaplay-bridge pre-build step).
func resolveDotnetCommand() (string, error) {
	if path, err := exec.LookPath("dotnet"); err == nil {
		return path, nil
	}
	if dotnetRoot := os.Getenv("DOTNET_ROOT"); dotnetRoot != "" {
		exe := "dotnet"
		if runtime.GOOS == "windows" {
			exe = "dotnet.exe"
		}
		candidate := filepath.Join(dotnetRoot, exe)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", clierrors.New("the .NET SDK was not found: 'dotnet' is not on PATH and DOTNET_ROOT is not set").
		WithSuggestion("The Metaplay bridge build needs the .NET 10 SDK (or newer). Install it from https://dotnet.microsoft.com/download and make sure 'dotnet' resolves (PATH), or set DOTNET_ROOT to the installation root (e.g. /usr/lib/dotnet).")
}

// checkDotnetSdkVersionAtLeast10 checks that the resolved dotnet command runs an SDK
// of major version 10 or newer (the bridge build's requirement, matching the SDK's
// metaplay-bridge pre-build step).
func checkDotnetSdkVersionAtLeast10(ctx context.Context, dotnetCmd string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, dotnetCmd, "--version")
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		versionErr := clierrors.New("failed to determine the .NET SDK version (dotnet --version failed)")
		if detail := strings.TrimSpace(errOut.String()); detail != "" {
			versionErr = versionErr.WithDetails(detail)
		}
		return versionErr.
			WithSuggestion(getDotnetInstallInstructions())
	}

	// Parse the version from stdout only: stderr can carry first-run noise
	// (telemetry, workload warnings) that has nothing to do with the version.
	versionStr := strings.TrimSpace(out.String())
	major, err := strconv.Atoi(strings.SplitN(versionStr, ".", 2)[0])
	if err != nil {
		return clierrors.Newf("could not determine the .NET SDK version ('dotnet --version' printed '%s')", versionStr)
	}
	if major < 10 {
		return clierrors.Newf("the .NET SDK %s is too old: the Metaplay bridge build needs .NET 10 or newer", versionStr).
			WithSuggestion("Install the .NET 10 SDK from https://dotnet.microsoft.com/download (or point PATH/DOTNET_ROOT at it).")
	}

	log.Info().Msgf("%s .NET SDK detected: %s %s", styles.RenderSuccess("✓"), styles.RenderTechnical(versionStr), styles.RenderMuted("[minimum: 10.0]"))
	log.Info().Msg("")
	return nil
}

// copyFileContents copies the contents of src to dst (the destination directory must
// exist; file mode 0644).
func copyFileContents(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", dst, err)
	}
	return nil
}

// runDotnetCommand runs a dotnet subcommand in a child process with the CLI's common
// dotnet environment variables set, streaming the child's output like execChildTask.
func runDotnetCommand(ctx context.Context, workDir string, dotnetCmd string, args []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, dotnetCmd, args...)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), commonDotnetEnvVars...)

	log.Info().Msg(styles.RenderMuted(fmt.Sprintf("%s$ %s %s", workDir, dotnetCmd, strings.Join(args, " "))))
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("dotnet %s exited with error: %w", args[0], err)
	}
	return nil
}
