package rich

import (
	"os"
	"strconv"
	"strings"
)

// Console sizing and terminal detection as rich.console.Console does it.

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// IsTerminal is Console.is_terminal for a console writing to fd (1 =
// stdout, 2 = stderr): TTY_COMPATIBLE=0/1 decides, else a set FORCE_COLOR
// (non-empty = terminal), else whether fd is a TTY.
func IsTerminal(fd int) bool {
	switch os.Getenv("TTY_COMPATIBLE") {
	case "0":
		return false
	case "1":
		return true
	}
	if fc, ok := os.LookupEnv("FORCE_COLOR"); ok {
		return fc != ""
	}
	return isatty(fd)
}

// isDumbTerminal is Console.is_dumb_terminal.
func isDumbTerminal(fd int) bool {
	t := strings.ToLower(os.Getenv("TERM"))
	return (t == "dumb" || t == "unknown") && IsTerminal(fd)
}

// consoleWidth is Console(width=explicit).size.width for a console on fd.
func consoleWidth(fd int, explicit *int) int {
	fixed := explicit
	if fixed == nil {
		if c := os.Getenv("COLUMNS"); isDigits(c) {
			n, _ := strconv.Atoi(c)
			fixed = &n
		}
	}
	if isDumbTerminal(fd) {
		return 80
	}
	if fixed != nil {
		return *fixed
	}
	width := 0
	for _, f := range []int{0, 1, 2} {
		if w, ok := terminalWidth(f); ok {
			width = w
			break
		}
	}
	if width == 0 {
		width = 80
	}
	return width
}

// Width is the width of a default rich Console on stdout: COLUMNS when it
// is all digits (even below 20), else the size of the first terminal among
// stdin/stdout/stderr, else 80 (80 on a dumb terminal).
func Width() int { return consoleWidth(1, nil) }

// StderrWidth is Width for a Console(stderr=True).
func StderrWidth() int { return consoleWidth(2, nil) }

// typerWidth is typer.rich_utils.MAX_WIDTH (TERMINAL_WIDTH), nil if unset.
func typerWidth() *int {
	if v := os.Getenv("TERMINAL_WIDTH"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return &n
		}
	}
	return nil
}

// TyperWidth is the width of typer's rich consoles (help output on stdout,
// error panels and tracebacks on stderr): TERMINAL_WIDTH when set, else as
// Width.
func TyperWidth(stderr bool) int {
	fd := 1
	if stderr {
		fd = 2
	}
	return consoleWidth(fd, typerWidth())
}
