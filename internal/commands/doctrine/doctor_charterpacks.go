package doctrine

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// doctor charter-packs (cli/commands/doctor.py charter_packs_check,
// _profile_health_render.py).

func doctorCharterPacksCmd() *cli.Command {
	return leaf("doctor_charter-packs", "charter-packs", []*cli.Flag{{Name: "json", Kind: cli.Bool, Help: machineJSONHelp}}, runDoctorCharterPacks)
}

func runDoctorCharterPacks(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root, err := doctorRoot(ctx, jsonMode, 1)
	if err != nil {
		return err
	}
	w := ctx.Stdout
	reg := charter.LoadPackRegistry(root)
	rep := charter.CollectCharterPackHealth(root)
	// Cross-grain scan: the shipped built-in missions are disjoint (no-op).
	// Operating-procedures scan: every built-in entry resolves.
	rep.OrgDRG.Set("operating_procedures_unresolved", []any{})
	charter.RunRetiredGovernanceKeyCheck(rep, root)
	charter.RunRetiredLayoutCheck(rep, root)
	charter.RunActivationLedgerCheck(rep, root)
	sel := charter.BuildSelectionBlock(root)
	code := 0
	if !rep.Healthy() {
		code = 1
	}
	if len(reg.Packs) == 0 {
		if jsonMode {
			emitCharterPacksJSON(w, rep, false, nil, nil, sel)
			return exitCode(code)
		}
		cprint(w, "[yellow]No org charter packs configured.[/yellow]")
		cprint(w, "Add a 'charter_packs.org' block to .kittify/config.yaml to register a pack.")
		fmt.Fprintln(w)
		for _, l := range charter.SelectionBlockLines(sel) {
			cprint(w, "%s", l)
		}
		renderCrossGrainFindings(w, rep)
		renderActivationLedgerFindings(w, rep)
		renderRetiredKeyFinding(w, rep)
		renderRetiredLayoutFindings(w, rep)
		return exitCode(code)
	}
	entries, err := charter.BuildPackEntries(root)
	if err != nil {
		return err
	}
	for _, p := range rep.Packs {
		if p.Layer == "org" {
			for _, e := range entries {
				if v, _ := e.Get("snapshot_present"); v == true {
					e.Set("pack_health", p.Object())
				}
			}
		}
	}
	collisions := charter.CollectLayerCollisions(root)
	if jsonMode {
		emitCharterPacksJSON(w, rep, true, entries, collisions, sel)
		return exitCode(code)
	}
	cprint(w, "\n[bold]Org Doctrine[/bold] — %d pack(s) configured\n", len(entries))
	for i, e := range entries {
		renderCharterPack(w, e, i)
	}
	if len(collisions) > 0 {
		cprint(w, "\n[bold]Collisions[/bold] — %d override(s) detected\n", len(collisions))
		for _, c := range collisions {
			cprint(w, "  • [yellow]%s[/yellow] %s: %s shadowed %s (%d replaced, %d inherited)", c.Kind, c.ItemID, c.Higher, c.Lower, c.Replaced, c.Inherited)
		}
	} else {
		cprint(w, "\n[dim]Collisions:[/dim] none — every artifact resolves from a single layer.")
	}
	renderOrgLayerSection(w, root)
	fmt.Fprintln(w)
	for _, l := range charter.SelectionBlockLines(sel) {
		cprint(w, "%s", l)
	}
	fmt.Fprintln(w)
	renderSanctionedOverrides(w, rep)
	renderOverridePolicyErrors(w, rep)
	renderUnsanctionedOverrides(w, rep)
	renderCrossGrainFindings(w, rep)
	renderActivationLedgerFindings(w, rep)
	renderRetiredKeyFinding(w, rep)
	renderRetiredLayoutFindings(w, rep)
	return exitCode(code)
}

func emitCharterPacksJSON(w io.Writer, rep *charter.CharterPackHealthReport, orgConfigured bool, entries []*jsonx.Object, collisions []charter.LayerCollision, sel map[string][]charter.SelectionEntry) {
	packs := []any{}
	for _, e := range entries {
		packs = append(packs, e)
	}
	rd := rep.Object()
	orgDRG, _ := rd.Get("org_drg")
	payload := jsonx.NewObject("org_configured", orgConfigured, "packs", packs, "selections", charter.SelectionBlockObject(sel),
		"org_drg", orgDRG, "profile_health", rd)
	if orgConfigured {
		cl := []any{}
		for _, c := range collisions {
			cl = append(cl, c.Object())
		}
		payload.Set("collisions", cl)
	}
	printJSONRich(w, payload)
}

func renderCharterPack(w io.Writer, e *jsonx.Object, idx int) {
	name := e.GetString("name")
	if name == "" {
		name = fmt.Sprintf("pack#%d", idx)
	}
	if v, _ := e.Get("snapshot_present"); v != true {
		cprint(w, "[yellow]Pack:[/yellow] %s  (snapshot missing at %s)", name, e.GetString("local_path"))
		return
	}
	version := e.GetString("pack_version")
	isGit, _ := e.Get("is_git_pack")
	parts := []string{"v" + version}
	if isGit == true {
		parts[0] = "git " + version
	}
	if c := e.GetObject("artifact_counts"); c != nil {
		for _, kv := range c.Pairs() {
			parts = append(parts, fmt.Sprintf("%v %s", kv.Value, kv.Key))
		}
	}
	healthy := false
	ph := e.GetObject("pack_health")
	if ph != nil {
		if v, _ := ph.Get("healthy"); v == true {
			healthy = true
		}
	}
	color, suffix := "green", ""
	if !healthy {
		color, suffix = "yellow", "  [yellow](degraded)[/yellow]"
	}
	cprint(w, "[%s]Pack:[/%s] %s  (%s)%s", color, color, name, strings.Join(parts, ", "), suffix)
	if ph != nil {
		if inv := ph.GetList("invalid_profiles"); len(inv) > 0 {
			cprint(w, "  [yellow]invalid profiles:[/yellow] %d skipped", len(inv))
			for _, x := range inv {
				o := x.(*jsonx.Object)
				cprintPlain(w, fmt.Sprintf("    • (%s) %s: %s", o.GetString("layer"), o.GetString("path"), o.GetString("error_summary")))
			}
		}
	}
	oc := e.GetObject("org_charter")
	if oc == nil {
		cprint(w, "  org-charter.yaml: [dim]not present[/dim]")
		return
	}
	if v, _ := oc.Get("present"); v != true {
		cprint(w, "  org-charter.yaml: [dim]not present[/dim]")
		return
	}
	if v, ok := oc.Get("module_available"); ok && v == false {
		cprint(w, "  org-charter.yaml: present (policy module not yet shipped)")
		return
	}
	n := func(k string) any {
		v, ok := oc.Get(k)
		if !ok {
			return 0
		}
		return v
	}
	cprint(w, "  org-charter.yaml: %v interview defaults, %v required directives, %v governance policies",
		n("interview_defaults_count"), n("required_directives_count"), n("governance_policies_count"))
}

const orgFindingsShown = 3

func renderOrgLayerSection(w io.Writer, root string) {
	cprint(w, "\n[bold]Organisation Layer[/bold] (WP07 / FR-007)")
	frags, err := charter.LoadOrgDRG(root)
	if err != nil {
		if _, ok := err.(*charter.OrgPackMissingError); ok {
			cprint(w, "  [red]org pack missing:[/red] %s", err.Error())
		} else {
			cprint(w, "  [red]org-DRG load error:[/red] %s", err.Error())
		}
		return
	}
	if len(frags) == 0 {
		cprint(w, "  (no organisation packs configured)")
		return
	}
	for _, f := range frags {
		cprint(w, "  - [green]%s[/green] [%s: %s] ✓ loaded (%d nodes, %d edges)", f.PackName, f.SourceKind, f.SourceRef, len(f.Nodes), len(f.Edges))
	}
	bi, err := charter.BuiltInMergedGraph()
	var merged *charter.MergedGraph
	if err == nil {
		merged, err = charter.MergeThreeLayers(bi, frags, nil)
	}
	if err != nil {
		if ce, ok := err.(*charter.OrgConflictError); ok {
			fatal, adv := ce.HardFailures(), ce.Advisory()
			if len(fatal) > 0 {
				cprint(w, "  [red]org-DRG REFUSED:[/red] %d fatal conflict(s) — the merged graph could not be assembled", len(fatal))
				renderConflictBullets(w, fatal, "red")
			}
			if len(adv) > 0 {
				cprint(w, "  collisions: %d built-in invariant override(s)", len(adv))
				renderConflictBullets(w, adv, "yellow")
			}
		} else {
			cprint(w, "  [yellow]org-layer merge checks skipped:[/yellow] %s", err.Error())
		}
		cprint(w, "  [yellow]dangling endpoints: not checked[/yellow] (no merged graph — see the merge findings above)")
		return
	}
	cprint(w, "  collisions: none")
	dangling := charter.ValidateDanglingReferences(merged)
	if len(dangling) == 0 {
		cprint(w, "  dangling endpoints: none")
		return
	}
	cprint(w, "  [red]dangling endpoints:[/red] %d org edge endpoint(s) name no node in any merged layer", len(dangling))
	for i, m := range dangling {
		if i >= orgFindingsShown {
			break
		}
		cprint(w, "    [red]•[/red] %s", m)
	}
	if len(dangling) > orgFindingsShown {
		cprint(w, "    … and %d more (run spec-kitty doctor charter-packs --json for the full list)", len(dangling)-orgFindingsShown)
	}
}

func renderConflictBullets(w io.Writer, cs []charter.OrgConflict, style string) {
	for i, c := range cs {
		if i >= orgFindingsShown {
			break
		}
		cprint(w, "    [%s]•[/%s] %s target=%s resolution=%s", style, style, c.Kind, c.TargetID, c.ResolutionApplied)
	}
	if len(cs) > orgFindingsShown {
		cprint(w, "    … and %d more (run charter lint for details)", len(cs)-orgFindingsShown)
	}
}

func orgDRGList(rep *charter.CharterPackHealthReport, key string) []any {
	v, _ := rep.OrgDRG.Get(key)
	l, _ := v.([]any)
	return l
}

func renderSanctionedOverrides(w io.Writer, rep *charter.CharterPackHealthReport) {
	var fs []*jsonx.Object
	for _, x := range orgDRGList(rep, "sanctioned_overrides") {
		if o, ok := x.(*jsonx.Object); ok {
			fs = append(fs, o)
		}
	}
	if len(fs) == 0 {
		return
	}
	cprint(w, "\n[dim]Sanctioned built-in override(s) — %d allowed[/dim]", len(fs))
	for _, f := range fs {
		source := "pack " + f.GetString("pack")
		if f.GetString("source") == "consumer" {
			source = "consumer allowlist"
		}
		reason := ""
		if r := f.GetString("reason"); r != "" {
			reason = ": " + r
		}
		line := fmt.Sprintf("  • %s (%s) sanctioned by %s%s", f.GetString("urn"), f.GetString("kind"), source, reason)
		cprintRawNoWrap(w, "[dim]"+rich.EscapeMarkup(line)+"[/dim]")
	}
}

func renderOverridePolicyErrors(w io.Writer, rep *charter.CharterPackHealthReport) {
	var msgs []string
	for _, m := range orgDRGList(rep, "pack_sanction_errors") {
		msgs = append(msgs, fmt.Sprint(m))
	}
	for _, m := range orgDRGList(rep, "errors") {
		if s := fmt.Sprint(m); strings.HasPrefix(s, charter.PolicyRelpath) {
			msgs = append(msgs, s)
		}
	}
	if len(msgs) == 0 {
		return
	}
	cprint(w, "\n[bold red]Override sanction file error(s)[/bold red] — %d\n", len(msgs))
	for _, m := range msgs {
		cprintRawNoWrap(w, "  • [red]"+rich.EscapeMarkup(m)+"[/red]")
	}
}

func renderUnsanctionedOverrides(w io.Writer, rep *charter.CharterPackHealthReport) {
	fs := orgDRGList(rep, "unsanctioned_overrides")
	if len(fs) == 0 {
		return
	}
	cprint(w, "\n[bold red]Unsanctioned built-in override(s)[/bold red] — %d not allowlisted\n", len(fs))
	for _, x := range fs {
		f, ok := x.(*jsonx.Object)
		if !ok {
			continue
		}
		cprintRaw(w, fmt.Sprintf("  • [red]%s[/red] (%s): %s", rich.EscapeMarkup(f.GetString("urn")), rich.EscapeMarkup(f.GetString("kind")), rich.EscapeMarkup(f.GetString("why"))))
		if lg := f.GetObject("legacy_template"); lg != nil {
			path := lg.GetString("path")
			if path == "" {
				path = charter.LegacyTemplateRelpath
			}
			target := filepath.Join(filepath.Dir(path), charter.PackPolicyFilename)
			if strings.HasSuffix(filepath.ToSlash(path), charter.LegacyTemplateRelpath) {
				target = filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(path))), charter.PackPolicyFilename)
			}
			cprintRawNoWrap(w, fmt.Sprintf("    [dim]pack author: move %s to %s (a template is not a sanction)[/dim]", rich.EscapeMarkup(path), rich.EscapeMarkup(target)))
			lines := charter.SanctionEntrySnippet(f.GetString("urn"), lg.GetString("reason"))
			if lines == nil {
				cprint(w, "    [dim]the template entry has no reason; a reason is required for a directive override[/dim]")
				continue
			}
			cprintRawNoWrap(w, "    [dim]consumer: add to "+rich.EscapeMarkup(charter.PolicyRelpath)+"[/dim]")
			for _, l := range lines {
				rich.PrintOpts(w, rich.PrintOptions{SoftWrap: rich.BoolP(true), Highlight: rich.BoolP(false)}, "      "+rich.EscapeMarkup(l))
			}
		}
	}
	cprintRawNoWrap(w, fmt.Sprintf("  [dim]Add the URN to %s (with a reason for directives), have the overriding pack ship it in its pack-root %s, or remove the org override.[/dim]", rich.EscapeMarkup(charter.PolicyRelpath), charter.PackPolicyFilename))
	cprint(w, "  [dim]Only org-tier overrides are adjudicated; project-tier (.kittify/charter-packs/) overrides are intentionally ungoverned (FR-012).[/dim]")
}

func renderRetiredKeyFinding(w io.Writer, rep *charter.CharterPackHealthReport) {
	f := rep.OrgDRG.GetObject("retired_governance_key")
	if f == nil {
		return
	}
	cprint(w, "\n[bold red]Retired charter key[/bold red] — %s is not read; the project's selections are ignored\n", f.GetString("key"))
	cprintPlainNoWrap(w, "  "+f.GetString("message"))
}

func renderRetiredLayoutFindings(w io.Writer, rep *charter.CharterPackHealthReport) {
	fs := orgDRGList(rep, "retired_layouts")
	if len(fs) == 0 {
		return
	}
	cprint(w, "\n[bold red]Retired layout[/bold red] — %d location(s) no longer read\n", len(fs))
	for _, x := range fs {
		if f, ok := x.(*jsonx.Object); ok {
			cprintPlainNoWrap(w, "  • "+f.GetString("message"))
		}
	}
}

func renderCrossGrainFindings(w io.Writer, rep *charter.CharterPackHealthReport) {
	fs := orgDRGList(rep, "cross_grain_collisions")
	if len(fs) == 0 {
		return
	}
	cprint(w, "\n[bold red]Cross-grain doctrine-integrity violation(s)[/bold red] — %d artifact(s) declared in both grains (FR-013)\n", len(fs))
	for _, x := range fs {
		if f, ok := x.(*jsonx.Object); ok {
			cprint(w, "  • [red]%s[/red] (%s): declared in both the type grain and an action grain for a shipped mission type.", f.GetString("artifact"), f.GetString("kind"))
		}
	}
	cprint(w, "  [dim]Remove the duplicate declaration — an artifact may appear in at most one grain.[/dim]")
}

// cprintRawNoWrap is console.print(..., soft_wrap=True).
func cprintRawNoWrap(w io.Writer, s string) { rich.PrintSoftWrap(w, s) }

// cprintPlainNoWrap is console.print(..., markup=False, soft_wrap=True).
func cprintPlainNoWrap(w io.Writer, s string) { rich.PrintPlainSoftWrap(w, s) }

func renderActivationLedgerFindings(w io.Writer, rep *charter.CharterPackHealthReport) {
	block := rep.OrgDRG.GetObject("activation_ledger")
	if block == nil {
		return
	}
	for _, severity := range []string{"errors", "warnings"} {
		findings := block.GetList(severity)
		if len(findings) == 0 {
			continue
		}
		style := "red"
		if severity == "warnings" {
			style = "yellow"
		}
		cprint(w, "\n[bold %s]Activation ledger %s[/bold %s] — %d\n", style, severity, style, len(findings))
		for _, raw := range findings {
			f := raw.(*jsonx.Object)
			cprintPlainNoWrap(w, "  • "+f.GetString("code")+": "+f.GetString("message"))
		}
	}
}
