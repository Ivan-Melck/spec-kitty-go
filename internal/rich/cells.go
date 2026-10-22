package rich

import "strings"

// CharWidth is rich.cells.get_character_cell_size: 0 for control and
// combining characters, 2 for wide (East Asian / emoji), else 1.
func CharWidth(r rune) int {
	if (r != 0 && r < 32) || (r >= 0x7F && r < 0xA0) {
		return 0
	}
	if r < 0x300 {
		return 1
	}
	lo, hi := 0, len(cellWidths)-1
	if r > cellWidths[hi][1] {
		return 1
	}
	for lo <= hi {
		mid := (lo + hi) >> 1
		e := cellWidths[mid]
		switch {
		case r < e[0]:
			hi = mid - 1
		case r > e[1]:
			lo = mid + 1
		default:
			return int(e[2])
		}
	}
	return 1
}

// CellLen is rich.cells.cell_len: the number of terminal cells s occupies
// (a ZWJ is zero width; a VS16 widens the preceding narrow emoji-capable
// character to 2 cells).
func CellLen(s string) int {
	n := 0
	ascii := true
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7E {
			ascii = false
			break
		}
	}
	if ascii {
		return len(s)
	}
	var last rune = -1
	for _, r := range s {
		switch r {
		case 0x200D:
			continue
		case 0xFE0F:
			if last >= 0 && narrowToWide[last] {
				n++
			}
			last = -1
			continue
		}
		if w := CharWidth(r); w > 0 {
			last = r
			n += w
		}
	}
	return n
}

// SetCellSize is rich.cells.set_cell_size: crop or pad s to exactly width
// cells (a wide character split by the crop becomes a space).
func SetCellSize(s string, width int) string {
	n := CellLen(s)
	if n == width {
		return s
	}
	if n < width {
		return s + strings.Repeat(" ", width-n)
	}
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		w := CharWidth(r)
		if used+w > width {
			break
		}
		b.WriteRune(r)
		used += w
	}
	if used < width {
		b.WriteString(strings.Repeat(" ", width-used))
	}
	return b.String()
}

// chopCells splits s into chunks of at most width cells (rich chop_cells).
func chopCells(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	var b strings.Builder
	used := 0
	for _, r := range s {
		w := CharWidth(r)
		if used+w > width && used > 0 {
			out = append(out, b.String())
			b.Reset()
			used = 0
		}
		b.WriteRune(r)
		used += w
	}
	out = append(out, b.String())
	return out
}
