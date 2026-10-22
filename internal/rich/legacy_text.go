package rich

import (
	"io"
	"sort"
)

// Span/segment helpers kept for the hand-laid-out colour renderers
// (PaddedTable, StyledPanelLines, StyledMarkupLines): a markup text with
// its spans and Text.render over a code point range.

type span struct {
	start, end int
	style      string
}

type richText struct {
	plain []rune
	spans []span
}

type segment struct {
	text  string
	style *Style
}

// renderMarkupRT is rich.markup.render as a richText (string styles).
func renderMarkupRT(s string) (*richText, error) {
	t, err := renderMarkup(s, "")
	if err != nil {
		return nil, err
	}
	rt := &richText{plain: t.plain}
	for _, sp := range t.Spans {
		name, _ := sp.Style.(string)
		rt.spans = append(rt.spans, span{sp.Start, sp.End, name})
	}
	return rt, nil
}

// segments is Text.render for the rune range [from, to).
func (t *richText) segments(from, to int, keepEmpty bool) []segment {
	type ev struct {
		off   int
		leave bool
		id    int
	}
	var evs []ev
	evs = append(evs, ev{from, false, 0})
	for i, sp := range t.spans {
		a, b := max(sp.start, from), min(sp.end, to)
		if a > b || (a == b && !keepEmpty) {
			continue
		}
		evs = append(evs, ev{a, false, i + 1}, ev{b, true, i + 1})
	}
	evs = append(evs, ev{to, true, 0})
	sort.SliceStable(evs, func(i, j int) bool {
		if evs[i].off != evs[j].off {
			return evs[i].off < evs[j].off
		}
		return !evs[i].leave && evs[j].leave
	})
	styles := map[int]*Style{0: NullStyle()}
	for i, sp := range t.spans {
		styles[i+1] = themeStyle(sp.style)
	}
	var stack []int
	var out []segment
	for k := 0; k+1 < len(evs); k++ {
		e := evs[k]
		if e.leave {
			for j, id := range stack {
				if id == e.id {
					stack = append(stack[:j], stack[j+1:]...)
					break
				}
			}
		} else {
			stack = append(stack, e.id)
		}
		if next := evs[k+1].off; next > e.off {
			ids := append([]int(nil), stack...)
			sort.Ints(ids)
			cur := NullStyle()
			for _, id := range ids {
				cur = cur.Add(styles[id])
			}
			out = append(out, segment{string(t.plain[e.off:next]), cur})
		}
	}
	return out
}

// writeHighlighted is console.print(text, markup=False) at width under sys.
func writeHighlighted(w io.Writer, t *richText, width int, sys ColorSystem) {
	c := &Console{Width: width, Color: sys, NoColor: noColorEnv(), NoMarkup: true, Highlighter: ReprHighlighter{}}
	c.Print(w, string(t.plain))
}
