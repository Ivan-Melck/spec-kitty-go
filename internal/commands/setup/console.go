package setup

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// out prints rich-markup text (stripped, wrapped at the console width).
func out(w io.Writer, s string) { rich.Print(w, s) }

func outf(w io.Writer, format string, a ...any) { rich.Print(w, fmt.Sprintf(format, a...)) }

// raw prints text verbatim (markup=False, soft_wrap=True).
func raw(w io.Writer, s string) { fmt.Fprintln(w, s) }

// plain prints text without markup processing but wrapped at the console
// width (console.print(..., markup=False)).
func plain(w io.Writer, s string) { rich.PrintNoMarkup(w, s) }

func runeLen(s string) int { return utf8.RuneCountInString(s) }

func padRight(s string, n int) string {
	if d := n - runeLen(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// panel renders an expanded rich Panel (ROUNDED, padding (1, 2)).
func panel(w io.Writer, content, title string) {
	panelStyled(w, content, title, "cyan", 1, 2)
}

// panelStyled renders an expanded rich Panel (ROUNDED) with a markup title
// and content, border_style and padding; on a colour terminal the border,
// title and content carry rich's segment styles.
func panelStyled(w io.Writer, content, title, border string, padY, padX int) {
	for _, l := range rich.StyledPanelLines(rich.PanelSpec{
		Content: content, Title: title, BorderStyle: border, Width: cli.Width(),
		PadY: padY, PadX: padX, Wrap: rich.WrapWords,
	}, rich.ColorSystemOf(w)) {
		fmt.Fprintln(w, l)
	}
}

// center prints text centered at the console width (rich Align.center pads
// both sides).
func center(w io.Writer, s string) {
	width := cli.Width()
	n := runeLen(s)
	if n >= width {
		fmt.Fprintln(w, s)
		return
	}
	left := (width - n) / 2
	fmt.Fprintln(w, strings.Repeat(" ", left)+s+strings.Repeat(" ", width-n-left))
}

// Banner is core.config.BANNER.
const Banner = "`````````````````````````````````````````````````````````\n" +
	"\n" +
	"           ▄█▄_                            ╓▄█_\n" +
	"          ▐█ └▀█▄_                      ▄█▀▀ ╙█\n" +
	"          █\"    `▀█▄                  ▄█▀     █▌\n" +
	"         ▐█        ▀█▄▄▄██████████▄▄▄█\"       ▐█\n" +
	"         ║█          \"` ╟█  ╫▌  █\" '\"          █\n" +
	"         ║█              ▀  ╚▀  ▀             J█\n" +
	"          █                                   █▌\n" +
	"          █▀   ,▄█████▄           ,▄█████▄_   █▌\n" +
	"         █▌  ▄█\"      \"██       ╓█▀      `▀█_  █▌\n" +
	"        ▐█__▐▌    ▄██▄  ╙█_____╒█   ▄██,   '█__'█\n" +
	"        █▀▀▀█M    ████   █▀╙\"\"\"██  ▐████    █▀▀\"█▌\n" +
	"        █─  ╟█    ╙▀▀\"  ██      █╕  ╙▀▀    ╓█   ║▌\n" +
	"   ╓▄▄▄▄█▌,_ ╙█▄_    _▄█▀╒██████ ▀█╥     ▄█▀ __,██▄▄▄▄\n" +
	"        ╚█'`\"  `╙▀▀▀▀▀\"   `▀██▀    \"▀▀▀▀▀\"   \"\"▐█\n" +
	"     _,▄▄███▀               █▌              ▀▀███▄▄,_\n" +
	"    ▀\"`   ▀█_         '▀█▄▄█▀▀█▄▄█▀          ▄█\"  '\"▀\"\n" +
	"           ╙██_                            ▄█▀\n" +
	"             └▀█▄_                      ,▓█▀\n" +
	"                └▀▀██▄,__        __╓▄██▀▀\n" +
	"                     `\"▀▀▀▀▀▀▀▀▀▀▀╙\"`\n" +
	"\n" +
	"`````````````````````````````````````````````````````````"

// Tagline is cli.helpers.TAGLINE.
const Tagline = "Spec Kitty - Spec-Driven Development Toolkit (forked from GitHub Spec Kit)"

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y", "on":
		return true
	}
	return false
}

// showBanner prints the ASCII banner unless suppressed (SPEC_KITTY_NO_BANNER
// or an agent harness environment).
func showBanner(w io.Writer) {
	if truthy(os.Getenv("SPEC_KITTY_NO_BANNER")) {
		return
	}
	for _, k := range []string{"CLAUDECODE", "CLAUDE_CODE", "CODEX", "OPENCODE", "CURSOR_TRACE_ID"} {
		if _, ok := os.LookupEnv(k); ok {
			return
		}
	}
	lines := strings.Split(Banner, "\n")
	maxw := 0
	for _, l := range lines {
		if n := runeLen(l); n > maxw {
			maxw = n
		}
	}
	// Text gradient (bright_blue..bright_white), then the italic
	// bright_yellow tagline and dim cyan version, each Align.center'ed.
	sys := rich.ColorSystemOf(w)
	colors := []string{"bright_blue", "blue", "cyan", "bright_cyan", "white", "bright_white"}
	for i, l := range lines {
		centerStyled(w, padRight(l, maxw), colors[i%len(colors)], sys)
	}
	center(w, padRight("", maxw))
	centerStyled(w, Tagline, "italic bright_yellow", sys)
	centerStyled(w, "v"+app.Version, "dim cyan", sys)
	fmt.Fprintln(w)
}

// centerStyled is center for one styled text (the padding stays unstyled).
func centerStyled(w io.Writer, s, style string, sys rich.ColorSystem) {
	width := cli.Width()
	n := runeLen(s)
	body := rich.RenderSegments([]rich.Segment{{Text: s, Style: style}}, sys)
	if n >= width {
		fmt.Fprintln(w, body)
		return
	}
	left := (width - n) / 2
	fmt.Fprintln(w, strings.Repeat(" ", left)+body+strings.Repeat(" ", width-n-left))
}

// isInteractive is core.env.is_interactive.
func isInteractive() bool {
	if truthy(os.Getenv("SPEC_KITTY_FORCE_INTERACTIVE")) {
		return true
	}
	if truthy(os.Getenv("SPEC_KITTY_NON_INTERACTIVE")) {
		return false
	}
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// tracker is a minimal StepTracker rendering its final tree.
type tracker struct {
	title string
	steps []*trackStep
}

type trackStep struct {
	key, label, status, detail string
}

func (t *tracker) add(key, label string) {
	t.steps = append(t.steps, &trackStep{key: key, label: label, status: "pending"})
}

func (t *tracker) set(key, status, detail string) {
	for _, s := range t.steps {
		if s.key == key {
			s.status, s.detail = status, detail
			return
		}
	}
	t.steps = append(t.steps, &trackStep{key: key, label: key, status: status, detail: detail})
}

func (t *tracker) complete(key, detail string) { t.set(key, "done", detail) }
func (t *tracker) fail(key, detail string)     { t.set(key, "error", detail) }

// render prints StepTracker.render()'s Tree: a cyan title and one
// markup label per step under grey50 guides.
func (t *tracker) render(w io.Writer) {
	for _, l := range t.lines(w) {
		fmt.Fprintln(w, l)
	}
}

// lines renders the tree's lines (styled for w's colour system).
func (t *tracker) lines(w io.Writer) []string {
	sys := rich.ColorSystemOf(w)
	guide := func(g string) string { return rich.RenderStyled(g, "grey50", sys) }
	out := []string{rich.RenderStyled(t.title, "cyan", sys)}
	for i, s := range t.steps {
		branch, cont := "├── ", "│   "
		if i == len(t.steps)-1 {
			branch, cont = "└── ", "    "
		}
		symbol := " "
		switch s.status {
		case "done":
			symbol = "[green]●[/green]"
		case "pending":
			symbol = "[green dim]○[/green dim]"
		case "running":
			symbol = "[cyan]○[/cyan]"
		case "error":
			symbol = "[red]●[/red]"
		case "skipped":
			symbol = "[yellow]○[/yellow]"
		}
		detail := strings.TrimSpace(s.detail)
		var line string
		switch {
		case s.status == "pending" && detail != "":
			line = fmt.Sprintf("%s [bright_black]%s (%s)[/bright_black]", symbol, s.label, detail)
		case s.status == "pending":
			line = fmt.Sprintf("%s [bright_black]%s[/bright_black]", symbol, s.label)
		case detail != "":
			line = fmt.Sprintf("%s [white]%s[/white] [bright_black](%s)[/bright_black]", symbol, s.label, detail)
		default:
			line = fmt.Sprintf("%s [white]%s[/white]", symbol, s.label)
		}
		// rich Tree wraps each label at the width left after the guide.
		for j, l := range rich.StyledMarkupLines(line, cli.Width()-4, rich.WrapWords, sys) {
			g := branch
			if j > 0 {
				g = cont
			}
			out = append(out, guide(g)+l.Styled)
		}
	}
	return out
}

func printJSON(w io.Writer, v any) { fmt.Fprintln(w, jsonx.Dumps(v, jsonx.Pretty)) }
