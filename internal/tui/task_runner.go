/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
)

// TaskStatus represents the current state of a task
type TaskStatus int

const (
	StatusPending TaskStatus = iota
	StatusRunning
	StatusCompleted
	StatusFailed
)

// Spinner frames for the running state
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Cached styles for task status rendering
var (
	styleRunning   = lipgloss.NewStyle().Foreground(styles.ColorBlue)
	styleCompleted = lipgloss.NewStyle().Foreground(styles.ColorGreen)
	styleFailed    = lipgloss.NewStyle().Foreground(styles.ColorRed)
	stylePending   = lipgloss.NewStyle().Foreground(styles.ColorNeutral)
)

// TaskOutput contains the outputs from a given task and is shown along with the task's status.
type TaskOutput struct {
	headerLines []string   // Header lines (all are shown, updates are logged)
	logLines    []string   // Append-only log lines of output (only 5 are shown)
	footerLines []string   // Footer lines (all are shown, updates are logged)
	mu          sync.Mutex // Protects the lines slice

	progress *plainProgress // The runner's progress reporting, which logs lines once the display has closed
}

// logsLines reports whether appended lines are logged, which they are when there is no display.
func (to *TaskOutput) logsLines() bool {
	return !isInteractiveMode || (to.progress != nil && to.progress.isLogging())
}

// TaskRunFunc is the function signature for task execution functions
type TaskRunFunc func(output *TaskOutput) error

// Append a new line at the end of the output.
func (to *TaskOutput) AppendLine(line string) {
	to.mu.Lock()
	to.logLines = append(to.logLines, line)
	to.mu.Unlock()

	if to.logsLines() {
		log.Info().Msgf("  %s", line)
	}
}

// AppendLinef appends a new formatted line at the end of the output using fmt.Sprintf.
func (to *TaskOutput) AppendLinef(format string, a ...any) {
	to.AppendLine(fmt.Sprintf(format, a...))
}

// Update the header lines to the provided ones. Also logged when there is no display.
func (to *TaskOutput) SetHeaderLines(lines []string) {
	to.mu.Lock()
	to.headerLines = lines
	to.mu.Unlock()

	if to.logsLines() {
		for _, line := range lines {
			log.Info().Msgf("  %s", line)
		}
	}
}

// TaskOutputIndent is how many columns the task runner indents a task's output lines by.
const TaskOutputIndent = 4

// Update the footer lines to the provided ones. Also logged when there is no display.
func (to *TaskOutput) SetFooterLines(lines []string) {
	to.mu.Lock()
	to.footerLines = lines
	to.mu.Unlock()

	if to.logsLines() {
		for _, line := range lines {
			log.Info().Msgf("  %s", line)
		}
	}
}

// getLines returns a copy of the current output lines.
func (to *TaskOutput) getLines() []string {
	to.mu.Lock()
	defer to.mu.Unlock()

	// Get the most recent log lines (up to numLinesToShow)
	numLinesToShow := 10
	shownLogLines := to.logLines
	if len(shownLogLines) > numLinesToShow {
		shownLogLines = shownLogLines[len(shownLogLines)-numLinesToShow:]
	}

	// Calculate total size needed
	totalSize := len(to.headerLines) + len(shownLogLines) + len(to.footerLines)

	// Create a result slice with the right capacity
	result := make([]string, 0, totalSize)

	// Append all sections in the correct order
	result = append(result, to.headerLines...)
	result = append(result, shownLogLines...)
	result = append(result, to.footerLines...)

	return result
}

// Task represents a single task with its title, function, and status
type Task struct {
	title     string        // Title for the task
	runFunc   TaskRunFunc   // Run function for the task
	cleanup   bool          // Runs also after an earlier task failed, see AddCleanupTask
	status    TaskStatus    // Status of the task
	error     error         // Error that was returned by the task execution function
	startTime time.Time     // Time when the task was started
	elapsed   time.Duration // Amount of time elapsed while running the task
	mu        sync.Mutex    // Protects status, error, startTime, and elapsed
	output    TaskOutput    // Output from the task
}

// TaskRunner manages and executes a sequence of tasks with visual progress
type TaskRunner struct {
	tasks      []*Task        // Tasks that the operation consists of, run sequentially
	progress   *plainProgress // Progress reporting as log lines, when there is no display
	done       chan struct{}  // Signals when all tasks are complete
	frameIndex int            // Current frame index for spinner animation
	lastTick   time.Time      // Last time the spinner was updated
	program    *tea.Program   // Reference to the tea program for quitting

	notStarted []string // Ordinary tasks the interrupt kept from starting, written by executeTasks
}

// plainProgress reports the tasks' progress as log lines: in non-interactive mode, and after the
// user interrupted the command, which closes the display while the tasks still run.
type plainProgress struct {
	mu      sync.Mutex
	logging bool  // Report progress as log lines
	current *Task // The task being run, or nil
}

func (p *plainProgress) isLogging() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.logging
}

// cleanupTimeout bounds each cleanup task.
const cleanupTimeout = 30 * time.Second

// tickMsg is sent when the spinner should advance one frame
type tickMsg struct{}

// doneMsg is sent when all tasks have completed or failed
type doneMsg struct{}

// NewTaskRunner creates a new TaskRunner
func NewTaskRunner() *TaskRunner {
	return &TaskRunner{
		tasks:    make([]*Task, 0),
		progress: &plainProgress{},
		done:     make(chan struct{}),
		lastTick: time.Now(),
	}
}

// AddTask adds a new task to the runner
func (m *TaskRunner) AddTask(title string, runFunc TaskRunFunc) {
	m.addTask(title, runFunc, false)
}

func (m *TaskRunner) addTask(title string, runFunc TaskRunFunc, cleanup bool) {
	m.tasks = append(m.tasks, &Task{
		title:   title,
		runFunc: runFunc,
		cleanup: cleanup,
		status:  StatusPending,
		output:  TaskOutput{progress: m.progress},
	})
}

// AddCleanupTask adds a task that undoes an earlier task's changes, such as a setting changed in a
// pod. Unlike other tasks, it runs also when an earlier task failed or the command was interrupted,
// as long as some task before it started. Its own failure does not stop the tasks after it. Its
// context is not cancelled with the command's, and is bounded by cleanupTimeout. runFunc must be
// safe to run when the change it undoes was not made.
func (m *TaskRunner) AddCleanupTask(title string, runFunc func(ctx context.Context, output *TaskOutput) error) {
	m.addTask(title, func(output *TaskOutput) error {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		return runFunc(ctx, output)
	}, true)
}

// taskStatusStyle returns the appropriate style for a task based on its status
func taskStatusStyle(status TaskStatus) lipgloss.Style {
	switch status {
	case StatusRunning:
		return styleRunning
	case StatusCompleted:
		return styleCompleted
	case StatusFailed:
		return styleFailed
	default:
		return stylePending
	}
}

// getStatusSymbol returns the appropriate symbol for a task status
func (m *TaskRunner) getStatusSymbol(status TaskStatus) string {
	switch status {
	case StatusPending:
		return "○"
	case StatusRunning:
		return spinnerFrames[m.frameIndex] // Use current spinner frame
	case StatusCompleted:
		return "✓"
	case StatusFailed:
		return "✗"
	default:
		return "?"
	}
}

// Run starts executing tasks sequentially and displays the progress
func (m *TaskRunner) Run() error {
	if isInteractiveMode {
		return m.runInteractive()
	}
	return m.runNonInteractive()
}

// runInteractive runs tasks with an interactive TUI using Bubble Tea
func (m *TaskRunner) runInteractive(options ...tea.ProgramOption) error {
	// Create and store the program instance
	m.program = tea.NewProgram(m, options...)

	// Start task execution in background
	go m.executeTasks()

	// Run the TUI. Notices printed meanwhile, such as main's "Interrupted" on SIGINT, are held
	// back until the terminal is restored, so they don't garble the display.
	beginDisplay()
	_, err := m.program.Run()
	endDisplay()
	displayFailed := err != nil && !errors.Is(err, tea.ErrInterrupted)
	if displayFailed {
		log.Warn().Msgf("Task progress display failed: %v", err)
	}

	// The display closes before the tasks finish when the user interrupts the command (Ctrl-C,
	// SIGINT or SIGTERM). Stop the command, as SIGINT does without the display, and wait for the
	// tasks, reporting their progress as log lines. A Ctrl-C key press also stops the command when
	// the tasks finished while the display was closing.
	tasksDone := false
	select {
	case <-m.done:
		tasksDone = true
	default:
	}
	if interrupted.Load() || (!tasksDone && !displayFailed) {
		Interrupt()
	}
	if !tasksDone {
		m.progress.closeDisplay()
		<-m.done
	}

	return m.result()
}

// closeDisplay switches progress reporting to log lines, starting with the task still running.
func (p *plainProgress) closeDisplay() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.logging = true
	if p.current != nil {
		log.Info().Msgf("Waiting for '%s' to finish...", p.current.title)
	}
}

// taskStarted records the task being run, and logs it once the display has closed.
func (p *plainProgress) taskStarted(task *Task) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current = task
	if p.logging {
		log.Info().Msgf("%s...", task.title)
	}
}

// taskEnded logs a task's success once the display has closed. Failures are shown by result.
func (p *plainProgress) taskEnded(err error, elapsed time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current = nil
	if p.logging && err == nil {
		log.Info().Msgf(" %s %s %s", styles.RenderSuccess("✓"), "Done", humanizeElapsed(elapsed))
	}
}

// runNonInteractive runs tasks with basic logging for non-interactive shells
func (m *TaskRunner) runNonInteractive() error {
	m.progress.logging = true
	m.executeTasks()
	err := m.result()
	if err == nil {
		log.Info().Msg("")
	}
	return err
}

// result returns the error for the command, once the tasks have finished, and shows the failures
// that error does not cover. The error is the first ordinary task's failure. Without one, it is
// context.Canceled if an interrupt skipped tasks, or else the first cleanup task's failure.
//
// The command shows its error itself, except after an interrupt: then it shows only "Canceled.",
// so result shows the failures here. It leaves out ordinary tasks' failures that wrap
// context.Canceled, which the interrupt caused. Cleanup tasks don't use the command's context, so
// their failures are always shown.
func (m *TaskRunner) result() error {
	wasInterrupted := interrupted.Load()
	var failed []*Task
	for _, task := range m.tasks {
		task.mu.Lock()
		if task.error != nil {
			failed = append(failed, task)
		}
		task.mu.Unlock()
	}

	// The interrupt kept tasks from running, so the command did not do all its work
	canceled := len(m.notStarted) > 0

	// The task whose failure is the error, if any
	var returnedTask *Task
	for _, task := range failed {
		if !task.cleanup {
			returnedTask = task
			break
		}
	}
	if returnedTask == nil && len(failed) > 0 && !canceled {
		returnedTask = failed[0]
	}

	for _, task := range failed {
		switch {
		case !wasInterrupted && task == returnedTask:
			// The command shows it
		case wasInterrupted && !task.cleanup && errors.Is(task.error, context.Canceled):
			// Caused by the interrupt
		default:
			errorDisplay(withTaskTitle(task))
		}
	}
	if canceled {
		log.Warn().Msgf("Not started because of the interrupt: %s", strings.Join(m.notStarted, ", "))
	}

	if returnedTask != nil {
		return returnedTask.error
	}
	if canceled {
		return context.Canceled
	}
	return nil
}

// withTaskTitle prefixes a failed task's error with its title. A CLIError is left as it is: its own
// message describes the failure, and the command's error display would show only that message.
func withTaskTitle(task *Task) error {
	if _, ok := clierrors.AsCLIError(task.error); ok {
		return task.error
	}
	return fmt.Errorf("%s: %w", task.title, task.error)
}

// executeTasks runs the tasks sequentially. After an ordinary task fails or the command is
// interrupted, only cleanup tasks run.
func (m *TaskRunner) executeTasks() {
	stopped := false
	anyStarted := false
	for _, task := range m.tasks {
		if task.cleanup {
			if !anyStarted {
				continue // Nothing to undo
			}
		} else if stopped {
			continue
		} else if interrupted.Load() {
			m.notStarted = append(m.notStarted, task.title)
			continue
		}
		anyStarted = true
		m.progress.taskStarted(task)

		// Update task status to running and start timing
		task.mu.Lock()
		task.status = StatusRunning
		task.startTime = time.Now()
		task.mu.Unlock()

		// Execute the task
		log.Debug().Msgf("Task start: %s", task.title)
		err := task.runFunc(&task.output)

		task.mu.Lock()
		task.elapsed = time.Since(task.startTime)
		elapsed := task.elapsed
		if err != nil {
			task.status = StatusFailed
			task.error = err
		} else {
			task.status = StatusCompleted
		}
		task.mu.Unlock()
		m.progress.taskEnded(err, elapsed)

		if err != nil {
			log.Debug().Msgf("Task failed: %s: %v", task.title, err)
			if !task.cleanup {
				stopped = true
			}
		} else {
			log.Debug().Msgf("Task completed: %s %s", task.title, humanizeElapsed(elapsed))
		}
	}

	// Signal completion and quit the program if in interactive mode
	close(m.done)
	if m.program != nil {
		m.program.Send(doneMsg{})
	}
	log.Debug().Msg("All tasks completed")
}

// Init implements tea.Model
func (m TaskRunner) Init() tea.Cmd {
	return m.tick() // Start the spinner ticker
}

// tick advances the spinner one frame
func (m *TaskRunner) tick() tea.Cmd {
	return tea.Tick(time.Millisecond*80, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

// Update implements tea.Model
func (m TaskRunner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			// Mark the command interrupted at once, so no further task starts while the display
			// closes. runInteractive cancels the command once the terminal is restored.
			interrupted.Store(true)
			return m, tea.Quit
		}
	case tickMsg:
		// Only advance the frame if enough time has passed and there's a running task
		if time.Since(m.lastTick) >= time.Millisecond*80 {
			hasRunningTask := false
			for _, task := range m.tasks {
				task.mu.Lock()
				if task.status == StatusRunning {
					hasRunningTask = true
					task.elapsed = time.Since(task.startTime)
				}
				task.mu.Unlock()
				if hasRunningTask {
					break
				}
			}
			if hasRunningTask {
				m.frameIndex = (m.frameIndex + 1) % len(spinnerFrames)
				m.lastTick = time.Now()
			}
		}
		return m, m.tick()
	case doneMsg:
		return m, tea.Quit
	}
	return m, nil
}

// humanizeElapsed formats a duration as seconds with one decimal place
func humanizeElapsed(d time.Duration) string {
	return styles.RenderMuted(fmt.Sprintf("[%.1fs]", d.Seconds()))
}

// View implements tea.Model
func (m TaskRunner) View() tea.View {
	// Build the content starting with the title
	var lines []string

	// Build the task list content
	for _, task := range m.tasks {
		task.mu.Lock()
		status := task.status
		err := task.error
		title := task.title
		elapsed := task.elapsed
		outputLines := task.output.getLines()
		task.mu.Unlock()

		statusStyle := taskStatusStyle(status)
		symbol := statusStyle.Render(m.getStatusSymbol(status))

		var taskLine string
		if err != nil {
			taskLine = fmt.Sprintf(" %s %s %s", symbol, title, styles.RenderError("[failed]"))
		} else if status == StatusCompleted || status == StatusRunning {
			taskLine = fmt.Sprintf(" %s %s %s", symbol, title, humanizeElapsed(elapsed))
		} else {
			taskLine = fmt.Sprintf(" %s %s", symbol, title)
		}
		lines = append(lines, taskLine)

		// Add output lines if there are any, indented
		for _, outputLine := range outputLines {
			lines = append(lines, strings.Repeat(" ", TaskOutputIndent)+styles.RenderMuted(outputLine))
		}
	}

	lines = append(lines, "")

	var sb strings.Builder
	for _, line := range lines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	return tea.NewView(sb.String())
}
