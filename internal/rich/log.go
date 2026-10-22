package rich

import (
	"fmt"
	"io"
	"strings"
)

// logKeywords is RichHandler.KEYWORDS.
var logKeywords = []string{"GET", "POST", "HEAD", "PUT", "DELETE", "OPTIONS", "TRACE", "PATCH"}

// LogRenderable is what upstream's CLI logging bootstrap prints for one
// record (rich.logging.RichHandler(show_time=False, show_path=False,
// markup=False)): a level column ("logging.level.<name>", 8 wide) and the
// repr-highlighted message with HTTP keywords highlighted, in an expanded
// grid.
func LogRenderable(level, msg string) any {
	t := NewGrid(0, 1)
	t.Expand = true
	t.AddColumn("", Column{Style: "log.level"})
	t.AddColumn("", Column{Ratio: IntP(1), Style: "log.message", Overflow: "fold"})
	lvl := TextStyled(fmt.Sprintf("%-8s", level), "logging.level."+strings.ToLower(level))
	m := Highlight(ReprHighlighter{}, msg)
	m.highlightWords(logKeywords, "logging.keyword")
	t.AddRow(lvl, Group{m})
	return t
}

// Log renders one record on the stderr console of the CLI logging
// bootstrap (CliConsole(stderr=True, highlight=False)).
func Log(w io.Writer, level, msg string) {
	c := ForWriter(w)
	c.Sanitize, c.NoHighlight = true, true
	c.Print(w, LogRenderable(level, msg))
}

// LogWarning is Log at WARNING level (logger.warning under the CLI bootstrap).
func LogWarning(w io.Writer, msg string) { Log(w, "WARNING", msg) }
