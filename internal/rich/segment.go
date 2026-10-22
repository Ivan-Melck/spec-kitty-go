package rich

import "strings"

// Port of rich.segment: a Segment is a piece of text with one style. The
// layout engine renders every renderable to segments so that output on a
// colour terminal carries exactly the SGR sequences rich emits (one per
// segment, no merging), while plain output is the concatenated text.

// Segment is rich.segment.Segment (control segments are not used).
type Segment struct {
	Text  string
	Style *Style // nil = no style
}

// truthy is Python's `if style:` for an optional Style.
func (s *Style) truthy() bool { return s != nil && !s.null }

func (s Segment) cellLen() int { return CellLen(s.Text) }

var newLineSegment = Segment{Text: "\n"}

// lineLen is Segment.get_line_length.
func lineLen(line []Segment) int {
	n := 0
	for _, s := range line {
		n += s.cellLen()
	}
	return n
}

// applyStyle is Segment.apply_style: style + segment.style + post.
func applyStyle(segs []Segment, style, post *Style) []Segment {
	if !style.truthy() && !post.truthy() {
		return segs
	}
	out := make([]Segment, len(segs))
	for i, s := range segs {
		st := s.Style
		if style.truthy() {
			st = style.Add(st)
		}
		if post.truthy() {
			if st.truthy() {
				st = st.Add(post)
			} else {
				st = post
			}
		}
		out[i] = Segment{s.Text, st}
	}
	return out
}

// splitLines is Segment.split_lines (a trailing empty line is dropped).
func splitLines(segs []Segment) [][]Segment {
	var lines [][]Segment
	var line []Segment
	for _, s := range segs {
		if strings.Contains(s.Text, "\n") {
			text := s.Text
			for text != "" {
				part, rest, nl := strings.Cut(text, "\n")
				if part != "" {
					line = append(line, Segment{part, s.Style})
				}
				if nl {
					lines = append(lines, line)
					line = nil
				}
				text = rest
			}
			continue
		}
		line = append(line, s)
	}
	if len(line) > 0 {
		lines = append(lines, line)
	}
	return lines
}

type termLine struct {
	segs    []Segment
	newLine bool
}

// splitLinesTerminator is Segment.split_lines_terminator.
func splitLinesTerminator(segs []Segment) []termLine {
	var out []termLine
	var line []Segment
	for _, s := range segs {
		if strings.Contains(s.Text, "\n") {
			text := s.Text
			for text != "" {
				part, rest, nl := strings.Cut(text, "\n")
				if part != "" {
					line = append(line, Segment{part, s.Style})
				}
				if nl {
					out = append(out, termLine{line, true})
					line = nil
				}
				text = rest
			}
			continue
		}
		line = append(line, s)
	}
	if len(line) > 0 {
		out = append(out, termLine{line, false})
	}
	return out
}

// splitAndCropLines is Segment.split_and_crop_lines.
func splitAndCropLines(segs []Segment, length int, style *Style, pad, includeNewLines bool) [][]Segment {
	var out [][]Segment
	var line []Segment
	for _, s := range segs {
		if strings.Contains(s.Text, "\n") {
			text := s.Text
			for text != "" {
				part, rest, nl := strings.Cut(text, "\n")
				if part != "" {
					line = append(line, Segment{part, s.Style})
				}
				if nl {
					cropped := adjustLineLength(line, length, style, pad)
					if includeNewLines {
						cropped = append(cropped, newLineSegment)
					}
					out = append(out, cropped)
					line = nil
				}
				text = rest
			}
			continue
		}
		line = append(line, s)
	}
	if len(line) > 0 {
		out = append(out, adjustLineLength(line, length, style, pad))
	}
	return out
}

// adjustLineLength is Segment.adjust_line_length.
func adjustLineLength(line []Segment, length int, style *Style, pad bool) []Segment {
	n := lineLen(line)
	switch {
	case n < length:
		out := append([]Segment(nil), line...)
		if pad {
			out = append(out, Segment{strings.Repeat(" ", length-n), style})
		}
		return out
	case n > length:
		var out []Segment
		n = 0
		for _, s := range line {
			sl := s.cellLen()
			if n+sl < length {
				out = append(out, s)
				n += sl
				continue
			}
			out = append(out, Segment{SetCellSize(s.Text, length-n), s.Style})
			break
		}
		return out
	}
	return append([]Segment(nil), line...)
}

// shape is Segment.get_shape.
func shape(lines [][]Segment) (int, int) {
	w := 0
	for _, l := range lines {
		w = max(w, lineLen(l))
	}
	return w, len(lines)
}

// setShape is Segment.set_shape (height <= 0: unchanged).
func setShape(lines [][]Segment, width, height int, style *Style) [][]Segment {
	if height <= 0 {
		height = len(lines)
	}
	out := make([][]Segment, 0, max(height, len(lines)))
	for _, l := range lines {
		out = append(out, adjustLineLength(l, width, style, true))
	}
	for len(out) < height {
		out = append(out, []Segment{{strings.Repeat(" ", width), style}})
	}
	return out
}

func blankLines(n, width int, style *Style) [][]Segment {
	out := make([][]Segment, n)
	for i := range out {
		out[i] = []Segment{{strings.Repeat(" ", width), style}}
	}
	return out
}

// alignTop / alignBottom / alignMiddle are Segment.align_*.
func alignTop(lines [][]Segment, width, height int, style *Style) [][]Segment {
	extra := height - len(lines)
	if extra == 0 {
		return append([][]Segment(nil), lines...)
	}
	lines = lines[:min(height, len(lines))]
	return append(append([][]Segment(nil), lines...), blankLines(max(0, extra), width, style)...)
}

func alignBottom(lines [][]Segment, width, height int, style *Style) [][]Segment {
	extra := height - len(lines)
	if extra == 0 {
		return append([][]Segment(nil), lines...)
	}
	lines = lines[:min(height, len(lines))]
	return append(blankLines(max(0, extra), width, style), lines...)
}

func alignMiddle(lines [][]Segment, width, height int, style *Style) [][]Segment {
	extra := height - len(lines)
	if extra == 0 {
		return append([][]Segment(nil), lines...)
	}
	lines = lines[:min(height, len(lines))]
	top := max(0, extra) / 2
	bottom := max(0, extra) - top
	out := blankLines(top, width, style)
	out = append(out, lines...)
	return append(out, blankLines(bottom, width, style)...)
}

// joinLines flattens lines, appending a newline segment after each.
func joinLines(lines [][]Segment) []Segment {
	var out []Segment
	for _, l := range lines {
		out = append(out, l...)
		out = append(out, newLineSegment)
	}
	return out
}

// plainText concatenates segment texts.
func plainText(segs []Segment) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.Text)
	}
	return b.String()
}
