package doctrine

import (
	"fmt"
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/commands/util/uconsole"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
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
	sync := payload.GetObject("charter_sync")
	cprint(w, "[bold]Charter sync[/bold]")
	if b, _ := sync.Get("available"); b == true {
		cprintRaw(w, "Charter: "+sync.GetString("charter_path"))
		if sync.GetString("status") == "stale" {
			cprint(w, "Status: [yellow]STALE[/yellow] (modified since last sync)")
			if s := sync.GetString("stored_hash"); s != "" {
				cprintRaw(w, "Expected hash: "+s)
			}
			cprintRaw(w, "Current hash:  "+sync.GetString("current_hash"))
			if r := payload.GetObject("freshness").GetObject("charter_source").GetString("remediation"); r != "" {
				cprint(w, "%s", "\n[dim]Run: "+r+"[/dim]")
			}
		} else {
			cprint(w, "Status: [green]SYNCED[/green]")
			if ls := sync.GetString("last_sync"); ls != "" {
				cprintRaw(w, "Last sync: "+ls)
			}
			cprintRaw(w, "Hash: "+sync.GetString("current_hash"))
		}
		lib, _ := sync.Get("library_docs")
		cprintRaw(w, fmt.Sprintf("Library docs: %v", lib))
		if warns := payload.GetObject("governance_references").GetList("warnings"); len(warns) > 0 {
			cprint(w, "\n[yellow]Governance reference warnings[/yellow]")
			for _, x := range warns {
				cprintRaw(w, "  - "+fmt.Sprint(x))
			}
		}
		cprintRaw(w, "\nExtracted files:")
		t := &rtable{Headers: []string{"File", "Status", "Size"}, Center: []bool{false, true, false}, Right: []bool{false, false, true}}
		for _, x := range sync.GetList("files") {
			f := x.(*jsonx.Object)
			ex, _ := f.Get("exists")
			sz, _ := f.Get("size_kb")
			if ex == true {
				t.Rows = append(t.Rows, []string{f.GetString("name"), "Y", fmt.Sprintf("%.1f KB", sz.(float64))})
			} else {
				t.Rows = append(t.Rows, []string{f.GetString("name"), "N", "-"})
			}
		}
		t.render(w, uconsole.Width())
	} else {
		cprintRaw(w, "Unavailable: "+sync.GetString("error"))
	}
	manifest, gen := syn.GetObject("manifest"), syn.GetObject("generated_inputs")
	ev, prov := syn.GetObject("evidence"), syn.GetObject("provenance")
	state := syn.GetString("generation_state")
	cprint(w, "\n[bold]Synthesis[/bold]")
	cprintRaw(w, "Generation state: "+strings.ToUpper(state))
	counts := gen.GetObject("counts")
	g := func(o *jsonx.Object, k string) any { v, _ := o.Get(k); return v }
	cprintRaw(w, fmt.Sprintf("Generated inputs: %v directive, %v tactic, %v styleguide under %s", g(counts, "directive"), g(counts, "tactic"), g(counts, "styleguide"), gen.GetString("path")))
	cprintRaw(w, fmt.Sprintf("Manifest: %s (%s)", strings.ToUpper(manifest.GetString("state")), manifest.GetString("path")))
	if g(manifest, "exists") == true {
		if manifest.GetString("run_id") != "" && manifest.GetString("adapter_id") != "" && manifest.GetString("adapter_version") != "" {
			cprintRaw(w, fmt.Sprintf("  Run: %s  Adapter: %s v%s", manifest.GetString("run_id"), manifest.GetString("adapter_id"), manifest.GetString("adapter_version")))
		}
		cprintRaw(w, fmt.Sprintf("  Artifacts: %v (live doctrine files: %v)", g(manifest, "artifact_count"), g(manifest, "live_artifact_count")))
	}
	if e := manifest.GetString("error"); e != "" {
		cprintRaw(w, "  Error: "+e)
	}
	if mp := manifest.GetList("missing_provenance_paths"); len(mp) > 0 {
		cprintRaw(w, "  Missing provenance paths:")
		for _, p := range mp {
			cprintRaw(w, "    - "+fmt.Sprint(p))
		}
	}
	if code := ev.GetObject("code"); code != nil {
		cprintRaw(w, fmt.Sprintf("Evidence: stack=%s (lang=%s, frameworks=%d, test_frameworks=%d)", code.GetString("stack_id"),
			code.GetString("primary_language"), len(code.GetList("frameworks")), len(code.GetList("test_frameworks"))))
	} else {
		cprintRaw(w, "Evidence: code signals unavailable")
	}
	snap := ev.GetString("corpus_snapshot_id")
	if snap == "" {
		snap = "(none)"
	}
	cprintRaw(w, fmt.Sprintf("  Configured URLs: %v  Corpus snapshot: %s", g(ev, "configured_url_count"), snap))
	for _, x := range ev.GetList("warnings") {
		cprintRaw(w, "  Warning: "+fmt.Sprint(x))
	}
	cprintRaw(w, fmt.Sprintf("Provenance: %v visible sidecar(s)", g(prov, "parsed_count")))
	if mac, _ := g(prov, "manifest_artifact_count").(int); mac > 0 {
		miss, _ := g(prov, "missing_for_manifest_count").(int)
		cprintRaw(w, fmt.Sprintf("  Manifest coverage: %d/%d", mac-miss, mac))
	}
	if ids := prov.GetList("corpus_snapshot_ids"); len(ids) > 0 {
		var s []string
		for _, x := range ids {
			s = append(s, fmt.Sprint(x))
		}
		cprintRaw(w, "  Corpus snapshots: "+strings.Join(s, ", "))
	}
	for _, x := range prov.GetList("warnings") {
		cprintRaw(w, "  Warning: "+fmt.Sprint(x))
	}
	if ctx.Bool("provenance") && len(prov.GetList("entries")) > 0 {
		cprintRaw(w, "\nProvenance entries:")
		t := &rtable{Headers: []string{"Kind", "Slug", "Artifact URN", "Adapter", "Corpus", "Evidence Hash"}}
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
			t.Rows = append(t.Rows, []string{fmt.Sprint(g(e, "kind")), fmt.Sprint(g(e, "slug")), fmt.Sprint(g(e, "artifact_urn")),
				fmt.Sprintf("%v v%v", g(e, "adapter_id"), g(e, "adapter_version")), corpus, h})
		}
		t.render(w, uconsole.Width())
	}
	org := payload.GetObject("org_layer")
	cprint(w, "\n[bold]Organisation Layer[/bold]")
	cprintRaw(w, "  built-in: present")
	if packs := org.GetList("packs"); len(packs) > 0 {
		for _, x := range packs {
			p := x.(*jsonx.Object)
			cprintRaw(w, fmt.Sprintf("  org:%s: %s (%v nodes, %v edges)", p.GetString("name"), p.GetString("source_ref"), g(p, "node_count"), g(p, "edge_count")))
		}
		for _, x := range org.GetList("collision_warnings") {
			c := x.(*jsonx.Object)
			cprintRaw(w, fmt.Sprintf("  collision: %v target=%v resolution=%v", g(c, "kind"), g(c, "target_id"), g(c, "resolution")))
		}
	} else {
		cprintRaw(w, "  org: (no packs configured)")
	}
	for _, x := range org.GetList("errors") {
		cprintRaw(w, "  Error: "+fmt.Sprint(x))
	}
	fr := payload.GetObject("freshness")
	cprint(w, "\n[bold]Freshness[/bold]")
	for _, l := range [][2]string{{"Charter source ", "charter_source"}, {"Synced bundle  ", "synced_bundle"}, {"Synthesized DRG", "synthesized_drg"}} {
		sub := fr.GetObject(l[1])
		line := "  " + l[0] + ": " + strings.ToUpper(sub.GetString("state"))
		if lc := sub.GetString("last_change"); lc != "" {
			line += "  last_change=" + lc
		}
		cprintRaw(w, line)
		if d := sub.GetString("detail"); d != "" {
			cprintRaw(w, "    "+d)
		}
		if r := sub.GetString("remediation"); r != "" {
			cprintRaw(w, "    Run: "+r)
		}
	}
	return nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
