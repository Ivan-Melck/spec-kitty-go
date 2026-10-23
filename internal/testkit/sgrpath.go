package testkit

import (
	"regexp"
	"strings"
)

// replaceSGRTolerant replaces occurrences of path that rich's repr
// highlighter split into styled runs (SGR codes between its characters,
// e.g. a path's digits coloured as numbers) with repl. The SGR codes inside
// a match are dropped; those around it stay.
func replaceSGRTolerant(text, path, repl string) string {
	if path == "" || !strings.Contains(text, "\x1b[") {
		return text
	}
	parts := make([]string, 0, len(path))
	for _, r := range path {
		parts = append(parts, regexp.QuoteMeta(string(r)))
	}
	re := regexp.MustCompile(strings.Join(parts, `(?:\x1b\[[0-9;]*m)*`))
	return re.ReplaceAllLiteralString(text, repl)
}
