package planning

import (
	"github.com/spec-kitty/go-kitty3/internal/bulkedit"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

func finalizeBulkEditGate(ctx *cli.Context, dir string, jsonMode bool, emit func(*jsonx.Object)) error {
	r, err := bulkedit.Ready(dir)
	if err != nil {
		return err
	}
	if r.Passed {
		return nil
	}
	if jsonMode {
		emit(jsonx.NewObject("error", bulkedit.FinalizeBlockedMessage, "gate_errors", r.Errors))
	} else {
		rich.CliConsoleFor(ctx.Stdout, 0).Print(ctx.Stdout, bulkedit.FailurePanel(r))
	}
	return cli.Exit(1)
}
