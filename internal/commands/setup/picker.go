package setup

// The interactive agent picker (cli/ui.multi_select_with_arrows): a Rich
// Live panel redrawn on every key, read in raw mode from the terminal
// (readchar). The layout, segment styles (SGR on a colour terminal), keys
// and cancellation behaviour match.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

var errSelectionCancelled = errors.New("selection cancelled")

// readPickerKey is cli/ui.get_key over a raw-mode reader.
func readPickerKey(r io.Reader) (string, error) {
	b := make([]byte, 1)
	next := func() (byte, error) {
		if _, err := io.ReadFull(r, b); err != nil {
			return 0, err
		}
		return b[0], nil
	}
	c, err := next()
	if err != nil {
		return "", err
	}
	switch c {
	case 0x03:
		return "", errSelectionCancelled // CTRL_C → KeyboardInterrupt
	case 0x10:
		return "up", nil
	case 0x0e:
		return "down", nil
	case '\r':
		return "enter", nil
	case 0x1b:
		c2, err := next()
		if err != nil {
			return "escape", nil
		}
		if c2 != '[' && c2 != 'O' {
			return "escape", nil
		}
		c3, err := next()
		if err != nil {
			return "escape", nil
		}
		switch c3 {
		case 'A':
			return "up", nil
		case 'B':
			return "down", nil
		}
		return string([]byte{0x1b, c2, c3}), nil
	}
	return string(c), nil
}

// pickerFrame renders the selection panel (Panel(Table.grid(padding=(0,2)),
// title=[bold]prompt, border_style="cyan", padding=(1,2))) at width, with
// the segment styles rich writes under sys (NoColor: plain text).
func pickerFrame(keys []string, labels map[string]string, selected map[int]bool, cursor int, prompt string, width int, sys rich.ColorSystem) []string {
	help := "Use ↑/↓ to move, Space to toggle, Enter to confirm, Esc to cancel"
	seg := func(t, st string) rich.Segment { return rich.Segment{Text: t, Style: st} }
	type row struct {
		ptr  string
		cell []rich.Segment
	}
	var rows []row
	for i, k := range keys {
		ptr := " "
		if i == cursor {
			ptr = "▶"
		}
		var cell []rich.Segment
		if selected[i] {
			cell = []rich.Segment{seg("☑ ", "cyan"), seg(k, "cyan"), seg(" ", "cyan"), seg("("+labels[k]+")", "dim cyan")}
		} else {
			cell = []rich.Segment{seg("☐ ", "bright_black"), seg(k, "cyan"), seg(" ", "bright_black"), seg("("+labels[k]+")", "dim bright_black")}
		}
		rows = append(rows, row{ptr, cell})
	}
	rows = append(rows, row{"", nil}, row{"", []rich.Segment{seg(help, "dim white")}})
	cellLen := func(c []rich.Segment) int {
		n := 0
		for _, s := range c {
			n += rich.CellLen(s.Text)
		}
		return n
	}
	c2w := 0
	for _, r := range rows {
		if n := cellLen(r.cell); n > c2w {
			c2w = n
		}
	}
	inner := width - 2
	content := inner - 4
	render := func(segs ...rich.Segment) string { return rich.RenderSegments(segs, sys) }
	var lines []string
	title := prompt
	dashes := inner - rich.CellLen(" "+title+" ")
	if dashes < 0 {
		dashes = 0
	}
	left, right := dashes/2, dashes-dashes/2
	lines = append(lines, render(seg("╭─", "cyan"), seg(strings.Repeat("─", left-1), "cyan"), seg(" ", "cyan"), seg(title, "bold cyan"), seg(" ", "cyan"),
		seg(strings.Repeat("─", right-1), "cyan"), seg("─╮", "cyan")))
	blank := render(seg("│", "cyan"), seg(strings.Repeat(" ", inner), ""), seg("│", "cyan"))
	lines = append(lines, blank)
	for _, r := range rows {
		segs := []rich.Segment{seg("│", "cyan"), seg("  ", ""), seg(padCells(r.ptr, 3), "cyan"), seg("  ", "cyan")}
		segs = append(segs, r.cell...)
		if pad := c2w - cellLen(r.cell); pad > 0 {
			segs = append(segs, seg(strings.Repeat(" ", pad), "white"))
		}
		rest := content - (3 + 2 + c2w)
		if rest < 0 {
			rest = 0
		}
		segs = append(segs, seg(strings.Repeat(" ", rest+2), ""), seg("│", "cyan"))
		lines = append(lines, render(segs...))
	}
	lines = append(lines, blank)
	lines = append(lines, render(seg("╰"+strings.Repeat("─", inner)+"╯", "cyan")))
	return lines
}

func padCells(s string, n int) string {
	if d := n - rich.CellLen(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// multiSelectWithArrows is cli/ui.multi_select_with_arrows on a terminal.
func multiSelectWithArrows(w io.Writer, readKey func() (string, error), keys []string, labels map[string]string, prompt string, defaults []string) ([]string, error) {
	selected := map[int]bool{}
	for _, d := range defaults {
		for i, k := range keys {
			if k == d {
				selected[i] = true
			}
		}
	}
	if len(selected) == 0 && len(keys) > 0 {
		selected[0] = true
	}
	cursor := 0
	for i := range keys {
		if selected[i] {
			cursor = i
			break
		}
	}
	width := cli.Width()
	sys := rich.ColorSystemOf(w)
	fmt.Fprint(w, "\n")
	draw := func(first bool) int {
		lines := pickerFrame(keys, labels, selected, cursor, prompt, width, sys)
		if !first {
			fmt.Fprintf(w, "\r\x1b[%dA", len(lines)-1)
		}
		for i, l := range lines {
			fmt.Fprint(w, "\x1b[2K"+l)
			if i < len(lines)-1 {
				fmt.Fprint(w, "\n")
			}
		}
		return len(lines)
	}
	fmt.Fprint(w, "\x1b[?25l")
	height := draw(true)
	clear := func() {
		// Live.stop() refreshes once more before the transient erase.
		height = draw(false)
		fmt.Fprint(w, "\n\x1b[?25h\r"+strings.Repeat("\x1b[1A\x1b[2K", height))
	}
	for {
		k, err := readKey()
		if err != nil {
			clear()
			out(w, "\n[yellow]Selection cancelled[/yellow]")
			return nil, errSelectionCancelled
		}
		switch k {
		case "up":
			cursor = (cursor - 1 + len(keys)) % len(keys)
		case "down":
			cursor = (cursor + 1) % len(keys)
		case " ":
			selected[cursor] = !selected[cursor]
		case "enter":
			var cur []string
			for i, key := range keys {
				if selected[i] {
					cur = append(cur, key)
				}
			}
			if len(cur) > 0 {
				clear()
				return cur, nil
			}
		case "escape":
			clear()
			out(w, "\n[yellow]Selection cancelled[/yellow]")
			return nil, errSelectionCancelled
		}
		height = draw(false)
	}
}

// pickAgentsInteractive runs the arrow-key picker when stdin is a terminal;
// ok=false means no terminal (the caller falls back to a line prompt).
func pickAgentsInteractive(ctx *cli.Context) ([]string, bool, error) {
	if !fdIsTTY(os.Stdin.Fd()) {
		return nil, false, nil
	}
	if restore, ok := rawMode(os.Stdin.Fd()); !ok {
		return nil, false, nil
	} else {
		restore()
	}
	// readchar switches the terminal to raw mode for each key only.
	readKey := func() (string, error) {
		restore, ok := rawMode(os.Stdin.Fd())
		if ok {
			defer restore()
		}
		return readPickerKey(os.Stdin)
	}
	sel, err := multiSelectWithArrows(ctx.Stdout, readKey, agents.Keys, agents.DisplayNames, "Choose your AI assistant(s):", []string{"copilot"})
	return sel, true, err
}
