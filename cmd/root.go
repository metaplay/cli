/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/internal/version"
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
	Short: "Metaplay CLI: manage your projects and cloud deployments",
	Long: trimIndent(`
		This CLI allows you to manage projects using Metaplay. You can
		integrate the Metaplay SDK to your project and manage the SDK versions.
		It also helps you build your backend and deploy it into the cloud.
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

		// Show CLI version & whether in interactive mode
		stderrLogger.Info().Msgf(styles.RenderMuted("Metaplay CLI %s, %s"), version.AppVersion, modeStr)

		// Check for new CLI version available.
		if !skipAppVersionCheck && cmd.Use != "update" {
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
	flags := rootCmd.PersistentFlags()
	flags.BoolVarP(&flagVerbose, "verbose", "v", false, "Enable verbose logging, useful for troubleshooting")
	flags.StringVarP(&flagProjectConfigPath, "project", "p", "", "Path to the to project directory (where metaplay-project.yaml is located)")
	flags.BoolVar(&skipAppVersionCheck, "skip-version-check", false, "Skip the check for a new CLI version being available")
	flags.StringVar(&flagColorMode, "color", "auto", "Should the output be colored (yes/no/auto)?")
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

// Create a Cobra.Run compatible runner function for a command implementing
// CommandOptions.
func runCommand(o CommandOptions) func(cmd *cobra.Command, args []string) {
	return func(cmd *cobra.Command, args []string) {
		// Parse arguments.
		err := o.Prepare(cmd, args)
		if err != nil {
			log.Info().Msgf("%s", cmd.UsageString())
			log.Error().Msgf("USAGE ERROR: %v", err)
			os.Exit(2)
		}

		// Run the command.
		err = o.Run(cmd)
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

// Return true if the value is truthy ('yes', 'y', 'true', '1').
func isTruthy(str string) bool {
	str = strings.ToLower(str)
	return str == "yes" || str == "y" || str == "true" || str == "1"
}

// Return true if the value is truthy ('yes', 'y', 'true', '1').
// Note: Returns false for an empty input!
func isFalsy(str string) bool {
	str = strings.ToLower(str)
	return str == "no" || str == "n" || str == "false" || str == "0"
}
