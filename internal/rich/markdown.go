package rich

import (
	"regexp"
	"strings"
)

// Markdown is rich.markdown.Markdown for the closed set of constructs the
// CLI renders (generated glossary entity pages): ATX headings, paragraphs
// with **strong**, *em* / _em_ and `code` spans, "- " bullet lists, pipe
// tables and "---" rules. Elements are styled and separated exactly like
// rich's renderer (markdown.* theme styles, blank line between elements).
type Markdown struct{ Source string }

type mdInline struct {
	text  string
	style string // "" | "strong" | "em" | "code"
}

var reMDInline = regexp.MustCompile("\\*\\*([^*]+)\\*\\*|`([^`]+)`|\\*([^*]+)\\*|(?:^|\\b)_([^_]+)_(?:\\b|$)")

// mdInlines splits s into text runs and inline-styled runs.
func mdInlines(s string) []mdInline {
	var out []mdInline
	pos := 0
	for _, m := range reMDInline.FindAllStringSubmatchIndex(s, -1) {
		if m[0] > pos {
			out = append(out, mdInline{s[pos:m[0]], ""})
		}
		switch {
		case m[2] >= 0:
			out = append(out, mdInline{s[m[2]:m[3]], "strong"})
		case m[4] >= 0:
			out = append(out, mdInline{s[m[4]:m[5]], "code"})
		case m[6] >= 0:
			out = append(out, mdInline{s[m[6]:m[7]], "em"})
		default:
			out = append(out, mdInline{s[m[8]:m[9]], "em"})
		}
		pos = m[1]
	}
	if pos < len(s) {
		out = append(out, mdInline{s[pos:], ""})
	}
	return out
}

// mdText builds a TextElement's Text: runs appended in the style of the
// context stack (base, element style, inline style).
func mdText(c *Console, base *Style, s string, justify string) *Text {
	t := &Text{End: "\n", Justify: justify}
	for _, r := range mdInlines(s) {
		st := base
		if r.style != "" {
			st = st.Add(c.GetStyle("markdown." + r.style))
		}
		t.Append(r.text, st)
	}
	return t
}

type mdBlock struct {
	kind  string // h1.., p, list, table, hr
	text  string
	items []string
	rows  [][]string
}

func mdBlocks(md string) []mdBlock {
	var out []mdBlock
	lines := strings.Split(md, "\n")
	for i := 0; i < len(lines); {
		l := lines[i]
		switch {
		case strings.TrimSpace(l) == "":
			i++
		case strings.HasPrefix(l, "#"):
			n := len(l) - len(strings.TrimLeft(l, "#"))
			out = append(out, mdBlock{kind: "h" + string(rune('0'+n)), text: strings.TrimSpace(l[n:])})
			i++
		case strings.TrimSpace(l) == "---":
			out = append(out, mdBlock{kind: "hr"})
			i++
		case strings.HasPrefix(l, "- "):
			b := mdBlock{kind: "list"}
			for i < len(lines) && strings.HasPrefix(lines[i], "- ") {
				b.items = append(b.items, strings.TrimPrefix(lines[i], "- "))
				i++
			}
			out = append(out, b)
		case strings.HasPrefix(l, "|"):
			b := mdBlock{kind: "table"}
			for i < len(lines) && strings.HasPrefix(lines[i], "|") {
				cells := strings.Split(strings.Trim(lines[i], "|"), "|")
				for k := range cells {
					cells[k] = strings.TrimSpace(cells[k])
				}
				if len(b.rows) != 1 || !strings.HasPrefix(cells[0], "-") {
					b.rows = append(b.rows, cells)
				}
				i++
			}
			out = append(out, b)
		default:
			var para []string
			for i < len(lines) && strings.TrimSpace(lines[i]) != "" && !strings.HasPrefix(lines[i], "#") &&
				!strings.HasPrefix(lines[i], "- ") && !strings.HasPrefix(lines[i], "|") && strings.TrimSpace(lines[i]) != "---" {
				para = append(para, strings.TrimSpace(lines[i]))
				i++
			}
			out = append(out, mdBlock{kind: "p", text: strings.Join(para, " ")})
		}
	}
	return out
}

// RichRender is Markdown.__rich_console__.
func (m *Markdown) RichRender(c *Console, o Options) []Seg {
	o.Height = nil
	base := c.GetStyle("none")
	var out []Seg
	newLine := false
	emit := func(r any, nl bool) {
		if newLine {
			out = append(out, newLineSegment)
		}
		out = append(out, c.Render(r, o)...)
		newLine = nl
	}
	for _, b := range mdBlocks(m.Source) {
		switch b.kind {
		case "p":
			emit(mdText(c, base.Add(c.GetStyle("markdown.paragraph")), b.text, "left"), true)
		case "hr":
			emit(Group{&mdRule{}, &Text{End: "\n"}}, false)
		case "list":
			emit(&mdList{items: b.items}, true)
		case "table":
			t := NewTable()
			t.Box, t.PadEdge, t.Style, t.ShowEdge, t.CollapsePadding = BoxSimple, false, "markdown.table.border", true, true
			for _, h := range b.rows[0] {
				ht := mdText(c, base, h, "default")
				ht.Stylize("markdown.table.header", 0, nil)
				t.AddColumn(ht, Column{})
			}
			for _, r := range b.rows[1:] {
				var cells []any
				for _, x := range r {
					cells = append(cells, mdText(c, base, x, "default"))
				}
				t.AddRow(cells...)
			}
			emit(t, true)
		default: // headings
			justify := "left"
			if b.kind == "h1" {
				justify = "center"
			}
			emit(mdText(c, base.Add(c.GetStyle("markdown."+b.kind)), b.text, justify), true)
		}
	}
	return out
}

// mdRule is rich.rule.Rule(style="markdown.hr", characters="-").
type mdRule struct{}

func (*mdRule) RichRender(c *Console, o Options) []Seg {
	t := NewStyledText(strings.Repeat("-", o.MaxWidth+1), c.GetStyle("markdown.hr"))
	t.truncate(o.MaxWidth, "", false)
	t.setPlain(SetCellSize(t.Plain(), o.MaxWidth))
	return c.Render(t, o)
}

// mdList is a bullet ListElement of single-paragraph items.
type mdList struct{ items []string }

func (l *mdList) RichRender(c *Console, o Options) []Seg {
	base := c.GetStyle("none")
	itemStyle := base.Add(c.GetStyle("markdown.item"))
	bullet := c.GetStyle("markdown.item.bullet")
	var out []Seg
	for _, it := range l.items {
		p := mdText(c, itemStyle.Add(c.GetStyle("markdown.paragraph")), it, "left")
		lines := c.RenderLines(Group{p}, o.withWidth(o.MaxWidth-3), itemStyle, true, false)
		for i, line := range lines {
			if i == 0 {
				out = append(out, Seg{" • ", bullet})
			} else {
				out = append(out, Seg{"   ", bullet})
			}
			out = append(out, line...)
			out = append(out, newLineSegment)
		}
	}
	return out
}
