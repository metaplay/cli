package cmd

import (
	"bytes"
	"encoding/json"
	"os"

	"github.com/mattn/go-isatty"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Value of the --project (or -p).
var flagProjectConfigPath string

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "metaplay",
	Short: "Metaplay CLI: manage your projects and cloud deployments",
	Long: `This CLI allows you to manage projects using Metaplay. You can
integrate the Metaplay SDK into your project and manage the SDK versions.
It also helps you build your backend and deploy it into the cloud.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Initialize zerolog
		isVerbose, _ := cmd.Flags().GetBool("verbose")
		initLogger(isVerbose)
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
	rootCmd.PersistentFlags().Bool("verbose", false, "Enable verbose logging")
	rootCmd.PersistentFlags().StringVarP(&flagProjectConfigPath, "project", "p", "", "Path to the project's .metaplay.yaml config file")
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
	var color string
	switch level {
	case "trace":
		color = "\033[95m" // Bright Magenta
	case "debug":
		color = "\033[94m" // Bright Blue
	case "info":
		color = "" // Default color
	case "warn":
		color = "\033[93m" // Bright Yellow
	case "error":
		color = "\033[91m" // Bright Red
	case "fatal":
		color = "\033[35m" // Magenta
	case "panic":
		color = "\033[31;1m" // Bold Red
	default:
		color = "\033[37m" // Bright White (default)
	}

	// Build the line
	var buf bytes.Buffer
	if w.UseColors {
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
func initLogger(isVerbose bool) {
	if isVerbose {
		// Verbose logging: Debug level with timestamps and log level included
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		zerolog.TimeFieldFormat = "2006-01-02 15:04:05.000"
		log.Logger = zerolog.New(zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: "2006-01-02 15:04:05.000",
		}).With().
			Timestamp().
			Logger()
	} else {
		// Determine if colors can be used
		useColors := isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())

		// Custom console writer with colored lines
		writer := &coloredLineConsoleWriter{
			Out:       os.Stdout,
			UseColors: useColors,
		}

		// Non-verbose logging: Info level with no decorations
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
		log.Logger = zerolog.New(writer).With().Logger()
	}
}
