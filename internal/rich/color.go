package rich

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// ColorSystem is rich.color.ColorSystem (0 = no colour).
type ColorSystem int

// Colour systems.
const (
	NoColor   ColorSystem = 0
	Standard  ColorSystem = 1
	EightBit  ColorSystem = 2
	TrueColor ColorSystem = 3
)

// DetectColorSystem is Console._detect_color_system for a console writing
// to fd (1 = stdout, 2 = stderr; 0 = an in-process buffer: only
// TTY_COMPATIBLE / FORCE_COLOR can make it a terminal).
func DetectColorSystem(fd int) ColorSystem {
	term := fd != 0 && IsTerminal(fd)
	if fd == 0 {
		switch os.Getenv("TTY_COMPATIBLE") {
		case "0":
			term = false
		case "1":
			term = true
		default:
			if fc, ok := os.LookupEnv("FORCE_COLOR"); ok {
				term = fc != ""
			}
		}
	}
	t := strings.ToLower(strings.TrimSpace(os.Getenv("TERM")))
	if !term || t == "dumb" || t == "unknown" {
		return NoColor
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COLORTERM"))) {
	case "truecolor", "24bit":
		return TrueColor
	}
	colors := t
	if i := strings.LastIndex(t, "-"); i >= 0 {
		colors = t[i+1:]
	}
	switch colors {
	case "kitty", "256color":
		return EightBit
	}
	return Standard
}

// noColorEnv is Console.no_color from the environment (NO_COLOR non-empty).
func noColorEnv() bool { return os.Getenv("NO_COLOR") != "" }

type colorType int

const (
	ctDefault colorType = iota
	ctStandard
	ctEightBit
	ctTrueColor
)

// Color is rich.color.Color.
type Color struct {
	typ     colorType
	number  int
	r, g, b int
}

var reColor = regexp.MustCompile(`^(?:#([0-9a-f]{6})|color\(([0-9]{1,3})\)|rgb\(([\d\s,]+)\))$`)

// ParseColor is Color.parse.
func ParseColor(s string) (*Color, error) {
	c := strings.ToLower(strings.TrimSpace(s))
	if c == "default" {
		return &Color{typ: ctDefault}, nil
	}
	if n, ok := ansiColorNames[c]; ok {
		return numbered(n), nil
	}
	m := reColor.FindStringSubmatch(c)
	if m == nil {
		return nil, fmt.Errorf("%s is not a valid color", pyQuote(s))
	}
	switch {
	case m[1] != "":
		v, _ := strconv.ParseUint(m[1], 16, 32)
		return &Color{typ: ctTrueColor, r: int(v >> 16), g: int(v >> 8 & 0xff), b: int(v & 0xff)}, nil
	case m[2] != "":
		n, _ := strconv.Atoi(m[2])
		if n > 255 {
			return nil, fmt.Errorf("color number must be <= 255 in %s", pyQuote(c))
		}
		return numbered(n), nil
	}
	parts := strings.Split(m[3], ",")
	if len(parts) != 3 {
		return nil, fmt.Errorf("expected three components in %s", pyQuote(s))
	}
	var v [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil, fmt.Errorf("%s is not a valid color", pyQuote(s))
		}
		if n > 255 {
			return nil, fmt.Errorf("color components must be <= 255 in %s", pyQuote(s))
		}
		v[i] = n
	}
	return &Color{typ: ctTrueColor, r: v[0], g: v[1], b: v[2]}, nil
}

func pyQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `\'`) + "'" }

func numbered(n int) *Color {
	if n < 16 {
		return &Color{typ: ctStandard, number: n}
	}
	return &Color{typ: ctEightBit, number: n}
}

func (c *Color) system() ColorSystem {
	switch c.typ {
	case ctEightBit:
		return EightBit
	case ctTrueColor:
		return TrueColor
	}
	return Standard
}

// downgrade is Color.downgrade.
func (c *Color) downgrade(sys ColorSystem) *Color {
	if c.typ == ctDefault || c.system() == sys {
		return c
	}
	if sys == EightBit && c.typ == ctTrueColor {
		_, l, s := rgbToHLS(float64(c.r)/255, float64(c.g)/255, float64(c.b)/255)
		if s < 0.15 {
			gray := int(math.RoundToEven(l * 25.0))
			n := 231 + gray
			switch gray {
			case 0:
				n = 16
			case 25:
				n = 231
			}
			return &Color{typ: ctEightBit, number: n}
		}
		six := func(v int) int {
			var f float64
			if v < 95 {
				f = float64(v) / 95
			} else {
				f = 1 + float64(v-95)/40
			}
			return int(math.RoundToEven(f))
		}
		return &Color{typ: ctEightBit, number: 16 + 36*six(c.r) + 6*six(c.g) + six(c.b)}
	}
	if sys == Standard {
		r, g, b := c.r, c.g, c.b
		if c.typ == ctEightBit {
			p := eightBitPalette[c.number]
			r, g, b = p[0], p[1], p[2]
		}
		return &Color{typ: ctStandard, number: paletteMatch(standardPalette, r, g, b)}
	}
	return c
}

func rgbToHLS(r, g, b float64) (float64, float64, float64) {
	maxc := math.Max(r, math.Max(g, b))
	minc := math.Min(r, math.Min(g, b))
	sumc := maxc + minc
	rangec := maxc - minc
	l := sumc / 2.0
	if minc == maxc {
		return 0, l, 0
	}
	var s float64
	if l <= 0.5 {
		s = rangec / sumc
	} else {
		s = rangec / (2.0 - maxc - minc)
	}
	return 0, l, s
}

// paletteMatch is Palette.match.
func paletteMatch(p [][3]int, r1, g1, b1 int) int {
	best, bestD := 0, math.Inf(1)
	for i, c := range p {
		redMean := (r1 + c[0]) / 2
		if r1+c[0] < 0 {
			redMean = int(math.Floor(float64(r1+c[0]) / 2))
		}
		dr, dg, db := r1-c[0], g1-c[1], b1-c[2]
		d := math.Sqrt(float64((((512 + redMean) * dr * dr) >> 8) + 4*dg*dg + (((767 - redMean) * db * db) >> 8)))
		if d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

func (c *Color) ansiCodes(fg bool) []string {
	switch c.typ {
	case ctDefault:
		if fg {
			return []string{"39"}
		}
		return []string{"49"}
	case ctStandard:
		fore, back := 30, 40
		if c.number >= 8 {
			fore, back = 82, 92
		}
		if fg {
			return []string{strconv.Itoa(fore + c.number)}
		}
		return []string{strconv.Itoa(back + c.number)}
	case ctEightBit:
		if fg {
			return []string{"38", "5", strconv.Itoa(c.number)}
		}
		return []string{"48", "5", strconv.Itoa(c.number)}
	}
	if fg {
		return []string{"38", "2", strconv.Itoa(c.r), strconv.Itoa(c.g), strconv.Itoa(c.b)}
	}
	return []string{"48", "2", strconv.Itoa(c.r), strconv.Itoa(c.g), strconv.Itoa(c.b)}
}

// ---------------------------------------------------------------- styles

var styleAttributes = map[string]int{
	"bold": 0, "b": 0, "dim": 1, "d": 1, "italic": 2, "i": 2, "underline": 3, "u": 3,
	"blink": 4, "blink2": 5, "reverse": 6, "r": 6, "conceal": 7, "c": 7, "strike": 8, "s": 8,
	"underline2": 9, "uu": 9, "frame": 10, "encircle": 11, "overline": 12, "o": 12,
}

var attrSGR = [13]string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "21", "51", "52", "53"}

// Style is rich.style.Style (without meta).
type Style struct {
	color, bgcolor *Color
	attrs, set     int
	link           string
	null           bool
}

// NullStyle is Style() / Style.null().
func NullStyle() *Style { return &Style{null: true} }

// ParseStyle is Style.parse.
func ParseStyle(def string) (*Style, error) {
	if strings.TrimSpace(def) == "none" || def == "" {
		return NullStyle(), nil
	}
	st := &Style{}
	words := strings.Fields(def)
	for i := 0; i < len(words); i++ {
		w := strings.ToLower(words[i])
		next := func() string {
			if i+1 < len(words) {
				i++
				return strings.ToLower(words[i])
			}
			return ""
		}
		switch {
		case w == "on":
			c := next()
			if c == "" {
				return nil, fmt.Errorf("color expected after 'on'")
			}
			col, err := ParseColor(c)
			if err != nil {
				return nil, fmt.Errorf("unable to parse %s as background color; %v", pyQuote(c), err)
			}
			st.bgcolor = col
		case w == "not":
			a := next()
			bit, ok := styleAttributes[a]
			if !ok {
				return nil, fmt.Errorf("expected style attribute after 'not', found %s", pyQuote(a))
			}
			st.set |= 1 << bit
			st.attrs &^= 1 << bit
		case w == "link":
			if i+1 >= len(words) {
				return nil, fmt.Errorf("URL expected after 'link'")
			}
			i++
			st.link = words[i]
		default:
			if bit, ok := styleAttributes[w]; ok {
				st.set |= 1 << bit
				st.attrs |= 1 << bit
				continue
			}
			col, err := ParseColor(w)
			if err != nil {
				return nil, fmt.Errorf("unable to parse %s as color; %v", pyQuote(w), err)
			}
			st.color = col
		}
	}
	return st, nil
}

// Add is Style.__add__ (other's set attributes and colours win).
func (s *Style) Add(o *Style) *Style {
	if o == nil || o.null {
		return s
	}
	if s == nil || s.null {
		return o
	}
	n := &Style{color: s.color, bgcolor: s.bgcolor, link: s.link}
	if o.color != nil {
		n.color = o.color
	}
	if o.bgcolor != nil {
		n.bgcolor = o.bgcolor
	}
	if o.link != "" {
		n.link = o.link
	}
	n.attrs = (s.attrs &^ o.set) | (o.attrs & o.set)
	n.set = s.set | o.set
	return n
}

func (s *Style) sgr(sys ColorSystem) string {
	var codes []string
	a := s.attrs & s.set
	for bit := 0; bit < 13; bit++ {
		if a&(1<<bit) != 0 {
			codes = append(codes, attrSGR[bit])
		}
	}
	if s.color != nil {
		codes = append(codes, s.color.downgrade(sys).ansiCodes(true)...)
	}
	if s.bgcolor != nil {
		codes = append(codes, s.bgcolor.downgrade(sys).ansiCodes(false)...)
	}
	return strings.Join(codes, ";")
}

// Render is Style.render (links are written without an id).
func (s *Style) Render(text string, sys ColorSystem) string {
	if text == "" || sys == NoColor || s == nil || s.null {
		return text
	}
	out := text
	if a := s.sgr(sys); a != "" {
		out = "\x1b[" + a + "m" + text + "\x1b[0m"
	}
	return out
}

// withoutColor is Segment.remove_color's style (NO_COLOR).
func (s *Style) withoutColor() *Style {
	if s == nil || s.null {
		return s
	}
	return &Style{attrs: s.attrs, set: s.set, link: s.link, null: s.attrs == 0 && s.set == 0 && s.link == ""}
}

// themeStyle is Console.get_style(name, default=null): the default theme,
// else Style.parse, else the null style.
func themeStyle(name string) *Style {
	styleCacheMu.Lock()
	defer styleCacheMu.Unlock()
	if st, ok := styleCache[name]; ok {
		return st
	}
	st := lookupStyle(name)
	styleCache[name] = st
	return st
}

var (
	styleCacheMu sync.Mutex
	styleCache   = map[string]*Style{}
)

func lookupStyle(name string) *Style {
	if def, ok := defaultStyles[name]; ok {
		st, err := ParseStyle(def)
		if err == nil {
			return st
		}
	}
	st, err := ParseStyle(name)
	if err != nil {
		return NullStyle()
	}
	return st
}
