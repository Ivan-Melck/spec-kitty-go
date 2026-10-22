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

const (
	tracebackTitle = "Traceback (most recent call last)"
	causeMessage   = "\nThe above exception was the direct cause of the following exception:\n"
	contextMessage = "\nDuring handling of the above exception, another exception occurred:\n"
)

// RenderTraceback returns the lines rich prints for exc at the given
// console width (each line without its newline).
func RenderTraceback(exc *Exception, width int) []string {
	// rich's Trace.stacks: the raised exception first, then its causes;
	// rendered in reverse (root cause first).
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
	var out []string
	for i := len(stacks) - 1; i >= 0; i-- {
		s := stacks[i]
		if len(s.e.Frames) > 0 {
			out = append(out, tracebackPanel(s.e.Frames, width)...)
		}
		out = append(out, WrapText(s.e.Error(), width, "default", "fold", false)...)
		for _, n := range s.e.Notes {
			out = append(out, WrapText("[NOTE] "+n, width, "default", "fold", false)...)
		}
		if i > 0 {
			msg := contextMessage
			if s.isCause {
				msg = causeMessage
			}
			out = append(out, WrapText(msg, width, "default", "fold", false)...)
		}
	}
	return out
}

// tracebackPanel is the expanded Panel(padding=(0, 1)) with a centred title
// around the frame list.
func tracebackPanel(frames []Frame, width int) []string {
	var body []string
	for i, f := range frames {
		if i > 0 {
			body = append(body, "")
		}
		body = append(body, fmt.Sprintf("in %s:%d", f.Name, f.Line))
	}
	inner := max(0, width-4)
	var out []string
	title := " " + tracebackTitle + " "
	if width <= 4 {
		out = append(out, "╭"+strings.Repeat("─", max(0, width-2))+"╮")
	} else {
		t := truncate(title, width-4, "fold", false)
		excess := width - 4 - CellLen(t)
		left := excess / 2
		out = append(out, "╭─"+strings.Repeat("─", left)+t+strings.Repeat("─", excess-left)+"─╮")
	}
	for _, b := range body {
		for _, l := range WrapText(b, inner, "default", "fold", false) {
			out = append(out, "│ "+SetCellSize(l, inner)+" │")
		}
	}
	out = append(out, "╰"+strings.Repeat("─", max(0, width-2))+"╯")
	return out
}

// PrintTraceback writes exc's rich traceback to w (a stderr console) at
// the console width.
func PrintTraceback(w io.Writer, exc *Exception) {
	for _, l := range RenderTraceback(exc, Width()) {
		fmt.Fprintln(w, l)
	}
}

// Traceback is PrintTraceback for a single exception "typ: value".
func Traceback(w io.Writer, typ, value string, frames ...Frame) {
	PrintTraceback(w, &Exception{Type: typ, Value: value, Frames: frames})
}
