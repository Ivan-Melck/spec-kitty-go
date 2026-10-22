package doctrine

import (
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

const profileShowShort = "Show the full resolved definition of an agent profile (FR-013/014/015)."

func profileLeaves() []*cli.Command {
	show := func(name string, hidden bool) *cli.Command {
		return &cli.Command{
			Name:   name,
			Short:  profileShowShort,
			Hidden: hidden,
			Flags: []*cli.Flag{
				{Name: "json", Kind: cli.Bool, Help: "Output JSON object."},
				{Name: "all", Kind: cli.Bool, Help: "Bypass the activation gate for inspection (show non-activated profiles)."},
			},
			Args: []*cli.Arg{{Name: "profile_id", Help: "Profile ID to show.", Required: true, Metavar: "{profile_id}"}},
			Run:  runProfileShow,
		}
	}
	return []*cli.Command{
		{
			Name:  "list",
			Short: "List agent profiles (activated-only by default; --all for the full catalog).",
			Flags: []*cli.Flag{
				{Name: "json", Kind: cli.Bool, Help: "Output JSON array."},
				{Name: "all", Kind: cli.Bool, Help: "Show every profile across all source layers (annotated by source layer and activated|available state). Supersedes the activated-only default and --show-available."},
				{Name: "show-available", Kind: cli.Bool, Help: "Also show available-but-not-activated profiles (annotated by state)."},
			},
			Run: runProfileList,
		},
		show("show", false),
		show("get", true),
	}
}

func init() {
	app.Register(func(root *cli.Command) {
		g := app.Group(root, "profiles", "Manage and list agent profiles.")
		g.Add(profileLeaves()...)
		agent := app.Group(root, "agent", "Commands for AI agents to execute spec-kitty mission actions programmatically")
		ap := app.Group(agent, "profile", "Compatibility alias for listing agent profiles")
		ap.Hidden = true
		ap.Add(profileLeaves()...)
	})
}

func profileSource(layer string) string {
	switch layer {
	case dct.LayerProject:
		return "project_local"
	case dct.LayerOrg:
		return "org"
	}
	return "built-in"
}

func profileDescriptor(p *dct.Profile) *jsonx.Object {
	return jsonx.NewObject("profile_id", p.ID, "identifier", p.ID, "name", p.Name(), "role", p.Role(),
		"action_domains", strs(p.ActionDomains()), "source", profileSource(p.Source))
}

func runProfileList(ctx *cli.Context) error {
	root := repoRoot()
	cat := dct.LoadProfileCatalog(root)
	profiles := cat.List()
	jsonMode := ctx.Bool("json")
	if len(profiles) == 0 {
		if jsonMode {
			fmt.Fprintln(ctx.Stdout, "[]")
		} else {
			cprint(ctx.Stdout, "[yellow]No profiles found.[/yellow] Run 'spec-kitty charter synthesize' to create project-local profiles.")
		}
		return nil
	}
	showAvail := ctx.Bool("show-available") || ctx.Bool("all")
	activated, err := dct.ActivatedProfiles(root)
	if err != nil {
		return err
	}
	var descs []*jsonx.Object
	for _, p := range profiles {
		if !showAvail && activated != nil && !activated[p.ID] {
			continue
		}
		d := profileDescriptor(p)
		if showAvail {
			st := "available"
			if activated == nil || activated[p.ID] {
				st = "activated"
			}
			d.Set("state", st)
		}
		descs = append(descs, d)
	}
	if jsonMode {
		l := make([]any, len(descs))
		for i, d := range descs {
			l[i] = d
		}
		jsonOut(ctx.Stdout, l, jsonx.Pretty)
		return nil
	}
	headers := []string{"Profile ID", "Friendly Name", "Role", "Source"}
	if showAvail {
		headers = append(headers, "State")
	}
	t := &rtable{Title: "Agent Profiles", Headers: headers, NoWrap: []bool{true}, Overflow: []string{"fold"}}
	for _, d := range descs {
		r := []string{d.GetString("profile_id"), d.GetString("name"), d.GetString("role"), d.GetString("source")}
		if showAvail {
			state := d.GetString("state")
			if _, ok := d.Get("state"); !ok {
				state = "activated"
			}
			st := "dim"
			if state == "activated" {
				st = "green"
			}
			r = append(r, "["+st+"]"+state+"[/"+st+"]")
		}
		t.Rows = append(t.Rows, r)
	}
	t.render(ctx.Stdout, rich.Width())
	return nil
}

func objList(v any) []*jsonx.Object {
	l, _ := v.([]any)
	var out []*jsonx.Object
	for _, x := range l {
		if o, ok := x.(*jsonx.Object); ok {
			out = append(out, o)
		}
	}
	return out
}

func strOf(o *jsonx.Object, k string) string {
	if o == nil {
		return ""
	}
	v, ok := o.Get(k)
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func listOf(o *jsonx.Object, k string) []any {
	out := []any{}
	if o == nil {
		return out
	}
	for _, x := range o.GetList(k) {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

// profilePayload is upstream _profile_payload.
func profilePayload(p *dct.Profile, warnings []string) *jsonx.Object {
	d := p.Data
	spec := d.GetObject("specialization")
	collab := d.GetObject("collaboration")
	var modes, drefs, trefs []any
	for _, m := range objList(mustGet(d, "mode-defaults")) {
		modes = append(modes, jsonx.NewObject("mode", strOf(m, "mode"), "description", strOf(m, "description"), "use_case", strOf(m, "use-case")))
	}
	for _, r := range objList(mustGet(d, "directive-references")) {
		drefs = append(drefs, jsonx.NewObject("code", strOf(r, "code"), "name", strOf(r, "name"), "rationale", strOf(r, "rationale")))
	}
	for _, r := range objList(mustGet(d, "tactic-references")) {
		trefs = append(trefs, jsonx.NewObject("id", strOf(r, "id"), "rationale", strOf(r, "rationale")))
	}
	nz := func(l []any) []any {
		if l == nil {
			return []any{}
		}
		return l
	}
	var src any
	if p.Source != "" {
		src = p.Source
	}
	return jsonx.NewObject(
		"profile_id", p.ID, "name", p.Name(), "role", p.Role(),
		"initialization_declaration", strOf(d, "initialization-declaration"),
		"specialization", jsonx.NewObject("primary_focus", strOf(spec, "primary-focus"), "secondary_awareness", strOf(spec, "secondary-awareness"),
			"avoidance_boundary", strOf(spec, "avoidance-boundary"), "success_definition", strOf(spec, "success-definition")),
		"collaboration", jsonx.NewObject("handoff_to", listOf(collab, "handoff-to"), "handoff_from", listOf(collab, "handoff-from"),
			"works_with", listOf(collab, "works-with"), "canonical_verbs", listOf(collab, "canonical-verbs")),
		"mode_defaults", nz(modes), "directive_references", nz(drefs), "tactic_references", nz(trefs),
		"source_layer", src, "warnings", strs(warnings),
	)
}

func mustGet(o *jsonx.Object, k string) any {
	v, _ := o.Get(k)
	return v
}

func runProfileShow(ctx *cli.Context) error {
	root := repoRoot()
	id := ctx.Arg("profile_id")
	jsonMode := ctx.Bool("json")
	cat := dct.LoadProfileCatalog(root)
	activated, err := dct.ActivatedProfiles(root)
	if err != nil {
		return err
	}
	var candidates []string
	for _, pid := range cat.IDs() {
		if activated == nil || activated[pid] {
			candidates = append(candidates, pid)
		}
	}
	inActive := false
	for _, c := range candidates {
		inActive = inActive || c == id
	}
	notActivated := func() error {
		if jsonMode {
			jsonOut(ctx.Stdout, jsonx.NewObject("activated_candidates", strs(candidates), "error", "profile_not_activated", "profile_id", id), jsonx.SortedPretty)
		} else {
			cprintRaw(ctx.Stdout, fmt.Sprintf("Error: profile '%s' is not activated.", id))
			if len(candidates) > 0 {
				cprintRaw(ctx.Stdout, "Activated candidates: "+strings.Join(candidates, ", "))
			} else {
				cprintRaw(ctx.Stdout, "No agent profiles are currently activated.")
			}
			cprintRaw(ctx.Stdout, "Use --all to inspect a non-activated profile, or activate it via 'spec-kitty charter activate agent-profile <id>'.")
		}
		return cli.Exit(1)
	}
	if !inActive && !ctx.Bool("all") {
		return notActivated()
	}
	if cat.ByID[id] == nil {
		return notActivated()
	}
	resolved := cat.Resolve(id)
	if resolved == nil {
		return fmt.Errorf("Profile '%s' references a missing parent", id)
	}
	var warnings []string
	if anc := cat.Ancestors(id); len(anc) > 0 && activated != nil {
		var non []string
		for _, a := range anc {
			if !activated[a] {
				non = append(non, a)
			}
		}
		if len(non) > 0 {
			warnings = append(warnings, "resolved via non-activated parent profile(s): "+strings.Join(non, ", ")+" — these act as abstract base profiles and are not directly selectable")
		}
	}
	payload := profilePayload(resolved, warnings)
	if jsonMode {
		jsonOut(ctx.Stdout, payload, jsonx.SortedPretty)
		return nil
	}
	renderProfileHuman(ctx, payload)
	for _, w := range warnings {
		cprintRaw(ctx.Stdout, "warning: "+w)
	}
	return nil
}

func renderProfileHuman(ctx *cli.Context, p *jsonx.Object) {
	const empty = "—"
	or := func(s string) string {
		if s == "" {
			return empty
		}
		return s
	}
	joinL := func(o *jsonx.Object, k string) string {
		var xs []string
		for _, x := range o.GetList(k) {
			xs = append(xs, fmt.Sprint(x))
		}
		return or(strings.Join(xs, ", "))
	}
	joinF := func(k, f string) string {
		var xs []string
		for _, x := range objList(mustGet(p, k)) {
			xs = append(xs, strOf(x, f))
		}
		return or(strings.Join(xs, ", "))
	}
	spec := p.GetObject("specialization")
	collab := p.GetObject("collaboration")
	src := "None"
	if s := p.GetString("source_layer"); s != "" {
		src = s
	}
	t := &rtable{Title: "Agent Profile: " + p.GetString("profile_id"), Headers: []string{"Field", "Value"}, NoWrap: []bool{true}, ShowLines: true,
		Styles: []string{"bold cyan", "white"}}
	t.Rows = [][]string{
		{"Profile ID", p.GetString("profile_id")},
		{"Name", p.GetString("name")},
		{"Role", p.GetString("role")},
		{"Source layer", src},
		{"Initialization", or(p.GetString("initialization_declaration"))},
		{"Primary focus", or(spec.GetString("primary_focus"))},
		{"Secondary awareness", or(spec.GetString("secondary_awareness"))},
		{"Avoidance boundary", or(spec.GetString("avoidance_boundary"))},
		{"Success definition", or(spec.GetString("success_definition"))},
		{"Handoff to", joinL(collab, "handoff_to")},
		{"Handoff from", joinL(collab, "handoff_from")},
		{"Works with", joinL(collab, "works_with")},
		{"Canonical verbs", joinL(collab, "canonical_verbs")},
		{"Mode defaults", joinF("mode_defaults", "mode")},
		{"Directive refs", joinF("directive_references", "code")},
		{"Tactic refs", joinF("tactic_references", "id")},
	}
	t.render(ctx.Stdout, rich.Width())
}
