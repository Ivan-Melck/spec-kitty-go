package rich

import "io"

// Segment is one styled run of a hand-laid-out renderable (rich.Segment).
type Segment struct {
	Text  string
	Style string // rich style definition ("" = unstyled)
}

// ColorSystemOf is the colour system rich detects for w's terminal
// (NoColor when w is not a terminal).
func ColorSystemOf(w io.Writer) ColorSystem { return DetectColorSystem(fdOf(w)) }

// RenderSegments renders segments the way rich's Console writes them under
// sys: each styled segment wrapped in its own SGR sequence (colours dropped
// under NO_COLOR, as Segment.remove_color does), unstyled text verbatim.
func RenderSegments(segs []Segment, sys ColorSystem) string {
	out := ""
	noColor := noColorEnv()
	for _, s := range segs {
		if s.Style == "" || sys == NoColor {
			out += s.Text
			continue
		}
		st, err := ParseStyle(s.Style)
		if err != nil {
			out += s.Text
			continue
		}
		if noColor {
			st = st.withoutColor()
		}
		out += st.Render(s.Text, sys)
	}
	return out
}
