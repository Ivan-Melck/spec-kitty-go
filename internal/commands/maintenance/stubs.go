package maintenance

import (
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/cli"
)

// stub registers a not-yet-ported leaf so the command tree and help listing
// stay complete.
func stub(name, short string) *cli.Command {
	return &cli.Command{
		Name:  name,
		Short: short,
		Long:  longHelp["migrate_"+name],
		Run: func(ctx *cli.Context) error {
			fmt.Fprintf(ctx.Stderr, "Error: `migrate %s` is not supported by this build.\n", name)
			return cli.Exit(1)
		},
	}
}

func newBackfillRuntimeStateCmd() *cli.Command {
	return stub("backfill-runtime-state", "Seed legacy runtime state as events, verify fail-closed, and flip status_phase.")
}
