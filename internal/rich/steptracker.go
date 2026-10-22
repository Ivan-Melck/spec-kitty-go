package rich

import "strings"

// StepTracker is specify_cli.cli.step_tracker.StepTracker: hierarchical
// steps rendered as a rich Tree.
type StepTracker struct {
	Title string
	steps []*trackerEntry
}

type trackerEntry struct{ key, label, status, detail string }

// NewStepTracker returns a tracker with a title.
func NewStepTracker(title string) *StepTracker { return &StepTracker{Title: title} }

// Add registers a pending step (once per key).
func (t *StepTracker) Add(key, label string) {
	for _, s := range t.steps {
		if s.key == key {
			return
		}
	}
	t.steps = append(t.steps, &trackerEntry{key: key, label: label, status: "pending"})
}

func (t *StepTracker) update(key, status, detail string) {
	for _, s := range t.steps {
		if s.key == key {
			s.status = status
			if detail != "" {
				s.detail = detail
			}
			return
		}
	}
	t.steps = append(t.steps, &trackerEntry{key: key, label: key, status: status, detail: detail})
}

// Start marks a step running.
func (t *StepTracker) Start(key, detail string) { t.update(key, "running", detail) }

// Complete marks a step done.
func (t *StepTracker) Complete(key, detail string) { t.update(key, "done", detail) }

// Error marks a step failed.
func (t *StepTracker) Error(key, detail string) { t.update(key, "error", detail) }

// Skip marks a step skipped.
func (t *StepTracker) Skip(key, detail string) { t.update(key, "skipped", detail) }

// Tree is StepTracker.render().
func (t *StepTracker) Tree() *Tree {
	tr := &Tree{Label: "[cyan]" + t.Title + "[/cyan]", GuideStyle: "grey50"}
	for _, s := range t.steps {
		detail := strings.TrimSpace(s.detail)
		var sym string
		switch s.status {
		case "done":
			sym = "[green]●[/green]"
		case "pending":
			sym = "[green dim]○[/green dim]"
		case "running":
			sym = "[cyan]○[/cyan]"
		case "error":
			sym = "[red]●[/red]"
		case "skipped":
			sym = "[yellow]○[/yellow]"
		default:
			sym = " "
		}
		var line string
		switch {
		case s.status == "pending" && detail != "":
			line = sym + " [bright_black]" + s.label + " (" + detail + ")[/bright_black]"
		case s.status == "pending":
			line = sym + " [bright_black]" + s.label + "[/bright_black]"
		case detail != "":
			line = sym + " [white]" + s.label + "[/white] [bright_black](" + detail + ")[/bright_black]"
		default:
			line = sym + " [white]" + s.label + "[/white]"
		}
		tr.Add(line)
	}
	return tr
}
