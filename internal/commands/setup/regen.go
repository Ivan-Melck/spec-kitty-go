package setup

import (
	"os"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
)

func init() {
	app.Register(func(root *cli.Command) {
		cmd := &cli.Command{
			Name:  "regen",
			Short: "Regenerate the committed generated agent-command + skill fixtures from source (#3447).",
			Flags: []*cli.Flag{
				{Name: "check", Kind: cli.Bool, Help: "Do not write; render into memory and byte-compare against the committed fixtures. Exit 1 when stale (prints the diff + the exact regen command), 0 when fresh. This is the fork-PR-safe freshness gate — a read-only token cannot commit back, so contributors run `spec-kitty regen` locally and push the result."},
				{Name: "json", Kind: cli.Bool, Help: "Emit a machine-readable JSON result instead of text."},
			},
		}
		cmd.Run = func(ctx *cli.Context) error { return runRegen(ctx, cmd) }
		root.Add(cmd)
	})
}

// sourceCheckoutRoot walks up from cwd for an upstream spec-kitty source
// checkout (packs/built-in/missions/mission-steps/software-dev + tests/specify_cli).
func sourceCheckoutRoot() string {
	cur, _ := os.Getwd()
	for {
		if exists(filepath.Join(cur, "packs", "built-in", "missions", "mission-steps", "software-dev")) && exists(filepath.Join(cur, "tests", "specify_cli")) {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// runRegen is the maintainer fixture regenerator. It only operates inside
// an upstream Python source checkout (its fixtures live under
// tests/specify_cli); everywhere else it refuses exactly like upstream. The
// Go port does not carry the Python fixture layout, so inside a source
// checkout it reports that the regeneration is not available.
func runRegen(ctx *cli.Context, cmd *cli.Command) error {
	if sourceCheckoutRoot() == "" {
		return cli.Usage(cmd, "Invalid value: spec-kitty regen must run inside a spec-kitty source checkout (could not locate packs/built-in/missions/mission-steps/software-dev + tests/specify_cli).")
	}
	out(ctx.Stderr, "[red]Error:[/red] fixture regeneration is a Python-source maintainer tool and is not available in the Go port.")
	return cli.Exit(1)
}
