package doctrine

import (
	"io"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// Thin wrappers over the renderables in internal/rich/table_doctrine.go
// (BoxTable, FitPanel, GridTable), kept under their historical names.

func rlen(s string) int { return utf8.RuneCountInString(s) }

// rtable is a rich.Table (HEAVY_HEAD box, padding (0,1)).
type rtable rich.BoxTable

func (t *rtable) render(w io.Writer, consoleWidth int) { (*rich.BoxTable)(t).Render(w, consoleWidth) }

// fitPanel prints rich.Panel(content, title=..., border_style=...,
// expand=False); content is console markup.
func fitPanel(w io.Writer, content, title, borderStyle string) {
	rich.FitPanelStyled(w, content, title, borderStyle)
}

// gridTable renders rich Table(box=None, padding=(0, 2), show_edge=False).
func gridTable(w io.Writer, headers []string, minWidths []int, right []bool, rows [][]string) {
	rich.GridTableStyled(w, headers, minWidths, right, []string{"cyan"}, rows)
}
