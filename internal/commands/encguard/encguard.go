// Package encguard is the uncaught-exception path for charter-read encoding
// failures. Commands without their own handler let upstream's
// CharterEncodingError / UnicodeDecodeError escape as a traceback (exit 1);
// this wraps every command so the Go port does the same instead of crashing
// with a Go panic. Registered last (after every area's commands).
package encguard

import (
	"errors"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/charsetnorm"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/rich"
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
			exc := &rich.Exception{Type: exceptionType(err), Value: charter.ExceptionMessage(err)}
			if charter.IsCharterEncodingError(err) {
				exc.Frames = charterEncodingFrames
			} else {
				exc.Frames = bomDecodeFrames
			}
			var noted *charsetnorm.NotedError
			if errors.As(err, &noted) {
				exc.Notes = noted.Notes
			}
			rich.PrintTraceback(ctx.Stderr, exc)
			return cli.Exit(1)
		}
	}
	for _, s := range c.Subcommands {
		wrap(s)
	}
}

// The innermost upstream frames of each failure (charter/activation/_io.py,
// charter/encoding_recovery.py); the command's own outer frames vary.
var (
	charterEncodingFrames = []rich.Frame{{Name: "load_charter_file", Line: 121}, {Name: "_load_inner", Line: 175}}
	bomDecodeFrames       = []rich.Frame{{Name: "load_charter_file", Line: 121}, {Name: "_load_inner", Line: 172},
		{Name: "recover", Line: 101}, {Name: "_recover_from_bom", Line: 127}, {Name: "_bom_result", Line: 131}}
)

func exceptionType(err error) string {
	if charter.IsCharterEncodingError(err) {
		return "CharterEncodingError"
	}
	return "UnicodeDecodeError"
}
