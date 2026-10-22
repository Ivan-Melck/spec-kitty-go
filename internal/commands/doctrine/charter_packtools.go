package doctrine

import (
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// charter pack validate / assemble / regenerate-graph
// (cli/commands/charter/pack_tooling.py).

const packJSONHelp = "Emit machine-readable JSON instead of rich text."

func init() {
	packLeafBuilders = append(packLeafBuilders, packValidateCmd, packAssembleCmd, packRegenerateGraphCmd)
}

func packValidateCmd() *cli.Command {
	short, long := helpK("charter_pack_validate")
	return &cli.Command{
		Name: "validate", Short: short, Long: long,
		Args:  []*cli.Arg{{Name: "pack_path", Help: "Path to the Charter Pack directory to validate.", Required: true, Metavar: "pack_path", Type: "<path>"}},
		Flags: []*cli.Flag{{Name: "json", Kind: cli.Bool, Help: packJSONHelp}},
		Run: func(ctx *cli.Context) error {
			res := charter.ValidatePack(ctx.Arg("pack_path"), true)
			charter.RenderValidationResult(ctx.Stdout, res, ctx.Bool("json"))
			if !res.OK() {
				return cli.Exit(1)
			}
			return nil
		},
	}
}

func packAssembleCmd() *cli.Command {
	short, long := helpK("charter_pack_assemble")
	return &cli.Command{
		Name: "assemble", Short: short, Long: long,
		Args: []*cli.Arg{
			{Name: "output_path", Help: "Output directory for the assembled distributable pack.", Required: true, Metavar: "output_path", Type: "<path>"},
			{Name: "input_packs", Help: "One or more input pack directories to assemble.", Required: true, Variadic: true, Metavar: "input_packs", Type: "<path>"},
		},
		Flags: []*cli.Flag{
			{Name: "conflicts-out", Kind: cli.String, Metavar: "<path>", Help: "Write the conflict report to this path (JSON)."},
			{Name: "force", Kind: cli.Bool, Help: "Resolve artifact-id conflicts by last-pack-wins and drop duplicate DRG edges silently."},
			{Name: "json", Kind: cli.Bool, Help: packJSONHelp},
		},
		Run: func(ctx *cli.Context) error {
			out := ctx.Arg("output_path")
			inputs := ctx.ArgList("input_packs")
			res := charter.AssemblePack(inputs, out, ctx.Bool("force"), ctx.OptString("conflicts-out"))
			charter.RenderAssemblyResult(ctx.Stdout, res, out, inputs, ctx.Bool("json"))
			if !res.OK {
				return cli.Exit(1)
			}
			return nil
		},
	}
}

func packRegenerateGraphCmd() *cli.Command {
	short, long := helpK("charter_pack_regenerate-graph")
	return &cli.Command{
		Name: "regenerate-graph", Short: short, Long: long,
		Flags: []*cli.Flag{
			{Name: "check", Kind: cli.Bool, Help: "Do not write; regenerate into a temp directory and compare the per-kind graph fragments against the committed packs/built-in source. Exit 1 when stale (operator-runnable freshness gate). Exit 0 when fresh."},
			{Name: "pack-root", Kind: cli.String, Metavar: "<path>", Help: "Regenerate the DRG fragments and pack manifest for the BUILT-IN-level pack at this root instead of the shipped built-in pack (e.g. a vendored copy). The root must carry a pack.yaml declaring pack_level: built-in; org and project packs are refused. Applies to both the write path and --check. Defaults to the built-in pack root, leaving the no-flag behaviour byte-identical."},
			{Name: "json", Kind: cli.Bool, Help: packJSONHelp},
		},
		Run: runRegenerateGraph,
	}
}

func runRegenerateGraph(ctx *cli.Context) error {
	w := ctx.Stdout
	jsonMode := ctx.Bool("json")
	root := dct.BuiltInRoot()
	if pr := ctx.OptString("pack-root"); pr != nil {
		if msg, ok := charter.RequireBuiltInLevel(*pr); !ok {
			cprint(w, "%s", msg)
			return cli.Exit(1)
		}
		root = *pr
	}
	emit := func(status string) {
		if jsonMode {
			fmt.Fprintln(w, jsonx.Dumps(jsonx.NewObject("status", status, "path", root), jsonx.Options{Indent: -1, EnsureASCII: true}))
			return
		}
		switch status {
		case "written":
			cprint(w, "[green]Regenerated DRG graph:[/green] %s", root)
		case "fresh":
			cprint(w, "[green]DRG graph is fresh:[/green] %s", root)
		case "stale":
			cprint(w, "[red]DRG graph is stale:[/red] %s\nRun [bold]spec-kitty charter pack regenerate-graph[/bold] and commit the result.", root)
		}
	}
	if ctx.Bool("check") {
		if charter.BuiltInGraphIsFresh(root) {
			emit("fresh")
			return nil
		}
		emit("stale")
		return cli.Exit(1)
	}
	if err := charter.WriteBuiltInGraph(root); err != nil {
		return err
	}
	emit("written")
	return nil
}
