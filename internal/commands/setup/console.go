package setup

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/commands/util/uconsole"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// out prints rich-markup text (stripped, wrapped at the console width).
func out(w io.Writer, s string) { uconsole.Print(w, s) }

func outf(w io.Writer, format string, a ...any) { uconsole.Print(w, fmt.Sprintf(format, a...)) }

// raw prints text verbatim (markup=False, soft_wrap=True).
func raw(w io.Writer, s string) { fmt.Fprintln(w, s) }

func runeLen(s string) int { return utf8.RuneCountInString(s) }

func padRight(s string, n int) string {
	if d := n - runeLen(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// wrapWords wraps on spaces at width (long words split), like rich.
func wrapWords(line string, width int) []string {
	if width <= 0 || runeLen(line) <= width {
		return []string{line}
	}
	indent := ""
	for _, r := range line {
		if r != ' ' {
			break
		}
		indent += " "
	}
	words := strings.Fields(line)
	var lines []string
	cur := indent
	for _, w := range words {
		for runeLen(w) > width {
			if strings.TrimSpace(cur) != "" {
				lines = append(lines, strings.TrimRight(cur, " "))
				cur = ""
			}
			r := []rune(w)
			lines = append(lines, string(r[:width]))
			w = string(r[width:])
		}
		switch {
		case strings.TrimSpace(cur) == "":
			cur += w
		case runeLen(cur)+1+runeLen(w) <= width:
			cur += " " + w
		default:
			lines = append(lines, cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// panel renders an expanded rich Panel (ROUNDED, padding (1, 2)).
func panel(w io.Writer, content, title string) {
	width := cli.Width()
	inner := width - 6
	top := strings.Repeat("─", width-2)
	if title != "" {
		t := " " + uconsole.Strip(title) + " "
		left := (width - 2 - runeLen(t)) / 2
		right := width - 2 - runeLen(t) - left
		top = strings.Repeat("─", left) + t + strings.Repeat("─", right)
	}
	fmt.Fprintln(w, "╭"+top+"╮")
	blank := "│" + strings.Repeat(" ", width-2) + "│"
	fmt.Fprintln(w, blank)
	for _, l := range strings.Split(uconsole.Strip(content), "\n") {
		for _, wl := range wrapWords(l, inner) {
			fmt.Fprintln(w, "│  "+padRight(wl, inner)+"  │")
		}
	}
	fmt.Fprintln(w, blank)
	fmt.Fprintln(w, "╰"+strings.Repeat("─", width-2)+"╯")
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
	for _, l := range lines {
		center(w, padRight(l, maxw))
	}
	center(w, padRight("", maxw))
	center(w, Tagline)
	center(w, "v"+app.Version)
	fmt.Fprintln(w)
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

func (t *tracker) render(w io.Writer) {
	fmt.Fprintln(w, t.title)
	for i, s := range t.steps {
		branch := "├── "
		if i == len(t.steps)-1 {
			branch = "└── "
		}
		sym := "○"
		switch s.status {
		case "done", "error":
			sym = "●"
		case "running":
			sym = "○"
		}
		line := sym + " " + s.label
		if s.detail != "" {
			line += " (" + s.detail + ")"
		}
		cont := "│   "
		if i == len(t.steps)-1 {
			cont = "    "
		}
		// rich Tree wraps each label at the width left after the guide.
		for j, l := range uconsole.WrapWords(line, cli.Width()-4) {
			if j == 0 {
				fmt.Fprintln(w, branch+l)
			} else {
				fmt.Fprintln(w, cont+l)
			}
		}
	}
}

func printJSON(w io.Writer, v any) { fmt.Fprintln(w, jsonx.Dumps(v, jsonx.Pretty)) }
