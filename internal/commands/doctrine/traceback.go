package doctrine

import (
	"fmt"
	"io"
	"os"

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

// jsonValueOptions is specify_cli._JSON_VALUE_OPTIONS (options whose value
// may itself be "--json").
var jsonValueOptions = map[string]bool{"--agent": true, "--answer": true, "--decision-id": true, "--feature": true,
	"--kind": true, "--mission": true, "--mode": true, "--provider": true, "--query": true, "--result": true,
	"--status": true, "--target": true, "--target-branch": true, "--tool": true}

// argsRequestJSONMode is specify_cli._args_request_json_mode.
func argsRequestJSONMode(args []string) bool {
	skip := false
	for _, a := range args {
		if skip {
			skip = false
			continue
		}
		if a == "--" {
			return false
		}
		if a == "--json" {
			return true
		}
		if len(a) > 2 && a[:2] == "--" && !containsRune(a, '=') {
			skip = jsonValueOptions[a]
			continue
		}
		skip = false
	}
	return false
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

// init is the CLI logging bootstrap (install_cli_logging_bootstrap): the
// charter modules' logger.warning / warnings.warn records reach stderr
// through a RichHandler at WARNING in human mode and are silenced when the
// command line carries --json. Python warnings carry their source location
// as the module path relative to site-packages (the absolute install prefix
// cannot be reproduced).
func init() {
	if argsRequestJSONMode(os.Args[1:]) {
		return
	}
	charter.WarnLog = func(m string) { rich.LogWarning(os.Stderr, m) }
	charter.PyWarnLog = func(module string, line int, category, msg, source string) {
		rich.LogWarning(os.Stderr, fmt.Sprintf("%s:%d: %s: %s\n  %s\n", module, line, category, msg, source))
	}
}
