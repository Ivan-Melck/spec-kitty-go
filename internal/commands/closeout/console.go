package closeout

import (
	"io"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// tracker is cli.StepTracker (rendered as a rich Tree).
type tracker struct {
	title string
	steps []*trackerStep
}

type trackerStep struct{ key, label, status, detail string }

func newTracker(title string) *tracker { return &tracker{title: title} }

func (t *tracker) add(key, label string) {
	for _, s := range t.steps {
		if s.key == key {
			return
		}
	}
	t.steps = append(t.steps, &trackerStep{key: key, label: label, status: "pending"})
}

func (t *tracker) update(key, status, detail string) {
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

func (t *tracker) start(key string)               { t.update(key, "running", "") }
func (t *tracker) complete(key, detail string)    { t.update(key, "done", detail) }
func (t *tracker) fail(key, detail string)        { t.update(key, "error", detail) }
func (t *tracker) startDetail(key, detail string) { t.update(key, "running", detail) }
func (t *tracker) skip(key, detail string)        { t.update(key, "skipped", detail) }
func (t *tracker) render(w io.Writer)             { rich.PrintTo(w, t.tree()) }

// tree is StepTracker.render: a rich Tree with the status symbols.
func (t *tracker) tree() *rich.Tree {
	tr := &rich.Tree{Label: "[cyan]" + t.title + "[/cyan]", GuideStyle: "grey50"}
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

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// simpleTable prints rich Table(title=title, header_style=headerStyle);
// right marks right-justified columns. Cells are console markup.
func simpleTable(w io.Writer, title string, headers []string, rows [][]string, right map[int]bool, headerStyle string) {
	t := rich.NewTable()
	if title != "" {
		t.Title = title
	}
	if headerStyle != "" {
		t.HeaderStyle = headerStyle
	}
	for i, h := range headers {
		c := rich.Column{}
		if right[i] {
			c.Justify = "right"
		}
		t.AddColumn(h, c)
	}
	for _, r := range rows {
		cells := make([]any, len(r))
		for i, x := range r {
			cells[i] = x
		}
		t.AddRow(cells...)
	}
	rich.PrintTo(w, t)
}

// fitPanel prints rich.Panel(content, title=title, expand=False);
// content is console markup.
func fitPanel(w io.Writer, content, title string) { rich.FitPanel(w, content, title) }
