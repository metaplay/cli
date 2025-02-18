/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/hashicorp/go-version"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
)

// Provide installation instructions based on the operating system
func getDotnetInstallInstructions() string {
	switch runtime.GOOS {
	case "windows":
		return `
.NET SDK is missing or outdated. Please install or upgrade .NET SDK:
1. Go to: https://dotnet.microsoft.com/download
2. Download and run the installer for desired .NET SDK version.
3. Follow the installation steps and ensure 'dotnet' is added to your PATH.
`
	case "darwin":
		return `
.NET SDK is missing or outdated. Please install or upgrade .NET SDK:
1. Open a terminal.
2. Install Homebrew (if not installed): https://brew.sh/
3. Run: brew install --cask dotnet-sdk
4. Add .NET SDK to your PATH by running: export PATH="$PATH:/usr/local/share/dotnet"
5. Verify installation with: dotnet --version
`
	case "linux":
		return `
.NET SDK is missing or outdated. Please install or upgrade .NET SDK:
1. Open a terminal.
2. Run the following commands:
   - For Ubuntu/Debian:
     sudo apt update
     sudo apt install -y dotnet-sdk-x.y
   - For Fedora:
     sudo dnf install dotnet-sdk-x.y
   - For other distributions: https://learn.microsoft.com/en-us/dotnet/core/install/linux
3. Verify installation with: dotnet --version
`
	default:
		return `
.NET SDK is missing or outdated. Please install or upgrade .NET SDK:
Visit: https://dotnet.microsoft.com/download for instructions specific to your operating system.
`
	}
}

// Checks if .NET SDK is installed and check that it is recent enough for the SDK
// version used.
func checkDotnetSdkVersion(requiredDotnetVersion *version.Version) error {
	// Note: This gets the SDK version, not runtime version (eg, 8.0.400)
	cmd := exec.Command("dotnet", "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return errors.New("dotnet SDK is not installed or not in PATH.\n" + getDotnetInstallInstructions())
	}

	// Parse installed .NET version
	installedVersionStr := strings.TrimSpace(out.String())
	installedVersion, err := version.NewVersion(installedVersionStr)
	if err != nil {
		return fmt.Errorf("failed to parse required .NET version from '%s': %v", installedVersionStr, err)
	}

	// Print the info.
	badge := styles.RenderMuted(fmt.Sprintf("[minimum: %s]", requiredDotnetVersion))
	log.Info().Msgf("Installed .NET SDK: %s %s", styles.RenderTechnical(installedVersion.String()), badge)

	// Check that .NET version is recent enough
	if installedVersion.LessThan(requiredDotnetVersion) {
		return fmt.Errorf(".NET SDK version %s or higher is required, but found %s.\n%s",
			requiredDotnetVersion, installedVersion, getDotnetInstallInstructions())
	}

	log.Info().Msg("")
	return nil
}

func execChildTask(workingDir string, binary string, args []string) error {
	cmd := exec.Command(binary, args...)
	cmd.Dir = workingDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Info().Msgf("Executing '%s %s'...", binary, strings.Join(args, " "))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to build the project: %w", err)
	}

	return nil
}

// Runs a child process in "interactive" mode where all inputs/outputs are forwarded
// to the sub-process.
func execChildInteractive(workingDir string, binary string, args []string) error {
	// Create the command to run the .NET binary
	cmd := exec.Command(binary, args...)
	cmd.Dir = workingDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Create a channel to forward signals to the subprocess
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	// Start the process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start the binary: %w", err)
	}

	// Goroutine to forward signals to the subprocess
	go func() {
		for sig := range signalChan {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		}
	}()

	// Wait for the subprocess to complete
	if err := cmd.Wait(); err != nil {
		// If the process was terminated by a signal, exit cleanly
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 0 {
			return fmt.Errorf("binary exited with error: %w", err)
		}
	}

	return nil
}
