package rich

import (
	"io"
	"os"
	"strings"
)

// SpinnerProgress is rich.progress.Progress(SpinnerColumn(),
// TextColumn("[progress.description]{task.description}"), transient=True)
// driving one task, as rich renders it on an interactive (non-dumb)
// terminal: the Live display is drawn on start (empty), on add_task and on
// stop, then erased. Without a terminal nothing is written. Auto-refresh
// ticks (10 per second) are not emulated: the commands that use it finish
// their work well inside the first tick.
type SpinnerProgress struct {
	w      io.Writer
	c      *Console
	on     bool
	shaped bool // the live render has a shape (rendered at least once)
	height int
	task   bool
	desc   string
	total  int
	done   int
}

// NewSpinnerProgress starts the display on w (Progress.__enter__).
func NewSpinnerProgress(w io.Writer) *SpinnerProgress {
	c := ForWriter(w)
	term := strings.ToLower(os.Getenv("TERM"))
	p := &SpinnerProgress{w: w, c: c, on: c.Terminal && term != "dumb" && term != "unknown"}
	if p.on {
		io.WriteString(w, "\x1b[?25l")
		p.refresh() // live.start(refresh=True): no tasks, empty render
	}
	return p
}

// AddTask is progress.add_task(description, total=total) (refreshes).
func (p *SpinnerProgress) AddTask(desc string, total int) {
	p.task, p.desc, p.total = true, desc, total
	p.refresh()
}

// Update is progress.update(task, description=desc) (no refresh).
func (p *SpinnerProgress) Update(desc string) { p.desc = desc }

// Advance is progress.advance(task) (no refresh).
func (p *SpinnerProgress) Advance() { p.done++ }

// Stop is Progress.__exit__: final refresh, newline, show the cursor and
// erase the display (transient); a non-interactive console gets
// Progress.stop's bare console.print() instead.
func (p *SpinnerProgress) Stop() {
	if !p.on {
		io.WriteString(p.w, "\n")
		return
	}
	p.refresh()
	if p.height > 0 {
		io.WriteString(p.w, "\n")
	}
	io.WriteString(p.w, "\x1b[?25h")
	io.WriteString(p.w, "\r"+strings.Repeat("\x1b[1A\x1b[2K", p.height))
}

func (p *SpinnerProgress) refresh() {
	if !p.on {
		return
	}
	var b strings.Builder
	if p.shaped {
		b.WriteString("\r\x1b[2K")
		for i := 1; i < p.height; i++ {
			b.WriteString("\x1b[1A\x1b[2K")
		}
	}
	p.shaped = true
	p.height = 0
	if p.task {
		spin := NewText("")
		if p.total > 0 && p.done >= p.total {
			spin.Append(" ", nil) // SpinnerColumn finished_text
		} else {
			spin.Append("⠋", "progress.spinner") // frame 0: well inside the first 80 ms
		}
		desc := FromMarkupStyle(p.desc, "progress.description")
		grid := NewGrid(0, 1)
		grid.AddColumn("", Column{})
		grid.AddColumn("", Column{})
		grid.AddRow(spin, desc)
		lines := p.c.RenderLines(grid, p.c.Options(), nil, false, false)
		p.height = len(lines)
		var out strings.Builder
		for i, l := range lines {
			if i > 0 {
				out.WriteString("\n")
			}
			out.WriteString(p.c.Output(l))
		}
		b.WriteString(out.String())
	}
	io.WriteString(p.w, b.String())
}
