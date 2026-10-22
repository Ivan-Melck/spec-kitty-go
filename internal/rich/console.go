package rich

import (
	"io"
	"os"
	"regexp"
	"strings"
)

// Port of the rendering core of rich.console.Console (rich 15):
// render / render_lines / render_str / print / _render_buffer.

// Options is rich.console.ConsoleOptions (the fields layout depends on).
type Options struct {
	MaxWidth  int
	MinWidth  int
	Justify   string // "" = None
	Overflow  string // "" = None
	NoWrap    *bool  // nil = None (default False)
	Highlight *bool  // nil = None
	Markup    *bool  // nil = None
	Height    *int   // nil = None
}

func (o Options) withWidth(w int) Options {
	w = max(0, w)
	o.MinWidth, o.MaxWidth = w, w
	return o
}

// Renderable is anything with __rich_console__.
type Renderable interface {
	RichRender(c *Console, o Options) []Seg
}

// Measurable is anything with __rich_measure__.
type Measurable interface {
	RichMeasure(c *Console, o Options) Measurement
}

// Highlighter is rich.highlighter.Highlighter.highlight (in place).
type Highlighter interface {
	Highlight(t *Text)
}

// highlightCopy is Highlighter.__call__: a highlighted copy of t.
func highlightCopy(h Highlighter, t *Text) *Text {
	c := t.Copy()
	if h != nil {
		h.Highlight(c)
	}
	return c
}

// Highlight is Highlighter.__call__ for a str (Text(s), highlighted).
func Highlight(h Highlighter, s string) *Text { return highlightCopy(h, NewText(s)) }

// Console is rich.console.Console.
type Console struct {
	Width       int
	Color       ColorSystem // NoColor when not a terminal
	NoColor     bool        // NO_COLOR: drop colours, keep attributes
	Terminal    bool
	Theme       map[string]*Style // extra theme styles over the default theme
	Highlighter Highlighter       // console.highlighter (nil = null highlighter)
	NoMarkup    bool              // markup=False
	NoHighlight bool              // highlight=False
	TabSize     int
	SoftWrap    bool
	Sanitize    bool // specify_cli CliConsole: strip terminal control text
}

// NewConsole is Console() writing to fd 1 (stdout) or 2 (stderr), with the
// ReprHighlighter and rich's environment detection.
func NewConsole(stderr bool) *Console {
	fd := 1
	if stderr {
		fd = 2
	}
	return &Console{
		Width:       consoleWidth(fd, nil),
		Color:       DetectColorSystem(fd),
		NoColor:     noColorEnv(),
		Terminal:    IsTerminal(fd),
		Highlighter: ReprHighlighter{},
	}
}

// NewCliConsole is specify_cli.cli.console.CliConsole() (stdout) or
// CliConsole(stderr=True).
func NewCliConsole(stderr bool) *Console {
	c := NewConsole(stderr)
	c.Sanitize = true
	return c
}

// ForWriter is NewConsole for the stream w writes to (an in-process buffer
// is a non-terminal stdout console unless TTY_COMPATIBLE / FORCE_COLOR say
// otherwise).
func ForWriter(w io.Writer) *Console {
	fd := fdOf(w)
	c := NewConsole(fd == 2)
	c.Color = DetectColorSystem(fd)
	if fd == 0 {
		c.Terminal = DetectColorSystem(0) != NoColor || termEnvForced()
	}
	return c
}

// CliConsoleFor is specify_cli's CliConsole(width=width) for the stream w
// writes to (width 0 = auto-detect; a dumb terminal is 80 wide).
func CliConsoleFor(w io.Writer, width int) *Console {
	c := ForWriter(w)
	c.Sanitize = true
	if width > 0 {
		fd := fdOf(w)
		if fd == 0 {
			fd = 1
		}
		c.Width = consoleWidth(fd, &width)
	}
	return c
}

// PrintTo prints renderables on CliConsole() for w.
func PrintTo(w io.Writer, objs ...any) { CliConsoleFor(w, 0).Print(w, objs...) }

func termEnvForced() bool {
	switch os.Getenv("TTY_COMPATIBLE") {
	case "0":
		return false
	case "1":
		return true
	}
	if fc, ok := os.LookupEnv("FORCE_COLOR"); ok {
		return fc != ""
	}
	return false
}

func (c *Console) tabSize() int {
	if c.TabSize > 0 {
		return c.TabSize
	}
	return 8
}

// Options is Console.options.
func (c *Console) Options() Options {
	return Options{MaxWidth: c.Width, MinWidth: 1, NoWrap: boolp(false)}
}

// GetStyle is Console.get_style(name, default=null): a *Style is
// returned as is; a name is looked up in the theme, else parsed.
func (c *Console) GetStyle(st any) *Style {
	switch s := st.(type) {
	case *Style:
		if s == nil {
			return NullStyle()
		}
		return s
	case string:
		if c != nil && c.Theme != nil {
			if t, ok := c.Theme[s]; ok {
				return t
			}
		}
		return themeStyle(s)
	}
	return NullStyle()
}

// Measure is Measurement.get.
func (c *Console) Measure(r any, o Options) Measurement {
	if o.MaxWidth < 1 {
		return Measurement{}
	}
	if s, ok := r.(string); ok {
		r = c.RenderStr(s, StrOptions{Markup: o.Markup, Highlight: boolp(false)})
	}
	if r == nil {
		return Measurement{0, o.MaxWidth}
	}
	m, ok := r.(Measurable)
	if !ok {
		return Measurement{0, o.MaxWidth}
	}
	w := m.RichMeasure(c, o).normalize().withMaximum(o.MaxWidth)
	if w.Max < 1 {
		return Measurement{}
	}
	return w.normalize()
}

// measureAll is measure_renderables.
func (c *Console) measureAll(o Options, rs []any) Measurement {
	if len(rs) == 0 {
		return Measurement{}
	}
	var m Measurement
	for i, r := range rs {
		x := c.Measure(r, o)
		if i == 0 {
			m = x
			continue
		}
		m.Min = max(m.Min, x.Min)
		m.Max = max(m.Max, x.Max)
	}
	return m
}

var reUnsafeControl = regexp.MustCompile("[\x00-\x08\x0b-\x1f\x7f\u009b\u009d]")

// Render is Console.render: r is a string (console markup), a *Text or a
// Renderable.
func (c *Console) Render(r any, o Options) []Seg {
	if o.MaxWidth < 1 || r == nil {
		return nil
	}
	var segs []Seg
	switch x := r.(type) {
	case string:
		segs = c.RenderStr(x, StrOptions{Highlight: o.Highlight, Markup: o.Markup}).RichRender(c, o)
	case Renderable:
		segs = x.RichRender(c, o)
	default:
		return nil
	}
	if c.Sanitize {
		for i, s := range segs {
			if s.Text != "" && reUnsafeControl.MatchString(s.Text) {
				segs[i] = Seg{SanitizeTerminalText(s.Text), s.Style}
			}
		}
	}
	return segs
}

// RenderLines is Console.render_lines.
func (c *Console) RenderLines(r any, o Options, style *Style, pad, newLines bool) [][]Seg {
	segs := c.Render(r, o)
	if style.truthy() {
		segs = applyStyle(segs, style, nil)
	}
	lines := splitAndCropLines(segs, o.MaxWidth, style, pad, newLines)
	if o.Height != nil {
		h := max(0, *o.Height)
		if len(lines) > h {
			lines = lines[:h]
		}
		for len(lines) < *o.Height {
			l := []Seg{{strings.Repeat(" ", o.MaxWidth), style}}
			if newLines {
				l = append(l, newLineSegment)
			}
			lines = append(lines, l)
		}
	}
	return lines
}

// StrOptions are render_str's keyword arguments.
type StrOptions struct {
	Style       any
	Justify     string
	Overflow    string
	Markup      *bool
	Highlight   *bool
	Highlighter Highlighter
}

// RenderStr is Console.render_str.
func (c *Console) RenderStr(text string, so StrOptions) *Text {
	if c.Sanitize {
		text = SanitizeTerminalText(text)
	}
	markup := (so.Markup != nil && *so.Markup) || (so.Markup == nil && !c.NoMarkup)
	hl := (so.Highlight != nil && *so.Highlight) || (so.Highlight == nil && !c.NoHighlight)
	var t *Text
	if markup {
		var err error
		t, err = renderMarkup(text, so.Style)
		if err != nil {
			t = NewStyledText(text, so.Style)
		}
	} else {
		t = NewStyledText(text, so.Style)
	}
	t.Justify, t.Overflow = so.Justify, so.Overflow
	if hl {
		h := so.Highlighter
		if h == nil {
			h = c.Highlighter
		}
		// the highlighted copy is a new Text: the base style, justify and
		// overflow are not carried over (rich's NullHighlighter included)
		ht := Highlight(h, t.Plain())
		ht.Spans = append(ht.Spans, t.Spans...)
		return ht
	}
	return t
}

var (
	reCSI = regexp.MustCompile("(?:\x1b\\[|\u009b)[0-?]*[ -/]*[@-~]")
	reOSC = regexp.MustCompile("(?:\x1b\\]|\u009d)[^\x07\x1b\u009c]*(?:\x07|\x1b\\\\|\u009c)?")
)

// SanitizeTerminalText is specify_cli.cli.console.sanitize_terminal_text.
func SanitizeTerminalText(s string) string {
	s = reOSC.ReplaceAllString(reCSI.ReplaceAllString(s, ""), "")
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r >= 0x20 {
			return r
		}
		return -1
	}, s)
}

// PrintOptions are Console.print's keyword arguments.
type PrintOptions struct {
	Style     any
	Justify   string
	Overflow  string
	NoWrap    *bool
	Markup    *bool
	Highlight *bool
	Width     int // 0 = console width
	NoCrop    bool
	SoftWrap  *bool
	End       *string // nil = "\n"
	Sep       *string // nil = " "
}

// Segments is Console.print rendered to segments (what print writes).
func (c *Console) Segments(objs []any, po PrintOptions) []Seg {
	end, sep := "\n", " "
	if po.End != nil {
		end = *po.End
	}
	if po.Sep != nil {
		sep = *po.Sep
	}
	if len(objs) == 0 {
		if end == "\n" {
			objs = []any{newLineRenderable{}}
		} else {
			objs = []any{""}
		}
	}
	softWrap := c.SoftWrap
	if po.SoftWrap != nil {
		softWrap = *po.SoftWrap
	}
	noWrap, overflow, crop := po.NoWrap, po.Overflow, !po.NoCrop
	if softWrap {
		if noWrap == nil {
			noWrap = boolp(true)
		}
		if overflow == "" {
			overflow = "ignore"
		}
		crop = false
	}
	renderables := c.collect(objs, sep, end, po)
	o := c.Options()
	o.Justify, o.Overflow = po.Justify, overflow
	if po.Width > 0 {
		o = o.withWidth(min(po.Width, c.Width))
	}
	o.Height = nil
	o.NoWrap, o.Markup, o.Highlight = noWrap, po.Markup, po.Highlight
	var segs []Seg
	if !styleTruthy(po.Style) {
		for _, r := range renderables {
			segs = append(segs, c.Render(r, o)...)
		}
	} else {
		st := c.GetStyle(po.Style)
		for _, r := range renderables {
			for _, l := range splitLinesTerminator(c.Render(r, o)) {
				segs = append(segs, applyStyle(l.segs, st, nil)...)
				if l.newLine {
					segs = append(segs, newLineSegment)
				}
			}
		}
	}
	if crop {
		var out []Seg
		for _, l := range splitAndCropLines(segs, c.Width, nil, false, true) {
			out = append(out, l...)
		}
		return out
	}
	return segs
}

type newLineRenderable struct{}

func (newLineRenderable) RichRender(*Console, Options) []Seg { return []Seg{newLineSegment} }

// collect is Console._collect_renderables.
func (c *Console) collect(objs []any, sep, end string, po PrintOptions) []any {
	var out []any
	var texts []*Text
	appendR := func(r any) {
		switch po.Justify {
		case "left", "center", "right":
			out = append(out, &Align{R: r, Align: po.Justify, Pad: true})
		default:
			out = append(out, r)
		}
	}
	flush := func() {
		if len(texts) > 0 {
			st := NewText(sep)
			st.Justify, st.End = po.Justify, end
			appendR(st.join(texts))
			texts = nil
		}
	}
	for _, o := range objs {
		switch x := o.(type) {
		case string:
			texts = append(texts, c.RenderStr(x, StrOptions{Markup: po.Markup, Highlight: po.Highlight}))
		case *Text:
			texts = append(texts, x)
		default:
			flush()
			appendR(o)
		}
	}
	flush()
	return out
}

// Output is Console._render_buffer: segments to the string written.
func (c *Console) Output(segs []Seg) string {
	var b strings.Builder
	sys := c.Color
	for _, s := range segs {
		st := s.Style
		if c.NoColor && sys != NoColor && st != nil {
			st = st.withoutColor()
		}
		if st.truthy() {
			b.WriteString(st.Render(s.Text, sys))
		} else {
			b.WriteString(s.Text)
		}
	}
	return b.String()
}

// Print is Console.print(*objs) written to w.
func (c *Console) Print(w io.Writer, objs ...any) {
	c.PrintWith(w, PrintOptions{}, objs...)
}

// PrintWith is Console.print(*objs, **po) written to w.
func (c *Console) PrintWith(w io.Writer, po PrintOptions, objs ...any) {
	io.WriteString(w, c.Output(c.Segments(objs, po)))
}

// HighlightText is Highlighter.__call__ for a Text: a highlighted copy.
func HighlightText(h Highlighter, t *Text) *Text { return highlightCopy(h, t) }

var reEscapable = regexp.MustCompile(`(\\*)(\[[a-z#/@][^\[]*?\])`)

// EscapeMarkup is rich.markup.escape.
func EscapeMarkup(s string) string {
	s = reEscapable.ReplaceAllStringFunc(s, func(m string) string {
		sub := reEscapable.FindStringSubmatch(m)
		return sub[1] + sub[1] + `\` + sub[2]
	})
	if strings.HasSuffix(s, `\`) && !strings.HasSuffix(s, `\\`) {
		s += `\`
	}
	return s
}
