package rich

import (
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Plain-text helpers of rich's layout engine (rich 15): wrapping
// (divide_line), measurements and the ratio / column-width algorithms. The
// styled renderables are in renderables.go.

// Measurement is rich.measure.Measurement: the minimum and maximum cell
// widths a renderable needs.
type Measurement struct{ Min, Max int }

func (m Measurement) normalize() Measurement {
	mn := min(max(0, m.Min), m.Max)
	return Measurement{max(0, mn), max(0, max(mn, m.Max))}
}

func (m Measurement) withMaximum(w int) Measurement {
	return Measurement{min(m.Min, w), min(m.Max, w)}
}

var reSplitLines = regexp.MustCompile(`\r\n|[\n\r\v\f\x1c\x1d\x1e\x{85}\x{2028}\x{2029}]`)

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// WrapText is rich Text.wrap: each line is word-wrapped (folding or
// cropping over-long words per overflow), justified and truncated.
func WrapText(text string, width int, justify, overflow string, noWrap bool) []string {
	if overflow == "ignore" {
		noWrap = true
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "\t") {
			line = expandTabs(line, 8)
		}
		var lines []string
		if noWrap {
			if overflow == "ignore" {
				out = append(out, line)
				continue
			}
			lines = []string{line}
		} else {
			offsets := divideLine(line, width, overflow == "fold")
			prev := 0
			for _, off := range append(offsets, len(line)) {
				lines = append(lines, rstripEnd(line[prev:off], width))
				prev = off
			}
		}
		for i, l := range lines {
			switch justify {
			case "left":
				l = truncate(l, width, overflow, true)
			case "center":
				l = truncate(strings.TrimRight(l, " \t\n\r\v\f"), width, overflow, false)
				n := CellLen(l)
				l = strings.Repeat(" ", max(0, (width-n)/2)) + l
				l += strings.Repeat(" ", max(0, width-CellLen(l)))
			case "right":
				l = truncate(strings.TrimRight(l, " \t\n\r\v\f"), width, overflow, false)
				l = strings.Repeat(" ", max(0, width-CellLen(l))) + l
			}
			lines[i] = truncate(l, width, overflow, false)
		}
		out = append(out, lines...)
	}
	return out
}

func expandTabs(s string, tab int) string {
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := tab - col%tab
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col += CharWidth(r)
	}
	return b.String()
}

// truncate is Text.truncate.
func truncate(s string, width int, overflow string, pad bool) string {
	if overflow == "ignore" {
		return s
	}
	n := CellLen(s)
	if n > width {
		if overflow == "ellipsis" {
			s = SetCellSize(s, width-1) + "…"
		} else {
			s = SetCellSize(s, width)
		}
	}
	if pad && n < width {
		s += strings.Repeat(" ", width-n)
	}
	return s
}

var reTrailingWS = regexp.MustCompile(`\s+$`)

// rstripEnd is Text.rstrip_end: drop trailing whitespace beyond size
// characters.
func rstripEnd(s string, size int) string {
	n := utf8.RuneCountInString(s)
	if n <= size {
		return s
	}
	excess := n - size
	if m := reTrailingWS.FindString(s); m != "" {
		cut := min(utf8.RuneCountInString(m), excess)
		r := []rune(s)
		return string(r[:len(r)-cut])
	}
	return s
}

var reWordSpan = regexp.MustCompile(`\s*\S+\s*`)

// divideLine is rich._wrap.divide_line: byte offsets to break text at so
// every piece fits width cells.
func divideLine(text string, width int, fold bool) []int {
	var breaks []int
	offset := 0
	pos := 0
	for pos <= len(text) {
		loc := reWordSpan.FindStringIndex(text[pos:])
		if loc == nil || loc[0] != 0 {
			break
		}
		start, end := pos, pos+loc[1]
		word := text[start:end]
		pos = end
		wordLen := CellLen(strings.TrimRightFunc(word, unicode.IsSpace))
		remaining := width - offset
		if remaining >= wordLen {
			offset += CellLen(word)
		} else if wordLen > width {
			if fold {
				chunks := chopCells(word, width)
				for i, chunk := range chunks {
					if start != 0 {
						breaks = append(breaks, start)
					}
					if i == len(chunks)-1 {
						offset = CellLen(chunk)
					} else {
						start += len(chunk)
					}
				}
			} else {
				if start != 0 {
					breaks = append(breaks, start)
				}
				offset = CellLen(word)
			}
		} else if offset != 0 && start != 0 {
			breaks = append(breaks, start)
			offset = CellLen(word)
		}
		if end == len(text) {
			break
		}
	}
	return breaks
}

// ---------------------------------------------------------------- ratios

// RatioReduce is rich._ratio.ratio_reduce.
func RatioReduce(total int, ratios, maximums, values []int) []int {
	rs := make([]int, len(ratios))
	totalRatio := 0
	for i, r := range ratios {
		if maximums[i] != 0 {
			rs[i] = r
		}
		totalRatio += rs[i]
	}
	out := append([]int(nil), values...)
	if totalRatio == 0 {
		return out
	}
	remaining := total
	for i, r := range rs {
		if r != 0 && totalRatio > 0 {
			d := min(maximums[i], pyRound(float64(r)*float64(remaining)/float64(totalRatio)))
			out[i] = values[i] - d
			remaining -= d
			totalRatio -= r
		}
	}
	return out
}

// RatioDistribute is rich._ratio.ratio_distribute (minimums may be nil).
func RatioDistribute(total int, ratios, minimums []int) []int {
	rs := append([]int(nil), ratios...)
	if minimums != nil {
		for i := range rs {
			if minimums[i] == 0 {
				rs[i] = 0
			}
		}
	}
	totalRatio := sum(rs)
	remaining := total
	out := make([]int, len(rs))
	for i, r := range rs {
		mn := 0
		if minimums != nil {
			mn = minimums[i]
		}
		var d int
		if totalRatio > 0 {
			d = max(mn, int(math.Ceil(float64(r)*float64(remaining)/float64(totalRatio))))
		} else {
			d = remaining
		}
		out[i] = d
		totalRatio -= r
		remaining -= d
	}
	return out
}

// CollapseWidths is rich.table.Table._collapse_widths.
func CollapseWidths(widths []int, wrapable []bool, maxWidth int) []int {
	widths = append([]int(nil), widths...)
	total := sum(widths)
	excess := total - maxWidth
	anyWrap := false
	for _, b := range wrapable {
		anyWrap = anyWrap || b
	}
	if !anyWrap {
		return widths
	}
	for total != 0 && excess > 0 {
		maxCol := 0
		for i, w := range widths {
			if wrapable[i] && w > maxCol {
				maxCol = w
			}
		}
		second := 0
		for i, w := range widths {
			if wrapable[i] && w != maxCol && w > second {
				second = w
			}
		}
		diff := maxCol - second
		ratios := make([]int, len(widths))
		anyRatio := false
		for i, w := range widths {
			if w == maxCol && wrapable[i] {
				ratios[i] = 1
				anyRatio = true
			}
		}
		if !anyRatio || diff == 0 {
			break
		}
		red := min(excess, diff)
		maxReduce := make([]int, len(widths))
		for i := range maxReduce {
			maxReduce[i] = red
		}
		widths = RatioReduce(excess, ratios, maxReduce, widths)
		total = sum(widths)
		excess = total - maxWidth
	}
	return widths
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
