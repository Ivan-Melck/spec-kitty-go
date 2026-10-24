package doctrine

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/toolsurface"
)

const resynthesizeHelp = "Eagerly refresh the FULL derived bundle/DRG (bundle content hash + project DRG layer, not just the compiled catalog) after this activation via the EXISTING synthesize pipeline (the same one `charter generate` + `charter synthesize` use). Default activation already recompiles `catalog.references` on its own via `charter generate`'s own compile seam (see --no-compile to opt out of that lightweight recompile); --resynthesize goes further and reconciles the freshness signal to fresh immediately (NFR-001)."
const noCompileHelp = "Skip the default post-activation catalog recompile (FR-003): the fast config-only write from before issue #4785's fix. `catalog.references` is left as-is and may go stale until a later `charter generate` or `charter activate --resynthesize`."

func charterActivateCmd() *cli.Command {
	return &cli.Command{
		Name:  "activate",
		Short: "Activate a doctrine artifact by kind and ID (FR-004), or apply a pack's preset.",
		Long:  "Activate a doctrine artifact by kind and ID (FR-004), or apply a pack's preset.\n\nTwo forms:\n\n  spec-kitty charter activate KIND ARTIFACT_ID [--cascade SCOPE]\n\n  spec-kitty charter activate [--pack PACK] --preset PRESET [--force] [--json]\n\nA preset replaces every activation key it governs: keys it lists are\nwritten (plus the org's required ids), keys it leaves out are removed.\nA change to a customised key is refused without --force.",
		Flags: []*cli.Flag{
			{Name: "cascade", Kind: cli.String, Help: "Cascade activation scope: 'all' for every referenced kind, or a comma-separated kind list (e.g. 'agent-profile,tactic'). Omit to skip cascade (referenced artifacts are reported as a warning)."},
			{Name: "resynthesize", Kind: cli.Bool, Negatable: true, Default: false, Help: resynthesizeHelp},
			{Name: "compile", Kind: cli.Bool, Negatable: true, Default: true, Help: noCompileHelp},
			{Name: "preset", Kind: cli.String, Help: "Apply this activation preset of the pack with replace semantics instead of activating one artifact."},
			{Name: "pack", Kind: cli.String, Default: "built-in", Help: "Pack whose preset --preset applies (built-in, an org pack name). Only with --preset."},
			{Name: "force", Kind: cli.Bool, Help: "Apply the preset even when it changes a customised activation key. Only with --preset."},
			{Name: "json", Kind: cli.Bool, Help: "Output the applied preset as JSON. Only with --preset."},
			{Name: "repo-root", Kind: cli.String, Default: ".", Hidden: true},
		},
		Args: []*cli.Arg{
			{Name: "kind", Help: "Activation kind (e.g. directive, agent-profile).", Metavar: "kind"},
			{Name: "artifact_id", Help: "Artifact ID to activate.", Metavar: "artifact_id"},
		},
		Run: runCharterActivate,
	}
}

func charterDeactivateCmd() *cli.Command {
	return &cli.Command{
		Name:  "deactivate",
		Short: "Deactivate a doctrine artifact by kind and ID (FR-005), with optional cascade.",
		Flags: []*cli.Flag{
			{Name: "cascade", Kind: cli.String, Help: "Cascade deactivation scope: 'all' for every exclusively-referenced kind, or a comma-separated kind list. Shared artifacts are never removed. Omit to deactivate only the named artifact."},
			{Name: "resynthesize", Kind: cli.Bool, Negatable: true, Default: false, Help: resynthesizeHelp},
			{Name: "compile", Kind: cli.Bool, Negatable: true, Default: true, Help: noCompileHelp},
			{Name: "repo-root", Kind: cli.String, Default: ".", Hidden: true},
		},
		Args: []*cli.Arg{
			{Name: "kind", Help: "Activation kind (e.g. directive, agent-profile).", Metavar: "kind"},
			{Name: "artifact_id", Help: "Artifact ID to deactivate.", Metavar: "artifact_id"},
		},
		Run: runCharterDeactivate,
	}
}

func validKindsListing() string {
	ks := append([]string(nil), dct.CharterKindTokens...)
	sort.Strings(ks)
	return strings.Join(ks, ", ")
}

// cascadeScope is CascadeScope.parse: nil = no cascade, all=true = every kind.
type cascadeScope struct {
	all   bool
	kinds map[string]bool // underscore kind values
}

func parseCascade(raw *string) (*cascadeScope, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	s := strings.TrimSpace(*raw)
	if strings.ToLower(s) == "all" {
		return &cascadeScope{all: true}, nil
	}
	sc := &cascadeScope{kinds: map[string]bool{}}
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		k, err := dct.ParseKind(tok)
		if err != nil {
			return nil, err
		}
		sc.kinds[string(k)] = true
	}
	if len(sc.kinds) == 0 {
		return nil, nil
	}
	return sc, nil
}

func (s *cascadeScope) selects(kind string) bool { return s.all || s.kinds[kind] }

func charterRoot(ctx *cli.Context) string {
	rr := ctx.String("repo-root")
	if rr == "" || rr == "." {
		return repoRoot()
	}
	if r := project.Locate(rr); r != "" {
		return r
	}
	return rr
}

func runCharterActivate(ctx *cli.Context) error {
	w := ctx.Stdout
	preset := ctx.OptString("preset")
	kind, id := ctx.Arg("kind"), ctx.Arg("artifact_id")
	positional := ctx.HasArg("kind") || ctx.HasArg("artifact_id")
	if preset != nil {
		if positional {
			return cli.Usage(ctx.Cmd, "--preset cannot be combined with a positional KIND ARTIFACT_ID.")
		}
		if ctx.IsSet("cascade") {
			return cli.Usage(ctx.Cmd, "--cascade cannot be combined with --preset (presets do not cascade).")
		}
		return runApplyPreset(ctx, *preset)
	}
	var given []string
	for _, f := range [][2]string{{"pack", "--pack"}, {"force", "--force"}, {"json", "--json"}} {
		if ctx.IsSet(f[0]) {
			given = append(given, f[1])
		}
	}
	if len(given) > 0 {
		verb := "applies"
		if len(given) > 1 {
			verb = "apply"
		}
		return cli.Usage(ctx.Cmd, "%s only %s with --preset.", strings.Join(given, ", "), verb)
	}
	if !ctx.HasArg("kind") || !ctx.HasArg("artifact_id") {
		cli.WriteHelp(w, ctx.Cmd)
		return nil
	}
	if !charter.ValidKindToken(kind) {
		cprint(w, "[red]Error:[/red] Unknown kind '%s'. Valid kinds: %s.", kind, validKindsListing())
		return cli.Exit(1)
	}
	if err := writeRootError(); err != nil {
		cprint(w, "[red]Error:[/red] %s", err.Error())
		return cli.Exit(1)
	}
	root := charterRoot(ctx)
	scope, err := parseCascade(ctx.OptString("cascade"))
	if err != nil {
		cprint(w, "[red]Error:[/red] %s", err.Error())
		return cli.Exit(1)
	}
	if _, err := dct.LoadActivation(root); err != nil {
		var ce *dct.ConfigError
		if errors.As(err, &ce) {
			cprint(w, "[red]Error[/red] (%s): %s", ce.Code(), ce.Body)
			return cli.Exit(1)
		}
	}
	if kind == dct.MissionTypeToken {
		// FR-008 step-removal warnings, then the FR-001 preflight; both
		// surface the strict roster's ValueErrors as "Error: ...".
		rc := &missionTypeRosterCache{root: root}
		lines, err := stepRemovalWarnings(root, id, rc)
		if err == nil {
			for _, l := range lines {
				cprint(w, "%s", l)
			}
			err = validateActivatableMissionType(root, id, rc)
		}
		if err != nil {
			cprint(w, "[red]Error:[/red] %s", err.Error())
			return cli.Exit(1)
		}
	}
	reg, err := charter.PlanProjectRegistration(root, app.Version)
	if err != nil {
		// activate_cmd catches (ValueError, DRGLoadError) only.
		if charter.IsUnexpectedError(err) {
			if _, isLoad := err.(*charter.DRGLoadError); !isLoad {
				return pyCrash(ctx, charter.PyExceptionClass(err), err.Error(),
					crashFrames(err, rich.Frame{Name: "activate_cmd", Line: 976}, rich.Frame{Name: "plan_project_registration", Line: 265})...)
			}
		}
		cprint(w, "[red]Error:[/red] %s", err.Error())
		return cli.Exit(1)
	}
	if ctx.Bool("resynthesize") {
		if err := preflightResynthesis(root, kind, id, scope); err != nil {
			cprint(w, "[red]Error:[/red] %s", err.Error())
			return cli.Exit(1)
		}
	}
	// #5779/#6006: the availability scan reads the whole org-pack chain
	// strictly (resolve_pack_chain(strict=True), fail-closed on a
	// declared-but-unfetched pack, #4984).
	if _, err := charter.RequireDeclaredOrgRoots(root); err != nil {
		cprint(w, "[red]Error:[/red] %s", err.Error())
		return cli.Exit(1)
	}
	res, err := charter.Activate(root, kind, id)
	if err != nil {
		cprint(w, "[red]Error:[/red] %s", err.Error())
		return cli.Exit(1)
	}
	if err := charter.CommitProjectRegistration(reg); err != nil {
		cprint(w, "[red]Error:[/red] %s", err.Error())
		return cli.Exit(1)
	}
	for _, a := range res.Activated {
		cprint(w, "[green]Activated[/green]: %s", a)
	}
	for _, m := range append(append([]string{}, reg.Warnings...), res.Warnings...) {
		cprint(w, "[yellow]Warning[/yellow]: %s", m)
	}
	renderTensionWarnings(ctx, root)
	if src := charter.SourceURN(root, kind, id); src != "" {
		if err := renderActivationCascade(ctx, root, src, scope); err != nil {
			return err
		}
	}
	if err := recompileOrNotify(ctx, root); err != nil {
		return err
	}
	return reprojectPackSkills(ctx, root, kind)
}

// reprojectPackSkills is activate.reproject_pack_skills: after a skill
// (de)activation the managed installer re-projects the pack skills in force.
func reprojectPackSkills(ctx *cli.Context, root, kind string) error {
	if kind != string(dct.Skill) {
		return nil
	}
	w := ctx.Stdout
	if toolsurface.LogWriter == nil {
		toolsurface.LogWriter = ctx.Stderr
		defer func() { toolsurface.LogWriter = nil }()
	}
	p, err := toolsurface.ReprojectPackSkills(root)
	if err != nil {
		cprint(w, "[red]Error:[/red] pack skills were not projected: %s. The activation change is recorded; fix the cause and re-run this command.", err.Error())
		return cli.Exit(1)
	}
	for _, path := range p.Changed {
		cprint(w, "[green]Skill file synced[/green]: %s", path)
	}
	for _, pr := range p.Preserved {
		cprint(w, "[yellow]Skill file preserved[/yellow]: %s (%s)", pr[0], pr[1])
	}
	return nil
}

func renderActivationCascade(ctx *cli.Context, root, src string, scope *cascadeScope) error {
	w := ctx.Stdout
	g, err := loadCascadeGraph(ctx, root, rich.Frame{Name: "activate_cmd", Line: 1015}, rich.Frame{Name: "_render_cascade_activation", Line: 261})
	if g == nil {
		return err
	}
	act, filtered := charter.CascadeRefs(g, src)
	ix := charter.NewConfigIDIndex(root)
	kinds := func(m map[string][]string) []string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return ks
	}
	tok := func(kind string) string { return strings.ReplaceAll(kind, "_", "-") }
	if scope == nil {
		for _, k := range kinds(act) {
			for _, id := range act[k] {
				cprint(w, "[yellow]Warning[/yellow]: referenced %s/%s was not activated (no --cascade).", tok(k), ix.ConfigID(k+":"+id))
			}
		}
		if len(act) > 0 {
			cprint(w, "[yellow]Hint[/yellow]: Re-run with `--cascade <scope>` (e.g. `--cascade all` for every referenced kind) to activate the referenced artifacts, or run `charter consistency-check` to confirm the activation set is coherent.")
		}
	} else {
		inScope, skipped := map[string][]string{}, map[string][]string{}
		for k, ids := range act {
			if scope.selects(k) {
				inScope[k] = ids
			} else {
				skipped[k] = ids
			}
		}
		for _, k := range kinds(inScope) {
			for _, id := range inScope[k] {
				cid := ix.ConfigID(k + ":" + id)
				if _, err := charter.Activate(root, tok(k), cid); err != nil {
					cprint(w, "[yellow]Warning[/yellow]: could not cascade-activate %s/%s: %s", tok(k), cid, err.Error())
					continue
				}
				cprint(w, "[cyan]Cascade-activated[/cyan]: %s/%s", tok(k), cid)
			}
		}
		for _, k := range kinds(skipped) {
			for _, id := range skipped[k] {
				cprint(w, "[dim]Skipped (out of scope)[/dim]: %s/%s", tok(k), ix.ConfigID(k+":"+id))
			}
		}
		defer func() {
			if len(act) == 0 && len(filtered) > 0 {
				cprint(w, "[yellow]Cascade resolved zero activatable targets[/yellow] (every referenced node was kind-filtered; see the lines above).")
			}
		}()
	}
	for _, k := range kinds(filtered) {
		for _, id := range filtered[k] {
			cprint(w, "[dim]Not cascaded[/dim]: %s/%s (kind not charter-activatable)", tok(k), ix.ConfigID(k+":"+id))
		}
	}
	return nil
}

// recompileOrNotify is recompile_or_notify (the catalog recompile is a
// no-op while .kittify/charter/charter.yaml does not exist).
func recompileOrNotify(ctx *cli.Context, root string) error {
	w := ctx.Stdout
	if ctx.Bool("resynthesize") {
		return runFullSynthesize(ctx, root)
	}
	if !ctx.Bool("compile") {
		cprint(w, "[yellow]Catalog not recompiled[/yellow] (--no-compile): catalog.references may go stale until the next `charter generate` or `charter activate --resynthesize`.")
		return nil
	}
	if err := charter.RecompileCatalog(root); err != nil {
		cprint(w, "[yellow]Catalog not recompiled[/yellow]: %s catalog.references may go stale until the next `charter generate` (from a git repository, with a clean activation set) or `charter activate --resynthesize`.", err.Error())
	}
	return nil
}

var actionProfileDefaults = [][3]string{
	{"documentation", "accept", "reviewer-renata"}, {"documentation", "audit", "researcher-robbie"},
	{"documentation", "design", "architect-alphonso"}, {"documentation", "discover", "researcher-robbie"},
	{"documentation", "generate", "implementer-ivan"}, {"documentation", "publish", "reviewer-renata"},
	{"documentation", "validate", "reviewer-renata"}, {"research", "gathering", "researcher-robbie"},
	{"research", "methodology", "researcher-robbie"}, {"research", "output", "reviewer-renata"},
	{"research", "scoping", "researcher-robbie"}, {"research", "synthesis", "researcher-robbie"},
	{"software-dev", "implement", "implementer-ivan"}, {"software-dev", "plan", "architect-alphonso"},
	{"software-dev", "review", "reviewer-renata"}, {"software-dev", "specify", "researcher-robbie"},
	{"software-dev", "tasks", "architect-alphonso"},
}

var defaultProfileRoles = map[string]string{"researcher-robbie": "researcher", "architect-alphonso": "architect", "implementer-ivan": "implementer", "reviewer-renata": "reviewer"}

// missionDefaultProfileWarning is mission_default_profile_warning.
func missionDefaultProfileWarning(profileID string) string {
	var bound []string
	for _, d := range actionProfileDefaults {
		if d[2] == profileID {
			bound = append(bound, d[0]+"/"+d[1])
		}
	}
	if len(bound) == 0 {
		return ""
	}
	note := "same-role"
	if r, ok := defaultProfileRoles[profileID]; ok {
		note = "'" + r + "'-role"
	}
	return fmt.Sprintf("agent profile '%s' is the built-in default profile for the mission steps: %s. With it deactivated, those steps dispatch through the next available %s profile, or block when no available profile carries that role — activate a profile with that role to keep them running.",
		profileID, strings.Join(bound, ", "), note)
}

func runCharterDeactivate(ctx *cli.Context) error {
	w := ctx.Stdout
	kind, id := ctx.Arg("kind"), ctx.Arg("artifact_id")
	if !ctx.HasArg("kind") || !ctx.HasArg("artifact_id") {
		cli.WriteHelp(w, ctx.Cmd)
		return nil
	}
	if !charter.ValidKindToken(kind) {
		cprint(w, "[red]Error:[/red] Unknown kind '%s'. Valid kinds: %s.", kind, validKindsListing())
		return cli.Exit(1)
	}
	if err := writeRootError(); err != nil {
		cprint(w, "[red]Error:[/red] %s", err.Error())
		return cli.Exit(1)
	}
	root := charterRoot(ctx)
	scope, err := parseCascade(ctx.OptString("cascade"))
	if err != nil {
		cprint(w, "[red]Error:[/red] %s", err.Error())
		return cli.Exit(1)
	}
	if _, err := dct.LoadActivation(root); err != nil {
		var ce *dct.ConfigError
		if errors.As(err, &ce) {
			cprint(w, "[red]Error[/red] (%s): %s", ce.Code(), ce.Body)
			return cli.Exit(1)
		}
	}
	res, err := charter.Deactivate(root, kind, id)
	if err != nil {
		cprint(w, "[red]Error:[/red] %s", err.Error())
		return cli.Exit(1)
	}
	for _, d := range res.Deactivated {
		cprint(w, "[green]Deactivated[/green]: %s", d)
	}
	for _, m := range res.Warnings {
		cprint(w, "[yellow]Warning[/yellow]: %s", m)
	}
	if len(res.Deactivated) > 0 && kind == "agent-profile" {
		if m := missionDefaultProfileWarning(strings.TrimPrefix(id, "agent_profile:")); m != "" {
			cprint(w, "[yellow]Warning[/yellow]: %s", m)
		}
	}
	if scope != nil && len(res.Deactivated) > 0 {
		if src := charter.SourceURN(root, kind, id); src != "" {
			if err := renderDeactivationCascade(ctx, root, src, scope); err != nil {
				return err
			}
		}
	}
	if err := recompileOrNotify(ctx, root); err != nil {
		return err
	}
	return reprojectPackSkills(ctx, root, kind)
}

func renderDeactivationCascade(ctx *cli.Context, root, target string, scope *cascadeScope) error {
	w := ctx.Stdout
	g, err := loadCascadeGraph(ctx, root, rich.Frame{Name: "deactivate_cmd", Line: 339}, rich.Frame{Name: "_render_cascade_deactivation", Line: 180})
	if g == nil {
		return err
	}
	act, filtered := charter.CascadeRefs(g, target)
	ix := charter.NewConfigIDIndex(root)
	var candidates []string
	for k, ids := range act {
		if scope.selects(k) {
			for _, id := range ids {
				candidates = append(candidates, k+":"+id)
			}
		}
	}
	active := charter.ActiveURNs(root)
	delete(active, target)
	var deact []string
	type skip struct{ urn, by string }
	var skips []skip
	reach := map[string]map[string]bool{}
	for src := range active {
		r1, r2 := charter.CascadeRefs(g, src)
		m := map[string]bool{}
		for _, mm := range []map[string][]string{r1, r2} {
			for k, ids := range mm {
				for _, id := range ids {
					m[k+":"+id] = true
				}
			}
		}
		reach[src] = m
	}
	for _, c := range candidates {
		var by []string
		for src, m := range reach {
			if m[c] {
				by = append(by, src)
			}
		}
		if len(by) > 0 {
			sort.Strings(by)
			skips = append(skips, skip{c, by[0]})
		} else {
			deact = append(deact, c)
		}
	}
	sort.Strings(deact)
	sort.Slice(skips, func(i, j int) bool { return skips[i].urn < skips[j].urn })
	for _, urn := range deact {
		kind, _ := dct.SplitURN(urn)
		tok := strings.ReplaceAll(kind, "_", "-")
		cid := ix.ConfigID(urn)
		if _, err := charter.Deactivate(root, tok, cid); err != nil {
			cprint(w, "[yellow]Warning[/yellow]: could not cascade-deactivate %s/%s: %s", tok, cid, err.Error())
			continue
		}
		cprint(w, "[cyan]Cascade-deactivated[/cyan]: %s/%s", tok, cid)
		if tok == "agent-profile" {
			if m := missionDefaultProfileWarning(cid); m != "" {
				cprint(w, "[yellow]Warning[/yellow]: %s", m)
			}
		}
	}
	for _, s := range skips {
		cprint(w, "[yellow]Skipped (shared artifact)[/yellow]: %s (still referenced by %s)", s.urn, s.by)
	}
	var fk []string
	for k := range filtered {
		fk = append(fk, k)
	}
	sort.Strings(fk)
	for _, k := range fk {
		for _, id := range filtered[k] {
			cprint(w, "[dim]Not cascaded[/dim]: %s/%s (kind not charter-activatable)", strings.ReplaceAll(k, "_", "-"), ix.ConfigID(k+":"+id))
		}
	}
	return nil
}

// ---- charter list -----------------------------------------------------------

func charterListCmd() *cli.Command {
	return &cli.Command{
		Name:       "list",
		Short:      "List activated charter artifacts by kind.",
		GroupUsage: true,
		Flags: []*cli.Flag{
			{Name: "show-available", Kind: cli.Bool, Help: "Also show available-but-not-activated artifacts."},
			{Name: "all", Kind: cli.Bool, Help: "Show every available artifact per kind across the built-in, org, and project layers (annotated by source layer), including the template kind. Supersedes --show-available."},
			{Name: "json", Kind: cli.Bool, Help: "Output JSON. Every kind row always carries an 'available' key and the payload always carries a top-level 'templates' key — both are null unless requested (available: null without --show-available/--all; templates: null without --all) rather than absent, so callers can rely on key presence and branch on the value instead of on which flags were passed."},
			{Name: "repo-root", Kind: cli.String, Default: ".", Hidden: true},
		},
		Run: runCharterList,
	}
}

func runCharterList(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	allLayers := ctx.Bool("all")
	showAvail := ctx.Bool("show-available") || allLayers
	root := charterRoot(ctx)
	activated, present, err := charter.ListActivated(root)
	if err != nil {
		var ce *dct.ConfigError
		msg := err.Error()
		if errors.As(err, &ce) {
			msg = ce.Code() + ": " + ce.Body
		}
		emitCharterError(ctx.Stdout, jsonMode, msg, "", nil)
		return cli.Exit(1)
	}
	var lr map[string]string
	var chain []string
	if allLayers {
		lr = dct.LayerRoots(root)
		chain = dct.OrgRoots(root)
	}
	headers := []string{"Kind", "Activated"}
	if showAvail {
		if allLayers {
			headers = append(headers, "Available (all layers)")
		} else {
			headers = append(headers, "Available (not activated)")
		}
	}
	tbl := &rtable{Title: "Charter Activation State", Headers: headers, NoWrap: []bool{true}, ShowLines: true,
		Styles: []string{"bold cyan", "white", "dim"}}
	var rows []any
	for _, kind := range dct.CharterKindTokens {
		vals, ok := activated[kind], present[kind]
		var actStr string
		var actJSON any
		switch {
		case !ok:
			actStr = "[dim](All built-ins — no explicit activation)[/dim]"
		case len(vals) == 0:
			actStr = "[yellow](Nothing activated — explicit restriction)[/yellow]"
			actJSON = []any{}
		default:
			actStr = strings.Join(vals, ", ")
			actJSON = strs(vals)
		}
		if ok && actJSON == nil {
			actJSON = []any{}
		}
		row := jsonx.NewObject("kind", kind, "activated", actJSON, "available", nil)
		cells := []string{kind, actStr}
		if showAvail {
			actSet := map[string]bool{}
			for _, v := range vals {
				actSet[v] = true
			}
			if allLayers {
				entries := listAvailableOverChain(kind, lr, chain)
				type pair struct{ id, layer string }
				var na []pair
				for _, e := range entries {
					if !actSet[e.ID] {
						na = append(na, pair{e.ID, e.Layer})
					}
				}
				sort.Slice(na, func(i, j int) bool {
					if na[i].id != na[j].id {
						return na[i].id < na[j].id
					}
					return na[i].layer < na[j].layer
				})
				av := []any{}
				var parts []string
				for _, p := range na {
					av = append(av, jsonx.NewObject("artifact_id", p.id, "layer", p.layer))
					parts = append(parts, p.id+" [dim]("+p.layer+")[/dim]")
				}
				row.Set("available", av)
				if len(parts) == 0 {
					cells = append(cells, "[dim]—[/dim]")
				} else {
					cells = append(cells, strings.Join(parts, ", "))
				}
			} else {
				avail := dct.AvailableIDs(kind, nil, nil)
				var na []string
				for id := range avail {
					if !actSet[id] {
						na = append(na, id)
					}
				}
				sort.Strings(na)
				row.Set("available", strs(na))
				if len(na) == 0 {
					cells = append(cells, "[dim]—[/dim]")
				} else {
					cells = append(cells, strings.Join(na, ", "))
				}
			}
		}
		tbl.Rows = append(tbl.Rows, cells)
		rows = append(rows, row)
	}
	payload := jsonx.NewObject("result", "success", "kinds", rows, "templates", nil)
	if allLayers {
		refs := discoverTemplates(root, lr, chain)
		tl := []any{}
		var parts []string
		for _, r := range refs {
			tl = append(tl, jsonx.NewObject("template_id", r.id, "mission", r.mission, "name", r.name, "tier", r.tier))
			parts = append(parts, r.id+" [dim]("+r.tier+")[/dim]")
		}
		payload.Set("templates", tl)
		cell := "[dim]—[/dim]"
		if len(parts) > 0 {
			cell = strings.Join(parts, ", ")
		}
		tbl.Rows = append(tbl.Rows, []string{"template", "[dim](mission-scoped — not separately activated)[/dim]", cell})
	}
	if jsonMode {
		jsonOut(ctx.Stdout, payload, jsonx.Pretty)
		return nil
	}
	width := rich.Width()
	if allLayers {
		width = 200
	}
	tbl.render(ctx.Stdout, width)
	return nil
}

func listAvailableOverChain(kind string, lr map[string]string, chain []string) []dct.AvailableArtifact {
	entries := dct.ListAvailable(kind, lr, nil)
	type key struct{ id, layer string }
	seen := map[key]bool{}
	for _, e := range entries {
		seen[key{e.ID, e.Layer}] = true
	}
	first := lr[dct.LayerOrg]
	for _, org := range chain {
		if org == first {
			continue
		}
		m := map[string]string{}
		for k, v := range lr {
			m[k] = v
		}
		m[dct.LayerOrg] = org
		for _, e := range dct.ListAvailable(kind, m, nil) {
			k := key{e.ID, e.Layer}
			if e.Layer == dct.LayerOrg && !seen[k] {
				seen[k] = true
				entries = append(entries, e)
			}
		}
	}
	return entries
}

type templateRef struct{ id, mission, name, tier string }

// discoverTemplates is template_catalog.discover_templates over the
// override (project pack missions), org and package-default tiers.
func discoverTemplates(root string, lr map[string]string, chain []string) []templateRef {
	type tierRoot struct {
		tier string
		pack *dct.PackFS
		dir  string
	}
	var tiers []tierRoot
	if p := lr[dct.LayerProject]; p != "" {
		pk := dct.DiskPack(p)
		if pk.Exists("missions") {
			tiers = append(tiers, tierRoot{"override", pk, "missions"})
		}
	}
	for _, org := range chain {
		pk := dct.DiskPack(org)
		if pk.Exists("missions") {
			tiers = append(tiers, tierRoot{"org", pk, "missions"})
		}
	}
	tiers = append(tiers, tierRoot{"package_default", dct.BuiltIn(), "missions"})
	seen := map[string]bool{}
	var out []templateRef
	for _, t := range tiers {
		// mission dirs sorted; per mission: templates/ then command-templates/
		// walked recursively in sorted path order.
		missions := map[string]bool{}
		for _, rel := range t.pack.Walk(t.dir, "", true) {
			parts := strings.Split(strings.TrimPrefix(rel, t.dir+"/"), "/")
			if len(parts) >= 2 {
				missions[parts[0]] = true
			}
		}
		var ms []string
		for m := range missions {
			ms = append(ms, m)
		}
		sort.Strings(ms)
		for _, m := range ms {
			for _, sub := range []string{"templates", "command-templates"} {
				dir := t.dir + "/" + m + "/" + sub
				for _, rel := range t.pack.Walk(dir, "", true) {
					name := rel[strings.LastIndex(rel, "/")+1:]
					if name == "README.md" {
						continue
					}
					id := m + "/" + name
					if seen[id] {
						continue
					}
					seen[id] = true
					out = append(out, templateRef{id, m, name, t.tier})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}
