package doctrine

import (
	"fmt"
	"io"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// pyCrash renders an uncaught Python exception the way typer's pretty
// exception handler does on stderr, and exits 1. The framed traceback box
// (site-packages paths, source excerpts) cannot be reproduced; the header
// and the final "<Type>: <message>" line (wrapped at the console width) are.
// TODO(PYF-01): switch to rich.PrintTraceback once it lands.
func pyCrash(ctx *cli.Context, excType, msg string) error {
	io.WriteString(ctx.Stderr, "Traceback (most recent call last):\n")
	io.WriteString(ctx.Stderr, rich.Wrap(excType+": "+msg, rich.Width())+"\n")
	return cli.Exit(1)
}

// installCharterLog routes charter's logger.warning / warnings.warn records
// to stderr the way the CLI logging bootstrap does (RichHandler at WARNING),
// unless the invocation is --json (the bootstrap silences diagnostics then).
// Python warnings carry their source location: the module path relative to
// site-packages (the absolute install prefix cannot be reproduced).
func installCharterLog(ctx *cli.Context) {
	if ctx.IsSet("json") && ctx.Bool("json") {
		charter.WarnLog, charter.PyWarnLog = nil, nil
		return
	}
	charter.WarnLog = func(m string) { rich.LogWarning(ctx.Stderr, m) }
	charter.PyWarnLog = func(module string, line int, category, msg, source string) {
		rich.LogWarning(ctx.Stderr, fmt.Sprintf("%s:%d: %s: %s\n  %s\n", module, line, category, msg, source))
	}
}
