package rich

import "strings"

// PanelSpec describes an expanded ROUNDED rich Panel whose content is a
// markup string (rendered without repr highlighting, as Panel does).
type PanelSpec struct {
	Content     string // markup
	Title       string // markup ("" = none)
	BorderStyle string // e.g. "cyan" ("" = none)
	Width       int    // total width including the borders
	PadY, PadX  int    // Panel padding (top/bottom, left/right)
	// Wrap splits one plain content line into lines of at most n cells;
	// nil keeps each line whole.
	Wrap func(line string, n int) []string
}

// StyledPanelLines renders spec under sys: border segments in the border
// style (the title text styled over it), content segments in their markup
// styles, padding unstyled. Under NoColor it is the plain panel.
func StyledPanelLines(spec PanelSpec, sys ColorSystem) []string {
	noColor := noColorEnv()
	render := func(text string, st *Style) string {
		if noColor && st != nil {
			st = st.withoutColor()
		}
		return st.Render(text, sys)
	}
	border := themeStyle(spec.BorderStyle)
	width := spec.Width
	inner := width - 2 - 2*spec.PadX
	var out []string

	// top: one segment, or ╭─ / dashes / title / dashes / ─╮ with a title
	if spec.Title == "" {
		out = append(out, render("╭"+strings.Repeat("─", width-2)+"╮", border))
	} else {
		tt, err := renderMarkup(spec.Title)
		if err != nil {
			tt = &richText{plain: []rune(Strip(spec.Title))}
		}
		// Text.pad(1) then stylize_before(border)
		padded := &richText{plain: append(append([]rune{' '}, tt.plain...), ' ')}
		padded.spans = append(padded.spans, span{0, len(padded.plain), spec.BorderStyle})
		for _, sp := range tt.spans {
			padded.spans = append(padded.spans, span{sp.start + 1, sp.end + 1, sp.style})
		}
		n := CellLen(string(padded.plain))
		excess := width - 4 - n
		left := max(excess/2, 0)
		right := max(excess-left, 0)
		var b strings.Builder
		b.WriteString(render("╭─", border))
		if left > 0 {
			b.WriteString(render(strings.Repeat("─", left), border))
		}
		for _, seg := range padded.segments(0, len(padded.plain), false) {
			b.WriteString(render(seg.text, seg.style))
		}
		if right > 0 {
			b.WriteString(render(strings.Repeat("─", right), border))
		}
		b.WriteString(render("─╮", border))
		out = append(out, b.String())
	}
	side := render("│", border)
	blank := side + strings.Repeat(" ", width-2) + side
	for i := 0; i < spec.PadY; i++ {
		out = append(out, blank)
	}

	pad := strings.Repeat(" ", spec.PadX)
	for _, ml := range StyledMarkupLines(spec.Content, inner, spec.Wrap, sys) {
		line := side + pad + ml.Styled
		if d := inner - CellLen(ml.Plain); d > 0 {
			line += strings.Repeat(" ", d)
		}
		out = append(out, line+pad+side)
	}
	for i := 0; i < spec.PadY; i++ {
		out = append(out, blank)
	}
	out = append(out, render("╰"+strings.Repeat("─", width-2)+"╯", border))
	return out
}

// MarkupLine is one wrapped line of rendered markup.
type MarkupLine struct {
	Plain  string // the text
	Styled string // the text with its SGR styling under the colour system
}

// StyledMarkupLines renders markup (no repr highlighting) split at
// newlines and wrapped by wrap at width cells (nil keeps lines whole), each
// line's segments styled under sys.
func StyledMarkupLines(markup string, width int, wrap func(string, int) []string, sys ColorSystem) []MarkupLine {
	noColor := noColorEnv()
	t, err := renderMarkup(markup)
	if err != nil {
		t = &richText{plain: []rune(Strip(markup))}
	}
	var out []MarkupLine
	lineStart := 0
	for _, src := range strings.Split(string(t.plain), "\n") {
		srcRunes := []rune(src)
		wrapped := []string{src}
		if wrap != nil {
			wrapped = wrap(src, width)
		}
		cursor := lineStart
		for _, wl := range wrapped {
			lr := []rune(wl)
			// locate the wrapped line (wrapping drops break spaces)
			for cursor < lineStart+len(srcRunes) && len(lr) > 0 && !hasPrefixAt(t.plain, cursor, lr) {
				cursor++
			}
			if !hasPrefixAt(t.plain, cursor, lr) {
				out = append(out, MarkupLine{wl, wl})
				continue
			}
			var b strings.Builder
			for _, seg := range t.segments(cursor, cursor+len(lr), false) {
				st := seg.style
				if noColor && st != nil {
					st = st.withoutColor()
				}
				b.WriteString(st.Render(seg.text, sys))
			}
			cursor += len(lr)
			out = append(out, MarkupLine{wl, b.String()})
		}
		lineStart += len(srcRunes) + 1
	}
	return out
}

// RenderStyled renders text in one rich style definition under sys (NO_COLOR
// drops the colours).
func RenderStyled(text, style string, sys ColorSystem) string {
	return RenderSegments([]Segment{{Text: text, Style: style}}, sys)
}
