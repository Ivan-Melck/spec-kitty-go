package doctrine

import (
	"io"

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
