package doctrine

import (
	"io"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// renderEntityMarkdown renders a glossary entity page the way
// console.print(rich.markdown.Markdown(text)) does on the glossary command's
// non-terminal CliConsole(width=120): the closed set of constructs the page
// generator emits (headings, paragraphs, bullets, a simple table, a rule).
func renderEntityMarkdown(w io.Writer, md string) {
	const width = 120
	inline := func(s string) string {
		s = reBoldMD.ReplaceAllString(s, "$1")
		s = reCodeMD.ReplaceAllString(s, "$1")
		s = reItalicMD.ReplaceAllString(s, "$1")
		s = reUnderMD.ReplaceAllString(s, "$1")
		return s
	}
	pad := func(line string) string {
		if n := rich.CellLen(line); n < width {
			return line + strings.Repeat(" ", width-n)
		}
		return line
	}
	var blocks [][]string
	lines := strings.Split(md, "\n")
	for i := 0; i < len(lines); {
		l := lines[i]
		switch {
		case strings.TrimSpace(l) == "":
			i++
		case strings.HasPrefix(l, "# "):
			t := inline(strings.TrimPrefix(l, "# "))
			left := (width - rich.CellLen(t)) / 2
			blocks = append(blocks, []string{pad(strings.Repeat(" ", left) + t)})
			i++
		case strings.HasPrefix(l, "## ") || strings.HasPrefix(l, "### "):
			t := inline(strings.TrimLeft(l, "# "))
			var b []string
			for _, x := range rich.WrapWords(t, width) {
				b = append(b, pad(x))
			}
			blocks = append(blocks, b)
			i++
		case strings.TrimSpace(l) == "---":
			blocks = append(blocks, []string{strings.Repeat("-", width)})
			i++
		case strings.HasPrefix(l, "- "):
			var b []string
			for i < len(lines) && strings.HasPrefix(lines[i], "- ") {
				for j, x := range rich.WrapWords(inline(strings.TrimPrefix(lines[i], "- ")), width-3) {
					prefix := "   "
					if j == 0 {
						prefix = " • "
					}
					b = append(b, pad(prefix+x))
				}
				i++
			}
			blocks = append(blocks, b)
		case strings.HasPrefix(l, "|"):
			var rows [][]string
			for i < len(lines) && strings.HasPrefix(lines[i], "|") {
				cells := strings.Split(strings.Trim(lines[i], "|"), "|")
				for k := range cells {
					cells[k] = strings.TrimSpace(cells[k])
				}
				if len(rows) != 1 || !strings.HasPrefix(cells[0], "-") {
					rows = append(rows, cells)
				}
				i++
			}
			widths := make([]int, len(rows[0]))
			for _, r := range rows {
				for k, c := range r {
					if k < len(widths) && rich.CellLen(c) > widths[k] {
						widths[k] = rich.CellLen(c)
					}
				}
			}
			total := 0
			for _, wd := range widths {
				total += wd + 2
			}
			row := func(r []string) string {
				var sb strings.Builder
				for k, wd := range widths {
					c := ""
					if k < len(r) {
						c = r[k]
					}
					sb.WriteString(" " + c + strings.Repeat(" ", wd-rich.CellLen(c)) + " ")
				}
				return sb.String()
			}
			b := []string{strings.Repeat(" ", total), row(rows[0]), " " + strings.Repeat("─", total-2) + " "}
			for _, r := range rows[1:] {
				b = append(b, row(r))
			}
			blocks = append(blocks, append(b, strings.Repeat(" ", total)))
		default:
			var para []string
			for i < len(lines) && strings.TrimSpace(lines[i]) != "" && !strings.HasPrefix(lines[i], "#") && !strings.HasPrefix(lines[i], "- ") && !strings.HasPrefix(lines[i], "|") && strings.TrimSpace(lines[i]) != "---" {
				para = append(para, strings.TrimSpace(lines[i]))
				i++
			}
			var b []string
			for _, x := range rich.WrapWords(inline(strings.Join(para, " ")), width) {
				b = append(b, pad(x))
			}
			blocks = append(blocks, b)
		}
	}
	for k, b := range blocks {
		if k > 0 {
			io.WriteString(w, "\n")
		}
		for _, l := range b {
			io.WriteString(w, l+"\n")
		}
	}
}

var (
	reBoldMD   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reCodeMD   = regexp.MustCompile("`([^`]+)`")
	reItalicMD = regexp.MustCompile(`\*([^*]+)\*`)
	reUnderMD  = regexp.MustCompile(`(?:^|\b)_([^_]+)_(?:\b|$)`)
)
