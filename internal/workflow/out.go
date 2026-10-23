// Package workflow implements the runtime-loop work-package actions
// (area F): lane workspace allocation for `implement`, the implement /
// review claims of `agent action implement|review`, the workflow commit
// receipts, lane auto-rebase after coordination commits and the
// implement/review prompt files.
package workflow

import (
	"bytes"
	"fmt"
	"io"

	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// Out is the command output: Print is Python print() (always stdout),
// Console is rich console.print (soft-wrapped at the console width; captured
// in --json mode like upstream's _json_safe_output wrapper).
type Out struct {
	W       io.Writer
	Err     io.Writer
	capture *bytes.Buffer
}

// NewOut returns an Out writing to w (stdout) and errw (stderr).
func NewOut(w, errw io.Writer) *Out { return &Out{W: w, Err: errw} }

// Capture redirects Console output into a buffer (json mode).
func (o *Out) Capture() { o.capture = &bytes.Buffer{} }

// Captured returns the captured console text.
func (o *Out) Captured() string {
	if o.capture == nil {
		return ""
	}
	return o.capture.String()
}

// Print writes a line like print().
func (o *Out) Print(s string) { fmt.Fprintln(o.W, s) }

// Printf is Print with formatting.
func (o *Out) Printf(format string, a ...any) { o.Print(fmt.Sprintf(format, a...)) }

// Console is console.print(s) on the CLI console (markup, highlighting,
// wrapping; ANSI styles on a colour terminal). In --json mode the console's
// file is an in-memory buffer but it keeps the colour system detected for
// stdout, like upstream's _json_safe_output capture.
func (o *Out) Console(s string) { o.ConsoleR(s) }

func (o *Out) ConsoleSoftWrap(s string) {
	c := rich.CliConsoleFor(o.W, 0)
	c.SoftWrap = true
	w := o.W
	if o.capture != nil {
		w = o.capture
	}
	c.Print(w, s)
}

// ConsoleR is console.print(r) for any renderable.
func (o *Out) ConsoleR(r any) {
	c := rich.CliConsoleFor(o.W, 0)
	w := o.W
	if o.capture != nil {
		w = o.capture
	}
	c.Print(w, r)
}

// Consolef is Console with formatting.
func (o *Out) Consolef(format string, a ...any) { o.Console(fmt.Sprintf(format, a...)) }

// Exit is a typer.Exit carrying an exit code (the message, if any, has
// already been printed).
type Exit struct{ Code int }

func (e *Exit) Error() string { return fmt.Sprintf("exit %d", e.Code) }

// ExitErr returns an *Exit error.
func ExitErr(code int) error { return &Exit{Code: code} }
