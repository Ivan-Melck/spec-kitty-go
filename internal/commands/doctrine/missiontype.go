package doctrine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

const missionGroupHelp = "Inspect mission types for this project.\n\nUse 'list' to see activated types (charter-filtered) and 'show <id>' for a full resolved definition.\n\nMission types are selected per mission run during /spec-kitty.specify."

const missionListLong = "List activated mission types for the current project (FR-016).\n\nAlias for ``spec-kitty charter mission-type list``.\n\nReturns only mission types that are explicitly activated in this\nproject's charter (activation-filtered).  For all doctrine-layer\ntypes regardless of activation, use ``spec-kitty charter mission-type list --include-inactive``."

const missionShowLong = "Show the fully resolved MissionType definition for this project (FR-017).\n\nDisplays all fields of the activated mission type:\nid, display_name, action_sequence, template_set,\nsource_layer, extends.\n\nExits with code 1 and lists registered IDs when ``mission_type_id``\nis not an activated type."

const charterMTListLong = "List mission types for the current project (FR-016).\n\nBy default, returns only mission types that are explicitly activated in\nthis project's charter. Pass ``--include-inactive`` to also see every\ntype registered in the built-in/org/project layers regardless of\nactivation state (it replaced the removed doctrine-group listing; it is\nnot a straight alias: activation state still distinguishes the two row\nclasses -- see ACTION SEQUENCE below).\n\nOutput columns (table): ID, SOURCE, DISPLAY NAME, ACTION SEQUENCE. A\nnon-activated ``--include-inactive`` row shows ``(not activated)`` in\nACTION SEQUENCE: resolving a real action sequence requires activation\n(the FR-006 gate), so there is nothing to compute for it."

func missionTypeLeaves() []*cli.Command {
	return []*cli.Command{
		{
			Name:  "current",
			Short: "Show currently active mission for a mission (auto-detects mission from cwd).",
			Flags: []*cli.Flag{{Name: "mission", Kind: cli.String, Help: "Mission slug"}},
			Run:   runMissionCurrent,
		},
		{
			Name:  "info",
			Short: "Show details for a specific mission without switching.",
			Args:  []*cli.Arg{{Name: "mission_name", Help: "Mission name to display details for", Required: true, Metavar: "{mission_name}"}},
			Run:   runMissionInfo,
		},
		{
			Name:       "switch",
			Short:      "[REMOVED] Switch active mission - this command was removed in v0.8.0.",
			Deprecated: true,
			Flags:      []*cli.Flag{{Name: "force", Kind: cli.Bool, Help: "(ignored)"}},
			Args:       []*cli.Arg{{Name: "mission_name", Help: "Mission name (no longer supported)", Required: true, Metavar: "{mission_name}"}},
			Run:        runMissionSwitch,
		},
		{
			Name:  "list",
			Short: "List activated mission types for the current project (FR-016).",
			Long:  missionListLong,
			Flags: []*cli.Flag{{Name: "json", Kind: cli.Bool, Help: "Output as JSON."}},
			Run: func(ctx *cli.Context) error {
				return missionTypeList(ctx, ctx.Bool("json"), false)
			},
		},
		{
			Name:  "show",
			Short: "Show the fully resolved MissionType definition for this project (FR-017).",
			Long:  missionShowLong,
			Flags: []*cli.Flag{{Name: "json", Kind: cli.Bool, Help: "Output as JSON."}},
			Args:  []*cli.Arg{{Name: "mission_type_id", Help: "Mission type ID (e.g. software-dev).", Required: true, Metavar: "{mission_type_id}"}},
			Run:   runMissionShow,
		},
	}
}

func init() {
	app.Register(func(root *cli.Command) {
		for _, name := range []string{"mission", "mission-type"} {
			g := app.Group(root, name, "Inspect mission types for this project.")
			if g.Long == "" {
				g.Long = missionGroupHelp
			}
			for _, c := range missionTypeLeaves() {
				if g.Find(c.Name) == nil {
					g.Add(c)
				}
			}
		}
	})
}

// charterMissionTypeGroup is `charter mission-type`.
func charterMissionTypeGroup() *cli.Command {
	mt := &cli.Command{Name: "mission-type", Short: "Mission type commands (activated types only)."}
	mt.Add(&cli.Command{
		Name:  "list",
		Short: "List mission types for the current project (FR-016).",
		Long:  charterMTListLong,
		Flags: []*cli.Flag{
			{Name: "json", Kind: cli.Bool, Help: "Output as JSON."},
			{Name: "include-inactive", Kind: cli.Bool, Help: "Also list mission types registered in the built-in/org/project layers but NOT activated for this project (activation-blind)."},
		},
		Run: func(ctx *cli.Context) error {
			return missionTypeList(ctx, ctx.Bool("json"), ctx.Bool("include-inactive"))
		},
	})
	return mt
}

// mtError renders the mission_type_error_boundary failure.
func mtError(ctx *cli.Context, jsonMode bool, err error) error {
	code := "invalid_mission_type"
	msg := err.Error()
	var ce *dct.ConfigError
	var ue *dct.UnknownMissionTypeError
	if errors.As(err, &ce) {
		code = ce.Code()
	} else if errors.As(err, &ue) {
		code = "unknown_mission_type"
	}
	if jsonMode {
		jsonError(ctx.Stdout, code, msg)
	} else {
		cprintRaw(ctx.Stdout, "Error: "+msg)
	}
	return cli.Exit(1)
}

func missionTypeList(ctx *cli.Context, jsonMode, includeInactive bool) error {
	root := cwd()
	activated, err := dct.ActivatedMissionTypes(root)
	if err != nil {
		return mtError(ctx, jsonMode, err)
	}
	roster := dct.MissionTypeRoster(root)
	ids := append([]string(nil), activated...)
	actSet := map[string]bool{}
	for _, a := range activated {
		actSet[a] = true
	}
	if includeInactive {
		var extra []string
		for id := range roster {
			if !actSet[id] {
				extra = append(extra, id)
			}
		}
		sort.Strings(extra)
		ids = append(ids, extra...)
	}
	var rows []*jsonx.Object
	for _, id := range ids {
		mt := roster[id]
		if mt == nil {
			return mtError(ctx, jsonMode, &dct.UnknownMissionTypeError{ID: id, Registered: activated})
		}
		var seq any = "(not activated)"
		if actSet[id] {
			seq = strs(mt.ActionSequence)
		}
		rows = append(rows, jsonx.NewObject("id", id, "source_layer", mt.SourceLayer, "display_name", mt.DisplayName,
			"action_sequence", seq, "activated", actSet[id]))
	}
	if jsonMode {
		l := make([]any, len(rows))
		for i, r := range rows {
			l[i] = r
		}
		fmt.Fprintln(ctx.Stdout, jsonx.Dumps(l, jsonx.Options{Indent: -1, EnsureASCII: true}))
		return nil
	}
	if len(rows) == 0 {
		cprint(ctx.Stdout, "[yellow]No activated mission types found for this project.[/yellow]")
		return nil
	}
	var trows [][]string
	for _, r := range rows {
		seq := ""
		if l, ok := r.Get("action_sequence"); ok {
			switch t := l.(type) {
			case []any:
				var parts []string
				for _, x := range t {
					parts = append(parts, x.(string))
				}
				seq = strings.Join(parts, ", ")
			case string:
				seq = t
			}
		}
		trows = append(trows, []string{r.GetString("id"), r.GetString("source_layer"), r.GetString("display_name"), seq})
	}
	rich.Table(ctx.Stdout, "", []string{"ID", "SOURCE", "DISPLAY NAME", "ACTION SEQUENCE"}, trows)
	return nil
}

func runMissionShow(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	id := ctx.Arg("mission_type_id")
	root := cwd()
	activated, err := dct.ActivatedMissionTypes(root)
	if err != nil {
		return mtError(ctx, jsonMode, err)
	}
	found := false
	for _, a := range activated {
		found = found || a == id
	}
	mt := dct.MissionTypeRoster(root)[id]
	if !found || mt == nil {
		return mtError(ctx, jsonMode, &dct.UnknownMissionTypeError{ID: id, Registered: activated})
	}
	if jsonMode {
		var ts, ext any
		if mt.TemplateSet != nil {
			ts = mt.TemplateSet
		}
		if mt.Extends != nil {
			ext = *mt.Extends
		}
		jsonOut(ctx.Stdout, jsonx.NewObject("id", mt.ID, "display_name", mt.DisplayName, "action_sequence", strs(mt.ActionSequence),
			"template_set", ts, "source_layer", mt.SourceLayer, "extends", ext), jsonx.Pretty)
		return nil
	}
	lines := []string{"ID: " + mt.ID, "Display Name: " + mt.DisplayName, "Source Layer: " + mt.SourceLayer}
	if mt.Extends != nil && *mt.Extends != "" {
		lines = append(lines, "Extends: "+*mt.Extends)
	}
	lines = append(lines, "Action Sequence: "+strings.Join(mt.ActionSequence, ", "))
	if mt.TemplateSet != nil {
		keys := mt.TemplateSet.Keys()
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, k+"="+mt.TemplateSet.GetString(k))
		}
		lines = append(lines, "Template Set: "+strings.Join(parts, ", "))
	} else {
		lines = append(lines, "Template Set: (none)")
	}
	rich.Panel(ctx.Stdout, strings.Join(lines, "\n"), "Mission Type · "+mt.ID)
	return nil
}

func runMissionSwitch(ctx *cli.Context) error {
	fmt.Fprintln(ctx.Stderr, "DeprecationWarning: The command 'switch' is deprecated.")
	w := ctx.Stdout
	for _, l := range []string{
		"Error: The 'mission switch' command was removed in v0.8.0.", "",
		"Mission types are now selected per mission run during /spec-kitty.specify.", "",
		"New workflow:",
		"  1. Run /spec-kitty.specify inside your coding agent (Claude Code, Codex, Cursor) to start a new mission",
		"  2. The system will infer and confirm the appropriate mission",
		"  3. Mission is stored in the mission's meta.json", "",
		"To see available missions:", "  spec-kitty mission list", "",
		"See: https://github.com/your-org/spec-kitty#mission-types",
	} {
		cprintRaw(w, l)
	}
	return cli.Exit(1)
}

// legacyMission locates a mission.yaml (project .kittify/missions/<name>,
// else the packaged missions).
type legacyMission struct {
	Dir  string
	Data *jsonx.Object
}

func findLegacyMission(root, name string) *legacyMission {
	p := filepath.Join(root, ".kittify", "missions", name, "mission.yaml")
	if b, err := os.ReadFile(p); err == nil {
		if o, err := yamlx.ParseObject(b); err == nil {
			return &legacyMission{filepath.Dir(p), o}
		}
	}
	bi := dct.BuiltIn()
	rel := "missions/" + name + "/mission.yaml"
	if b, err := bi.ReadFile(rel); err == nil {
		if o, err := yamlx.ParseObject(b); err == nil {
			return &legacyMission{filepath.Dir(bi.Path(rel)), o}
		}
	}
	return nil
}

func listLegacyMissions(root string) []string {
	set := map[string]bool{}
	ents, _ := os.ReadDir(filepath.Join(root, ".kittify", "missions"))
	for _, e := range ents {
		if _, err := os.Stat(filepath.Join(root, ".kittify", "missions", e.Name(), "mission.yaml")); err == nil {
			set[e.Name()] = true
		}
	}
	bi := dct.BuiltIn()
	for _, rel := range bi.Walk("missions", "mission.yaml", true) {
		parts := strings.Split(rel, "/")
		if len(parts) == 3 {
			set[parts[1]] = true
		}
	}
	return dct.SortedKeys(set)
}

func missionDetailLines(m *legacyMission) []string {
	d := m.Data
	l := []string{"Name: " + d.GetString("name"), "Domain: " + d.GetString("domain"), "Version: " + d.GetString("version"), "Path: " + m.Dir}
	if s := d.GetString("description"); s != "" {
		l = append(l, "Description: "+s)
	}
	l = append(l, "", "Workflow Phases:")
	if wf := d.GetObject("workflow"); wf != nil {
		for _, ph := range wf.GetList("phases") {
			if o, ok := ph.(*jsonx.Object); ok {
				l = append(l, "  • "+o.GetString("name")+" – "+o.GetString("description"))
			}
		}
	}
	art := d.GetObject("artifacts")
	if art == nil {
		art = &jsonx.Object{}
	}
	l = append(l, "", "Required Artifacts:")
	if req := art.GetList("required"); len(req) > 0 {
		for _, x := range req {
			l = append(l, "  • "+fmt.Sprint(x))
		}
	} else {
		l = append(l, "  • (none)")
	}
	if opt := art.GetList("optional"); len(opt) > 0 {
		l = append(l, "", "Optional Artifacts:")
		for _, x := range opt {
			l = append(l, "  • "+fmt.Sprint(x))
		}
	}
	l = append(l, "", "Validation Checks:")
	val := d.GetObject("validation")
	if val != nil && len(val.GetList("checks")) > 0 {
		for _, x := range val.GetList("checks") {
			l = append(l, "  • "+fmt.Sprint(x))
		}
	} else {
		l = append(l, "  • (none)")
	}
	if p := d.GetObject("paths"); p != nil && p.Len() > 0 {
		l = append(l, "", "Path Conventions:")
		for _, kv := range p.Pairs() {
			l = append(l, fmt.Sprintf("  • %s: %v", kv.Key, kv.Value))
		}
	}
	if mcp := d.GetObject("mcp_tools"); mcp != nil {
		join := func(k string) string {
			var xs []string
			for _, x := range mcp.GetList(k) {
				xs = append(xs, fmt.Sprint(x))
			}
			if len(xs) == 0 {
				return "none"
			}
			return strings.Join(xs, ", ")
		}
		l = append(l, "", "MCP Tools:", "  • Required: "+join("required"), "  • Recommended: "+join("recommended"), "  • Optional: "+join("optional"))
	}
	return l
}

func runMissionInfo(ctx *cli.Context) error {
	root := repoRoot()
	name := ctx.Arg("mission_name")
	m := findLegacyMission(root, name)
	if m == nil {
		cprintRaw(ctx.Stdout, "Mission not found: "+name)
		if av := listLegacyMissions(root); len(av) > 0 {
			fmt.Fprintln(ctx.Stdout)
			fmt.Fprintln(ctx.Stdout, "Available missions:")
			for _, a := range av {
				fmt.Fprintln(ctx.Stdout, "  • "+a)
			}
		}
		return cli.Exit(1)
	}
	rich.Panel(ctx.Stdout, strings.Join(missionDetailLines(m), "\n"), "Mission Details · "+m.Data.GetString("name"))
	return nil
}

func runMissionCurrent(ctx *cli.Context) error {
	root := repoRoot()
	slug := strings.TrimSpace(ctx.String("mission"))
	if !ctx.IsSet("mission") {
		w := ctx.Stdout
		fmt.Fprintln(w, "No active mission detected.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Use --mission <slug> to specify one, or run from within a mission worktree.")
		ents, _ := os.ReadDir(project.Specs(root))
		var ms []string
		for _, e := range ents {
			if e.IsDir() && e.Name() != "" && e.Name()[0] >= '0' && e.Name()[0] <= '9' {
				ms = append(ms, e.Name())
			}
		}
		sort.Strings(ms)
		if len(ms) > 0 {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "Available missions:")
			for i, s := range ms {
				if i >= 10 {
					break
				}
				fmt.Fprintln(w, "  - "+s)
			}
			if len(ms) > 10 {
				fmt.Fprintf(w, "  ... and %d more\n", len(ms)-10)
			}
		}
		return cli.Exit(2)
	}
	if slug == "" {
		return cli.BadParameter(ctx.Cmd, "", "--mission <slug> is required")
	}
	dir := findMissionDir(root, slug)
	if dir == "" {
		cprintRaw(ctx.Stdout, "Mission not found: "+slug)
		return cli.Exit(1)
	}
	mtype := "software-dev"
	if b, err := os.ReadFile(filepath.Join(dir, "meta.json")); err == nil {
		if o, err := jsonx.DecodeObject(b); err == nil {
			if s := o.GetString("mission_type"); s != "" {
				mtype = s
			} else if s := o.GetString("mission"); s != "" {
				mtype = s
			}
		}
	}
	m := findLegacyMission(root, mtype)
	if m == nil {
		cprintRaw(ctx.Stdout, fmt.Sprintf("Error: Mission '%s' not found", mtype))
		return cli.Exit(1)
	}
	rich.Panel(ctx.Stdout, strings.Join(missionDetailLines(m), "\n"), "Active Mission (Mission: "+slug+")")
	return nil
}

// findMissionDir resolves a mission handle (full slug, numeric prefix or
// mid8 prefix) under kitty-specs/.
// TODO(area A): replace with internal/mission handle resolution once merged.
func findMissionDir(root, handle string) string {
	specs := project.Specs(root)
	if st, err := os.Stat(filepath.Join(specs, handle)); err == nil && st.IsDir() {
		return filepath.Join(specs, handle)
	}
	ents, _ := os.ReadDir(specs)
	var hits []string
	for _, e := range ents {
		if e.IsDir() && (strings.HasPrefix(e.Name(), handle+"-") || strings.HasSuffix(e.Name(), "-"+handle)) {
			hits = append(hits, e.Name())
		}
	}
	if len(hits) == 1 {
		return filepath.Join(specs, hits[0])
	}
	return ""
}
