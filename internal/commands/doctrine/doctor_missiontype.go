package doctrine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// doctor mission-type (cli/commands/_mission_type_audit.py).

var missionTypeStates = []string{"resolved", "activated-unresolvable", "unknown", "typeless", "legacy-key-only", "error"}

type missionTypeState struct {
	path, slug  string
	raw         *string
	resolvedKey *string
	state       string
	err         *string
}

func (s *missionTypeState) object() *jsonx.Object {
	opt := func(p *string) any {
		if p == nil {
			return nil
		}
		return *p
	}
	return jsonx.NewObject("path", s.path, "slug", s.slug, "mission_type_raw", opt(s.raw),
		"resolved_key", opt(s.resolvedKey), "state", s.state, "error", opt(s.err))
}

func canonicalMissionTypeKey(v any) *string {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	k := strings.TrimSpace(s)
	if k == "" {
		return nil
	}
	return &k
}

func classifyMissionType(dir string, registered map[string]bool, roster map[string]*dct.MissionType) *missionTypeState {
	st := &missionTypeState{path: dir, slug: filepath.Base(dir)}
	meta, err := migrate.LoadMetaFailClosed(dir)
	if err != nil {
		msg := err.Error()
		st.state, st.err = "error", &msg
		return st
	}
	if meta == nil {
		meta = jsonx.NewObject()
	}
	if raw, ok := meta.Get("mission_type"); ok {
		key := canonicalMissionTypeKey(raw)
		if s, ok := raw.(string); ok {
			st.raw = &s
		}
		switch {
		case key == nil:
			st.state = "typeless"
		case !registered[*key]:
			st.resolvedKey, st.state = key, "unknown"
		case roster[*key] != nil:
			st.resolvedKey, st.state = key, "resolved"
		default:
			st.resolvedKey, st.state = key, "activated-unresolvable"
		}
		return st
	}
	legacy, _ := meta.Get("mission")
	if key := canonicalMissionTypeKey(legacy); key != nil {
		st.resolvedKey, st.state = key, "legacy-key-only"
	} else {
		st.state = "typeless"
	}
	return st
}

func auditMissionTypes(root string) []*missionTypeState {
	specs := filepath.Join(root, project.SpecsDir)
	entries, err := os.ReadDir(specs)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	ids, _ := dct.ActivatedMissionTypes(root)
	registered := map[string]bool{}
	for _, id := range ids {
		registered[id] = true
	}
	roster := dct.MissionTypeRoster(root)
	var out []*missionTypeState
	for _, n := range names {
		p := filepath.Join(specs, n)
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			continue
		}
		out = append(out, classifyMissionType(p, registered, roster))
	}
	return out
}

func doctorMissionTypeCmd() *cli.Command {
	return leaf("doctor_mission-type", "mission-type", []*cli.Flag{
		{Name: "json", Kind: cli.Bool, Help: "Emit structured JSON output (suitable for CI)"},
		{Name: "mission", Kind: cli.String, Help: "Scope report to a single mission slug"},
		{Name: "fail-on", Kind: cli.String, Help: "Exit non-zero if any mission is in the given state(s). Comma-separated list of: resolved, activated-unresolvable, unknown, typeless, legacy-key-only, error."},
	}, runDoctorMissionType)
}

func runDoctorMissionType(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root, err := doctorRoot(ctx, jsonMode, 1)
	if err != nil {
		return err
	}
	w := ctx.Stdout
	states := auditMissionTypes(root)
	if m := ctx.OptString("mission"); m != nil {
		var scoped []*missionTypeState
		for _, s := range states {
			if s.slug == *m {
				scoped = append(scoped, s)
			}
		}
		if len(scoped) == 0 {
			cprint(w, "[red]Error:[/red] Mission not found: %s", dct.PyRepr(*m))
			return cli.Exit(1)
		}
		states = scoped
	}
	counts := jsonx.NewObject()
	for _, s := range missionTypeStates {
		counts.Set(s, 0)
	}
	for _, s := range states {
		n, _ := counts.GetInt(s.state)
		counts.Set(s.state, n+1)
	}
	failOn := ctx.String("fail-on")
	failStates := map[string]bool{}
	triggered := false
	if failOn != "" {
		valid := map[string]bool{}
		for _, s := range missionTypeStates {
			valid[s] = true
		}
		for _, t := range strings.Split(failOn, ",") {
			if t = strings.TrimSpace(t); t != "" {
				failStates[t] = true
			}
		}
		var unknown []string
		for t := range failStates {
			if !valid[t] {
				unknown = append(unknown, t)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return cli.Usage(ctx.Cmd, "Invalid value: unknown --fail-on state(s): %s; valid states are: %s",
				strings.Join(unknown, ", "), strings.Join(missionTypeStates, ", "))
		}
		for _, s := range states {
			if failStates[s.state] {
				triggered = true
			}
		}
	}
	code := 0
	if triggered {
		code = 1
	}
	if jsonMode {
		missions := []any{}
		for _, s := range states {
			missions = append(missions, s.object())
		}
		fmt.Fprintln(w, jsonx.Dumps(jsonx.NewObject("summary", counts, "missions", missions, "fail_on_triggered", triggered), jsonx.Pretty))
		return exitCode(code)
	}
	cprint(w, "\n[bold]Mission Type Audit[/bold] — %d mission(s)\n", len(states))
	var rows [][]string
	for _, s := range missionTypeStates {
		n, _ := counts.GetInt(s)
		st := map[string]string{"resolved": "green", "activated-unresolvable": "red", "unknown": "red",
			"typeless": "yellow", "legacy-key-only": "yellow", "error": "red"}[s]
		rows = append(rows, []string{"[" + st + "]" + s + "[/]", fmt.Sprint(n)})
	}
	gridTable(w, []string{"State", "Count"}, []int{10, 6}, []bool{false, true}, rows)
	fmt.Fprintln(w)
	if triggered {
		var fs []string
		for s := range failStates {
			fs = append(fs, s)
		}
		sort.Strings(fs)
		cprint(w, "[bold red]FAIL:[/bold red] --fail-on %s triggered (one or more missions in: %s)", dct.PyRepr(failOn), strings.Join(fs, ", "))
	}
	return exitCode(code)
}
