package closeout

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
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
func (t *tracker) render(w io.Writer)             { fmt.Fprint(w, t.String()) }
func (t *tracker) lines() []string                { return strings.Split(strings.TrimRight(t.String(), "\n"), "\n") }
func (t *tracker) String() string                 { return t.renderText() }
func (t *tracker) renderText() string {
	var b strings.Builder
	b.WriteString(t.title + "\n")
	for i, s := range t.steps {
		guide := "├── "
		if i == len(t.steps)-1 {
			guide = "└── "
		}
		sym := " "
		switch s.status {
		case "done", "error":
			sym = "●"
		case "pending", "running", "skipped":
			sym = "○"
		}
		detail := strings.TrimSpace(s.detail)
		line := sym + " " + s.label
		if detail != "" {
			line += " (" + detail + ")"
		}
		b.WriteString(guide + line + "\n")
	}
	return b.String()
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// simpleTable renders a rich HEAVY_HEAD table whose content fits the
// console (no wrapping). right marks right-justified columns.
func simpleTable(w io.Writer, title string, headers []string, rows [][]string, right map[int]bool) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = runeLen(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if n := runeLen(c); n > widths[i] {
				widths[i] = n
			}
		}
	}
	cell := func(i int, s string) string {
		padN := widths[i] - runeLen(s)
		if right[i] {
			return " " + strings.Repeat(" ", padN) + s + " "
		}
		return " " + s + strings.Repeat(" ", padN) + " "
	}
	border := func(l, m, r, f string) string {
		var parts []string
		for _, wd := range widths {
			parts = append(parts, strings.Repeat(f, wd+2))
		}
		return l + strings.Join(parts, m) + r
	}
	total := len(widths) + 1
	for _, wd := range widths {
		total += wd + 2
	}
	if title != "" {
		ex := total - runeLen(title)
		l := ex / 2
		fmt.Fprintln(w, strings.Repeat(" ", l)+title+strings.Repeat(" ", ex-l))
	}
	fmt.Fprintln(w, border("┏", "┳", "┓", "━"))
	var hc []string
	for i, h := range headers {
		hc = append(hc, cell(i, h))
	}
	fmt.Fprintln(w, "┃"+strings.Join(hc, "┃")+"┃")
	fmt.Fprintln(w, border("┡", "╇", "┩", "━"))
	for _, r := range rows {
		var rc []string
		for i, c := range r {
			rc = append(rc, cell(i, c))
		}
		fmt.Fprintln(w, "│"+strings.Join(rc, "│")+"│")
	}
	fmt.Fprintln(w, border("└", "┴", "┘", "─"))
}
