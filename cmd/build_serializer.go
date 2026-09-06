/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"os"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/metaplay/cli/pkg/unrealbridge"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type buildSerializerOpts struct {
	flagProjectDir   string // Unreal project directory (where MetaplayBridge.json lives).
	flagDotnetConfig string // dotnet configuration override (Debug/Release).
	flagSdkRoot      string // Explicit Metaplay SDK root override.
	flagSkipBuild    bool   // Reuse the existing bridge host assembly instead of building.
}

func init() {
	o := buildSerializerOpts{}

	cmd := &cobra.Command{
		Use:   "serializer [flags]",
		Short: "Generate the prebuilt serializer for an Unreal bridge host project",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Build the game's bridge host assembly and generate its prebuilt serializer
			with the SDK's SerializerGen tool: Metaplay.Generated.<SerializerName>.dll
			plus its .pdb and .hash land in the bridge project's serializer directory.

			This is the serializer step of 'metaplay build unreal-bridge' (and of the
			SDK's metaplay-bridge pre-build step) as a standalone command, useful for
			iteration when the full publish is not needed.

			The project declares its bridge project in MetaplayBridge.json next to the
			.uproject; the serializer name, dotnet configuration, and serializer directory
			default to the values declared there and in the bridge .csproj. Note that
			--dotnet-config here means the .NET build configuration (Debug/Release), not
			the Unreal target configuration that 'metaplay build unreal-bridge' takes via
			its --config flag.

			Related commands:
			- 'metaplay build unreal-bridge' runs the full chain (serializer, mirror
			  header, NativeAOT publish, staging).
			- 'metaplay init unreal' scaffolds a new Unreal project wired to the plugin.
		`),
		Example: renderExample(`
			# Build the bridge host and generate its serializer:
			MyUnrealProject$ metaplay build serializer

			# Build with the Debug dotnet configuration:
			MyUnrealProject$ metaplay build serializer --dotnet-config Debug

			# Regenerate the serializer from an already-built bridge host assembly:
			MyUnrealProject$ metaplay build serializer --skip-build
		`),
	}

	flags := cmd.Flags()
	flags.StringVar(&o.flagProjectDir, "project-dir", ".", "Unreal project directory where MetaplayBridge.json is located")
	flags.StringVar(&o.flagDotnetConfig, "dotnet-config", "", "dotnet configuration to build the bridge host with (default: the MetaplayBridge.json Configuration, or Release)")
	flags.StringVar(&o.flagSdkRoot, "sdk-root", "", "Path to the Metaplay SDK root (MetaplaySDK directory; default: resolve from the bridge project, METAPLAY_SDK_ROOT, or metaplay-project.yaml)")
	flags.BoolVar(&o.flagSkipBuild, "skip-build", false, "Skip building the bridge host assembly and generate the serializer from the existing build output")

	buildCmd.AddCommand(cmd)
}

func (o *buildSerializerOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

func (o *buildSerializerOpts) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	// Resolve the bridge build configuration from MetaplayBridge.json.
	settings, err := unrealbridge.ResolveBridgeSettings(o.flagProjectDir, o.flagDotnetConfig, o.flagSdkRoot)
	if err != nil {
		return clierrors.Wrap(err, "Failed to resolve the Metaplay bridge configuration").
			WithSuggestion("Every Unreal project consuming the MetaplayUnreal plugin declares its bridge host project in MetaplayBridge.json next to the .uproject (see MetaplaySDK/Unreal/MetaplayUnreal/BridgeBuild/README.md)")
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Build Metaplay Prebuilt Serializer"))
	log.Info().Msg("")

	// Check .NET SDK prerequisites.
	dotnetCmd, err := resolveDotnetCommand()
	if err != nil {
		return err
	}
	if err := checkDotnetSdkVersionAtLeast10(ctx, dotnetCmd); err != nil {
		return err
	}

	// Serialize serializer generation with other bridge build chains on the same
	// project (same lock file as metaplay-bridge.sh).
	releaseLock, err := unrealbridge.AcquireProjectLock(settings.ProjectDir)
	if err != nil {
		return clierrors.Wrap(err, "Failed to acquire the per-project bridge build lock")
	}
	defer releaseLock()

	log.Info().Msgf("bridge:      %s", styles.RenderTechnical(settings.BridgeCsProjPath))
	log.Info().Msgf("serializer:  %s", styles.RenderTechnical("Metaplay.Generated."+settings.SerializerName))
	log.Info().Msg("")

	// 1. Build the game's bridge host assembly (the game graph the serializer is
	//    generated from), unless the caller wants to reuse the existing output.
	if !o.flagSkipBuild {
		if err := runDotnetCommand(ctx, settings.BridgeDir, dotnetCmd, []string{"build", settings.BridgeCsProjPath, "-c", settings.DotnetConfig}); err != nil {
			return clierrors.Wrap(err, "Failed to build the bridge host .NET project").
				WithSuggestion("Check the build output above for details")
		}
	}
	if _, err := os.Stat(settings.RootAssemblyPath); err != nil {
		return clierrors.Newf("The bridge host assembly was not found at %s", settings.RootAssemblyPath).
			WithSuggestion("Build it first (run without --skip-build)")
	}

	// 2. Generate (or reuse, by hash) the prebuilt serializer.
	if err := generateSerializer(ctx, dotnetCmd, settings); err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderSuccess("✅ Prebuilt serializer generated successfully"))
	log.Info().Msgf("Serializer assembly: %s", styles.RenderTechnical(settings.SerializerDllPath))
	log.Info().Msgf("Serializer hash:     %s", styles.RenderTechnical(settings.SerializerHashPath()))
	log.Info().Msg("")
	return nil
}
