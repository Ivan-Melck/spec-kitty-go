package setup

import (
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// initLive is init's `with Live(tracker.render(), console=_console,
// refresh_per_second=8, transient=True)` as rich 15 draws it through
// specify_cli's CliConsole on an interactive (terminal, non-dumb) stdout:
//
//   - start hides the cursor and draws the tree as it is when Live starts;
//   - every console.print inside the block writes its text, then the live
//     render again (the CliConsole segment sanitizer strips the
//     position_cursor control codes, so frames are appended, never
//     redrawn in place);
//   - stop draws the final tree (vertical overflow visible), a newline,
//     shows the cursor and erases the last frame's height (restore_cursor).
//
// tracker.attach_refresh only calls live.update(refresh=False), so the
// frames drawn by prints show the renderable of the last refresh: the start
// tree here. The 8-per-second auto-refresh ticks are not emulated: their
// number and the state they catch depend on wall-clock timing (upstream
// draws none when the block finishes inside the first 125 ms tick).
// Intermediate frames taller than the console height are cropped with
// rich's centred "..." line. Off a terminal (or on a dumb one) nothing is
// drawn.
type initLive struct {
	w       io.Writer
	t       *tracker
	on      bool
	stopped bool
	shown   []string // the live render's renderable (last refresh)
	height  int      // LiveRender._shape height
}

func startLive(w io.Writer, t *tracker) *initLive {
	l := &initLive{w: w, t: t, on: liveTerminal(w)}
	if !l.on {
		return l
	}
	io.WriteString(w, "\x1b[?25l")
	l.shown = t.lines(w)
	l.frame(false)
	return l
}

// liveTerminal is console.is_terminal and not console.is_dumb_terminal.
func liveTerminal(w io.Writer) bool {
	term := strings.ToLower(os.Getenv("TERM"))
	return rich.ForWriter(w).Terminal && term != "dumb" && term != "unknown"
}

// print runs one console.print inside the Live block.
func (l *initLive) print(fn func(w io.Writer)) {
	fn(l.w)
	if l.on && !l.stopped {
		l.frame(false)
	}
}

// stop is Live.__exit__ (idempotent).
func (l *initLive) stop() {
	if !l.on || l.stopped {
		return
	}
	l.stopped = true
	l.shown = l.t.lines(l.w)
	l.frame(true)
	if l.height > 0 {
		io.WriteString(l.w, "\n")
	}
	io.WriteString(l.w, "\x1b[?25h\r"+strings.Repeat("\x1b[1A\x1b[2K", l.height))
}

// frame writes the live render (no trailing newline).
func (l *initLive) frame(visible bool) {
	lines := l.shown
	if h := consoleHeight(); !visible && len(lines) > h {
		width := cli.Width()
		left := max(0, (width-3)/2)
		right := max(0, width-3-left)
		ell := rich.RenderStyled(strings.Repeat(" ", left)+"..."+strings.Repeat(" ", right), "bold red", rich.ColorSystemOf(l.w))
		lines = append(append([]string{}, lines[:max(0, h-1)]...), ell)
	}
	l.height = len(lines)
	io.WriteString(l.w, strings.Join(lines, "\n"))
}

// consoleHeight is rich Console().size.height: LINES when all digits, else
// the rows of the first terminal among stdin/stdout/stderr, else 25.
func consoleHeight() int {
	if v := os.Getenv("LINES"); v != "" && strings.Trim(v, "0123456789") == "" {
		n, _ := strconv.Atoi(v)
		if n > 0 {
			return n
		}
	}
	h := 0
	for _, fd := range []uintptr{0, 1, 2} {
		if rows, ok := termRows(fd); ok {
			h = rows
			break
		}
	}
	if h == 0 {
		h = 25
	}
	return h
}
