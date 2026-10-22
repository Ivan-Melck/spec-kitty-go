// Package encguard is the uncaught-exception path for charter-read encoding
// failures. Commands without their own handler let upstream's
// CharterEncodingError / UnicodeDecodeError escape as a traceback (exit 1);
// this wraps every command so the Go port does the same instead of crashing
// with a Go panic. Registered last (after every area's commands).
package encguard

import (
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
)

func init() {
	app.RegisterOrdered(1<<30, func(root *cli.Command) { wrap(root) })
}

func wrap(c *cli.Command) {
	if run := c.Run; run != nil {
		c.Run = func(ctx *cli.Context) error {
			var ret error
			err := charter.CatchLoad(func() { ret = run(ctx) })
			if err == nil {
				return ret
			}
			fmt.Fprintln(ctx.Stderr, "Traceback (most recent call last):")
			msg := err.Error() // UnicodeDecodeError text plus any PEP 678 notes
			if charter.IsCharterEncodingError(err) {
				msg = charter.ExceptionMessage(err) // str(exc) is the code
			}
			fmt.Fprintf(ctx.Stderr, "%s: %s\n", exceptionType(err), msg)
			return cli.Exit(1)
		}
	}
	for _, s := range c.Subcommands {
		wrap(s)
	}
}

func exceptionType(err error) string {
	if charter.IsCharterEncodingError(err) {
		return "CharterEncodingError"
	}
	return "UnicodeDecodeError"
}
