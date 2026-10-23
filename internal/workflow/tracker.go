package workflow

import "github.com/spec-kitty/go-kitty3/internal/rich"

// Tracker is specify_cli.cli.StepTracker (rendered as a rich Tree).
type Tracker struct{ *rich.StepTracker }

// NewTracker creates a tracker with a title.
func NewTracker(title string) *Tracker { return &Tracker{rich.NewStepTracker(title)} }

// Start marks a step running.
func (t *Tracker) Start(key string) { t.StepTracker.Start(key, "") }

// Render returns the tracker's Tree renderable.
func (t *Tracker) Render() *rich.Tree { return t.Tree() }
