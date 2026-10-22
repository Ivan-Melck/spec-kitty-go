package doctrine

import (
	"fmt"
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

func init() { registerCharterLeaf(charterStatusCmd) }

func charterStatusCmd() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Short: "Display charter sync status plus synthesis/operator state.",
		Flags: []*cli.Flag{
			{Name: "json", Kind: cli.Bool, Help: "Output JSON"},
			{Name: "provenance", Kind: cli.Bool, Help: "Include per-artifact provenance details."},
		},
		Run: runCharterStatus,
	}
}

func runCharterStatus(ctx *cli.Context) error {
	w := ctx.Stdout
	jsonMode := ctx.Bool("json")
	root := repoRoot()
	fail := func(msg string) error {
		emitCharterError(w, jsonMode, msg, "", nil)
		return cli.Exit(1)
	}
	if fileExists(charter.Path(root, charter.CharterYAML)) {
		if err := charter.AssertBundleCompatible(root); err != nil {
			return fail(err.Error())
		}
	}
	syn, err := charter.CollectSynthesis(root, ctx.Bool("provenance"))
	if err != nil {
		return fail(err.Error())
	}
	payload := jsonx.NewObject("result", "success", "charter_sync", charter.CollectCharterSync(root), "synthesis", syn,
		"org_layer", charter.CollectOrgLayer(root), "governance_references", charter.CollectGovernanceReferences(root),
		"freshness", charter.ComputeFreshness(root))
	if jsonMode {
		jsonOut(w, payload, jsonx.Options{Indent: 2, EnsureASCII: true})
		return nil
	}
	p := func(x string) { cprint(w, "%s", x) }
	sync := payload.GetObject("charter_sync")
	p("[bold]Charter sync[/bold]")
	if b, _ := sync.Get("available"); b == true {
		p("Charter: " + sync.GetString("charter_path"))
		if sync.GetString("status") == "stale" {
			p("Status: [yellow]STALE[/yellow] (modified since last sync)")
			if s := sync.GetString("stored_hash"); s != "" {
				p("Expected hash: " + s)
			}
			p("Current hash:  " + sync.GetString("current_hash"))
			if r := payload.GetObject("freshness").GetObject("charter_source").GetString("remediation"); r != "" {
				p("\n[dim]Run: " + r + "[/dim]")
			}
		} else {
			p("Status: [green]SYNCED[/green]")
			if ls := sync.GetString("last_sync"); ls != "" {
				p("Last sync: " + ls)
			}
			p("Hash: " + sync.GetString("current_hash"))
		}
		lib, _ := sync.Get("library_docs")
		p(fmt.Sprintf("Library docs: %v", lib))
		if warns := payload.GetObject("governance_references").GetList("warnings"); len(warns) > 0 {
			p("\n[yellow]Governance reference warnings[/yellow]")
			for _, x := range warns {
				p("  - " + fmt.Sprint(x))
			}
		}
		p("\nExtracted files:")
		t := rich.NewTable()
		t.HeaderStyle = "bold"
		t.AddColumn("File", rich.Column{Style: "cyan"})
		t.AddColumn("Status", rich.Column{Justify: "center"})
		t.AddColumn("Size", rich.Column{Justify: "right"})
		for _, x := range sync.GetList("files") {
			f := x.(*jsonx.Object)
			ex, _ := f.Get("exists")
			sz, _ := f.Get("size_kb")
			if ex == true {
				t.AddRow(f.GetString("name"), "[green]Y[/green]", fmt.Sprintf("%.1f KB", sz.(float64)))
			} else {
				t.AddRow(f.GetString("name"), "[red]N[/red]", "[dim]-[/dim]")
			}
		}
		rich.PrintTo(w, t)
	} else {
		p("[yellow]Unavailable[/yellow]: " + sync.GetString("error"))
	}
	manifest, gen := syn.GetObject("manifest"), syn.GetObject("generated_inputs")
	ev, prov := syn.GetObject("evidence"), syn.GetObject("provenance")
	state := syn.GetString("generation_state")
	styleOf := func(m map[string]string, k string) string {
		if s, ok := m[k]; ok {
			return s
		}
		return "white"
	}
	ss := styleOf(map[string]string{"promoted": "green", "ready_for_validation": "yellow", "needs_attention": "red", "not_started": "blue"}, state)
	p("\n[bold]Synthesis[/bold]")
	p("Generation state: [" + ss + "]" + strings.ToUpper(state) + "[/" + ss + "]")
	counts := gen.GetObject("counts")
	g := func(o *jsonx.Object, k string) any { v, _ := o.Get(k); return v }
	p(fmt.Sprintf("Generated inputs: %v directive, %v tactic, %v styleguide under %s", g(counts, "directive"), g(counts, "tactic"), g(counts, "styleguide"), gen.GetString("path")))
	ms := styleOf(map[string]string{"valid": "green", "missing": "blue", "partial": "yellow", "invalid": "red"}, manifest.GetString("state"))
	p(fmt.Sprintf("Manifest: [%s]%s[/%s] (%s)", ms, strings.ToUpper(manifest.GetString("state")), ms, manifest.GetString("path")))
	if g(manifest, "exists") == true {
		if manifest.GetString("run_id") != "" && manifest.GetString("adapter_id") != "" && manifest.GetString("adapter_version") != "" {
			p(fmt.Sprintf("  Run: %s  Adapter: %s v%s", manifest.GetString("run_id"), manifest.GetString("adapter_id"), manifest.GetString("adapter_version")))
		}
		p(fmt.Sprintf("  Artifacts: %v (live doctrine files: %v)", g(manifest, "artifact_count"), g(manifest, "live_artifact_count")))
	}
	if e := manifest.GetString("error"); e != "" {
		p("  [red]Error:[/red] " + e)
	}
	if mp := manifest.GetList("missing_provenance_paths"); len(mp) > 0 {
		p("  Missing provenance paths:")
		for _, x := range mp {
			p("    [red]-[/red] " + fmt.Sprint(x))
		}
	}
	if code := ev.GetObject("code"); code != nil {
		p(fmt.Sprintf("Evidence: stack=%s (lang=%s, frameworks=%d, test_frameworks=%d)", code.GetString("stack_id"),
			code.GetString("primary_language"), len(code.GetList("frameworks")), len(code.GetList("test_frameworks"))))
	} else {
		p("Evidence: code signals unavailable")
	}
	snap := ev.GetString("corpus_snapshot_id")
	if snap == "" {
		snap = "(none)"
	}
	p(fmt.Sprintf("  Configured URLs: %v  Corpus snapshot: %s", g(ev, "configured_url_count"), snap))
	for _, x := range ev.GetList("warnings") {
		p("  [yellow]Warning:[/yellow] " + fmt.Sprint(x))
	}
	p(fmt.Sprintf("Provenance: %v visible sidecar(s)", g(prov, "parsed_count")))
	if mac, _ := g(prov, "manifest_artifact_count").(int); mac > 0 {
		miss, _ := g(prov, "missing_for_manifest_count").(int)
		p(fmt.Sprintf("  Manifest coverage: %d/%d", mac-miss, mac))
	}
	if ids := prov.GetList("corpus_snapshot_ids"); len(ids) > 0 {
		var s []string
		for _, x := range ids {
			s = append(s, fmt.Sprint(x))
		}
		p("  Corpus snapshots: " + strings.Join(s, ", "))
	}
	for _, x := range prov.GetList("warnings") {
		p("  [yellow]Warning:[/yellow] " + fmt.Sprint(x))
	}
	if ctx.Bool("provenance") && len(prov.GetList("entries")) > 0 {
		p("\nProvenance entries:")
		t := rich.NewTable()
		t.HeaderStyle = "bold"
		t.AddColumn("Kind", rich.Column{Style: "cyan"})
		t.AddColumn("Slug", rich.Column{Style: "cyan"})
		t.AddColumn("Artifact URN", rich.Column{Style: "magenta"})
		for _, h := range []string{"Adapter", "Corpus", "Evidence Hash"} {
			t.AddColumn(h, rich.Column{})
		}
		for _, x := range prov.GetList("entries") {
			e := x.(*jsonx.Object)
			h := e.GetString("evidence_bundle_hash")
			if len(h) > 12 {
				h = h[:12]
			}
			if h == "" {
				h = "-"
			}
			corpus := e.GetString("corpus_snapshot_id")
			if corpus == "" {
				corpus = "-"
			}
			t.AddRow(fmt.Sprint(g(e, "kind")), fmt.Sprint(g(e, "slug")), fmt.Sprint(g(e, "artifact_urn")),
				fmt.Sprintf("%v v%v", g(e, "adapter_id"), g(e, "adapter_version")), corpus, h)
		}
		rich.PrintTo(w, t)
	}
	org := payload.GetObject("org_layer")
	p("\n[bold]Organisation Layer[/bold]")
	p("  [dim]built-in[/dim]: [green]present[/green]")
	if packs := org.GetList("packs"); len(packs) > 0 {
		for _, x := range packs {
			pk := x.(*jsonx.Object)
			name := pk.GetString("name")
			if _, ok := pk.Get("name"); !ok {
				name = "unknown"
			}
			if f, ok := pk.Get("fetched"); ok && f == false {
				p(fmt.Sprintf("  [red]org:%s[/red]: [red]MISSING[/red] — %s", name, pk.GetString("source_ref")))
				continue
			}
			p(fmt.Sprintf("  [green]org:%s[/green]: [dim]%s[/dim] (%v nodes, %v edges)", name, pk.GetString("source_ref"), g(pk, "node_count"), g(pk, "edge_count")))
		}
		for _, x := range org.GetList("collision_warnings") {
			c := x.(*jsonx.Object)
			p(fmt.Sprintf("  [yellow]collision[/yellow]: %v target=%v resolution=%v", g(c, "kind"), g(c, "target_id"), g(c, "resolution")))
		}
	} else {
		p("  org: [dim](no packs configured)[/dim]")
	}
	for _, x := range org.GetList("errors") {
		p("  [red]Error:[/red] " + fmt.Sprint(x))
	}
	fr := payload.GetObject("freshness")
	p("\n[bold]Freshness[/bold]")
	fs := map[string]string{"fresh": "green", "stale": "yellow", "missing": "blue", "built_in_only": "cyan", "invalid": "red"}
	for _, l := range [][2]string{{"Charter source ", "charter_source"}, {"Synced bundle  ", "synced_bundle"}, {"Synthesized DRG", "synthesized_drg"}} {
		sub := fr.GetObject(l[1])
		c := styleOf(fs, sub.GetString("state"))
		line := "  " + l[0] + ": [" + c + "]" + strings.ToUpper(sub.GetString("state")) + "[/" + c + "]"
		if lc := sub.GetString("last_change"); lc != "" {
			line += "  [dim]last_change=" + lc + "[/dim]"
		}
		p(line)
		if d := sub.GetString("detail"); d != "" {
			p("    [dim]" + d + "[/dim]")
		}
		if r := sub.GetString("remediation"); r != "" {
			p("    [dim]Run: " + r + "[/dim]")
		}
	}
	return nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
