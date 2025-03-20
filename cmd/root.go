/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/internal/version"
	"github.com/metaplay/cli/pkg/common"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/muesli/termenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Logger to stderr (for out-of-band information to not mess up JSON outputs and such).
var stderrLogger zerolog.Logger

var flagProjectConfigPath string // Path to Metaplay project (--project or -p).
var flagVerbose bool             // Verbose logging with (--verbose or -v).
var flagColorMode string         // Color usage mode for output (yes, no, auto).
var skipAppVersionCheck bool     // Skip check for a new version of the CLI (--skip-version-check)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "metaplay",
	Short: "Metaplay CLI for development, deployment, and operations",
	Example: trimIndent(`
		# Initialize a new Metaplay project in an existing Unity project
		MyGame$ metaplay init project

		# Run your game server locally for development
		MyGame$ metaplay dev server

		# Manually deploy your game server to a cloud environment
		MyGame$ metaplay build image
		MyGame$ metaplay deploy server

		# View server logs in a cloud environment
		MyGame$ metaplay debug logs
	`),
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Determine if colors can be used
		hasTerminal := isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())

		// Determine whether to use colors.
		colorMode := coalesceString(os.Getenv("METAPLAYCLI_COLOR"), flagColorMode)
		var useColors bool
		if isTruthy(colorMode) {
			useColors = true
		} else if isFalsy(colorMode) {
			useColors = false
		} else {
			if colorMode != "auto" {
				fmt.Printf("ERROR: Invalid color mode (--color or METAPLAYCLI_COLOR): %s. Allowed values are yes/no/auto.\n", flagColorMode)
				os.Exit(2)
			}
			useColors = hasTerminal
		}

		// Configure lipgloss to use/not use colors.
		if useColors {
			lipgloss.SetColorProfile(termenv.TrueColor)
		} else {
			lipgloss.SetColorProfile(termenv.Ascii)
		}

		// Resolve whether using verbose mode
		isVerbose := isTruthy(os.Getenv("METAPLAYCLI_VERBOSE")) || flagVerbose

		// Initialize zerolog
		initLogger(useColors, isVerbose)

		// Check for common CI environment variables
		isCI := os.Getenv("CI") != "" ||
			os.Getenv("GITHUB_ACTIONS") != "" ||
			os.Getenv("GITLAB_CI") != "" ||
			os.Getenv("BITBUCKET_BUILD_NUMBER") != "" ||
			os.Getenv("CIRCLECI") != "" ||
			os.Getenv("TRAVIS") != "" ||
			os.Getenv("APPVEYOR") != "" ||
			os.Getenv("TEAMCITY_VERSION") != "" ||
			os.Getenv("BUILDKITE") != "" ||
			os.Getenv("HUDSON_URL") != "" ||
			os.Getenv("JENKINS_URL") != "" ||
			os.Getenv("BAMBOO_AGENT_HOME") != "" ||
			os.Getenv("TFS_BUILD") != "" ||
			os.Getenv("NETLIFY") != "" ||
			os.Getenv("NOW_BUILDER") != ""

		// Determine if the CLI is running in interactive mode:
		// - Interactive mode requires a terminal
		// - Being in CI disabled interactive mode
		// - Verbose mode disables interactive mode
		isInteractive := true
		modeStr := "interactive mode"
		if !hasTerminal {
			modeStr = "non-interactive mode (no terminal)"
			isInteractive = false
		} else if isVerbose {
			modeStr = "non-interactive mode (verbose)"
			isInteractive = false
		} else if isCI {
			modeStr = "non-interactive mode (CI detected)"
			isInteractive = false
		}

		tui.SetInteractiveMode(isInteractive)

		// Silence the boilerplate for commands where it makes no sense.
		parentCmd := cmd.Parent()
		isCompletion := parentCmd != nil && parentCmd.Name() == "completion"
		isExecCredential := cmd.Name() == "kubernetes-execcredential"
		if isCompletion || isExecCredential {
			return
		}

		// Show CLI version & whether in interactive mode
		stderrLogger.Info().Msgf(styles.RenderMuted("Metaplay CLI %s, %s"), version.AppVersion, modeStr)

		// Log about non-default portal being used.
		if common.PortalBaseURL != common.DefaultPortalBaseURL {
			stderrLogger.Info().Msgf(styles.RenderMuted("Portal base URL: %s"), common.PortalBaseURL)
		}

		// Check for new CLI version available.
		isUpdateCliCmd := parentCmd != nil && parentCmd.Name() == "update" && cmd.Use == "cli"
		if !skipAppVersionCheck && !isUpdateCliCmd {
			version.CheckVersion(&stderrLogger)
		}
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	// Register global flags.
	flags := rootCmd.PersistentFlags()
	flags.BoolVarP(&flagVerbose, "verbose", "v", false, "Enable verbose logging, useful for troubleshooting [env: METAPLAYCLI_VERBOSE]")
	flags.StringVarP(&flagProjectConfigPath, "project", "p", "", "Path to the to project directory (where metaplay-project.yaml is located)")
	flags.BoolVar(&skipAppVersionCheck, "skip-version-check", false, "Skip the check for a new CLI version being available")
	flags.StringVar(&flagColorMode, "color", "auto", "Should the output be colored (yes/no/auto)? [env: METAPLAYCLI_COLOR]")

	// Add command groups to root.
	coreGroup := &cobra.Group{
		ID:    "core",
		Title: "Core workflows:",
	}
	projectGroup := &cobra.Group{
		ID:    "project",
		Title: "Manage project:",
	}
	manageGroup := &cobra.Group{
		ID:    "manage",
		Title: "Manage resources:",
	}
	otherGroup := &cobra.Group{
		ID:    "other",
		Title: "Other:",
	}
	rootCmd.AddGroup(coreGroup, projectGroup, manageGroup, otherGroup)

	// Core workflows:
	buildCmd.GroupID = "core"
	debugCmd.GroupID = "core"
	deployCmd.GroupID = "core"
	devCmd.GroupID = "core"

	// Manage project:
	initCmd.GroupID = "project"
	updateCmd.GroupID = "project"

	// Manage resources:
	getCmd.GroupID = "manage"
	imageCmd.GroupID = "manage"
	secretsCmd.GroupID = "manage"
	removeCmd.GroupID = "manage"

	// Other:
	authCmd.GroupID = "other"
	versionCmd.GroupID = "other"
	rootCmd.SetHelpCommandGroupID("other")
	rootCmd.SetCompletionCommandGroupID("other")

	// Initialize colored help templates
	initColoredHelpTemplates(rootCmd)
}

// Customer version of zerolog's ConsoleWriter that writes out the full
// line with a color dependent on the log level. Intended for the default
// CLI non-decorated output mode.
type coloredLineConsoleWriter struct {
	Out       *os.File
	UseColors bool
}

func (w *coloredLineConsoleWriter) Write(p []byte) (n int, err error) {
	var event map[string]interface{}
	if err := json.Unmarshal(p, &event); err != nil {
		return 0, err
	}

	// Extract fields
	level, _ := event["level"].(string)
	message, _ := event["message"].(string)

	// Determine color based on level
	var color string = ""
	switch level {
	case "trace":
		// color = "\033[95m" // Bright Magenta
	case "debug":
		// color = "\033[94m" // Bright Blue
	case "info":
		// color = "" // Default color
	case "warn":
		color = "\033[93m" // Bright Yellow
	case "error":
		color = "\033[91m" // Bright Red
	case "fatal":
		color = "\033[35m" // Magenta
	case "panic":
		color = "\033[31;1m" // Bold Red
	default:
		// color = "\033[37m" // Bright White (default)
	}

	// Build the line
	var buf bytes.Buffer
	if w.UseColors && color != "" {
		buf.WriteString(color)
	}
	buf.WriteString(message)
	if w.UseColors {
		buf.WriteString("\033[0m") // Reset color
	}
	buf.WriteString("\n")

	// Write to the output
	return w.Out.Write(buf.Bytes())
}

// Initialize zerolog:
// In verbose mode, the output includes timestamps and log levels. Colors are
// always enabled.
// In non-verbose mode, the output is plain-text only, so its compatible with
// piping to `jq` and other tools. Colors are auto-detected based on the TTY used.
func initLogger(useColors, isVerbose bool) {
	if isVerbose {
		// Verbose logging: Debug level with timestamps and log level included
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		zerolog.TimeFieldFormat = "2006-01-02 15:04:05.000"
		stdoutWriter := zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: "2006-01-02 15:04:05.000",
		}
		log.Logger = zerolog.New(stdoutWriter).With().Timestamp().Logger()

		stderrWriter := zerolog.ConsoleWriter{
			Out:        os.Stderr,
			TimeFormat: "2006-01-02 15:04:05.000",
		}
		stderrLogger = zerolog.New(stderrWriter).With().Timestamp().Logger()
	} else {
		// Non-verbose logging: Info level with no decorations
		zerolog.SetGlobalLevel(zerolog.InfoLevel)

		// Custom console stdoutWriter with colored lines
		stdoutWriter := &coloredLineConsoleWriter{
			Out:       os.Stdout,
			UseColors: useColors,
		}
		log.Logger = zerolog.New(stdoutWriter).With().Logger()

		// Custom console stderrWriter with colored lines
		stderrWriter := &coloredLineConsoleWriter{
			Out:       os.Stderr,
			UseColors: useColors,
		}
		stderrLogger = zerolog.New(stderrWriter).With().Logger()
	}
}

// Base interface for a options-based command. Take a look at any of the
// structs implementing commands to see how this should be used.
type CommandOptions interface {
	Prepare(cmd *cobra.Command, args []string) error
	Run(cmd *cobra.Command) error
}

// Get the UsePositionalArgs for the given command
func getUsePositionalArgs(opts CommandOptions) (UsePositionalArgs, bool) {
	// Use reflection to access the embedded UsePositionalArgs (to parse PositionalArgs)
	v := reflect.ValueOf(opts)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	field := v.FieldByName("UsePositionalArgs")
	if field.IsValid() {
		baseOpts, hasBase := field.Interface().(UsePositionalArgs)
		if hasBase {
			return baseOpts, true
		} else {
			return UsePositionalArgs{}, false
		}
	} else {
		return UsePositionalArgs{}, false
	}
}

// Create a Cobra.Run compatible runner function for a command implementing
// CommandOptions.
func runCommand(opts CommandOptions) func(cmd *cobra.Command, args []string) {
	return func(cmd *cobra.Command, args []string) {
		posArgs, hasPosArgs := getUsePositionalArgs(opts)
		if hasPosArgs {
			err := posArgs.Arguments().ParseCommandLine(args)
			if err != nil {
				log.Error().Msgf("Expected usage: %s", cmd.UseLine())
				log.Warn().Msgf("%s", posArgs.args.GetHelpText())
				log.Info().Msgf("Run with --help flag for full help.")
				os.Exit(2)
			}
		} else {
			// \todo implement me: expect no args provided
		}

		// Prepare the command.
		err := opts.Prepare(cmd, args)
		if err != nil {
			log.Info().Msgf("%s", cmd.UsageString())
			log.Error().Msgf("USAGE ERROR: %v", err)
			os.Exit(2)
		}

		// Run the command.
		err = opts.Run(cmd)
		if err != nil {
			log.Error().Msgf("ERROR: %v", err)
			os.Exit(1)
		}
	}
}

// Trim the indentation from the beginning of each line in the string.
// To be used with the multiline `Long` and `Example` of the Cobra commands.
func trimIndent(str string) string {
	str = strings.TrimSpace(str)
	lines := strings.Split(str, "\n")
	trimmedLines := []string{}
	for _, line := range lines {
		trimmed := "  " + strings.TrimSpace(line)
		trimmedLines = append(trimmedLines, trimmed)
	}
	return strings.Join(trimmedLines, "\n")
}

// Render a long command description.
func renderLong(opts CommandOptions, str string) string {
	// Trim indent from string.
	str = trimIndent(str)

	// Inject positional arguments descriptions.
	if strings.Contains(str, "{Arguments}") {
		posArgs, hasPosArgs := getUsePositionalArgs(opts)
		if hasPosArgs {
			str = strings.Replace(str, "{Arguments}", posArgs.Arguments().GetHelpText(), 1)
		} else {
			log.Panic().Msgf("Description text refers to positional arguments but command does not use any")
		}
	}

	// Highlight important keywords
	for _, keyword := range []string{"Note:", "Warning:", "Important:"} {
		str = strings.ReplaceAll(str, keyword, styles.RenderAttention(keyword))
	}

	// Style code blocks and inline code with a different color
	str = styleInlineCode(str)

	// Return final result
	return str
}

// Return true if the value is truthy ('yes', 'y', 'true', '1').
func isTruthy(str string) bool {
	str = strings.ToLower(str)
	return str == "yes" || str == "y" || str == "true" || str == "1"
}

// Return true if the value is falsy ('no', 'n', 'false', '0').
func isFalsy(str string) bool {
	str = strings.ToLower(str)
	return str == "no" || str == "n" || str == "false" || str == "0"
}
