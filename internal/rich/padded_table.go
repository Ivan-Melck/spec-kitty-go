package rich

import "strings"

// PaddedTable is rich Table(box=None, padding=(0, 2), show_edge=False) with
// a styled header row and per-column styles, laid out like rich: columns
// measured with their min_width, collapsed (widest first, then evenly)
// when the table is wider than the console, re-measured, cells wrapped
// with ellipsis overflow, and every line cropped to the console width.
type PaddedTable struct {
	Headers     []string
	MinWidths   []int      // column min_width (0 = none)
	Justify     []byte     // 'l' (default), 'r', 'c'
	Styles      []string   // column style ("" = none)
	HeaderStyle string     // e.g. "bold" (table.header)
	Rows        [][]string // cell markup
}

func (t *PaddedTable) at(xs []int, i int) int {
	if i < len(xs) {
		return xs[i]
	}
	return 0
}

// Lines renders the table at console width under sys.
func (t *PaddedTable) Lines(width int, sys ColorSystem) []string {
	const pad = 4
	n := len(t.Headers)
	cells := func(i int) []string {
		out := []string{t.Headers[i]}
		for _, r := range t.Rows {
			c := ""
			if i < len(r) {
				c = r[i]
			}
			out = append(out, c)
		}
		return out
	}
	maxLen := make([]int, n)
	widths := make([]int, n)
	for i := 0; i < n; i++ {
		for _, c := range cells(i) {
			for _, l := range strings.Split(Strip(c), "\n") {
				maxLen[i] = max(maxLen[i], CellLen(l))
			}
		}
		w := min(maxLen[i]+pad, width)
		if mw := t.at(t.MinWidths, i); mw > 0 {
			w = max(w, mw+pad)
		}
		widths[i] = max(w, 1)
	}
	sum := func() int {
		s := 0
		for _, w := range widths {
			s += w
		}
		return s
	}
	if sum() > width {
		wrapable := make([]bool, n)
		for i := range wrapable {
			wrapable[i] = true
		}
		widths = boxCollapseWidths(widths, wrapable, width)
		if excess := sum() - width; excess > 0 {
			ones := make([]int, n)
			for i := range ones {
				ones[i] = 1
			}
			widths = boxRatioReduce(excess, ones, append([]int(nil), widths...), widths)
		}
		// re-measure each column at its collapsed width
		for i, w := range widths {
			m := w
			if w-pad >= 1 {
				m = min(maxLen[i]+pad, w)
			}
			if mw := t.at(t.MinWidths, i); mw > 0 {
				m = max(m, mw+pad)
			}
			widths[i] = m
		}
	}

	var out []string
	emit := func(row []string, header bool) {
		col := make([][][]segment, n)
		height := 1
		for i := 0; i < n; i++ {
			c := ""
			if i < len(row) {
				c = row[i]
			}
			j := byte('l')
			if i < len(t.Justify) {
				j = t.Justify[i]
			}
			col[i] = markupCellLines(c, max(widths[i]-pad, 1), max(widths[i]-pad, 0), j)
			height = max(height, len(col[i]))
		}
		for l := 0; l < height; l++ {
			var segs []segment
			for i := 0; i < n; i++ {
				name := ""
				if header {
					name = t.HeaderStyle
				} else if i < len(t.Styles) {
					name = t.Styles[i]
				}
				base := themeStyle(name)
				cw := max(widths[i]-pad, 0)
				segs = append(segs, segment{"  ", base})
				if l < len(col[i]) {
					for _, sg := range col[i][l] {
						segs = append(segs, segment{sg.text, base.Add(sg.style)})
					}
				} else {
					segs = append(segs, segment{strings.Repeat(" ", cw), base})
				}
				segs = append(segs, segment{"  ", base})
			}
			// Segment.split_and_crop_lines: crop at the console width
			noColor := noColorEnv()
			var b strings.Builder
			room := width
			for _, sg := range segs {
				if room <= 0 {
					break
				}
				txt := sg.text
				if CellLen(txt) > room {
					txt = SetCellSize(txt, room)
				}
				room -= CellLen(txt)
				st := sg.style
				if noColor && st != nil {
					st = st.withoutColor()
				}
				b.WriteString(st.Render(txt, sys))
			}
			out = append(out, b.String())
		}
	}
	emit(t.Headers, true)
	for _, r := range t.Rows {
		emit(r, false)
	}
	return out
}

// markupCellLines renders a markup cell wrapped to wrapWidth with ellipsis
// overflow, each line justified to cellWidth, as styled segments (the
// justification padding unstyled, under the cell style).
func markupCellLines(markup string, wrapWidth, cellWidth int, justify byte) [][]segment {
	t, err := renderMarkup(markup)
	if err != nil {
		t = &richText{plain: []rune(Strip(markup))}
	}
	var out [][]segment
	cursor := 0
	for _, wl := range BoxCellLines(string(t.plain), wrapWidth) {
		lr := []rune(wl)
		body := lr
		ellipsis := false
		if !hasPrefixAt(t.plain, cursor, lr) && len(lr) > 0 && lr[len(lr)-1] == '…' {
			body, ellipsis = lr[:len(lr)-1], true
		}
		for cursor < len(t.plain) && len(body) > 0 && !hasPrefixAt(t.plain, cursor, body) {
			cursor++
		}
		aligned := []rune(AlignText(wl, cellWidth, justify))
		left := 0 // justification padding before the text
		if k := strings.Index(string(aligned), wl); k > 0 && wl != "" {
			left = len([]rune(string(aligned)[:k]))
		}
		line := &richText{plain: aligned}
		if hasPrefixAt(t.plain, cursor, body) {
			for _, sp := range t.spans {
				a, z := max(sp.start, cursor), min(sp.end, cursor+len(body))
				if ellipsis && sp.end >= cursor+len(body) && sp.start < cursor+len(body) {
					z++ // the ellipsis keeps the style it replaces
				}
				if a < z {
					line.spans = append(line.spans, span{a - cursor + left, z - cursor + left, sp.style})
				}
			}
			cursor += len(body)
		}
		out = append(out, line.segments(0, len(aligned), false))
	}
	return out
}
