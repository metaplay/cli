/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package unrealbridge

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Unreal target types that do not consume the bridge: dedicated servers do not host a
// Metaplay client (the plugin skips them at runtime) and UBT programs have no game.
const (
	// TargetTypeServer is the Unreal dedicated server target type.
	TargetTypeServer = "Server"
	// TargetTypeProgram is the Unreal build-graph program target type.
	TargetTypeProgram = "Program"
)

// TargetTypeConsumesBridge reports whether an Unreal target type consumes the Metaplay
// bridge at all.
func TargetTypeConsumesBridge(targetType string) bool {
	switch targetType {
	case TargetTypeServer, TargetTypeProgram:
		return false
	default:
		return true
	}
}

// ResolveRuntimeIdentifier maps an Unreal target platform to the .NET runtime
// identifier of the NativeAOT bridge library, enforcing the .NET toolchain's
// no-cross-OS limitation against the given host OS/architecture (pass runtime.GOOS and
// runtime.GOARCH in production; parameters keep the function testable).
//
// It returns (rid, nil) for desktop platforms, ("", nil) when the platform has no
// bridge publish flow yet (IOS, Android, ...: the build is skipped, not failed), and an
// error when the host OS cannot publish the requested platform.
func ResolveRuntimeIdentifier(platform string, hostGOOS string, hostGOARCH string) (string, error) {
	switch platform {
	case "Linux":
		if hostGOOS != "linux" {
			return "", fmt.Errorf("a Linux Metaplay bridge can only be published on a Linux host (the .NET NativeAOT toolchain does not cross-compile operating systems); this build runs on %s for target platform %s", hostGOOS, platform)
		}
		switch hostGOARCH {
		case "amd64":
			return "linux-x64", nil
		case "arm64":
			return "linux-arm64", nil
		default:
			return "", fmt.Errorf("unsupported Linux host architecture '%s' for the Metaplay bridge publish", hostGOARCH)
		}

	case "Win64":
		if hostGOOS != "windows" {
			return "", fmt.Errorf("a win-x64 Metaplay bridge can only be published on a Windows host (the .NET NativeAOT toolchain does not cross-compile operating systems); this build runs on %s for target platform %s", hostGOOS, platform)
		}
		return "win-x64", nil

	case "Mac":
		if hostGOOS != "darwin" {
			return "", fmt.Errorf("a macOS Metaplay bridge can only be published on a macOS host (the .NET NativeAOT toolchain does not cross-compile operating systems); this build runs on %s for target platform %s", hostGOOS, platform)
		}
		if hostGOARCH == "arm64" {
			return "osx-arm64", nil
		}
		return "osx-x64", nil

	default:
		// Not a desktop platform (IOS/Android are Phase 2 of the Unreal support plan):
		// their publish flows are not part of this step yet, so the bridge build is
		// skipped by the caller (empty rid, no error).
		return "", nil
	}
}

// CheckNativeToolchain verifies that the NativeAOT linker's prerequisite, a C compiler,
// is available when publishing for a Linux runtime identifier. Other RIDs have their
// toolchain provided by the OS/IDE.
func CheckNativeToolchain(rid string) error {
	if rid != "linux-x64" && rid != "linux-arm64" {
		return nil
	}
	if _, err := exec.LookPath("gcc"); err == nil {
		return nil
	}
	if _, err := exec.LookPath("clang"); err == nil {
		return nil
	}
	return fmt.Errorf("no C compiler found on PATH: the NativeAOT publish of the bridge library needs gcc or clang (e.g. 'sudo apt install gcc' or install clang)")
}

// NativeLibraryGlob returns the filename glob of the published NativeAOT bridge
// library for a runtime identifier ("*.so", "*.dll", or "*.dylib").
func NativeLibraryGlob(rid string) string {
	switch {
	case len(rid) >= 3 && rid[:4] == "win-":
		return "*.dll"
	case len(rid) >= 3 && rid[:4] == "osx-":
		return "*.dylib"
	default:
		return "*.so"
	}
}

// HostRuntimeIdentifier returns the runtime identifier of the current host, used by
// 'metaplay init unreal' to point the project's editor-iteration bridge library path
// at the host platform's publish output.
func HostRuntimeIdentifier() (string, error) {
	return ResolveRuntimeIdentifier(hostUnrealPlatformForGOOS(runtime.GOOS), runtime.GOOS, runtime.GOARCH)
}

func hostUnrealPlatformForGOOS(goos string) string {
	switch goos {
	case "linux":
		return "Linux"
	case "windows":
		return "Win64"
	case "darwin":
		return "Mac"
	default:
		return goos
	}
}
