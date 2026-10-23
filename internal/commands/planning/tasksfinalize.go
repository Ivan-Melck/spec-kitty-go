package planning

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

const tasksFinalizeLong = `Parse tasks.md and inject dependencies into WP frontmatter.

Scans tasks.md for "Depends on: WP##" patterns or phase groupings,
builds dependency graph, validates for cycles, and writes dependencies
field to each WP file's frontmatter.

Examples:
    spec-kitty agent tasks finalize-tasks --mission 001-my-feature --json
    spec-kitty agent tasks finalize-tasks --mission 021-my-feature --json`

func init() {
	addCommands(func(_, _, _, tasksG *cli.Command) {
		tasksG.Add(&cli.Command{
			Name:  "finalize-tasks",
			Short: "Parse tasks.md and inject dependencies into WP frontmatter.",
			Long:  tasksFinalizeLong,
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug"},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
				{Name: "validate-only", Kind: cli.Bool, Help: "Validate without writing changes"},
			},
			Run: runTasksFinalize,
		})
	})
}

func runTasksFinalize(ctx *cli.Context) error {
	jsonMode, validateOnly := ctx.Bool("json"), ctx.Bool("validate-only")
	fail := func(msg string) error { tasksOutputError(ctx, jsonMode, msg); return cli.Exit(1) }
	root := project.Locate("")
	if root == "" {
		return fail("Could not locate project root")
	}
	slug, err := resolveTasksMission(ctx, root, ctx.String("mission"), jsonMode)
	if err != nil {
		return err
	}
	target, err := targetBanner(ctx, root, slug, jsonMode)
	if err != nil {
		return fail(err.Error())
	}
	dir := project.Specs(root, slug)
	tasksMD := filepath.Join(dir, "tasks.md")
	tasksDir := filepath.Join(dir, "tasks")
	b, err := os.ReadFile(tasksMD)
	if err != nil {
		return fail("tasks.md not found: " + tasksMD)
	}
	if _, err := os.Stat(tasksDir); err != nil {
		return fail("Tasks directory not found: " + tasksDir)
	}
	if err := finalizeBulkEditGate(ctx, dir, jsonMode, func(o *jsonx.Object) { printCompact(ctx.Stdout, o) }); err != nil {
		return err
	}
	depsMap, _ := parseDependenciesFromTasksMD(string(b))
	var expected []string
	for _, p := range scandirGlob(tasksDir, "WP*.md") {
		id := strings.SplitN(strings.TrimSuffix(filepath.Base(p), ".md"), "-", 2)[0]
		if wpExact.MatchString(id) {
			expected = append(expected, id)
		}
	}
	sort.Strings(expected)
	exp := map[string]bool{}
	missing := false
	for _, id := range expected {
		exp[id] = true
		if _, ok := depsMap[id]; !ok {
			missing = true
		}
	}
	for id := range depsMap {
		if !exp[id] {
			missing = true
		}
	}
	if missing {
		return fail("tasks.md work package coverage is incomplete. finalize-tasks could not match all WP files to parsed sections, so dependency lanes would be unreliable.")
	}
	// compute_wp_frontmatter_updates
	type write struct {
		id, file string
		deps     []string
	}
	var writes []write
	var warnings []string
	modified, unchanged, preserved := []string{}, []string{}, []string{}
	effective := map[string][]string{}
	ids := sortedKeysObj(depsMap)
	for _, id := range ids {
		parsed := depsMap[id]
		files := append(scandirGlob(tasksDir, id+"-*.md"), scandirGlob(tasksDir, id+".md")...)
		if len(files) == 0 {
			warnings = append(warnings, "No file found for "+id)
			continue
		}
		w, err := wp.Read(files[0])
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("Could not read %s: %v", filepath.Base(files[0]), err))
			continue
		}
		existing := w.Dependencies()
		deps := parsed
		isPreserved := false
		if len(parsed) == 0 && len(existing) > 0 {
			deps = existing
			preserved = append(preserved, id)
			isPreserved = true
		}
		if deps == nil {
			deps = []string{}
		}
		effective[id] = deps
		if !equalStrings(existing, deps) {
			writes = append(writes, write{id, files[0], deps})
			if !isPreserved {
				modified = append(modified, id)
			}
		} else if !isPreserved {
			unchanged = append(unchanged, id)
		}
	}
	if err := (&finalizeRun{ctx: ctx, json: jsonMode, deps: effective, depOrder: ids}).validateGraph(); err != nil {
		return err
	}
	var conflicts []string
	for _, id := range sortedKeysObj(depsMap) {
		parsed := depsMap[id]
		var existing []string
		if files := scandirGlob(tasksDir, id+"*.md"); len(files) > 0 {
			if w, err := wp.Read(files[0]); err == nil {
				existing = w.Dependencies()
			}
		}
		if len(existing) > 0 && len(parsed) > 0 && !sameSet(existing, parsed) {
			a, b := append([]string{}, existing...), append([]string{}, parsed...)
			sort.Strings(a)
			sort.Strings(b)
			conflicts = append(conflicts, fmt.Sprintf("%s: frontmatter has %s, tasks.md parsed %s. Resolve the disagreement in tasks.md or WP frontmatter before finalizing.", id, pyListRepr(a), pyListRepr(b)))
		}
	}
	if len(conflicts) > 0 {
		return fail("Dependency disagreement detected:\n" + strings.Join(conflicts, "\n"))
	}
	for _, w := range warnings {
		say(ctx.Stdout, "[yellow]Warning:[/yellow] "+w)
	}
	would := []any{}
	for _, w := range writes {
		if !validateOnly {
			doc, err := wp.Read(w.file)
			if err != nil {
				return fail(err.Error())
			}
			dumped, err := wpMetaDump(doc.Meta())
			if err != nil {
				return fail(err.Error())
			}
			dumped.Set("dependencies", strList(w.deps))
			if err := writeWPMetaDump(doc, dumped); err != nil {
				return fail(err.Error())
			}
		} else {
			would = append(would, jsonx.NewObject("wp_id", w.id, "changes", jsonx.NewObject("dependencies", strList(w.deps))))
		}
	}
	surface := coord.Resolve(root, slug)
	if !validateOnly {
		surface.WriteDir()
		if prev, _ := lanes.Read(dir); prev == nil {
			if err := tasksFinalizeLanes(ctx, jsonMode, root, slug, dir, tasksDir, target, depsMap); err != nil {
				return err
			}
		}
	}
	// bootstrap_canonical_state on the primary dir
	stream, _ := status.ReadStream(surface.StatusDir(), "")
	have := map[string]bool{}
	if stream != nil {
		for _, t := range stream.Transitions {
			have[t.WPID] = true
		}
	}
	total, already, seeded, skipped := 0, 0, 0, 0
	details := jsonx.NewObject()
	files := scandirGlob(tasksDir, "WP*.md")
	sort.Strings(files)
	var toSeed []string
	for _, p := range files {
		w, err := wp.Read(p)
		id := ""
		if err == nil {
			id = w.Meta().GetString("work_package_id")
		}
		if err != nil || !wpIDAny.MatchString(id) {
			skipped++
			details.Set(strings.TrimSuffix(filepath.Base(p), ".md"), "skipped_malformed")
			continue
		}
		total++
		if have[id] {
			already++
			details.Set(id, "already_exists")
			continue
		}
		toSeed = append(toSeed, id)
	}
	for _, id := range toSeed {
		if validateOnly {
			details.Set(id, "would_seed")
			seeded++
			continue
		}
		r := "canonical bootstrap"
		if _, err := surface.EmitTransition(&status.TransitionRequest{WPID: id, ToLane: "planned", Actor: status.Actor{Name: "finalize-tasks"}, Reason: &r, RepoRoot: root}); err != nil {
			return fail(err.Error())
		}
		seeded++
		details.Set(id, "initialized")
	}
	bootstrap := jsonx.NewObject("total_wps", total, "already_initialized", already, "newly_seeded", seeded, "skipped", skipped, "wp_details", details)
	effObj := jsonx.NewObject()
	for _, id := range ids {
		if v, ok := effective[id]; ok {
			effObj.Set(id, strList(v))
		}
	}
	var res *jsonx.Object
	if validateOnly {
		res = jsonx.NewObject("result", "validation_passed", "validate_only", true, "would_modify", would, "would_preserve", strList(preserved),
			"unchanged", strList(unchanged), "updated_wp_count", len(writes), "dependencies", effObj)
	} else {
		res = jsonx.NewObject("result", "success", "updated_wp_count", len(writes), "modified_wps", strList(modified), "unchanged_wps", strList(unchanged),
			"preserved_wps", strList(preserved), "dependencies", effObj)
	}
	for _, kv := range identityPayload(dir).Pairs() {
		res.Set(kv.Key, kv.Value)
	}
	res.Set("bootstrap", bootstrap)
	if jsonMode {
		printCompact(ctx.Stdout, res)
	} else {
		say(ctx.Stdout, fmt.Sprintf("[green]✓[/green] Updated %d WP files with dependencies (bootstrap: %d seeded, %d existing)", len(writes), seeded, already))
	}
	return nil
}

var wpIDAny = regexp.MustCompile(`^WP\d{2,}$`)

func tasksFinalizeLanes(ctx *cli.Context, jsonMode bool, root, slug, dir, tasksDir, target string, depsMap map[string][]string) error {
	fr := &finalizeRun{ctx: ctx, json: jsonMode, root: root, slug: slug, dir: dir, tasksDir: tasksDir, target: target, deps: depsMap,
		frontmatter: map[string]*jsonx.Object{}, bodies: map[string]string{}}
	for _, p := range scandirGlob(tasksDir, "WP*.md") {
		id := strings.SplitN(strings.TrimSuffix(filepath.Base(p), ".md"), "-", 2)[0]
		if !wpExact.MatchString(id) {
			continue
		}
		w, err := wp.Read(p)
		if err != nil {
			continue
		}
		dumped, err := wpMetaDump(w.Meta())
		if err != nil {
			continue
		}
		fr.frontmatter[id] = dumped
		fr.bodies[id] = w.Doc.Body
	}
	man := map[string]ownManifest{}
	for id, m := range fr.frontmatter {
		if mode, owned := m.GetString("execution_mode"), wp.StringList(m, "owned_files"); mode != "" && len(owned) > 0 {
			man[id] = ownManifest{mode: mode, owned: owned}
		}
	}
	if len(man) == 0 {
		return nil
	}
	if fr.executionHasBegun() {
		return func() error {
			tasksOutputError(ctx, jsonMode, fmt.Sprintf("Cannot finalize mission %s: execution has begun (a WP is past 'planned') but no lanes.json exists on disk. Refusing to seed further canonical events without lanes.json. Run 'spec-kitty doctor mission-state --fix --mission %s' to rebuild lanes.json from the event log.", mission.PyRepr(slug), slug))
			return cli.Exit(1)
		}()
	}
	var sha *string
	if t := branchTip(root, target); t != "" {
		sha = &t
	}
	lm, err := fr.computeLanes(man, nil, sha)
	if err != nil {
		tasksOutputError(ctx, jsonMode, err.Error())
		return cli.Exit(1)
	}
	return lanes.Write(dir, lm)
}
