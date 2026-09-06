/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/metaplay/cli/pkg/unrealbridge"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

//go:embed initunreal/templates
var initUnrealTemplates embed.FS

// initUnrealTemplateFiles maps embedded template files to their output paths relative
// to the scaffolded Unreal project directory. Output paths are themselves templates
// (the game name appears in file names).
var initUnrealTemplateFiles = []struct {
	template string
	output   string
}{
	{"uproject.tmpl", "{{.GameName}}.uproject"},
	{"metaplay-bridge.json.tmpl", "MetaplayBridge.json"},
	{"gitignore.tmpl", ".gitignore"},
	{"README.md.tmpl", "README.md"},
	{"default-engine.ini.tmpl", "Config/DefaultEngine.ini"},
	{"default-game.ini.tmpl", "Config/DefaultGame.ini"},
	{"game-target.cs.tmpl", "Source/{{.GameName}}.Target.cs"},
	{"editor-target.cs.tmpl", "Source/{{.GameName}}Editor.Target.cs"},
	{"game-build.cs.tmpl", "Source/{{.GameName}}/{{.GameName}}.Build.cs"},
	{"game-module.cpp.tmpl", "Source/{{.GameName}}/Private/{{.GameName}}Module.cpp"},
	{"editor-build.cs.tmpl", "Source/{{.GameName}}Editor/{{.GameName}}Editor.Build.cs"},
	{"editor-module.cpp.tmpl", "Source/{{.GameName}}Editor/Private/{{.GameName}}EditorModule.cpp"},
	{"bridge-csproj.tmpl", "Bridge/{{.GameName}}.Bridge.csproj"},
	{"bridge-module.cs.tmpl", "Bridge/{{.GameName}}BridgeModule.cs"},
	{"bridge-env-provider.cs.tmpl", "Bridge/OfflineEnvironmentConfigProvider.cs"},
	{"gamelogic-csproj.tmpl", "GameLogic/GameLogic.csproj"},
	{"global-options.cs.tmpl", "SharedCode/GlobalOptions.cs"},
	{"player-model.cs.tmpl", "SharedCode/Player/PlayerModel.cs"},
	{"player-actions.cs.tmpl", "SharedCode/Player/PlayerActions.cs"},
	{"player-listeners.cs.tmpl", "SharedCode/Player/PlayerModelListeners.cs"},
}

// gameConfigArchiveRel is where the offline game config archive lives in the scaffolded
// project (the bridge's offline server loads SharedGameConfig.mpa from the project's
// Content directory with plain file IO).
const gameConfigArchiveRel = "Content/SharedGameConfig.mpa"

type initUnrealOpts struct {
	flagGameName      string // Name of the game (e.g. MyGame), used for the project, modules and serializer.
	flagSdkRoot       string // Path to the Metaplay SDK root (MetaplaySDK directory).
	flagEngineVersion string // Unreal engine version the project associates with.
	flagTargetDir     string // Target directory for the scaffolded project.
}

func init() {
	o := initUnrealOpts{}

	cmd := &cobra.Command{
		Use:   "unreal [flags]",
		Short: "Scaffold a new Unreal project wired to the MetaplayUnreal plugin",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Scaffold a new Unreal project wired to the Metaplay SDK through the
			MetaplayUnreal plugin: the .uproject, the MetaplayBridge.json bridge
			declaration, a bridge host .NET project (published as the NativeAOT bridge
			library), a minimal GameLogic/SharedCode shell (a clicker game), and the
			Unreal source modules referencing the plugin.

			The project layout follows the SDK's Unreal samples (Samples/HelloUnreal and
			MetaplaySDK/Unreal/TestHost): all gameplay stays in C#, reached from Unreal
			through the bridge's commands, queries and events.

			The Metaplay SDK is referenced by path: --sdk-root must point at an extracted
			MetaplaySDK directory (Unreal plugin discovery, the bridge build recipe, the
			analyzers and the .NET client are all referenced from there). The offline game
			config archive is copied from the SDK's HelloWorld sample when present.

			After scaffolding, build the .NET side with 'metaplay build unreal-bridge'
			(inside the new project directory) and open the project in Unreal Editor.

			Related commands:
			- 'metaplay build unreal-bridge' builds and stages the .NET side of the project.
			- 'metaplay build serializer' regenerates only the prebuilt serializer.
		`),
		Example: renderExample(`
			# Scaffold a project in the current directory using a local SDK checkout:
			MyGame$ metaplay init unreal --game-name MyGame --sdk-root ../metaplay-sdk/MetaplaySDK

			# Scaffold into a specific directory with a specific engine version:
			metaplay init unreal --game-name MyGame --sdk-root ~/sdk/MetaplaySDK --target-dir ~/projects/MyGame --engine-version 5.8
		`),
	}

	flags := cmd.Flags()
	flags.StringVar(&o.flagGameName, "game-name", "", "Name of the game (PascalCase, e.g. MyGame): used for the .uproject, the Unreal modules and the serializer name")
	flags.StringVar(&o.flagSdkRoot, "sdk-root", "", "Path to the Metaplay SDK root (the MetaplaySDK directory)")
	flags.StringVar(&o.flagEngineVersion, "engine-version", "5.8", "Unreal engine version the project associates with")
	flags.StringVar(&o.flagTargetDir, "target-dir", "", "Target directory for the scaffolded project (default: the current directory)")

	initCmd.AddCommand(cmd)
}

// Validate the game name: a PascalCase identifier usable as an Unreal module name, a
// C# namespace fragment and a file name.
var initUnrealGameNameRegex = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)

func (o *initUnrealOpts) Prepare(cmd *cobra.Command, args []string) error {
	if o.flagGameName == "" {
		return clierrors.NewUsageError("--game-name is required").
			WithSuggestion("Choose a PascalCase name for the game, e.g. --game-name MyGame")
	}
	if !initUnrealGameNameRegex.MatchString(o.flagGameName) {
		return clierrors.NewUsageErrorf("invalid --game-name '%s'", o.flagGameName).
			WithSuggestion("The game name must be PascalCase alphanumeric (e.g. MyGame): it is used as the Unreal module name, a C# namespace and part of file names")
	}
	if o.flagSdkRoot == "" {
		return clierrors.NewUsageError("--sdk-root is required").
			WithSuggestion("Point it at an extracted Metaplay SDK root (the MetaplaySDK directory)")
	}
	return nil
}

func (o *initUnrealOpts) Run(cmd *cobra.Command) error {
	// Resolve and validate the SDK root.
	sdkRoot, err := filepath.Abs(o.flagSdkRoot)
	if err != nil {
		return fmt.Errorf("failed to resolve --sdk-root: %w", err)
	}
	if err := unrealbridge.ValidateSdkRoot(sdkRoot); err != nil {
		return clierrors.Wrap(err, "Invalid --sdk-root").
			WithSuggestion("Unpack the Metaplay SDK release zip first, or point --sdk-root at your MetaplaySDK git checkout")
	}

	// Resolve the target directory (default: current directory, like 'metaplay init project').
	targetDir := o.flagTargetDir
	if targetDir == "" {
		targetDir = coalesceString(flagProjectConfigPath, ".")
	}
	absTargetDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("failed to resolve target directory: %w", err)
	}
	if err := os.MkdirAll(absTargetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory %s: %w", absTargetDir, err)
	}

	// Refuse to overwrite an existing Unreal project.
	existingUproject := filepath.Join(absTargetDir, o.flagGameName+".uproject")
	if _, err := os.Stat(existingUproject); err == nil {
		return clierrors.Newf("%s already exists", existingUproject).
			WithSuggestion("Choose a different --target-dir or --game-name")
	}

	// Compute the relative paths from the scaffolded project to the SDK. The Unreal
	// project references the SDK tree by relative path (plugin discovery through
	// AdditionalPluginDirectories, and the bridge .csproj's import of the shared
	// recipe), so the SDK must live at a stable relative location.
	relFromProject, err := filepath.Rel(absTargetDir, sdkRoot)
	if err != nil {
		return fmt.Errorf("failed to compute the relative path to the SDK: %w", err)
	}
	// Bridge/ and GameLogic/ are one level under the project root.
	relFromBridge := filepath.Join("..", relFromProject)
	relFromGameLogic := filepath.Join("..", relFromProject)

	// The editor-iteration bridge library path in the project settings points at the
	// publish output of the *host* platform's runtime identifier.
	hostRid, err := unrealbridge.HostRuntimeIdentifier()
	if err != nil {
		return clierrors.Wrap(err, "Could not determine the host platform's runtime identifier")
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Scaffold Metaplay Unreal Project"))
	log.Info().Msg("")
	log.Info().Msgf("Game name:    %s", styles.RenderTechnical(o.flagGameName))
	log.Info().Msgf("Project dir:  %s", styles.RenderTechnical(absTargetDir))
	log.Info().Msgf("SDK root:     %s", styles.RenderTechnical(sdkRoot))
	log.Info().Msgf("Engine:       %s", styles.RenderTechnical(o.flagEngineVersion))
	log.Info().Msg("")

	// Render the template data.
	data := initUnrealTemplateData{
		GameName:            o.flagGameName,
		EngineVersion:       o.flagEngineVersion,
		SdkRelFromProject:   filepath.ToSlash(relFromProject),
		SdkRelFromBridge:    filepath.ToSlash(relFromBridge),
		SdkRelFromGameLogic: filepath.ToSlash(relFromGameLogic),
		HostRid:             hostRid,
		HostLibExt:          libExtForRid(hostRid),
	}
	if err := renderUnrealTemplates(data, absTargetDir); err != nil {
		return err
	}

	// Copy the offline game config archive so the offline environment works out of the
	// box (the clicker shell matches that config). Prefer the copy checked into the SDK
	// tree's TestHost; fall back to the HelloWorld sample next to the SDK checkout.
	var sampleArchive string
	for _, candidate := range []string{
		filepath.Join(sdkRoot, "Unreal", "TestHost", "Content", "SharedGameConfig.mpa"),
		filepath.Join(sdkRoot, "..", "Samples", "HelloWorld", "Assets", "StreamingAssets", "SharedGameConfig.mpa"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			sampleArchive = candidate
			break
		}
	}
	if sampleArchive != "" {
		destArchive := filepath.Join(absTargetDir, filepath.FromSlash(gameConfigArchiveRel))
		if err := os.MkdirAll(filepath.Dir(destArchive), 0755); err != nil {
			return fmt.Errorf("failed to create Content directory: %w", err)
		}
		if err := copyFileContents(sampleArchive, destArchive); err != nil {
			return clierrors.Wrapf(err, "Failed to copy the game config archive to %s", destArchive)
		}
	} else {
		log.Warn().Msgf("the offline game config archive was not found under the SDK: the scaffolded project's offline mode cannot load a game config until you add one at %s", gameConfigArchiveRel)
	}

	log.Info().Msg(styles.RenderSuccess("✅ Metaplay Unreal project scaffolded successfully"))
	log.Info().Msg("")
	log.Info().Msgf("Build the .NET side with:   %s", styles.RenderPrompt("metaplay build unreal-bridge"))
	log.Info().Msgf("Regenerate the serializer:  %s", styles.RenderPrompt("metaplay build serializer"))
	log.Info().Msgf("Then open the project in Unreal Editor:  %s", styles.RenderTechnical(existingUproject))
	log.Info().Msg("")
	return nil
}

// initUnrealTemplateData is the data the Unreal project templates are rendered with.
type initUnrealTemplateData struct {
	GameName            string
	EngineVersion       string
	SdkRelFromProject   string
	SdkRelFromBridge    string
	SdkRelFromGameLogic string
	HostRid             string
	HostLibExt          string
}

// renderUnrealTemplates renders the embedded template set into the target directory.
func renderUnrealTemplates(data initUnrealTemplateData, targetDir string) error {
	for _, file := range initUnrealTemplateFiles {
		content, err := initUnrealTemplates.ReadFile("initunreal/templates/" + file.template)
		if err != nil {
			return fmt.Errorf("failed to read embedded template %s: %w", file.template, err)
		}

		tmpl, err := template.New(file.template).Parse(string(content))
		if err != nil {
			return fmt.Errorf("failed to parse template %s: %w", file.template, err)
		}

		outputPathTmpl, err := template.New("outputPath").Parse(file.output)
		if err != nil {
			return fmt.Errorf("failed to parse output path %s: %w", file.output, err)
		}
		var outputName strings.Builder
		if err := outputPathTmpl.Execute(&outputName, data); err != nil {
			return fmt.Errorf("failed to render output path for %s: %w", file.template, err)
		}

		var rendered strings.Builder
		if err := tmpl.Execute(&rendered, data); err != nil {
			return fmt.Errorf("failed to render template %s: %w", file.template, err)
		}

		destPath := filepath.Join(targetDir, filepath.FromSlash(outputName.String()))
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("failed to create directory for %s: %w", destPath, err)
		}
		if err := os.WriteFile(destPath, []byte(rendered.String()), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", destPath, err)
		}
		log.Debug().Msgf("Wrote %s", destPath)
	}
	return nil
}

// libExtForRid returns the NativeAOT shared library extension for a runtime identifier.
func libExtForRid(rid string) string {
	switch unrealbridge.NativeLibraryGlob(rid) {
	case "*.dll":
		return ".dll"
	case "*.dylib":
		return ".dylib"
	default:
		return ".so"
	}
}
