package migrate

import (
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// pydError is one pydantic-core line error. The formatter below mirrors
// str(pydantic.ValidationError) (pydantic 2.14) for the few models this
// package validates; swap it for the shared internal/pydantic helper once
// that lands.
type pydError struct {
	loc   []any
	msg   string
	typ   string
	input any
}

// pydValidationError is str(pydantic.ValidationError) for title.
type pydValidationError struct {
	title  string
	errors []pydError
}

func (e *pydValidationError) Error() string {
	var b strings.Builder
	plural := "s"
	if len(e.errors) == 1 {
		plural = ""
	}
	fmt.Fprintf(&b, "%d validation error%s for %s", len(e.errors), plural, e.title)
	for _, fe := range e.errors {
		b.WriteString("\n")
		if len(fe.loc) > 0 {
			parts := make([]string, len(fe.loc))
			for i, l := range fe.loc {
				parts[i] = fmt.Sprint(l)
			}
			b.WriteString(strings.Join(parts, ".") + "\n")
		}
		fmt.Fprintf(&b, "  %s [type=%s, input_value=%s, input_type=%s]\n", fe.msg, fe.typ, pydTruncate(pyjson.Repr(fe.input)), pyjson.TypeName(fe.input))
		fmt.Fprintf(&b, "    For further information visit https://errors.pydantic.dev/2.14/v/%s", fe.typ)
	}
	return b.String()
}

// pydTruncate is pydantic-core's input_value truncation (first 25 and last
// 24 characters of a repr longer than 50).
func pydTruncate(s string) string {
	r := []rune(s)
	if len(r) <= 50 {
		return s
	}
	return string(r[:25]) + "..." + string(r[len(r)-24:])
}

func pydLoc(loc []any, more ...any) []any {
	return append(append([]any{}, loc...), more...)
}
