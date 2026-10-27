package doctrine

import (
	"errors"
	"fmt"
	"slices"
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
		Long:  "Activate a doctrine artifact by kind and ID (FR-004), or apply a pack's preset.\n\nTwo forms:\n\n  spec-kitty charter activate KIND ARTIFACT_ID [--cascade SCOPE [--include-deactivated]]\n\n  spec-kitty charter activate [--pack PACK] --preset PRESET [--force] [--json]\n\nA preset replaces every activation key it governs: keys it lists are\nwritten (plus the org's required ids), keys it leaves out are removed.\nA change to a customised key is refused without --force. A deactivated\nid the preset lists stays deactivated (and is left out) unless --force,\nwhich also clears its exclusion.\n\nThe cascade stops at a deactivated artifact and reports it;\n--include-deactivated walks through and clears the exclusions.",
		Flags: []*cli.Flag{
			{Name: "cascade", Kind: cli.String, Help: "Cascade activation scope: 'all' for every referenced kind, or a comma-separated kind list (e.g. 'agent-profile,tactic'). Omit to skip cascade (referenced artifacts are reported as a warning)."},
			{Name: "include-deactivated", Kind: cli.Bool, Panel: "Cascade options", Help: "Walk through deactivated artifacts during the cascade: activate them and clear their exclusions. Without it the cascade stops at a deactivated artifact and reports it. Only with --cascade."},
			{Name: "resynthesize", Kind: cli.Bool, Negatable: true, Default: false, Help: resynthesizeHelp},
			{Name: "compile", Kind: cli.Bool, Negatable: true, Default: true, Help: noCompileHelp},
			{Name: "preset", Kind: cli.String, Help: "Apply this activation preset of the pack with replace semantics instead of activating one artifact."},
			{Name: "pack", Kind: cli.String, Default: "built-in", Help: "Pack whose preset --preset applies (built-in, an org pack name). Only with --preset."},
			{Name: "force", Kind: cli.Bool, Help: "Apply the preset even when it changes a customised activation key, and clear the exclusion of every deactivated id the preset lists. Only with --preset."},
			{Name: "json", Kind: cli.Bool, Help: "Output the applied preset as JSON. Only with --preset."},
			{Name: "only", Kind: cli.Bool, Help: "Replace this kind's activation list with this id plus org-required ids (prints what stops being in force)."},
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
	if ctx.Bool("include-deactivated") && !ctx.IsSet("cascade") {
		return cli.Usage(ctx.Cmd, "--include-deactivated only applies with --cascade.")
	}
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
		if ctx.Bool("only") {
			return cli.Usage(ctx.Cmd, "--only cannot be combined with --preset (a preset already replaces every key it governs).")
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
					crashFrames(err, rich.Frame{Name: "activate_cmd", Line: 1162}, rich.Frame{Name: "plan_project_registration", Line: 265})...)
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
	res, err := charter.ActivateWithOptions(root, kind, id, ctx.Bool("only"), "activate")
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
	if ctx.Bool("only") {
		dropped := strings.Join(res.Deactivated, ", ")
		if dropped == "" {
			dropped = "(none)"
		}
		cprint(w, "[yellow]No longer in force (%d)[/yellow]: %s", len(res.Deactivated), dropped)
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
	for _, warning := range p.Warnings {
		cprint(w, "[yellow]Warning: %s[/yellow]", warning)
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
	g, err := loadCascadeGraph(ctx, root, rich.Frame{Name: "activate_cmd", Line: 1203}, rich.Frame{Name: "_render_cascade_activation", Line: 434})
	if g == nil {
		return err
	}
	act, filtered := charter.CascadeRefs(g, src)
	activation, err := dct.LoadActivation(root)
	if err != nil {
		return err
	}
	denied := map[string]bool{}
	for k, tokens := range activation.Denied {
		for token := range tokens {
			if strings.HasPrefix(token, string(k)+":") {
				denied[token] = true
			}
		}
	}
	delete(denied, src)
	include := ctx.Bool("include-deactivated")
	reached := map[string]bool{src: true}
	blockedGraph := *g
	blockedGraph.Edges = nil
	for _, e := range g.Edges {
		if !denied[e.Source] || include {
			blockedGraph.Edges = append(blockedGraph.Edges, e)
		}
	}
	reachedAct, _ := charter.CascadeRefs(&blockedGraph, src)
	for kind, ids := range reachedAct {
		for _, id := range ids {
			reached[kind+":"+id] = true
		}
	}
	// Non-artifact parents (actions and mission types) can also require a denied child.
	closure := dct.WalkEdges(&blockedGraph, []string{src}, map[string]bool{dct.RelRequires: true, dct.RelSuggests: true, dct.RelRefines: true, dct.RelScope: true, dct.RelInstantiates: true}, -1)
	for urn := range closure {
		reached[urn] = true
	}
	parents := map[string][]string{}
	for _, e := range g.Edges {
		if e.Relation == dct.RelRequires && reached[e.Source] && (!denied[e.Source] || include) {
			parents[e.Target] = append(parents[e.Target], e.Source)
		}
	}

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
				if !denied[k+":"+id] {
					cprint(w, "[yellow]Warning[/yellow]: referenced %s/%s was not activated (no --cascade).", tok(k), ix.ConfigID(k+":"+id))
				}
			}
		}
		for _, k := range kinds(act) {
			for _, id := range act[k] {
				if denied[k+":"+id] {
					cprint(w, "[yellow]Warning[/yellow]: referenced %s/%s is deactivated and was not activated.", tok(k), ix.ConfigID(k+":"+id))
				}
			}
		}
		hasSkipped := false
		for k, ids := range act {
			for _, id := range ids {
				hasSkipped = hasSkipped || !denied[k+":"+id]
			}
		}
		if hasSkipped {
			cprint(w, "[yellow]Hint[/yellow]: Re-run with `--cascade <scope>` (e.g. `--cascade all` for every referenced kind) to activate the referenced artifacts, or run `charter consistency-check` to confirm the activation set is coherent.")
		}
	} else {
		inScope, skipped := map[string][]string{}, map[string][]string{}
		for k, ids := range act {
			for _, id := range ids {
				urn := k + ":" + id
				if !scope.selects(k) {
					skipped[k] = append(skipped[k], id)
				} else if include || (!denied[urn] && reached[urn]) {
					inScope[k] = append(inScope[k], id)
				}
			}
		}
		for _, k := range kinds(inScope) {
			for _, id := range inScope[k] {
				cid := ix.ConfigID(k + ":" + id)
				if _, err := charter.ActivateWithOptions(root, tok(k), cid, false, func() string {
					if include {
						return "cascade-include-deactivated"
					}
					return "activate"
				}()); err != nil {
					cprint(w, "[yellow]Warning[/yellow]: could not cascade-activate %s/%s: %s", tok(k), cid, err.Error())
					continue
				}
				cprint(w, "[cyan]Cascade-activated[/cyan]: %s/%s", tok(k), cid)
			}
		}
		if include {
			var reinstated []string
			for k, ids := range inScope {
				for _, id := range ids {
					urn := k + ":" + id
					if denied[urn] {
						reinstated = append(reinstated, urn)
					}
				}
			}
			sort.Strings(reinstated)
			after, _ := dct.LoadActivation(root)
			for _, urn := range reinstated {
				k, id := dct.SplitURN(urn)
				kind, _ := dct.ParseKind(k)
				if after != nil && !after.Denied[kind][urn] {
					cprint(w, "[green]Reinstated[/green]: %s/%s (exclusion cleared)", tok(k), ix.ConfigID(k+":"+id))
				}
			}
		}
		for _, k := range kinds(skipped) {
			for _, id := range skipped[k] {
				note := ""
				if denied[k+":"+id] && !include {
					note = " (deactivated; nothing behind it was walked)"
				}
				cprint(w, "[dim]Skipped (out of scope)[/dim]: %s/%s%s", tok(k), ix.ConfigID(k+":"+id), note)
			}
		}
		skippedDenied, unreached := false, false
		if !include {
			for _, k := range kinds(act) {
				if !scope.selects(k) {
					continue
				}
				for _, id := range act[k] {
					urn := k + ":" + id
					if !denied[urn] {
						continue
					}
					note := ""
					if ps := parents[urn]; len(ps) > 0 {
						sort.Strings(ps)
						note = " (required by " + strings.Join(ps, ", ") + " — the parent no longer has this requirement)"
					}
					cprint(w, "[yellow]Skipped, deactivated[/yellow]: %s/%s%s", tok(k), ix.ConfigID(urn), note)
					skippedDenied = true
				}
			}
			for _, k := range kinds(act) {
				if !scope.selects(k) {
					continue
				}
				for _, id := range act[k] {
					urn := k + ":" + id
					if denied[urn] || reached[urn] {
						continue
					}
					cprint(w, "[yellow]Unreached (behind a deactivated artifact)[/yellow]: %s/%s", tok(k), ix.ConfigID(urn))
					unreached = true
				}
			}
			if skippedDenied || unreached {
				cprint(w, "[yellow]Hint[/yellow]: Run with --include-deactivated to activate these and clear their exclusions.")
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
	{"documentation", "validate", "reviewer-renata"}, {"plan", "plan", "architect-alphonso"},
	{"plan", "research", "researcher-robbie"}, {"plan", "review", "reviewer-renata"},
	{"plan", "specify", "researcher-robbie"}, {"research", "gathering", "researcher-robbie"},
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
		var ce *dct.ConfigError
		if errors.As(err, &ce) {
			cprint(w, "[red]Error[/red] (%s): %s", ce.Code(), ce.Body)
		} else {
			cprint(w, "[red]Error:[/red] %s", err.Error())
		}
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
	g, err := loadCascadeGraph(ctx, root, rich.Frame{Name: "deactivate_cmd", Line: 418}, rich.Frame{Name: "_render_cascade_deactivation", Line: 235})
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
		if _, err := charter.DeactivateWithOptions(root, tok, cid, false); err != nil {
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
	store, err := dct.ResolveActivationStore(root)
	if err != nil {
		return err
	}
	dct.ReportActivationWarnings(store)
	identity := dct.NewIdentityIndex(root)
	label := func(kind, token, layer string) string {
		k, e := dct.ParseKind(kind)
		if e != nil {
			return token
		}
		declared := identity.DeclaredID(k, token)
		if layer != "" {
			declared = identity.DeclaredIDInLayer(k, token, layer)
		}
		stem := identity.Stem(k, token)
		if declared != "" && declared != stem {
			return stem + " (" + declared + ")"
		}
		return stem
	}
	deactivated := map[string][]string{}
	showDeactivated := false
	if store.Ledger {
		for _, k := range dct.AllKinds {
			if !k.Activatable() {
				continue
			}
			values, _, _ := charter.CurrentActivationList(store.Data, "deactivated_"+k.Plural())
			sort.Strings(values)
			deactivated[k.OperatorToken()] = values
			showDeactivated = showDeactivated || len(values) > 0
		}
	}
	omissions, err := charter.RequiredNotInForce(root, false)
	if err != nil {
		return err
	}
	inForce, floorUnreadable := requiredInForceByKind(root, present, identity)
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
	if showDeactivated {
		headers = append(headers, "Deactivated")
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
			actStr = absentKeyCell(kind)
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
		if len(vals) > 0 {
			var labels []string
			for _, v := range vals {
				labels = append(labels, label(kind, v, ""))
			}
			actStr = strings.Join(labels, ", ")
		}
		var deniedJSON any
		if kind != dct.MissionTypeToken {
			deniedJSON = strs(deactivated[kind])
		}
		row := jsonx.NewObject("kind", kind, "activated", actJSON, "available", nil, "deactivated", deniedJSON)
		for _, om := range omissions {
			if om.Kind.OperatorToken() != kind {
				continue
			}
			var packs []string
			for _, p := range om.Packs {
				packs = append(packs, p.Describe())
			}
			actStr += "\n[yellow]" + label(kind, om.ID, "") + " — required (not in force: explicit allow list omits it); required by " + strings.Join(packs, ", ") + "[/yellow]"
		}
		// #6417: the org-required ids an absent allow list leaves in force.
		for _, rf := range inForce[kind] {
			var packs []string
			for _, p := range rf.packs {
				packs = append(packs, p.Describe())
			}
			actStr += "\n[green]" + label(kind, rf.stem, "") + " — in force: required by " + strings.Join(packs, ", ") + "[/green]"
		}
		if slices.Contains(floorUnreadable, kind) {
			actStr += "\n[dim]org required floor unreadable — run `spec-kitty doctor charter-packs`[/dim]"
		}
		cells := []string{kind, actStr}
		if showAvail {
			actSet := map[string]bool{}
			for _, v := range vals {
				actSet[v] = true
				if k, e := dct.ParseKind(kind); e == nil {
					actSet[identity.Stem(k, v)] = true
				}
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
					av = append(av, jsonx.NewObject("artifact_id", p.id, "layer", p.layer, "declared_id", func() string {
						k, e := dct.ParseKind(kind)
						if e != nil {
							return p.id
						}
						id := identity.DeclaredIDInLayer(k, p.id, p.layer)
						if id == "" {
							return p.id
						}
						return id
					}()))
					parts = append(parts, label(kind, p.id, p.layer)+" [dim]("+p.layer+")[/dim]")
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
					var labels []string
					for _, id := range na {
						labels = append(labels, label(kind, id, ""))
					}
					cells = append(cells, strings.Join(labels, ", "))
				}
			}
		}
		if showDeactivated {
			var labels []string
			for _, id := range deactivated[kind] {
				labels = append(labels, label(kind, id, ""))
			}
			cell := strings.Join(labels, ", ")
			if cell == "" {
				cell = "[dim]—[/dim]"
			}
			cells = append(cells, cell)
		}
		tbl.Rows = append(tbl.Rows, cells)
		rows = append(rows, row)
	}
	payload := jsonx.NewObject("result", "success", "kinds", rows, "templates", nil)
	if len(omissions) > 0 {
		missing := &jsonx.Object{}
		for _, om := range omissions {
			key := om.Kind.Plural()
			rows := missing.GetList(key)
			packs := []any{}
			seen := map[string]bool{}
			for _, p := range om.Packs {
				if !seen[p.Pack] {
					packs = append(packs, p.Pack)
					seen[p.Pack] = true
				}
			}
			rows = append(rows, jsonx.NewObject("id", om.ID, "packs", packs))
			missing.Set(key, rows)
		}
		payload.Set("required_not_in_force", missing)
	}
	// #6417: additive, present only when an absent allow list leaves a
	// required id in force / the floor of an absent kind cannot be read.
	if len(inForce) > 0 {
		obj := &jsonx.Object{}
		for _, kind := range dct.CharterKindTokens {
			items, ok := inForce[kind]
			if !ok {
				continue
			}
			k, _ := dct.ParseKind(kind)
			list := []any{}
			for _, rf := range items {
				packs := []any{}
				for _, p := range rf.packs {
					if !slices.Contains(packs, any(p.Pack)) {
						packs = append(packs, p.Pack)
					}
				}
				list = append(list, jsonx.NewObject("id", rf.stem, "packs", packs))
			}
			obj.Set(k.Plural(), list)
		}
		payload.Set("required_in_force", obj)
	}
	if len(floorUnreadable) > 0 {
		var plurals []string
		for _, kind := range floorUnreadable {
			k, _ := dct.ParseKind(kind)
			plurals = append(plurals, k.Plural())
		}
		payload.Set("required_floor_unreadable", strs(plurals))
	}
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
		templateCells := []string{"template", "[dim](mission-scoped — not separately activated)[/dim]", cell}
		tbl.Rows = append(tbl.Rows, templateCells)
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

// requiredInForce is one org-required id an absent allow list leaves in
// force, with the packs requiring it.
type requiredInForce struct {
	stem  string
	packs []dct.RequiringPack
}

// requiredInForceByKind is list_cmd._required_in_force_by_kind (#6417): per
// operator kind token whose activated_<kind> key is absent, the org-required
// ids some layer (built-in, project or any org pack) provides, in floor
// order. The floor is read strictly; a kind whose floor cannot be read is
// returned in the second result instead (the listing itself never fails).
func requiredInForceByKind(root string, present map[string]bool, ix *dct.IdentityIndex) (map[string][]requiredInForce, []string) {
	out := map[string][]requiredInForce{}
	var unreadable []string
	for _, kind := range dct.CharterKindTokens {
		if present[kind] {
			continue
		}
		k, err := dct.ParseKind(kind)
		if err != nil || !slices.Contains(charter.OrgRequirableKinds, k.Plural()) {
			continue
		}
		floor, err := dct.OrgRequiredProvenance(root, k, true, ix)
		if err != nil {
			unreadable = append(unreadable, kind)
			continue
		}
		if len(floor) == 0 {
			continue
		}
		// read_org_required_provenance is first-seen ordered over the packs'
		// required_<plural> lists.
		tokens, err := dct.OrgRequiredTokens(root, k.Plural(), true)
		if err != nil {
			unreadable = append(unreadable, kind)
			continue
		}
		provided := map[string]bool{}
		for _, e := range listAvailableOverChain(kind, dct.LayerRoots(root), dct.OrgRoots(root)) {
			provided[e.ID] = true
		}
		seen := map[string]bool{}
		for _, token := range tokens {
			stem := ix.Stem(k, token)
			if seen[stem] {
				continue
			}
			seen[stem] = true
			if provided[stem] {
				out[kind] = append(out[kind], requiredInForce{stem, floor[stem]})
			}
		}
	}
	return out, unreadable
}

// absentKeyCell is list_cmd._absent_key_cell: an absent allow key leaves
// every built-in in force, or (effective_when_absent == "required", the
// skill kind) only the org-required ids.
func absentKeyCell(kind string) string {
	if kind == "skill" {
		return "[dim](Org-required only — no explicit activation)[/dim]"
	}
	return "[dim](All built-ins — no explicit activation)[/dim]"
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
