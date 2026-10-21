package workflow

import (
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// Tracker is specify_cli.cli.StepTracker (rendered as a rich Tree).
type Tracker struct {
	Title string
	steps []*trackerStep
}

type trackerStep struct{ key, label, status, detail string }

// NewTracker creates a tracker with a title.
func NewTracker(title string) *Tracker { return &Tracker{Title: title} }

// Add registers a pending step.
func (t *Tracker) Add(key, label string) {
	for _, s := range t.steps {
		if s.key == key {
			return
		}
	}
	t.steps = append(t.steps, &trackerStep{key: key, label: label, status: "pending"})
}

func (t *Tracker) update(key, status, detail string) {
	for _, s := range t.steps {
		if s.key == key {
			s.status = status
			if detail != "" {
				s.detail = detail
			}
			return
		}
	}
	t.steps = append(t.steps, &trackerStep{key: key, label: key, status: status, detail: detail})
}

// Start marks a step running.
func (t *Tracker) Start(key string) { t.update(key, "running", "") }

// Complete marks a step done.
func (t *Tracker) Complete(key, detail string) { t.update(key, "done", detail) }

// Error marks a step failed.
func (t *Tracker) Error(key, detail string) { t.update(key, "error", detail) }

// Render returns the tree text (no colour).
func (t *Tracker) Render() string {
	lines := []string{t.Title}
	width := cli.Width()
	for i, s := range t.steps {
		symbol := " "
		switch s.status {
		case "done", "error":
			symbol = "●"
		case "pending", "running", "skipped":
			symbol = "○"
		}
		detail := strings.TrimSpace(s.detail)
		line := symbol + " " + s.label
		if detail != "" {
			line += " (" + detail + ")"
		}
		first, rest := "├── ", "│   "
		if i == len(t.steps)-1 {
			first, rest = "└── ", "    "
		}
		for j, l := range strings.Split(rich.Wrap(line, width-4), "\n") {
			if j == 0 {
				lines = append(lines, first+l)
			} else {
				lines = append(lines, rest+l)
			}
		}
	}
	return strings.Join(lines, "\n")
}
