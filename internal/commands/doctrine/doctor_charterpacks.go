package doctrine

import "github.com/spec-kitty/go-kitty3/internal/cli"

func doctorCharterPacksCmd() *cli.Command {
	return leaf("doctor_charter-packs", "charter-packs", []*cli.Flag{{Name: "json", Kind: cli.Bool, Help: machineJSONHelp}}, func(ctx *cli.Context) error { return cli.Exit(1) })
}
