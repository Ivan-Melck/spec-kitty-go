package rich

import "strings"

// Box is rich.box.Box.
type Box struct {
	topLeft, top, topDivider, topRight                     string
	headLeft, headVertical, headRight                      string
	headRowLeft, headRowHorizontal, headRowCross, headRowR string
	midLeft, midVertical, midRight                         string
	rowLeft, rowHorizontal, rowCross, rowRight             string
	footRowLeft, footRowHorizontal, footRowCross, footRowR string
	footLeft, footVertical, footRight                      string
	bottomLeft, bottom, bottomDivider, bottomRight         string
}

func newBox(def string) *Box {
	l := strings.Split(strings.TrimSuffix(def, "\n"), "\n")
	c := func(i int) []string {
		r := []rune(l[i])
		return []string{string(r[0]), string(r[1]), string(r[2]), string(r[3])}
	}
	b := &Box{}
	x := c(0)
	b.topLeft, b.top, b.topDivider, b.topRight = x[0], x[1], x[2], x[3]
	x = c(1)
	b.headLeft, b.headVertical, b.headRight = x[0], x[2], x[3]
	x = c(2)
	b.headRowLeft, b.headRowHorizontal, b.headRowCross, b.headRowR = x[0], x[1], x[2], x[3]
	x = c(3)
	b.midLeft, b.midVertical, b.midRight = x[0], x[2], x[3]
	x = c(4)
	b.rowLeft, b.rowHorizontal, b.rowCross, b.rowRight = x[0], x[1], x[2], x[3]
	x = c(5)
	b.footRowLeft, b.footRowHorizontal, b.footRowCross, b.footRowR = x[0], x[1], x[2], x[3]
	x = c(6)
	b.footLeft, b.footVertical, b.footRight = x[0], x[2], x[3]
	x = c(7)
	b.bottomLeft, b.bottom, b.bottomDivider, b.bottomRight = x[0], x[1], x[2], x[3]
	return b
}

// Box definitions (rich.box).
var (
	BoxAscii             = newBox("+--+\n| ||\n|-+|\n| ||\n|-+|\n|-+|\n| ||\n+--+\n")
	BoxAscii2            = newBox("+-++\n| ||\n+-++\n| ||\n+-++\n+-++\n| ||\n+-++\n")
	BoxAsciiDoubleHead   = newBox("+-++\n| ||\n+=++\n| ||\n+-++\n+-++\n| ||\n+-++\n")
	BoxSquare            = newBox("┌─┬┐\n│ ││\n├─┼┤\n│ ││\n├─┼┤\n├─┼┤\n│ ││\n└─┴┘\n")
	BoxSquareDoubleHead  = newBox("┌─┬┐\n│ ││\n╞═╪╡\n│ ││\n├─┼┤\n├─┼┤\n│ ││\n└─┴┘\n")
	BoxMinimal           = newBox("  ╷ \n  │ \n╶─┼╴\n  │ \n╶─┼╴\n╶─┼╴\n  │ \n  ╵ \n")
	BoxMinimalHeavyHead  = newBox("  ╷ \n  │ \n╺━┿╸\n  │ \n╶─┼╴\n╶─┼╴\n  │ \n  ╵ \n")
	BoxMinimalDoubleHead = newBox("  ╷ \n  │ \n ═╪ \n  │ \n ─┼ \n ─┼ \n  │ \n  ╵ \n")
	BoxSimple            = newBox("    \n    \n ── \n    \n    \n ── \n    \n    \n")
	BoxSimpleHead        = newBox("    \n    \n ── \n    \n    \n    \n    \n    \n")
	BoxSimpleHeavy       = newBox("    \n    \n ━━ \n    \n    \n ━━ \n    \n    \n")
	BoxHorizontals       = newBox(" ── \n    \n ── \n    \n ── \n ── \n    \n ── \n")
	BoxRounded           = newBox("╭─┬╮\n│ ││\n├─┼┤\n│ ││\n├─┼┤\n├─┼┤\n│ ││\n╰─┴╯\n")
	BoxHeavy             = newBox("┏━┳┓\n┃ ┃┃\n┣━╋┫\n┃ ┃┃\n┣━╋┫\n┣━╋┫\n┃ ┃┃\n┗━┻┛\n")
	BoxHeavyEdge         = newBox("┏━┯┓\n┃ │┃\n┠─┼┨\n┃ │┃\n┠─┼┨\n┠─┼┨\n┃ │┃\n┗━┷┛\n")
	BoxHeavyHead         = newBox("┏━┳┓\n┃ ┃┃\n┡━╇┩\n│ ││\n├─┼┤\n├─┼┤\n│ ││\n└─┴┘\n")
	BoxDouble            = newBox("╔═╦╗\n║ ║║\n╠═╬╣\n║ ║║\n╠═╬╣\n╠═╬╣\n║ ║║\n╚═╩╝\n")
	BoxDoubleEdge        = newBox("╔═╤╗\n║ │║\n╟─┼╢\n║ │║\n╟─┼╢\n╟─┼╢\n║ │║\n╚═╧╝\n")
	BoxMarkdown          = newBox("    \n| ||\n|-||\n| ||\n|-||\n|-||\n| ||\n    \n")
)

var plainHeaded = map[*Box]*Box{
	BoxHeavyHead:         BoxSquare,
	BoxSquareDoubleHead:  BoxSquare,
	BoxMinimalDoubleHead: BoxMinimal,
	BoxMinimalHeavyHead:  BoxMinimal,
	BoxAsciiDoubleHead:   BoxAscii2,
}

func (b *Box) plainHeaded() *Box {
	if p, ok := plainHeaded[b]; ok {
		return p
	}
	return b
}

func joinWidths(widths []int, fill, div string) string {
	var s strings.Builder
	for i, w := range widths {
		s.WriteString(strings.Repeat(fill, max(0, w)))
		if i < len(widths)-1 {
			s.WriteString(div)
		}
	}
	return s.String()
}

func (b *Box) getTop(widths []int) string {
	return b.topLeft + joinWidths(widths, b.top, b.topDivider) + b.topRight
}

func (b *Box) getBottom(widths []int) string {
	return b.bottomLeft + joinWidths(widths, b.bottom, b.bottomDivider) + b.bottomRight
}

func (b *Box) getRow(widths []int, level string, edge bool) string {
	var left, h, cross, right string
	switch level {
	case "head":
		left, h, cross, right = b.headRowLeft, b.headRowHorizontal, b.headRowCross, b.headRowR
	case "mid":
		left, h, cross, right = b.midLeft, " ", b.midVertical, b.midRight
	case "foot":
		left, h, cross, right = b.footRowLeft, b.footRowHorizontal, b.footRowCross, b.footRowR
	default:
		left, h, cross, right = b.rowLeft, b.rowHorizontal, b.rowCross, b.rowRight
	}
	s := joinWidths(widths, h, cross)
	if edge {
		s = left + s + right
	}
	return s
}
