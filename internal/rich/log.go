package rich

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Log renders one record the way upstream's CLI logging bootstrap does
// (rich.logging.RichHandler(show_time=False, show_path=False, markup=False)
// on stderr): an 8-column level name, a space, then the message wrapped to
// the remaining width; every line padded to the console width.
func Log(w io.Writer, level, msg string) {
	width := Width()
	msgWidth := width - 9
	if msgWidth < 1 {
		msgWidth = 1
	}
	for i, line := range strings.Split(Wrap(msg, msgWidth), "\n") {
		prefix := strings.Repeat(" ", 9)
		if i == 0 {
			prefix = fmt.Sprintf("%-8s ", level)
		}
		text := prefix + line
		if n := utf8.RuneCountInString(text); n < width {
			text += strings.Repeat(" ", width-n)
		}
		fmt.Fprintln(w, text)
	}
}

// LogWarning is Log at WARNING level (logger.warning under the CLI bootstrap).
func LogWarning(w io.Writer, msg string) { Log(w, "WARNING", msg) }
