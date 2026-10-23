package testkit

import (
	"regexp"
	"strings"
)

// reANSI also takes normalized SGR sequences (terminated by SGREnd).
var reANSI = regexp.MustCompile("\x1b\\[[0-9;?]*(?:[A-Za-z]|\x01)")

// StripANSI removes SGR / CSI escape sequences.
func StripANSI(s string) string { return reANSI.ReplaceAllString(s, "") }

// IsRichTraceback reports whether s contains a rich traceback panel
// (upstream's uncaught-exception output).
func IsRichTraceback(s string) bool {
	return strings.Contains(StripANSI(s), "Traceback (most recent call last)")
}

// MaskTracebackFrames collapses the body of every rich traceback panel in s
// into a single "│…│" line. Upstream's frames show site-packages paths and
// source excerpts a Go binary cannot reproduce (it renders sourceless
// "in <function>:<line>" frames instead), so parity is the panel borders
// (width, centred title), the wrapped "<Type>: <message>" lines and the
// chained-exception messages, all of which stay intact.
func MaskTracebackFrames(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	in := false
	for _, l := range lines {
		pl := StripANSI(l)
		switch {
		case strings.HasPrefix(pl, "╭") && strings.Contains(pl, "Traceback (most recent call last)"):
			in = true
			out = append(out, l, "│…│")
		case in && strings.HasPrefix(pl, "╰"):
			in = false
			out = append(out, l)
		case in:
		default:
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
