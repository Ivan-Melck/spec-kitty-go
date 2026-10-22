package doctrine

import (
	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// catchCharterLoad renders an exception raised by the charter-read encoding
// chokepoint (CharterEncodingError / UnicodeDecodeError) the way the
// command's generic `except Exception` handler does: _emit_error(...,
// unexpected=True), exit 1.
func catchCharterLoad(run func(*cli.Context) error) func(*cli.Context) error {
	return func(ctx *cli.Context) error {
		var ret error
		err := charter.CatchLoad(func() { ret = run(ctx) })
		if err == nil {
			return ret
		}
		msg := charter.ExceptionMessage(err)
		if ctx.Bool("json") {
			jsonOut(ctx.Stdout, jsonx.NewObject("error", msg, "result", "error", "success", false), jsonx.Line)
		} else {
			cprintRaw(ctx.Stdout, "Unexpected error: "+msg)
		}
		return cli.Exit(1)
	}
}
