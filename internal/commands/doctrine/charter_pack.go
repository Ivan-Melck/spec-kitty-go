package doctrine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

func init() { registerCharterLeaf(charterPackGroup) }

// packLeafBuilders collects charter pack subcommands (other files append).
var packLeafBuilders = []func() *cli.Command{packListCmd, packPathCmd, packAssetGroup}

var packOrder = []string{"list", "path", "validate", "assemble", "regenerate-graph", "asset"}

func charterPackGroup() *cli.Command {
	g := &cli.Command{Name: "pack", Short: "Charter pack management commands."}
	g.InvokeWithoutCommand = true
	g.Run = func(ctx *cli.Context) error {
		cli.WriteNoArgsIsHelp(ctx.Stdout, ctx.Cmd)
		return cli.Exit(2)
	}
	byName := map[string]*cli.Command{}
	for _, f := range packLeafBuilders {
		c := f()
		byName[c.Name] = c
	}
	for _, n := range packOrder {
		if c := byName[n]; c != nil {
			g.Add(c)
		}
	}
	return g
}

func packListCmd() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Short: "List the packs of the project's offering and the presets each ships (FR-004).",
		Flags: []*cli.Flag{
			{Name: "json", Kind: cli.Bool, Help: "Output as JSON."},
			{Name: "repo-root", Kind: cli.String, Default: ".", Hidden: true},
		},
		Run: runPackList,
	}
}

func packRepoRoot(ctx *cli.Context) string {
	rr := ctx.String("repo-root")
	if rr == "" || rr == "." {
		return cwd()
	}
	a, _ := filepath.Abs(rr)
	return a
}

func runPackList(ctx *cli.Context) error {
	root := packRepoRoot(ctx)
	jsonMode := ctx.Bool("json")
	_, inProject := os.Stat(filepath.Join(root, ".kittify"))
	var rows []any
	type row struct{ name, tier, presets string }
	var trows []row
	for _, p := range charter.ListOfferingPacks(root) {
		if p.Name == "project" && p.Tier == "project" && inProject != nil {
			continue
		}
		presets := []any{}
		var names []string
		if st, err := os.Stat(p.Root); p.ShipsPresets() && err == nil && st.IsDir() {
			for _, f := range charter.PresetFiles(p.Root) {
				pr, err := charter.LoadPresetFile(f)
				if err != nil {
					renderCharterErr(ctx.Stdout, err, jsonMode)
					return cli.Exit(1)
				}
				presets = append(presets, jsonx.NewObject("name", pr.Name, "description", pr.Description, "path", pr.Source))
				names = append(names, pr.Name)
			}
		}
		rows = append(rows, jsonx.NewObject("name", p.Name, "tier", p.Tier, "root", p.Root, "presets", presets))
		pn := strings.Join(names, ", ")
		if pn == "" {
			pn = "—"
		}
		trows = append(trows, row{p.Name, p.Tier, pn})
	}
	if jsonMode {
		if rows == nil {
			rows = []any{}
		}
		jsonOut(ctx.Stdout, jsonx.NewObject("packs", rows), jsonx.PrettyUTF8)
		return nil
	}
	t := &rtable{Title: "Charter packs", Headers: []string{"Pack", "Tier", "Presets"}}
	for _, r := range trows {
		t.Rows = append(t.Rows, []string{r.name, r.tier, r.presets})
	}
	t.render(ctx.Stdout, rich.Width())
	cprint(ctx.Stdout, "\n[dim]Apply a preset with `spec-kitty charter activate [--pack <pack>] --preset <preset>`.[/dim]")
	return nil
}

func packPathCmd() *cli.Command {
	return &cli.Command{
		Name:  "path",
		Short: "Print a pack's root, or with --preset the preset file (FR-006).",
		Long:  "Print a pack's root, or with --preset the preset file (FR-006).\n\nFails closed (exit 1) with PACK_NOT_FOUND or PRESET_NOT_FOUND, listing the\nvalid names.",
		Flags: []*cli.Flag{
			{Name: "preset", Kind: cli.String, Help: "Print this preset's file instead of the pack root."},
			{Name: "json", Kind: cli.Bool, Help: "Output as JSON."},
			{Name: "repo-root", Kind: cli.String, Default: ".", Hidden: true},
		},
		Args: []*cli.Arg{{Name: "pack", Help: "Pack name (built-in, an org pack name, or project).", Required: true, Metavar: "{pack}"}},
		Run: func(ctx *cli.Context) error {
			root := packRepoRoot(ctx)
			jsonMode := ctx.Bool("json")
			pack, err := charter.FindOfferingPack(root, ctx.Arg("pack"))
			var path string
			if err == nil {
				path = pack.Root
				if p := ctx.OptString("preset"); p != nil {
					pr, perr := charter.LoadPackPreset(pack, *p)
					if perr != nil {
						err = perr
					} else {
						path = pr.Source
					}
				}
			}
			if err != nil {
				renderCharterErr(ctx.Stdout, err, jsonMode)
				return cli.Exit(1)
			}
			if jsonMode {
				o := jsonx.NewObject("pack", pack.Name, "path", path)
				if p := ctx.OptString("preset"); p != nil {
					o.Set("preset", *p)
				}
				jsonOut(ctx.Stdout, o, jsonx.PrettyUTF8)
				return nil
			}
			fmt.Fprintln(ctx.Stdout, path)
			return nil
		},
	}
}

// ---- charter pack asset ------------------------------------------------------

type assetEntry struct {
	id, tier, path string
}

// listAssets reads every *.asset.yaml manifest across built-in, org packs
// and the project pack (more specific tiers win), sorted by id.
func listAssets() []assetEntry {
	byID := map[string]assetEntry{}
	scan := func(p *dct.PackFS, tier string) {
		for _, rel := range p.Walk("assets", ".asset.yaml", true) {
			b, err := p.ReadFile(rel)
			if err != nil {
				continue
			}
			o, err := yamlx.ParseObject(b)
			if err != nil || o == nil {
				continue
			}
			id := o.GetString("id")
			if id == "" {
				continue
			}
			bp := o.GetString("path")
			resolved := "<unresolvable: path escapes root>"
			if bp != "" && !strings.Contains(bp, "..") && !filepath.IsAbs(bp) {
				resolved = p.Path(bp)
			}
			byID[id] = assetEntry{id, tier, resolved}
		}
	}
	scan(dct.BuiltIn(), "built-in")
	if root := project.Locate(""); root != "" {
		for _, r := range dct.OrgRoots(root) {
			scan(dct.DiskPack(r), "org")
		}
		scan(dct.ProjectPack(root), "project")
	}
	var out []assetEntry
	for _, e := range byID {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func packAssetGroup() *cli.Command {
	g := &cli.Command{Name: "asset", Short: "Resolve shipped and overlay charter pack assets (no install — C-002)."}
	g.InvokeWithoutCommand = true
	g.Run = func(ctx *cli.Context) error {
		cli.WriteNoArgsIsHelp(ctx.Stdout, ctx.Cmd)
		return cli.Exit(2)
	}
	g.Add(&cli.Command{
		Name:  "list",
		Short: "List all resolvable charter pack assets and their source tiers.",
		Flags: []*cli.Flag{{Name: "json", Kind: cli.Bool, Help: "Emit machine-readable JSON instead of rich text."}},
		Run: func(ctx *cli.Context) error {
			rows := listAssets()
			if ctx.Bool("json") {
				l := []any{}
				for _, r := range rows {
					l = append(l, jsonx.NewObject("id", r.id, "tier", r.tier, "path", r.path))
				}
				fmt.Fprintln(ctx.Stdout, jsonx.Dumps(l, lineJSON))
				return nil
			}
			if len(rows) == 0 {
				cprint(ctx.Stdout, "[yellow]No charter pack assets found.[/yellow]")
				return nil
			}
			t := &rtable{Headers: []string{"ID", "TIER", "PATH"}, Styles: []string{"cyan", "green"}}
			for _, r := range rows {
				t.Rows = append(t.Rows, []string{r.id, r.tier, r.path})
			}
			t.render(ctx.Stdout, rich.Width())
			return nil
		},
	}, &cli.Command{
		Name:  "path",
		Short: "Resolve an asset identifier to a filesystem path (fail-closed on miss).",
		Long:  "Resolve an asset identifier to a filesystem path (fail-closed on miss).\n\nExits ``0`` and prints the path on success. An unknown id or a containment\nrefusal exits non-zero with the offending id named (A-7 / NFR-006).",
		Flags: []*cli.Flag{{Name: "json", Kind: cli.Bool, Help: "Emit machine-readable JSON instead of rich text."}},
		Args:  []*cli.Arg{{Name: "asset_id", Metavar: "ASSET_ID", Help: "Identifier of the asset to resolve (see `charter pack asset list`).", Required: true}},
		Run: func(ctx *cli.Context) error {
			id := ctx.Arg("asset_id")
			for _, r := range listAssets() {
				if r.id == id && !strings.HasPrefix(r.path, "<") {
					if ctx.Bool("json") {
						fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("id", id, "tier", r.tier, "path", r.path), jsonx.Pretty))
					} else {
						fmt.Fprintln(ctx.Stdout, r.path)
					}
					return nil
				}
			}
			msg := "Unknown asset id: " + id
			if ctx.Bool("json") {
				fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("id", id, "error", msg), jsonx.Pretty))
			} else {
				cprintRaw(ctx.Stdout, "[red]"+msg+"[/red]")
			}
			return cli.Exit(1)
		},
	})
	return g
}
