package setup

// The interactive agent picker (cli/ui.multi_select_with_arrows): a Rich
// Live panel redrawn on every key, read in raw mode from the terminal
// (readchar). Colours are not emitted (the Go console renders no SGR
// styling anywhere); the layout, keys and cancellation behaviour match.

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
// title=prompt, padding=(1,2))) at width.
func pickerFrame(keys []string, labels map[string]string, selected map[int]bool, cursor int, prompt string, width int) []string {
	help := "Use ↑/↓ to move, Space to toggle, Enter to confirm, Esc to cancel"
	type row struct{ c1, c2 string }
	var rows []row
	for i, k := range keys {
		ind := "☐"
		if selected[i] {
			ind = "☑"
		}
		ptr := " "
		if i == cursor {
			ptr = "▶"
		}
		rows = append(rows, row{ptr, ind + " " + k + " (" + labels[k] + ")"})
	}
	rows = append(rows, row{"", ""}, row{"", help})
	c2w := 0
	for _, r := range rows {
		if n := rich.CellLen(r.c2); n > c2w {
			c2w = n
		}
	}
	inner := width - 2
	content := inner - 4
	var lines []string
	title := " " + prompt + " "
	dashes := inner - rich.CellLen(title)
	if dashes < 0 {
		dashes = 0
	}
	lines = append(lines, "╭"+strings.Repeat("─", dashes/2)+title+strings.Repeat("─", dashes-dashes/2)+"╮")
	blank := "│" + strings.Repeat(" ", inner) + "│"
	lines = append(lines, blank)
	for _, r := range rows {
		cell := padCells(r.c1, 3) + "  " + padCells(r.c2, c2w)
		lines = append(lines, "│  "+padCells(cell, content)+"  │")
	}
	lines = append(lines, blank)
	lines = append(lines, "╰"+strings.Repeat("─", inner)+"╯")
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
	fmt.Fprint(w, "\n")
	draw := func(first bool) int {
		lines := pickerFrame(keys, labels, selected, cursor, prompt, width)
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
