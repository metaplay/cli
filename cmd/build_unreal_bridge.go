/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/metaplay/cli/pkg/unrealbridge"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type buildUnrealBridgeOpts struct {
	flagProjectDir  string // Unreal project directory (where MetaplayBridge.json lives).
	flagPlatform    string // Unreal target platform (Linux, Win64, Mac).
	flagUeConfig    string // Unreal target configuration (informational).
	flagTargetType  string // Unreal target type (Game, Editor, Client, Server, Program).
	flagSkipPublish bool   // Run only the serializer and mirror generation.
	flagSdkRoot     string // Explicit Metaplay SDK root override.
}

func init() {
	o := buildUnrealBridgeOpts{}

	cmd := &cobra.Command{
		Use:   "unreal-bridge [flags]",
		Short: "Build the .NET side of an Unreal project using the MetaplayUnreal plugin",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Build the .NET side of an Unreal project consuming the MetaplayUnreal plugin:
			the game's bridge host assembly, its prebuilt serializer (SerializerGen), its
			USTRUCT mirror header (MirrorGen), and the NativeAOT bridge library for the
			target runtime identifier, staged where the plugin loads them from.

			This is a native implementation of the SDK's metaplay-bridge pre-build step
			(MetaplaySDK/Unreal/MetaplayUnreal/BridgeBuild/metaplay-bridge.sh), running the
			same steps on all host platforms. The MetaplayUnreal plugin can run this command
			through its PreBuildSteps instead of the shell script.

			Note: unlike the shell script, this command resolves the MetaplayBridge.json
			configuration before skipping inapplicable target types (Server, Program), so
			a broken bridge declaration fails loudly regardless of the target being built.

			The project declares its bridge project in MetaplayBridge.json next to the
			.uproject; see MetaplaySDK/Unreal/MetaplayUnreal/BridgeBuild/README.md for all
			options. The Metaplay SDK root is resolved from the bridge project's import of
			the MetaplayUnreal.BridgeHost.props recipe, from METAPLAY_SDK_ROOT, from
			metaplay-project.yaml, or from --sdk-root.

			Related commands:
			- 'metaplay build serializer' runs only the serializer generation step.
			- 'metaplay init unreal' scaffolds a new Unreal project wired to the plugin.
		`),
		Example: renderExample(`
			# Full bridge build chain for the local host platform (what the plugin's
			# pre-build step runs):
			MyUnrealProject$ metaplay build unreal-bridge

			# Only generate the serializer and the mirror header (the plugin's init-time
			# check then catches a stale, not-republished bridge library):
			MyUnrealProject$ metaplay build unreal-bridge --skip-publish

			# Explicitly select the Unreal build parameters and the SDK root:
			metaplay build unreal-bridge --project-dir ~/MyGame/Unreal --platform Linux --target-type Game --sdk-root ~/sdk/MetaplaySDK
		`),
	}

	flags := cmd.Flags()
	flags.StringVar(&o.flagProjectDir, "project-dir", ".", "Unreal project directory where MetaplayBridge.json is located")
	flags.StringVar(&o.flagPlatform, "platform", "Linux", "Unreal target platform (Linux, Win64, Mac)")
	flags.StringVar(&o.flagUeConfig, "config", "Development", "Unreal target configuration (informational)")
	flags.StringVar(&o.flagTargetType, "target-type", "Game", "Unreal target type (Game, Editor, Client, Server, Program)")
	flags.BoolVar(&o.flagSkipPublish, "skip-publish", false, "Skip the NativeAOT publish: only build the bridge host, generate the serializer and mirror header, and stage the expected-serializer hash")
	flags.StringVar(&o.flagSdkRoot, "sdk-root", "", "Path to the Metaplay SDK root (MetaplaySDK directory; default: resolve from the bridge project, METAPLAY_SDK_ROOT, or metaplay-project.yaml)")

	buildCmd.AddCommand(cmd)
}

func (o *buildUnrealBridgeOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *buildUnrealBridgeOpts) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	// Resolve the bridge build configuration from MetaplayBridge.json.
	settings, err := unrealbridge.ResolveBridgeSettings(o.flagProjectDir, "", o.flagSdkRoot)
	if err != nil {
		return clierrors.Wrap(err, "Failed to resolve the Metaplay bridge configuration").
			WithSuggestion("Every Unreal project consuming the MetaplayUnreal plugin declares its bridge host project in MetaplayBridge.json next to the .uproject (see MetaplaySDK/Unreal/MetaplayUnreal/BridgeBuild/README.md)")
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Build Metaplay Unreal Bridge"))
	log.Info().Msg("")

	// Dedicated servers do not host a Metaplay client and UBT programs have no game:
	// nothing to build.
	if !unrealbridge.TargetTypeConsumesBridge(o.flagTargetType) {
		log.Info().Msgf("target type '%s' does not consume the bridge: skipping the bridge build", o.flagTargetType)
		return nil
	}

	// Resolve the native runtime identifier for the target platform. The .NET toolchain
	// cannot NativeAOT-compile across operating systems, so the host OS must match.
	rid, err := unrealbridge.ResolveRuntimeIdentifier(o.flagPlatform, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return clierrors.Wrap(err, "Cannot publish the Metaplay bridge for the target platform")
	}
	if rid == "" {
		// Not a desktop platform (IOS/Android are Phase 2 of the Unreal support plan).
		log.Info().Msgf("target platform '%s' has no Metaplay bridge publish flow yet: skipping the bridge build", o.flagPlatform)
		return nil
	}

	// Check .NET SDK prerequisites.
	dotnetCmd, err := resolveDotnetCommand()
	if err != nil {
		return err
	}
	if err := checkDotnetSdkVersionAtLeast10(ctx, dotnetCmd); err != nil {
		return err
	}
	if err := unrealbridge.CheckNativeToolchain(rid); err != nil {
		return clierrors.Wrap(err, "The NativeAOT toolchain prerequisites are not met")
	}

	// One chain at a time per project: a single Unreal build invocation regenerates
	// several targets' makefiles in parallel, each running the pre-build step against
	// the same .NET projects, whose concurrent builds race on their output files. Same
	// lock file as metaplay-bridge.sh, so the two implementations exclude each other.
	releaseLock, err := unrealbridge.AcquireProjectLock(settings.ProjectDir)
	if err != nil {
		return clierrors.Wrap(err, "Failed to acquire the per-project bridge build lock")
	}
	defer releaseLock()
	log.Info().Msgf("acquired the per-project build lock")

	log.Info().Msgf("project:       %s", styles.RenderTechnical(settings.ProjectDir))
	log.Info().Msgf("bridge:        %s", styles.RenderTechnical(settings.BridgeCsProjPath))
	log.Info().Msgf("serializer:    %s %s", styles.RenderTechnical("Metaplay.Generated."+settings.SerializerName), styles.RenderMuted(fmt.Sprintf("(from %s)", settings.RootAssemblyPath)))
	if !o.flagSkipPublish {
		log.Info().Msgf("publish:       %s/%s -> %s", settings.DotnetConfig, rid, styles.RenderTechnical(settings.StageDir))
	}
	if settings.MirrorHeaderRel != "" {
		log.Info().Msgf("mirror header: %s", styles.RenderTechnical(settings.MirrorHeaderRel))
	}
	log.Info().Msg("")

	// 1. Build the game's bridge host assembly (the game graph the serializer is
	//    generated from). On a clean checkout the prebuilt serializer reference is not
	//    resolvable yet: MSB3245 is a warning, the serializer step below produces it
	//    and later builds resolve it.
	if err := runDotnetCommand(ctx, settings.BridgeDir, dotnetCmd, []string{"build", settings.BridgeCsProjPath, "-c", settings.DotnetConfig}); err != nil {
		return clierrors.Wrap(err, "Failed to build the bridge host .NET project").
			WithSuggestion("Check the build output above for details")
	}
	if _, err := os.Stat(settings.RootAssemblyPath); err != nil {
		return clierrors.Newf("The bridge host assembly was not found at %s after building %s", settings.RootAssemblyPath, settings.BridgeCsProjPath).
			WithSuggestion("Unexpected output layout: set <AssemblyName>/<TargetFramework> in the csproj explicitly")
	}

	// 2. Generate (or reuse, by hash) the prebuilt serializer for the game graph: the
	//    dll, its .pdb and its .hash land in the csproj's serializer directory.
	if err := generateSerializer(ctx, dotnetCmd, settings); err != nil {
		return err
	}

	// The expected-serializer hash: what the CURRENT game code implies. The plugin
	// compares it at Metaplay_Init against the hash the loaded bridge library was
	// published with.
	if err := os.MkdirAll(settings.StageDir, 0755); err != nil {
		return clierrors.Wrap(err, "Failed to create the bridge staging directory")
	}
	if err := copyFileContents(settings.SerializerHashPath(), settings.ExpectedHashPath()); err != nil {
		return clierrors.Wrapf(err, "Failed to stage the expected-serializer hash to %s", settings.ExpectedHashPath())
	}

	// 3. The USTRUCT mirror header for the game's Unreal module (only when the project
	//    declares where it goes). Diff-copied so an unchanged header does not recompile
	//    the game module.
	if settings.MirrorHeaderRel != "" {
		updated, err := unrealbridge.UpdateMirrorHeader(settings.ProjectDir, settings.MirrorHeaderRel, func(tmpPath string) error {
			return runDotnetCommand(ctx, settings.BridgeDir, dotnetCmd, []string{
				"run", "--project", settings.MirrorGenProject(), "--",
				settings.RootAssemblyPath, "--output", tmpPath,
			})
		})
		if err != nil {
			return clierrors.Wrap(err, "Failed to generate the USTRUCT mirror header")
		}
		mirrorHeaderPath := filepath.Join(settings.ProjectDir, filepath.FromSlash(settings.MirrorHeaderRel))
		if updated {
			log.Info().Msgf("mirror header updated: %s", styles.RenderTechnical(mirrorHeaderPath))
		} else {
			log.Info().Msgf("mirror header unchanged: %s", styles.RenderTechnical(mirrorHeaderPath))
		}
	}

	// 4. Publish the NativeAOT bridge library for the target RID. The post-publish
	//    staging target (MetaplayUnreal.BridgeHost.targets) copies the serializer
	//    artifacts next to the library, records the library's serializer hash as
	//    <library>.hash, and mirrors everything into the staging directory.
	if o.flagSkipPublish {
		log.Info().Msgf("SKIPPING the publish (--skip-publish): the staged expected-serializer hash is %s; a stale bridge library now fails at Metaplay_Init with a readable error.", settings.ExpectedHashPath())
		log.Info().Msg("")
		return nil
	}

	if err := runDotnetCommand(ctx, settings.BridgeDir, dotnetCmd, []string{
		"publish", settings.BridgeCsProjPath,
		"-c", settings.DotnetConfig,
		"-r", rid,
		fmt.Sprintf("-p:MetaplayStageDir=%s", settings.StageDir),
	}); err != nil {
		return clierrors.Wrap(err, "Failed to publish the NativeAOT bridge library").
			WithSuggestion("Check the build output above for details")
	}

	// Validate the staged outcome (the publish target resolves the exact library name;
	// validate what landed in the staging directory, not the publish directory layout).
	stagedLibs, err := settings.StagedLibraries(rid)
	if err != nil {
		return err
	}
	if len(stagedLibs) == 0 {
		return clierrors.Newf("The bridge library was not staged into %s after publishing", settings.StageDir).
			WithSuggestion("The MetaplayUnreal.BridgeHost.targets staging target did not run: is the bridge csproj importing MetaplayUnreal.BridgeHost.props?")
	}
	for _, lib := range stagedLibs {
		if _, err := os.Stat(lib + ".hash"); err != nil {
			return clierrors.Newf("The staged bridge library %s has no .hash sidecar (the post-publish staging target writes it)", lib)
		}
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderSuccess("✅ Metaplay Unreal bridge built successfully"))
	log.Info().Msgf("Staged into %s: %s", styles.RenderTechnical(settings.StageDir), styles.RenderTechnical(strings.Join(stagedLibs, ", ")))
	log.Info().Msg("")
	return nil
}

// generateSerializer runs the SDK's SerializerGen tool on the built bridge host
// assembly and validates its outputs.
func generateSerializer(ctx context.Context, dotnetCmd string, settings *unrealbridge.BridgeSettings) error {
	if err := runDotnetCommand(ctx, settings.BridgeDir, dotnetCmd, []string{
		"run", "--project", settings.SerializerGenProject(), "--",
		settings.RootAssemblyPath, settings.SerializerName,
		"--output", settings.SerializerDir,
	}); err != nil {
		return clierrors.Wrap(err, "SerializerGen failed").
			WithSuggestion("Check the build output above for details")
	}
	if _, err := os.Stat(settings.SerializerHashPath()); err != nil {
		return clierrors.Newf("SerializerGen did not produce %s", settings.SerializerHashPath())
	}
	return nil
}
