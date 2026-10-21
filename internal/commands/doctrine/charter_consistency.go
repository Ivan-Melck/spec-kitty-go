package doctrine

import (
	"fmt"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// charter consistency-check (cli/commands/charter/consistency_check.py).

func init() { registerCharterLeaf(charterConsistencyCheckCmd) }

func charterConsistencyCheckCmd() *cli.Command {
	short, long := helpK("charter_consistency-check")
	return &cli.Command{
		Name: "consistency-check", Short: short, Long: long,
		Flags: []*cli.Flag{
			{Name: "json", Kind: cli.Bool, Help: "Output as JSON."},
			{Name: "repo-root", Kind: cli.String, Default: ".", Hidden: true},
		},
		Run: runCharterConsistencyCheck,
	}
}

func runCharterConsistencyCheck(ctx *cli.Context) error {
	w := ctx.Stdout
	root := ctx.String("repo-root")
	if root == "" || root == "." {
		root = cwd()
	} else {
		root, _ = filepath.Abs(root)
	}
	rep := charter.RunConsistencyCheck(root)
	if ctx.Bool("json") {
		fmt.Fprintln(w, jsonx.Dumps(rep.Object(), jsonx.Pretty))
	} else if rep.Coherent {
		cprint(w, "[green]Active charter is coherent.[/green]")
	} else {
		cprint(w, "[red]Consistency issues found:[/red]")
		for _, r := range rep.UnknownReferences {
			cprint(w, "  [red]Unknown reference:[/red] %s", r)
		}
		for _, r := range rep.MissingFromOffering {
			cprint(w, "  [yellow]Missing from charter.offering:[/yellow] %s", r)
		}
		for _, r := range rep.KindViolations {
			cprint(w, "  [red]Kind violation:[/red] %s", r)
		}
		for _, r := range rep.ReferenceIDDivergences {
			cprint(w, "  [red]Reference ID divergence:[/red] %s", r)
		}
		for _, r := range rep.GraphKindGaps {
			cprint(w, "  [red]Graph kind gap:[/red] %s", r)
		}
		for _, s := range rep.Suggestions {
			cprint(w, "  [dim]Suggestion:[/dim] %s", s)
		}
	}
	if !rep.Coherent {
		return cli.Exit(1)
	}
	return nil
}
