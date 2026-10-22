package rich

import (
	"fmt"
	"io"
	"strings"
)

// This file reproduces the uncaught-exception output of upstream's CLI:
// typer's except hook prints rich.traceback.Traceback (show_locals=False,
// word_wrap=True, width=None, typer/click frames omitted) on a stderr
// console. Each exception of the chain renders as a framed, full-width
// panel titled "Traceback (most recent call last)" followed by the
// "<ExcType>: <message>" line wrapped at the console width.
//
// The frames upstream shows (site-packages paths and source excerpts) cannot
// exist for a Go binary. Frames are therefore rendered the way rich renders a
// frame whose source file is not on disk ("in <function>:<line>"), from the
// upstream function names and line numbers a call site supplies.

// Frame is one stack frame of an upstream traceback.
type Frame struct {
	Name string // function name, e.g. "merge_driver_meta"
	Line int    // upstream line number
}

// Exception is one exception of a (possibly chained) traceback.
type Exception struct {
	// Type is the exception class. rich prints the class __name__, so a
	// dotted module prefix ("sqlite3.IntegrityError") is dropped.
	Type string
	// Value is str(exc); empty prints the bare type name.
	Value  string
	Frames []Frame
	Notes  []string
	// Cause is __cause__ ("raise X from cause"); Context is __context__
	// (raised while handling Context). Cause wins when both are set.
	Cause   *Exception
	Context *Exception
}

// Error is the plain "Type: value" form (the final traceback line).
func (e *Exception) Error() string {
	t := ShortTypeName(e.Type)
	if e.Value == "" {
		return t
	}
	return t + ": " + e.Value
}

// ShortTypeName drops a dotted module prefix from an exception class name,
// like rich does (it prints type.__name__).
func ShortTypeName(t string) string {
	if i := strings.LastIndexByte(t, '.'); i >= 0 {
		return t[i+1:]
	}
	return t
}

// ParseException splits a "Type: value" final traceback line (the form the
// Go ports used for their crash errors) into an Exception.
func ParseException(line string, frames ...Frame) *Exception {
	typ, val, ok := strings.Cut(line, ": ")
	if !ok || strings.ContainsAny(typ, " \n'\"(") {
		if !strings.ContainsAny(line, " \n'\"(:") {
			return &Exception{Type: line, Frames: frames}
		}
		return &Exception{Type: "Exception", Value: line, Frames: frames}
	}
	return &Exception{Type: typ, Value: val, Frames: frames}
}

// TracebackRenderable is rich.traceback.Traceback.__rich_console__ for exc:
// per exception of the chain (root cause first) a red framed panel titled
// "Traceback (most recent call last)" around its frames, the
// "<ExcType>: <message>" line (message repr-highlighted), [NOTE] lines and
// the chaining separator.
func TracebackRenderable(exc *Exception) Group {
	type stack struct {
		e       *Exception
		isCause bool
	}
	var stacks []stack
	isCause := false
	seen := map[*Exception]bool{}
	for e := exc; e != nil && !seen[e]; {
		seen[e] = true
		stacks = append(stacks, stack{e, isCause})
		switch {
		case e.Cause != nil:
			e, isCause = e.Cause, true
		case e.Context != nil:
			e, isCause = e.Context, false
		default:
			e = nil
		}
	}
	hl := ReprHighlighter{}
	var out Group
	for i := len(stacks) - 1; i >= 0; i-- {
		s := stacks[i]
		if len(s.e.Frames) > 0 {
			var body Group
			for k, f := range s.e.Frames {
				if k > 0 {
					body = append(body, "")
				}
				t := Assemble(AssemblePart{Plain: "in "}, AssemblePart{Plain: f.Name, Style: "pygments.function"},
					AssemblePart{Plain: ":", Style: "pygments.text"},
					AssemblePart{Plain: fmt.Sprint(f.Line), Style: "pygments.number"})
				t.Style = "pygments.text"
				body = append(body, t)
			}
			out = append(out, &Constrain{R: &RichPanel{R: body, Title: "[traceback.title]Traceback [dim](most recent call last)",
				BorderStyle: "traceback.border", Padding: []int{0, 1}}})
		}
		typ := ShortTypeName(s.e.Type)
		if s.e.Value != "" {
			out = append(out, Assemble(AssemblePart{Plain: typ + ": ", Style: "traceback.exc_type"},
				AssemblePart{Text: Highlight(hl, s.e.Value)}))
		} else {
			out = append(out, Assemble(AssemblePart{Plain: typ, Style: "traceback.exc_type"}))
		}
		for _, n := range s.e.Notes {
			out = append(out, Assemble(AssemblePart{Plain: "[NOTE] ", Style: "traceback.note"},
				AssemblePart{Text: Highlight(hl, n)}))
		}
		if i > 0 {
			if s.isCause {
				out = append(out, FromMarkup("\n[i]The above exception was the direct cause of the following exception:\n"))
			} else {
				out = append(out, FromMarkup("\n[i]During handling of the above exception, another exception occurred:\n"))
			}
		}
	}
	return out
}

// RenderTraceback returns the plain lines rich prints for exc at the given
// console width.
func RenderTraceback(exc *Exception, width int) []string {
	c := &Console{Width: width}
	s := strings.TrimSuffix(c.Output(c.Segments([]any{TracebackRenderable(exc)}, PrintOptions{})), "\n")
	return strings.Split(s, "\n")
}

// PrintTraceback writes exc's rich traceback to w (typer's stderr console).
func PrintTraceback(w io.Writer, exc *Exception) {
	TyperConsole(true).Print(w, TracebackRenderable(exc))
}

// Traceback is PrintTraceback for a single exception "typ: value".
func Traceback(w io.Writer, typ, value string, frames ...Frame) {
	PrintTraceback(w, &Exception{Type: typ, Value: value, Frames: frames})
}
