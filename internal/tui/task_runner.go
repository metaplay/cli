/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

// Task represents a single task with its title, function, and status
type Task struct {
	title     string        // Title for the task
	runFunc   func() error  // Run function for the task
	status    TaskStatus    // Status of the task
	error     error         // Error that was returned by the task execution function
	startTime time.Time     // Time when the task was started
	elapsed   time.Duration // Amount of time elapsed while running the task
	mu        sync.Mutex    // Protects status, error, startTime, and elapsed
}

// TaskRunner manages and executes a sequence of tasks with visual progress

type TaskRunner struct {
	tasks      []*Task       // Tasks that the operation consists of, run sequentially
	quitting   bool          // Is the operation quitting?
	done       chan struct{} // Signals when all tasks are complete
	frameIndex int           // Current frame index for spinner animation
	lastTick   time.Time     // Last time the spinner was updated
	program    *tea.Program  // Reference to the tea program for quitting
}

// tickMsg is sent when the spinner should advance one frame
type tickMsg struct{}

// doneMsg is sent when all tasks have completed or failed
type doneMsg struct{ err error }

// NewTaskRunner creates a new TaskRunner
func NewTaskRunner() *TaskRunner {
	return &TaskRunner{
		tasks:    make([]*Task, 0),
		done:     make(chan struct{}),
		lastTick: time.Now(),
	}
}

// AddTask adds a new task to the runner
func (m *TaskRunner) AddTask(title string, runFunc func() error) {
	m.tasks = append(m.tasks, &Task{
		title:   title,
		runFunc: runFunc,
		status:  StatusPending,
	})
}

// taskStatusStyle returns the appropriate style for a task based on its status
func taskStatusStyle(status TaskStatus) lipgloss.Style {
	switch status {
	case StatusRunning:
		return lipgloss.NewStyle().Foreground(styles.ColorBlue)
	case StatusCompleted:
		return lipgloss.NewStyle().Foreground(styles.ColorGreen)
	case StatusFailed:
		return lipgloss.NewStyle().Foreground(styles.ColorRed)
	default:
		return lipgloss.NewStyle().Foreground(styles.ColorNeutral)
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
func (m *TaskRunner) runInteractive() error {
	// Create and store the program instance
	m.program = tea.NewProgram(m)

	// Start task execution in background
	go m.executeTasks()

	// Run the TUI
	if _, err := m.program.Run(); err != nil {
		return fmt.Errorf("error running tasks: %w", err)
	}

	// Wait for all tasks to complete
	<-m.done

	return m.checkErrors()
}

// runNonInteractive runs tasks with basic logging for non-interactive shells
func (m *TaskRunner) runNonInteractive() error {
	for _, task := range m.tasks {
		log.Info().Msgf("%s...", task.title)

		task.mu.Lock()
		task.status = StatusRunning
		task.startTime = time.Now()
		task.mu.Unlock()

		if err := task.runFunc(); err != nil {
			task.mu.Lock()
			task.elapsed = time.Since(task.startTime)
			task.status = StatusFailed
			task.error = err
			task.mu.Unlock()

			// log.Error().Msgf(styleError.Render("ERROR: %v"), err)
			return err
		}

		task.mu.Lock()
		task.status = StatusCompleted
		elapsed := time.Since(task.startTime)
		task.elapsed = elapsed
		task.mu.Unlock()

		log.Info().Msgf(" %s %s %s", styles.RenderSuccess("✓"), "Done", humanizeElapsed(elapsed))
	}

	log.Info().Msg("")

	close(m.done)
	return nil
}

// checkErrors checks if any tasks failed and returns the first error
func (m *TaskRunner) checkErrors() error {
	var errors []error
	for _, task := range m.tasks {
		task.mu.Lock()
		if task.error != nil {
			errors = append(errors, task.error)
		}
		task.mu.Unlock()
	}

	if len(errors) > 0 {
		return errors[0]
	}

	return nil
}

// executeTasks runs all tasks sequentially in interactive mode
func (m *TaskRunner) executeTasks() {
	var firstError error
	for _, task := range m.tasks {
		// Update task status to running and start timing
		task.mu.Lock()
		task.status = StatusRunning
		task.startTime = time.Now()
		task.mu.Unlock()

		// Execute the task
		log.Debug().Msgf("Task start: %s", task.title)
		if err := task.runFunc(); err != nil {
			task.mu.Lock()
			task.elapsed = time.Since(task.startTime)
			task.status = StatusFailed
			task.error = err
			task.mu.Unlock()
			if firstError == nil {
				firstError = err
			}
			break
		} else {
			task.mu.Lock()
			task.status = StatusCompleted
			elapsed := time.Since(task.startTime)
			task.elapsed = elapsed
			task.mu.Unlock()
			log.Debug().Msgf("Task completed: %s %s", task.title, humanizeElapsed(elapsed))
		}
	}

	// Signal completion and quit the program if in interactive mode
	close(m.done)
	if m.program != nil {
		m.program.Send(doneMsg{err: firstError})
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
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			m.quitting = true
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
func (m TaskRunner) View() string {
	// Build the content starting with the title
	var lines []string

	// Build the task list content
	for _, task := range m.tasks {
		task.mu.Lock()
		status := task.status
		err := task.error
		title := task.title
		elapsed := task.elapsed
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
	}

	lines = append(lines, "")

	var sb strings.Builder
	for _, line := range lines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	return sb.String()
}
