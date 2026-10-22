package rich

// wrapLine is rich's divide_line (fold=True) followed by rstrip_end(width):
// a line keeps its trailing whitespace unless that exceeds the width.
func wrapLine(line string, width int) []string {
	if CellLen(line) <= width {
		return []string{line}
	}
	return splitAt(line, divideLine(line, width, true), width)
}

func splitAt(line string, offsets []int, width int) []string {
	var lines []string
	prev := 0
	for _, off := range append(offsets, len(line)) {
		lines = append(lines, rstripEnd(line[prev:off], width))
		prev = off
	}
	return lines
}

// WrapEllipsis wraps a line like a rich table cell (Column overflow
// "ellipsis"): divide_line without folding, so a word wider than width
// keeps its own line and is truncated to width-1 cells plus "…".
func WrapEllipsis(line string, width int) []string {
	if width <= 0 || CellLen(line) <= width {
		return []string{line}
	}
	lines := splitAt(line, divideLine(line, width, false), width)
	for i, l := range lines {
		lines[i] = truncate(l, width, "ellipsis", false)
	}
	return lines
}
