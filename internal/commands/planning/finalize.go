package planning

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/lifecycle"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missionart"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/reqmap"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

const finalizeCmd = "spec-kitty agent mission finalize-tasks"

const finalizeLong = `Parse dependencies from tasks.md and update WP frontmatter, then commit to target branch.

This command is designed to be called after LLM generates WP files via /spec-kitty.tasks.
It post-processes the generated files to add dependency information and commits everything.

Examples:
    spec-kitty agent mission finalize-tasks --json
    spec-kitty agent mission finalize-tasks --mission 020-my-feature --json
    spec-kitty agent mission finalize-tasks --mission 020-my-feature --validate-only --json`

func init() {
	addCommands(func(_, _, missionG, _ *cli.Command) {
		missionG.Add(&cli.Command{
			Name:  "finalize-tasks",
			Short: "Parse dependencies from tasks.md and update WP frontmatter, then commit to target branch.",
			Long:  finalizeLong,
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug (e.g., '020-my-mission')"},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
				{Name: "validate-only", Kind: cli.Bool, Help: "Run all validations without committing. Reports issues that would block finalization."},
				{Name: "target-branch", Kind: cli.String, Metavar: "<str>", Help: "Override the canonical planning target branch read from meta.json. Use this for legacy missions created before WP07 persisted target_branch in meta.json, or to correct a mission whose target_branch is stale (FR-012 escape hatch). The override is persisted into the primary meta.json as part of this run, so every other target_branch consumer converges on it too (#3466)."},
				{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: "Explicit owned checkout for a single-branch mission."},
				{Name: "refresh-planning-commit", Kind: cli.Bool, Help: "Force a refresh-ONLY run: re-point the recorded planning_commit_sha in lanes.json to the current target-branch tip and exit, without running the rest of finalize-tasks (#4141)."},
				{Name: "allow-orphaned", Kind: cli.Bool, Help: "Only meaningful with --refresh-planning-commit (#4827). Permits the re-pin to re-point a recorded planning_commit_sha that is ORPHANED."},
			},
			Run: runFinalizeTasks,
		})
	})
}

type finalizeRun struct {
	ctx          *cli.Context
	json         bool
	validateOnly bool
	root, slug   string
	dir          string // primary mission dir (planning dir)
	tasksDir     string
	target       string
	mergeTarget  string
	override     string
	missionType  string

	wpFiles       []string // sorted
	wpIDs         []string
	specIDs       reqmap.SpecIDs
	declared      map[string]bool
	functional    map[string]bool
	extractWarns  []string
	specContent   string
	deps          map[string][]string
	depOrder      []string
	refs          map[string][]string
	diagnostics   *jsonx.Object
	preexisting   map[string]bool
	persisted     bool
	previousValue string

	// bootstrap state
	workPackages  []*jsonx.Object
	updated       int
	modified      []string
	unchanged     []string
	preserved     []string
	wouldModify   []any
	ownershipWarn []string
	postIntegWarn []string
	frontmatter   map[string]*jsonx.Object // in-memory (updated) WP metadata
	bodies        map[string]string
	pending       []pendingWrite
}

type pendingWrite struct {
	w     *wp.WP
	delta *jsonx.Object
}

func (f *finalizeRun) emitErr(msg string, extra ...any) error {
	if f.json {
		o := jsonx.NewObject("error", msg)
		for i := 0; i+1 < len(extra); i += 2 {
			o.Set(extra[i].(string), extra[i+1])
		}
		emitJSON(f.ctx.Stdout, o)
	} else {
		say(f.ctx.Stdout, "[red]Error:[/red] "+msg)
	}
	return cli.Exit(1)
}

func (f *finalizeRun) human(s string) {
	if !f.json {
		say(f.ctx.Stdout, s)
	}
}

var wpFilePrefix = regexp.MustCompile(`^(WP\d{2})(?:[-_.]|$)`)

func runFinalizeTasks(ctx *cli.Context) error {
	f := &finalizeRun{ctx: ctx, json: ctx.Bool("json"), validateOnly: ctx.Bool("validate-only"), override: strings.TrimSpace(ctx.String("target-branch"))}
	if ctx.Bool("refresh-planning-commit") {
		return f.emitErr("--refresh-planning-commit is not supported by this build")
	}
	f.root = project.Locate("")
	if f.root == "" {
		return f.emitErr("Could not locate project root")
	}
	if ctx.String("owned-checkout") != "" {
		return f.emitErr("--owned-checkout is not supported by this build")
	}
	handle := ctx.String("mission")
	dir, err := primaryAnchoredFeatureDir(f.root, handle)
	code := "MISSION_CONTEXT_UNRESOLVED"
	if de, ok := err.(*detectionErr); ok {
		code = de.code
	}
	if dir == "" {
		rd, rerr := readPathFor(f.root, handle)
		if err == nil {
			err = rerr
		}
		if rerr == nil {
			dir = rd
		} else {
			var args []string
			if f.json {
				args = []string{"--json"}
			}
			emitDetectionError(ctx, f.json, detectionError(f.root, err.Error(), handle, code, "finalize-tasks", args))
			return cli.Exit(1)
		}
	}
	f.slug = filepath.Base(dir)
	f.dir = project.Specs(f.root, f.slug)
	f.missionType = missionart.MissionTypeFor(f.dir)
	if err := f.resolveBranches(); err != nil {
		return err
	}
	if err := f.validationGates(); err != nil {
		return err
	}
	if err := f.bootstrapLoop(); err != nil {
		return err
	}
	if !f.validateOnly {
		if err := f.flushWrites(); err != nil {
			return f.emitErr(err.Error())
		}
	}
	manifests, err := f.ownershipGates()
	if err != nil {
		return err
	}
	if !f.validateOnly {
		lifecycle.EmitArtifactPhase(statusWriteDir(f.root, f.slug), lifecycle.PhaseInput{EventType: lifecycle.TasksStarted, MissionSlug: f.slug, Actor: finalizeCmd})
	}
	if f.validateOnly {
		return f.validateOnlyReport(manifests)
	}
	return f.commitPipeline(manifests)
}

// resolveBranches is _run_finalize_branch_setup.
func (f *finalizeRun) resolveBranches() error {
	meta, merr := mission.ReadMeta(f.dir)
	if f.override != "" {
		f.target = f.override
	} else {
		if merr == mission.ErrNoMeta {
			return f.planningBranchErr(fmt.Sprintf("meta.json not found at %s. Re-run with --target-branch <ref> to override.", filepath.Join(f.dir, "meta.json")))
		}
		if merr != nil {
			return f.planningBranchErr(fmt.Sprintf("meta.json at %s is unreadable: %v. Re-run with --target-branch <ref> to override.", filepath.Join(f.dir, "meta.json"), merr))
		}
		t := strings.TrimSpace(meta.GetString("target_branch"))
		if t == "" {
			t = strings.TrimSpace(meta.GetString("merge_target_branch"))
		}
		if t == "" {
			return f.planningBranchErr("meta.json is missing target_branch / merge_target_branch. This mission was created before branch context was persisted. Re-run with --target-branch <ref> to override, or re-create the mission so meta.json records the canonical target.")
		}
		f.target = t
	}
	if meta == nil {
		meta = &jsonx.Object{}
	}
	if truthyValue(func() any { v, _ := meta.Get("pr_bound"); return v }()) && meta.GetString("merge_target_branch") == "" && f.override == "" &&
		coord.IsProtected(f.root, "", f.target) {
		msg := "Legacy PR-bound metadata records a protected merge target as its planning branch, but the original planning branch cannot be proven from the current checkout. Re-run with --target-branch <planning-ref> to repair the branch contract explicitly."
		return f.emitErr(msg, "error_code", "PR_BOUND_PLANNING_BRANCH_REQUIRED")
	}
	f.mergeTarget = f.target
	if mt := strings.TrimSpace(meta.GetString("merge_target_branch")); mt != "" {
		f.mergeTarget = mt
	} else if truthyValue(func() any { v, _ := meta.Get("pr_bound"); return v }()) {
		if dt := strings.TrimSpace(meta.GetString("target_branch")); dt != "" && dt != f.target {
			f.mergeTarget = dt
		}
	}
	f.human(fmt.Sprintf("[bold cyan]Branch:[/bold cyan] %s (target for this mission)", f.target))
	if !f.validateOnly && f.override != "" && merr == nil {
		prev := meta.GetString("target_branch")
		f.previousValue = prev
		if prev != f.target {
			meta.Set("target_branch", f.target)
			if err := mission.WriteMeta(f.dir, meta, false); err == nil {
				f.persisted = true
				if prev != "" {
					f.human(fmt.Sprintf("[yellow]Note:[/yellow] target_branch override persisted to meta.json (%s -> %s)", prev, f.target))
				}
			}
		}
	}
	return nil
}

func (f *finalizeRun) planningBranchErr(msg string) error {
	if f.json {
		emitJSON(f.ctx.Stdout, jsonx.NewObject("error", msg, "error_code", "PLANNING_BRANCH_NOT_PERSISTED"))
	} else {
		say(f.ctx.Stdout, "[red]Error:[/red] "+msg)
		say(f.ctx.Stdout, "[yellow]Hint:[/yellow] re-run with [bold]--target-branch <ref>[/bold] to override.")
	}
	return cli.Exit(1)
}

// validationGates is _run_finalize_validation_gates.
func (f *finalizeRun) validationGates() error {
	f.tasksDir = filepath.Join(f.dir, "tasks")
	if _, err := os.Stat(f.tasksDir); err != nil {
		return f.emitErr("Tasks directory not found: " + f.tasksDir)
	}
	f.wpFiles = scandirGlob(f.tasksDir, "WP*.md")
	seen := map[string]bool{}
	for _, p := range f.wpFiles {
		if m := wpFilePrefix.FindStringSubmatch(filepath.Base(p)); m != nil && !seen[m[1]] {
			seen[m[1]] = true
			f.wpIDs = append(f.wpIDs, m[1])
		}
	}
	sort.Strings(f.wpIDs)
	specPath := filepath.Join(f.dir, "spec.md")
	b, err := os.ReadFile(specPath)
	if err != nil {
		msg := "spec.md not found: " + specPath
		if f.json {
			printCompact(f.ctx.Stdout, jsonx.NewObject("error", msg))
		} else {
			say(f.ctx.Stdout, "[red]Error:[/red] "+msg)
		}
		return cli.Exit(1)
	}
	f.specContent = string(b)
	f.specIDs = reqmap.ParseSpecIDs(f.specContent, f.missionType)
	f.declared = map[string]bool{}
	for _, id := range f.specIDs.All {
		f.declared[id] = true
	}
	f.functional = map[string]bool{}
	fn := f.specIDs.Functional()
	if f.missionType == "research" {
		fn = append(append(append([]string{}, f.specIDs.Buckets["data_collection"]...), f.specIDs.Buckets["analysis"]...), f.specIDs.Buckets["quality"]...)
	}
	for _, id := range fn {
		f.functional[id] = true
	}
	f.extractWarns = reqmap.FindUndeclaredRequirementCitations(f.specContent)
	f.preexisting = map[string]bool{}
	filepath.Walk(f.dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			f.preexisting[p] = true
		}
		return nil
	})
	// TODO(wps.yaml): the tasks-outline manifest flow is not ported.
	if err := f.resolveDeps(); err != nil {
		return err
	}
	if err := f.validateGraph(); err != nil {
		return err
	}
	if err := f.validateMapping(); err != nil {
		return err
	}
	if err := f.depConflicts(); err != nil {
		return err
	}
	for _, w := range f.extractWarns {
		f.human("[yellow]Warning:[/yellow] " + w)
	}
	return nil
}

func (f *finalizeRun) resolveDeps() error {
	f.deps = map[string][]string{}
	raw, _ := reqmap.ReadAllWPRawRefs(f.tasksDir)
	f.refs = raw
	tasksMD := filepath.Join(f.dir, "tasks.md")
	b, err := os.ReadFile(tasksMD)
	if err != nil {
		return nil
	}
	content := string(b)
	tdeps, _ := parseDependenciesFromTasksMD(content)
	var missing, extra []string
	exp := map[string]bool{}
	for _, id := range f.wpIDs {
		exp[id] = true
		if _, ok := tdeps[id]; !ok {
			missing = append(missing, id)
		}
	}
	for id := range tdeps {
		if !exp[id] {
			extra = append(extra, id)
		}
	}
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		msg := "tasks.md work package coverage is incomplete. finalize-tasks could not match all WP files to parsed sections, so dependency lanes would be unreliable."
		hint := "Use supported section headers such as '## WP01', '## Work Package WP01', or '## Work Package 1 — Title'."
		if f.json {
			emitJSON(f.ctx.Stdout, jsonx.NewObject("error", msg, "missing_wp_sections", strList(nonNil(missing)), "extra_wp_sections", strList(nonNil(extra)), "hint", hint))
		} else {
			say(f.ctx.Stdout, "[red]Error:[/red] "+msg)
			if len(missing) > 0 {
				say(f.ctx.Stdout, "  Missing WP sections: "+strings.Join(missing, ", "))
			}
			if len(extra) > 0 {
				say(f.ctx.Stdout, "  Extra WP sections: "+strings.Join(extra, ", "))
			}
			say(f.ctx.Stdout, "  "+hint)
		}
		return cli.Exit(1)
	}
	for id, r := range reqmap.TasksMDRefs(content, f.missionType) {
		if len(r) > 0 && len(f.refs[id]) == 0 {
			f.refs[id] = r
		}
	}
	for _, p := range f.wpFiles {
		m := wpFilePrefix.FindStringSubmatch(filepath.Base(p))
		if m == nil {
			continue
		}
		w, err := wp.Read(p)
		if err != nil {
			return f.emitErr(err.Error())
		}
		if d := w.Dependencies(); len(d) > 0 {
			f.deps[m[1]] = d
		} else {
			d := tdeps[m[1]]
			if d == nil {
				d = []string{}
			}
			f.deps[m[1]] = d
		}
	}
	f.depOrder = nil
	for _, p := range f.wpFiles {
		if m := wpFilePrefix.FindStringSubmatch(filepath.Base(p)); m != nil {
			f.depOrder = append(f.depOrder, m[1])
		}
	}
	return nil
}

func (f *finalizeRun) validateGraph() error {
	if len(f.deps) == 0 {
		return nil
	}
	if cycles := detectCycles(f.deps, f.depOrder); len(cycles) > 0 {
		var parts []string
		var cl []any
		for _, c := range cycles {
			parts = append(parts, pyListRepr(c))
			cl = append(cl, strList(c))
		}
		msg := "Circular dependencies detected: [" + strings.Join(parts, ", ") + "]"
		if f.json {
			emitJSON(f.ctx.Stdout, jsonx.NewObject("error", msg, "cycles", cl))
		} else {
			say(f.ctx.Stdout, "[red]Error:[/red] Circular dependencies detected:")
			for _, c := range cycles {
				say(f.ctx.Stdout, "  "+strings.Join(c, " → "))
			}
		}
		return cli.Exit(1)
	}
	for _, id := range f.depOrder {
		if errs := validateDependencies(id, f.deps[id], f.deps, f.depOrder); len(errs) > 0 {
			msg := fmt.Sprintf("Invalid dependencies for %s: %s", id, pyListRepr(errs))
			if f.json {
				emitJSON(f.ctx.Stdout, jsonx.NewObject("error", msg, "wp_id", id, "errors", strList(errs)))
			} else {
				say(f.ctx.Stdout, fmt.Sprintf("[red]Error:[/red] Invalid dependencies for %s:", id))
				for _, e := range errs {
					say(f.ctx.Stdout, "  - "+e)
				}
			}
			return cli.Exit(1)
		}
	}
	return nil
}

func depsObject(m map[string][]string, order []string) *jsonx.Object {
	o := jsonx.NewObject()
	for _, k := range order {
		if v, ok := m[k]; ok {
			o.Set(k, strList(v))
		}
	}
	return o
}

func (f *finalizeRun) refsObject() *jsonx.Object {
	_, order := reqmap.ReadAllWPRawRefs(f.tasksDir)
	o := jsonx.NewObject()
	seen := map[string]bool{}
	for _, k := range order {
		seen[k] = true
		o.Set(k, strList(f.refs[k]))
	}
	var extra []string
	for k := range f.refs {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		o.Set(k, strList(f.refs[k]))
	}
	return o
}

// validateMapping is _validate_requirement_mapping (+ diagnostics).
func (f *finalizeRun) validateMapping() error {
	var missingWPs []string
	unknown := jsonx.NewObject()
	rejected := jsonx.NewObject()
	mapped := map[string]bool{}
	for _, id := range f.wpIDs {
		acc := map[string]bool{}
		var rej []any
		var failing []string
		for _, raw := range f.refs[id] {
			a, reason := reqmap.Classify(raw, f.declared, f.missionType)
			if reason == "" {
				acc[a.Canonical()] = true
				continue
			}
			rej = append(rej, jsonx.NewObject("ref", raw, "reason", reason))
			if reqmap.FailingReasons[reason] {
				failing = append(failing, raw)
			}
		}
		if len(rej) > 0 {
			rejected.Set(id, rej)
			if len(failing) > 0 {
				sort.Strings(failing)
				unknown.Set(id, strList(failing))
			}
		}
		if len(acc) == 0 {
			missingWPs = append(missingWPs, id)
		}
		for k := range acc {
			mapped[k] = true
		}
	}
	var unmapped []string
	for k := range f.functional {
		if !mapped[k] {
			unmapped = append(unmapped, k)
		}
	}
	sort.Strings(unmapped)
	var bare []string
	for _, c := range reqmap.FindBareProseRequirementIDs(f.specContent) {
		bare = append(bare, c.IDs...)
	}
	bare = dedupSorted(bare)
	parsed := jsonx.NewObject()
	for _, k := range reqmap.BucketOrder {
		parsed.Set(k, strList(f.specIDs.Buckets[k]))
	}
	if f.specIDs.Research {
		for _, k := range []string{"data_collection", "analysis", "quality"} {
			parsed.Set(k, strList(f.specIDs.Buckets[k]))
		}
	}
	referenced := jsonx.NewObject()
	wpKeys := make([]string, 0, len(f.refs))
	for k := range f.refs {
		wpKeys = append(wpKeys, k)
	}
	sort.Strings(wpKeys)
	for _, id := range wpKeys {
		for _, raw := range f.refs[id] {
			a, reason := reqmap.Classify(raw, f.declared, f.missionType)
			if reason != "" || a.Kind != "SC" {
				continue
			}
			c := a.Canonical()
			var cur []any
			if v, ok := referenced.Get(c); ok {
				cur = v.([]any)
			}
			dup := false
			for _, x := range cur {
				if x == id {
					dup = true
				}
			}
			if !dup {
				referenced.Set(c, append(cur, id))
			}
		}
	}
	var unref []string
	for _, sc := range f.specIDs.Buckets["success_criteria"] {
		if !referenced.Has(sc) {
			unref = append(unref, sc)
		}
	}
	sort.Strings(unref)
	f.diagnostics = jsonx.NewObject("parsed_spec_ids", parsed, "rejected_requirement_refs", rejected,
		"success_criteria_coverage", jsonx.NewObject("referenced", referenced, "unreferenced", strList(nonNil(unref))))
	if len(missingWPs) == 0 && unknown.Len() == 0 && len(unmapped) == 0 && len(bare) == 0 {
		return nil
	}
	if f.json {
		p := jsonx.NewObject("error", "Requirement mapping validation failed", "missing_requirement_refs_wps", strList(nonNil(missingWPs)),
			"unknown_requirement_refs", unknown, "unmapped_functional_requirements", strList(nonNil(unmapped)),
			"bare_prose_requirement_ids", strList(nonNil(bare)), "dependencies_parsed", depsObject(f.deps, f.depOrder),
			"requirement_refs_parsed", f.refsObject())
		for _, kv := range f.diagnostics.Pairs() {
			p.Set(kv.Key, kv.Value)
		}
		printCompact(f.ctx.Stdout, p)
		return cli.Exit(1)
	}
	out := f.ctx.Stdout
	say(out, "[red]Error:[/red] Requirement mapping validation failed")
	if len(missingWPs) > 0 {
		say(out, "[red]Missing requirement refs:[/red]")
		for _, id := range missingWPs {
			say(out, "  - "+id)
		}
	}
	if unknown.Len() > 0 {
		say(out, "[red]Unknown requirement refs:[/red]")
		for _, kv := range unknown.Pairs() {
			var parts []string
			for _, x := range kv.Value.([]any) {
				parts = append(parts, x.(string))
			}
			say(out, fmt.Sprintf("  - %s: %s", kv.Key, strings.Join(parts, ", ")))
		}
	}
	if len(unmapped) > 0 {
		say(out, "[red]Unmapped functional requirements:[/red]")
		for _, id := range unmapped {
			say(out, "  - "+id)
		}
	}
	if len(bare) > 0 {
		say(out, "[red]Bare-prose requirement id(s) found, uncounted:[/red]")
		for _, id := range bare {
			say(out, "  - "+id)
		}
	}
	if rejected.Len() > 0 {
		say(out, "[red]Rejected requirement refs:[/red]")
		for _, kv := range rejected.Pairs() {
			for _, e := range kv.Value.([]any) {
				eo := e.(*jsonx.Object)
				sayPlain(out, fmt.Sprintf("  - %s: %s (%s)", kv.Key, eo.GetString("ref"), eo.GetString("reason")))
			}
		}
	}
	return cli.Exit(1)
}

func (f *finalizeRun) depConflicts() error {
	var errs []string
	for _, id := range f.depOrder {
		parsed := f.deps[id]
		var existing []string
		for _, p := range f.wpFiles {
			if m := wpFilePrefix.FindStringSubmatch(filepath.Base(p)); m != nil && m[1] == id {
				if w, err := wp.Read(p); err == nil {
					existing = w.Dependencies()
				}
				break
			}
		}
		if len(existing) > 0 && len(parsed) > 0 && !sameSet(existing, parsed) {
			a, b := append([]string{}, existing...), append([]string{}, parsed...)
			sort.Strings(a)
			sort.Strings(b)
			errs = append(errs, fmt.Sprintf("%s: frontmatter has %s, tasks.md parsed %s. Resolve the disagreement in tasks.md or WP frontmatter before finalizing.", id, pyListRepr(a), pyListRepr(b)))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	msg := "Dependency disagreement detected:\n" + strings.Join(errs, "\n")
	return f.emitErr(msg, "dependency_conflicts", strList(errs))
}

func sameSet(a, b []string) bool {
	m := map[string]bool{}
	for _, x := range a {
		m[x] = true
	}
	n := map[string]bool{}
	for _, x := range b {
		n[x] = true
		if !m[x] {
			return false
		}
	}
	return len(m) == len(n)
}

func branchStrategy(target, final string) string {
	return fmt.Sprintf("Planning artifacts for this mission were generated on %s. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into %s unless the human explicitly redirects the landing branch.", target, final)
}

// bootstrapLoop is _run_bootstrap_loop.
func (f *finalizeRun) bootstrapLoop() error {
	f.frontmatter = map[string]*jsonx.Object{}
	f.bodies = map[string]string{}
	f.ownershipWarn = []string{}
	f.postIntegWarn = []string{}
	f.modified, f.unchanged, f.preserved, f.wouldModify = []string{}, []string{}, []string{}, []any{}
	for _, p := range f.wpFiles {
		m := wpFilePrefix.FindStringSubmatch(filepath.Base(p))
		if m == nil {
			continue
		}
		id := m[1]
		w, err := wp.Read(p)
		if err != nil {
			f.human(fmt.Sprintf("[yellow]Warning:[/yellow] Could not read %s: %v", filepath.Base(p), err))
			continue
		}
		dumped, err := wpMetaDump(w.Meta())
		if err != nil {
			f.human(fmt.Sprintf("[yellow]Warning:[/yellow] Could not read %s: %v", filepath.Base(p), err))
			continue
		}
		hasDepsLine := w.Meta().Has("dependencies")
		_, depsIsString := func() (any, bool) { v, _ := w.Meta().Get("dependencies"); _, s := v.(string); return v, s }()
		deps := f.deps[id]
		if deps == nil {
			deps = []string{}
		}
		refs := f.refs[id]
		title := w.Meta().GetString("title")
		if title == "" {
			title = id
		}
		f.workPackages = append(f.workPackages, jsonx.NewObject("id", id, "title", title, "dependencies", strList(deps), "requirement_refs", strList(refs)))
		changes := jsonx.NewObject()
		existingDeps := wp.StringList(dumped, "dependencies")
		if depsIsString || !hasDepsLine || !equalStrings(existingDeps, deps) {
			changes.Set("dependencies", strList(deps))
		}
		final := f.mergeTarget
		if dumped.GetString("planning_base_branch") != f.target {
			changes.Set("planning_base_branch", f.target)
		}
		if dumped.GetString("merge_target_branch") != final {
			changes.Set("merge_target_branch", final)
		}
		if bs := branchStrategy(f.target, final); dumped.GetString("branch_strategy") != bs {
			changes.Set("branch_strategy", bs)
		}
		if len(wp.StringList(dumped, "requirement_refs")) == 0 && len(refs) > 0 {
			changes.Set("requirement_refs", strList(refs))
		}
		// TODO: ownership inference (infer_ownership) for WPs missing
		// execution_mode / owned_files is not ported.
		updated := dumped.Clone()
		for _, kv := range changes.Pairs() {
			updated.Set(kv.Key, kv.Value)
		}
		f.frontmatter[id] = updated
		f.bodies[id] = w.Doc.Body
		if changes.Len() > 0 {
			if !f.validateOnly {
				f.pending = append(f.pending, pendingWrite{w: w, delta: changes})
			} else {
				f.wouldModify = append(f.wouldModify, jsonx.NewObject("wp_id", id, "changes", changes))
			}
			f.updated++
			f.modified = append(f.modified, id)
		} else {
			f.unchanged = append(f.unchanged, id)
		}
	}
	// owned_files under kitty-specs/ are refused.
	var invalid []any
	var invalidHuman []string
	ids := sortedKeysObj(f.frontmatter)
	for _, id := range ids {
		for _, of := range wp.StringList(f.frontmatter[id], "owned_files") {
			n := strings.TrimLeft(strings.TrimSpace(filepath.ToSlash(of)), "")
			for strings.HasPrefix(n, "./") {
				n = n[2:]
			}
			if n == "kitty-specs" || strings.HasPrefix(n, "kitty-specs/") {
				invalid = append(invalid, jsonx.NewObject("wp_id", id, "path", of))
				invalidHuman = append(invalidHuman, fmt.Sprintf("  - %s: %s", id, of))
			}
		}
	}
	if len(invalid) > 0 {
		msg := "WP owned_files cannot include paths under kitty-specs/"
		if f.json {
			emitJSON(f.ctx.Stdout, jsonx.NewObject("error", msg, "error_code", "INVALID_WP_OWNED_FILES_KITTY_SPECS", "invalid_owned_files", invalid))
		} else {
			say(f.ctx.Stdout, "[red]Error:[/red] "+msg)
			for _, l := range invalidHuman {
				say(f.ctx.Stdout, l)
			}
		}
		return cli.Exit(1)
	}
	return nil
}

func sortedKeysObj[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// flushWrites is _flush_frontmatter_writes (delta applied to a fresh read).
func (f *finalizeRun) flushWrites() error {
	for _, pw := range f.pending {
		fresh, err := wp.Read(pw.w.Path)
		if err != nil {
			return err
		}
		dumped, err := wpMetaDump(fresh.Meta())
		if err != nil {
			return err
		}
		for _, kv := range pw.delta.Pairs() {
			if kv.Key == "requirement_refs" && len(wp.StringList(dumped, "requirement_refs")) > 0 {
				continue
			}
			dumped.Set(kv.Key, kv.Value)
		}
		if err := writeWPMetaDump(fresh, dumped); err != nil {
			return err
		}
	}
	return nil
}

type ownManifest struct {
	mode, surface, scope string
	owned               []string
}

// ownershipGates validates ownership manifests (validate_all + globs).
func (f *finalizeRun) ownershipGates() (map[string]ownManifest, error) {
	man := map[string]ownManifest{}
	for id, m := range f.frontmatter {
		mode := m.GetString("execution_mode")
		owned := wp.StringList(m, "owned_files")
		if mode != "" && len(owned) > 0 {
			man[id] = ownManifest{mode: mode, surface: m.GetString("authoritative_surface"), scope: m.GetString("scope"), owned: owned}
		}
	}
	if len(man) == 0 {
		return man, nil
	}
	ids := sortedKeysObj(man)
	var errs, warns []string
	reach := map[string]map[string]bool{}
	var walk func(id string, seen map[string]bool)
	walk = func(id string, seen map[string]bool) {
		for _, d := range wp.StringList(f.frontmatter[id], "dependencies") {
			if !seen[d] {
				seen[d] = true
				walk(d, seen)
			}
		}
	}
	for _, id := range ids {
		s := map[string]bool{}
		walk(id, s)
		reach[id] = s
	}
	narrow := []string{}
	for _, id := range f.wpIDs {
		if m, ok := man[id]; ok && m.scope != "codebase-wide" {
			narrow = append(narrow, id)
		}
	}
	for i := 0; i < len(narrow); i++ {
		for j := i + 1; j < len(narrow); j++ {
			a, b := narrow[i], narrow[j]
			if reach[a][b] || reach[b][a] {
				continue
			}
			for _, ga := range man[a].owned {
				for _, gb := range man[b].owned {
					if lanes.GlobsOverlap(ga, gb) {
						errs = append(errs, fmt.Sprintf("Overlap: %s (%s) and %s (%s) claim overlapping paths.", a, mission.PyRepr(ga), b, mission.PyRepr(gb)))
					}
				}
			}
		}
	}
	for _, id := range f.wpIDs {
		m, ok := man[id]
		if !ok || m.scope == "codebase-wide" {
			continue
		}
		if m.surface == "" {
			errs = append(errs, id+": authoritative_surface is empty.")
		} else {
			okp := false
			for _, p := range m.owned {
				if p == m.surface || strings.HasPrefix(p, m.surface) {
					okp = true
				}
			}
			if !okp {
				errs = append(errs, fmt.Sprintf("%s: authoritative_surface %s is not a prefix of any owned_files entry: %s", id, mission.PyRepr(m.surface), pyListRepr(m.owned)))
			}
		}
		if m.mode == "code_change" {
			has := false
			for _, p := range m.owned {
				if strings.HasPrefix(p, "src/") || strings.HasPrefix(p, "tests/") {
					has = true
				}
			}
			if !has {
				warns = append(warns, fmt.Sprintf("%s: code_change WP does not own any files under src/ or tests/. owned_files: %s", id, pyListRepr(m.owned)))
			}
		}
	}
	for _, w := range warns {
		f.human("[yellow]Ownership warning:[/yellow] " + w)
	}
	if len(errs) > 0 {
		if f.json {
			emitJSON(f.ctx.Stdout, jsonx.NewObject("error", "Ownership validation failed", "ownership_errors", strList(errs)))
		} else {
			say(f.ctx.Stdout, "[red]Error:[/red] Ownership validation failed")
			for _, e := range errs {
				say(f.ctx.Stdout, "  - "+e)
			}
		}
		return nil, cli.Exit(1)
	}
	var gerrs []string
	for _, id := range ids {
		intent := map[string]bool{}
		for _, c := range wp.StringList(f.frontmatter[id], "create_intent") {
			intent[c] = true
		}
		for _, pat := range man[id].owned {
			matches, _ := filepath.Glob(filepath.Join(f.root, pat))
			if len(matches) > 0 {
				continue
			}
			switch {
			case strings.ContainsAny(pat, "*?["):
				msg := fmt.Sprintf("%s: owned_files glob '%s' matches zero files in the repository", id, pat)
				f.ownershipWarn = append(f.ownershipWarn, msg)
				if !f.json {
					fmt.Fprintln(f.ctx.Stderr, wrapPlain("WARNING: Ownership: "+msg))
				}
			case intent[pat]:
				if !f.json {
					fmt.Fprintln(f.ctx.Stderr, wrapPlain(fmt.Sprintf("INFO: %s: owned_files path '%s' has no match — suppressed by create_intent (planned-new-file).", id, pat)))
				}
			default:
				gerrs = append(gerrs, fmt.Sprintf("%s: owned_files path '%s' is a literal file path that matches zero files in the repository. If this file will be created during implementation, declare it in the WP frontmatter:\n  create_intent:\n    - %s", id, pat, pat))
			}
		}
	}
	if len(gerrs) > 0 {
		if !f.json {
			for _, e := range gerrs {
				fmt.Fprintln(f.ctx.Stderr, wrapPlain("ERROR: Ownership: "+e))
			}
		}
		msg := "Ownership validation failed: literal-path owned_files entries match zero files. Fix the paths or add them to 'create_intent'."
		if f.json {
			emitJSON(f.ctx.Stdout, jsonx.NewObject("error", msg, "ownership_literal_path_errors", strList(gerrs)))
		} else {
			say(f.ctx.Stdout, "[red]Error:[/red] "+msg)
		}
		return nil, cli.Exit(1)
	}
	return man, nil
}

func (f *finalizeRun) metaObj() *jsonx.Object { return mission.ReadMetaOrEmpty(f.dir) }

func (f *finalizeRun) computeLanes(man map[string]ownManifest, previous *lanes.Manifest, planningSHA *string) (*lanes.Manifest, error) {
	meta := f.metaObj()
	own := map[string]*lanes.Ownership{}
	for id, m := range man {
		own[id] = &lanes.Ownership{ExecutionMode: m.mode, OwnedFiles: m.owned}
	}
	graph := map[string][]string{}
	for k, v := range f.deps {
		graph[k] = v
	}
	var mid *string
	if s := meta.GetString("mission_id"); s != "" {
		mid = &s
	}
	in := lanes.ComputeInput{DependencyGraph: graph, Ownership: own, MissionSlug: f.slug, TargetBranch: f.target, WPBodies: f.bodies,
		MissionID: mid, Previous: previous, Topology: coord.TopologyFromMeta(meta, f.dir), MissionBranch: meta.GetString("mission_branch")}
	m, err := lanes.Compute(in)
	if err != nil {
		return nil, err
	}
	m.PlanningCommitSHA = planningSHA
	return m, nil
}

// bootstrapPreview counts WPs without canonical events.
func (f *finalizeRun) bootstrapCounts() (total, seeded, existing int, toSeed []string) {
	s := coord.Resolve(f.root, f.slug)
	stream, _ := status.ReadStream(s.StatusDir(), "")
	have := map[string]bool{}
	if stream != nil {
		for _, t := range stream.Transitions {
			have[t.WPID] = true
		}
	}
	files := append([]string(nil), f.wpFiles...)
	sort.Strings(files)
	for _, p := range files {
		w, err := wp.Read(p)
		if err != nil {
			continue
		}
		id := w.Meta().GetString("work_package_id")
		if id == "" {
			continue
		}
		total++
		if have[id] {
			existing++
		} else {
			seeded++
			toSeed = append(toSeed, id)
		}
	}
	return
}

func (f *finalizeRun) lanesStats(m *lanes.Manifest) *jsonx.Object {
	if m == nil {
		return jsonx.NewObject("computed", false)
	}
	ids := []any{}
	for _, l := range m.Lanes {
		ids = append(ids, l.LaneID)
	}
	var cr any
	if m.CollapseReport != nil {
		cr = m.CollapseReport.ToObject()
	}
	return jsonx.NewObject("computed", true, "count", len(m.Lanes), "lane_ids", ids, "planning_artifact_wps", strList(nonNil(m.PlanningArtifactWPs)), "collapse_report", cr)
}

func (f *finalizeRun) laneInputsOK(man map[string]ownManifest) error {
	if len(man) > 0 && len(f.deps) > 0 {
		return nil
	}
	var code []string
	for id, m := range f.frontmatter {
		mode := m.GetString("execution_mode")
		if (mode == "" || mode == "code_change") && m.GetString("lane") != "canceled" {
			code = append(code, id)
		}
	}
	if len(code) == 0 {
		return nil
	}
	var missing []string
	if len(man) == 0 {
		missing = append(missing, "ownership manifests")
	}
	if len(f.deps) == 0 {
		missing = append(missing, "WP dependencies")
	}
	msg := "Lane computation aborted: missing " + strings.Join(missing, " and ") + ". finalize-tasks cannot write lanes.json without complete tasks and ownership metadata."
	return f.emitErr(msg, "error_code", "LANE_COMPUTATION_ABORTED_EMPTY_INPUTS")
}

func (f *finalizeRun) laneError(err error) error {
	switch e := err.(type) {
	case *lanes.DependencyCycleError:
		return f.emitErr(e.Error(), "error_code", e.ErrorCode())
	}
	return f.emitErr(err.Error())
}

func (f *finalizeRun) validateOnlyReport(man map[string]ownManifest) error {
	total, seeded, existing, _ := f.bootstrapCounts()
	if err := f.laneInputsOK(man); err != nil {
		return err
	}
	var lm *lanes.Manifest
	if len(man) > 0 && len(f.deps) > 0 {
		prev, _ := lanes.Read(f.dir)
		m, err := f.computeLanes(man, prev, nil)
		if err != nil {
			return f.laneError(err)
		}
		lm = m
	}
	if f.json {
		r := jsonx.NewObject("result", "validation_passed", "mission_slug", f.slug, "wp_count", len(f.workPackages), "validate_only", true,
			"would_modify", f.wouldModify, "would_preserve", strList(f.preserved), "unchanged", strList(f.unchanged), "updated_wp_count", f.updated,
			"tasks_md_stale", false, "ownership_warnings", strList(f.ownershipWarn), "requirement_extraction_warnings", strList(nonNil(f.extractWarns)),
			"post_integration_acceptance_warnings", strList(f.postIntegWarn),
			"validation", jsonx.NewObject("bootstrap_preview", jsonx.NewObject("total_wps", total, "newly_seeded", seeded, "already_initialized", existing),
				"lanes_preview", f.lanesStats(lm)),
			"message", "All validations passed. Run without --validate-only to commit.")
		for _, kv := range f.diagnostics.Pairs() {
			r.Set(kv.Key, kv.Value)
		}
		emitJSON(f.ctx.Stdout, r)
		return nil
	}
	out := f.ctx.Stdout
	say(out, "[green]✓[/green] All validations passed (--validate-only mode, no commit)")
	say(out, "  Mission: "+f.slug)
	say(out, fmt.Sprintf("  WPs validated: %d", len(f.workPackages)))
	say(out, fmt.Sprintf("  Would modify: %d WP(s), preserve: %d, unchanged: %d", len(f.wouldModify), len(f.preserved), len(f.unchanged)))
	say(out, fmt.Sprintf("  Bootstrap: %d WPs would be seeded, %d already initialized", seeded, existing))
	if lm != nil {
		say(out, fmt.Sprintf("  Lanes: %d lane(s) would be computed", len(lm.Lanes)))
		if lm.CollapseReport != nil && lm.CollapseReport.IndependentWPsCollapsed > 0 {
			say(out, fmt.Sprintf("[yellow]⚠[/yellow] %d independent WP pair(s) collapsed into same lane. Run with --json to see details.", lm.CollapseReport.IndependentWPsCollapsed))
		}
	}
	return nil
}

// planningPin is _preserve_or_capture_planning_commit_sha (non-refresh).
type planningPin struct {
	sha, action, previous, tip, pinClass, reason string
}

func (f *finalizeRun) executionHasBegun() bool {
	s := coord.Resolve(f.root, f.slug)
	snap, err := status.ReduceDir(s.StatusDir())
	if err != nil || snap == nil {
		return false
	}
	for _, id := range f.wpIDs {
		if l := snap.Lane(id); l != "" && l != "planned" {
			return true
		}
	}
	return false
}

func branchTip(root, branch string) string {
	out, err := gitx.Run(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (f *finalizeRun) planningPin(previous *lanes.Manifest) (*planningPin, error) {
	if !f.executionHasBegun() {
		return &planningPin{sha: branchTip(f.root, f.target), action: "captured"}, nil
	}
	if previous == nil {
		return nil, f.emitErr(fmt.Sprintf("Cannot re-finalize mission %s: execution has begun (a WP is past 'planned') but no lanes.json exists on disk to preserve planning provenance from. Refusing to write a new lanes.json rather than guess a planning_commit_sha. Run 'spec-kitty doctor mission-state --fix --mission %s' to rebuild lanes.json from the event log.", mission.PyRepr(f.slug), f.slug))
	}
	recorded := ""
	if previous.PlanningCommitSHA != nil {
		recorded = *previous.PlanningCommitSHA
	}
	tip := branchTip(f.root, f.target)
	class := "indeterminate"
	switch {
	case tip == "" || recorded == "":
	case gitx.IsAncestor(f.root, recorded, tip):
		class = "advanced"
	case gitx.OK(f.root, "cat-file", "-e", recorded+"^{commit}"):
		class = "orphaned"
	default:
		class = "foreign"
	}
	switch class {
	case "orphaned":
		return nil, f.emitErr(fmt.Sprintf("Cannot re-finalize mission %s: the recorded planning_commit_sha %s is orphaned — present in this repository but not an ancestor of the %s tip %s (a mid-mission rebase). Lanes would keep merging a dead base. Re-run with --refresh-planning-commit --allow-orphaned to re-point the recorded SHA to the live tip, or investigate the divergence manually. Refusing to write lanes.json.", mission.PyRepr(f.slug), recorded, mission.PyRepr(f.target), tip))
	case "advanced":
		if recorded != tip && f.planningChanged(recorded, tip) && !f.bookkeepingOnly(recorded, tip) {
			return &planningPin{sha: tip, action: "refreshed", previous: recorded, tip: tip, pinClass: class}, nil
		}
	case "foreign":
		if recorded != tip {
			return &planningPin{sha: recorded, action: "kept_with_warning", previous: recorded, tip: tip, pinClass: class, reason: class}, nil
		}
	case "indeterminate":
		return &planningPin{sha: recorded, action: "kept_with_warning", previous: recorded, tip: tip, pinClass: class, reason: "indeterminate_tip_uncapturable"}, nil
	}
	return &planningPin{sha: recorded, action: "preserved", previous: recorded, tip: tip, pinClass: class}, nil
}

func (f *finalizeRun) planningChanged(recorded, tip string) bool {
	out, err := gitx.Run(f.root, "diff", "--name-only", recorded, tip, "--", "kitty-specs/"+f.slug)
	if err != nil {
		return false
	}
	for _, p := range strings.Split(out, "\n") {
		if p == "" || filepath.Base(p) == "lanes.json" {
			continue
		}
		if k := mission.ArtifactKindForPath(p); k != "" && safecommit.IsPrimaryKind(k) {
			return true
		}
	}
	return false
}

func (f *finalizeRun) bookkeepingOnly(recorded, tip string) bool {
	out, err := gitx.Run(f.root, "log", "--format=%s", recorded+".."+tip)
	if err != nil {
		return false
	}
	ok := false
	for _, s := range strings.Split(out, "\n") {
		if s == "" {
			continue
		}
		if s != "Add tasks for mission "+f.slug && s != "Add tasks for feature "+f.slug {
			return false
		}
		ok = true
	}
	return ok
}

func optAny(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// commitPipeline is _run_commit_pipeline.
func (f *finalizeRun) commitPipeline(man map[string]ownManifest) error {
	sdir := statusWriteDir(f.root, f.slug)
	// local canonical WPCreated + TasksCompleted
	for _, w := range f.workPackages {
		id := w.GetString("id")
		var wpPath any
		if m, _ := filepath.Glob(filepath.Join(f.tasksDir, id+"*.md")); len(m) > 0 {
			sort.Strings(m)
			if rel, err := filepath.Rel(f.root, m[0]); err == nil {
				wpPath = filepath.ToSlash(rel)
			}
		}
		deps, _ := w.Get("dependencies")
		payload := jsonx.NewObject("mission_slug", f.slug, "mission_number", nil, "wp_id", id, "wp_title", w.GetString("title"), "wp_path", wpPath,
			"depends_on", deps, "actor", finalizeCmd, "created_at", lifecycle.PydanticUTC(clock.Now()))
		lifecycle.Persist(sdir, "WPCreated", payload, id, "WorkPackage", nil, nil, jsonx.NewObject("mission_slug", f.slug, "wp_id", id))
	}
	tasksRel := "tasks.md"
	if _, err := os.Stat(filepath.Join(f.dir, "tasks.md")); err == nil {
		if r, err := filepath.Rel(f.root, filepath.Join(f.dir, "tasks.md")); err == nil {
			tasksRel = filepath.ToSlash(r)
		}
	}
	wc := len(f.workPackages)
	lifecycle.EmitArtifactPhase(sdir, lifecycle.PhaseInput{EventType: lifecycle.TasksCompleted, MissionSlug: f.slug, Actor: finalizeCmd, ArtifactPath: tasksRel, WPCount: &wc})
	// canonical bootstrap
	total, _, existing, toSeed := f.bootstrapCounts()
	surface := coord.Resolve(f.root, f.slug)
	seeded := 0
	reason := "canonical bootstrap"
	for _, id := range toSeed {
		r := reason
		req := &status.TransitionRequest{WPID: id, ToLane: "planned", Actor: status.Actor{Name: "finalize-tasks"}, Reason: &r, RepoRoot: f.root}
		if _, err := surface.EmitTransition(req); err != nil {
			return f.emitErr(err.Error())
		}
		seeded++
	}
	if seeded > 0 {
		f.human(fmt.Sprintf("[green]✓[/green] Bootstrapped canonical status: %d WPs seeded", seeded))
	}
	// lanes
	if err := f.laneInputsOK(man); err != nil {
		return err
	}
	var lm *lanes.Manifest
	var pin *planningPin
	lanesPath := ""
	if len(man) > 0 && len(f.deps) > 0 {
		prev, _ := lanes.Read(f.dir)
		var err error
		pin, err = f.planningPin(prev)
		if err != nil {
			return err
		}
		var sha *string
		if pin.sha != "" {
			s := pin.sha
			sha = &s
		}
		lm, err = f.computeLanes(man, prev, sha)
		if err != nil {
			return f.laneError(err)
		}
		if err := lanes.Write(f.dir, lm); err != nil {
			return f.emitErr(err.Error())
		}
		lanesPath = filepath.Join(f.dir, lanes.File)
		f.reportPin(pin)
		f.human(fmt.Sprintf("[green]✓[/green] Computed %d execution lane(s)", len(lm.Lanes)))
		if lm.CollapseReport != nil && lm.CollapseReport.IndependentWPsCollapsed > 0 {
			f.human(fmt.Sprintf("[yellow]⚠[/yellow] %d independent WP pair(s) collapsed into same lane. Run with --json to see details.", lm.CollapseReport.IndependentWPsCollapsed))
		}
	}
	if lm != nil {
		if p := f.scaffoldAcceptanceMatrix(); p != "" {
			rel, err := filepath.Rel(f.root, p)
			if err != nil {
				rel = p
			}
			f.human("[info] Scaffolded " + rel)
		}
	}
	// commit
	files := f.collectArtifacts(lanesPath)
	var rels []string
	for _, p := range files {
		rels = append(rels, relOrAbs(f.root, p))
	}
	primaryDirty := false
	if len(rels) > 0 {
		if e, _ := safecommit.StatusEntries(f.root, "", rels...); len(e) > 0 {
			primaryDirty = true
		}
	}
	coordFiles, coordDirty := f.coordCandidates()
	seen := map[string]bool{}
	for _, p := range files {
		seen[p] = true
	}
	for _, c := range coordFiles {
		if !seen[c] {
			files = append(files, c)
			rels = append(rels, relOrAbs(f.root, c))
			seen[c] = true
		}
	}
	commitCreated := false
	var commitHash any
	commitHashes := []any{}
	commitSurfaces := []any{}
	filesCommitted := []string{}
	refusal := false
	if !(primaryDirty || coordDirty) {
		f.human("[dim]Tasks unchanged, no commit needed[/dim]")
	} else {
		created := map[string]bool{}
		for _, p := range files {
			if !f.preexisting[p] && strings.HasPrefix(p, f.dir+string(filepath.Separator)) {
				created[p] = true
			}
		}
		r := safecommit.CommitForMission(f.root, f.slug, files, "Add tasks for mission "+f.slug, mission.KindTasksIndex, safecommit.RouterOptions{CreatedThisInvocation: created})
		commitSurfaces = r.SurfacesPayload()
		refusal = !r.Success() && r.Status != safecommit.StatusError && r.Status != safecommit.StatusNoOpWrongSurface
		for _, s := range r.Surfaces {
			if s.Status == "refused" || s.Status == "error" {
				refusal = true
			}
		}
		switch r.Status {
		case safecommit.StatusCommitted:
			commitCreated = true
			commitHash = r.CommitHash
			filesCommitted = rels
			for _, h := range r.CommitHashes {
				commitHashes = append(commitHashes, jsonx.NewObject("branch", h[0], "hash", h[1]))
			}
			if !f.json {
				r.PrintRendered(f.ctx.Stdout, wrapPlain)
				say(f.ctx.Stdout, fmt.Sprintf("[dim]Updated %d WP files with dependencies[/dim]", f.updated))
			}
		case safecommit.StatusUnchanged:
			if !f.json {
				if lines := r.Render(); len(lines) > 0 {
					r.PrintRendered(f.ctx.Stdout, wrapPlain)
				} else {
					say(f.ctx.Stdout, "[dim]Tasks unchanged, no commit needed[/dim]")
				}
			}
		default:
			d := r.Diagnostic
			if d == "" {
				d = "Failed to commit tasks updates"
			}
			if f.json {
				printCompact(f.ctx.Stdout, jsonx.NewObject("error", "Git commit failed: "+d))
			} else {
				r.PrintRendered(f.ctx.Stdout, wrapPlain)
				say(f.ctx.Stdout, "[red]Error:[/red] Git commit failed: "+d)
			}
			return cli.Exit(1)
		}
	}
	if f.json {
		depsObj := jsonx.NewObject()
		refsObj := jsonx.NewObject()
		for _, w := range f.workPackages {
			id := w.GetString("id")
			depsObj.Set(id, strList(nonNil(f.deps[id])))
		}
		for _, id := range sortedKeysObj(f.refs) {
			refsObj.Set(id, strList(f.refs[id]))
		}
		var override any
		if f.override != "" {
			override = f.override
		}
		metaRel := relOrAbs(f.root, filepath.Join(f.dir, "meta.json"))
		metaCommitted := false
		for _, r := range filesCommitted {
			if r == metaRel {
				metaCommitted = true
			}
		}
		var prevVal any
		if f.override != "" && f.previousValue != "" {
			prevVal = f.previousValue
		}
		pinObj := jsonx.NewObject("sha", nil, "action", nil, "previous_sha", nil, "branch_tip", nil)
		var refresh any
		if pin != nil {
			pinObj = jsonx.NewObject("sha", optAny(pin.sha), "action", pin.action, "previous_sha", optAny(pin.previous), "branch_tip", optAny(pin.tip))
			if pin.action == "preserved" || pin.action == "refreshed" || pin.action == "kept_with_warning" {
				refresh = jsonx.NewObject("status", pin.action, "recorded", optAny(pin.previous), "candidate", optAny(pin.tip), "pin_class", optAny(pin.pinClass), "reason", optAny(pin.reason))
			}
		}
		p := jsonx.NewObject("result", "success", "wp_count", len(f.workPackages), "updated_wp_count", f.updated, "modified_wps", strList(f.modified),
			"unchanged_wps", strList(f.unchanged), "preserved_wps", strList(f.preserved), "tasks_dir", f.tasksDir, "commit_created", commitCreated,
			"commit_hash", commitHash, "commit_hashes", commitHashes, "commit_surfaces", commitSurfaces, "files_committed", strList(filesCommitted),
			"dependencies_parsed", depsObj, "requirement_refs_parsed", refsObj,
			"bootstrap", jsonx.NewObject("total_wps", total, "newly_seeded", seeded, "already_initialized", existing),
			"lanes", f.lanesStats(lm), "ownership_warnings", strList(f.ownershipWarn), "requirement_extraction_warnings", strList(nonNil(f.extractWarns)),
			"post_integration_acceptance_warnings", strList(f.postIntegWarn),
			"target_branch_override", jsonx.NewObject("requested", override, "persisted", f.persisted, "committed", f.persisted && metaCommitted, "previous_value", prevVal, "persist_error", nil),
			"planning_commit", pinObj, "planning_commit_refresh", refresh)
		for _, kv := range f.diagnostics.Pairs() {
			p.Set(kv.Key, kv.Value)
		}
		emitJSON(f.ctx.Stdout, p)
	}
	if refusal {
		return cli.Exit(1)
	}
	return nil
}

func relOrAbs(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return p
}

func (f *finalizeRun) reportPin(pin *planningPin) {
	if f.json || pin == nil {
		return
	}
	switch pin.action {
	case "refreshed":
		prev := pin.previous
		if prev == "" {
			prev = "(none)"
		}
		f.human(fmt.Sprintf("[green]✓[/green] Refreshed planning_commit_sha %s -> %s (lanes merge the %s tip at their next allocation)", prev, pin.sha, f.target))
	case "preserved":
		if pin.sha != "" && pin.tip != "" && pin.tip != pin.sha && !f.bookkeepingOnly(pin.sha, pin.tip) {
			f.human(fmt.Sprintf("[yellow]⚠[/yellow] Planning branch %s has advanced since planning_commit_sha was recorded (%s -> tip %s); lanes keep merging the recorded snapshot. If that advance is a legitimate planning amendment, re-run finalize-tasks with --refresh-planning-commit to re-point it.", f.target, pin.sha, pin.tip))
		}
	}
}

// collectArtifacts is _collect_finalize_artifacts.
func (f *finalizeRun) collectArtifacts(lanesPath string) []string {
	var out []string
	add := func(p string) {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	add(filepath.Join(f.dir, "tasks.md"))
	add(filepath.Join(f.dir, "meta.json"))
	entries, _ := os.ReadDir(f.tasksDir)
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		out = append(out, filepath.Join(f.tasksDir, n))
	}
	if lanesPath != "" {
		add(lanesPath)
	}
	return out
}

// coordCandidates is _coord_candidate_dirt.
func (f *finalizeRun) coordCandidates() ([]string, bool) {
	s := coord.Resolve(f.root, f.slug)
	dir, err := s.WriteDir()
	if err != nil {
		dir = f.dir
	}
	checkout := f.root
	if s.Routed() && strings.HasPrefix(dir, s.CoordWorktree) {
		checkout = s.CoordWorktree
	}
	var files []string
	for _, n := range []string{"status.events.jsonl", "status.json", "issue-matrix.json", "issue-matrix.md", "acceptance-matrix.json"} {
		p := filepath.Join(dir, n)
		if _, err := os.Stat(p); err == nil {
			files = append(files, p)
		}
	}
	var rels []string
	for _, p := range files {
		rels = append(rels, relOrAbs(checkout, p))
	}
	dirty := false
	if len(rels) > 0 {
		if e, _ := safecommit.StatusEntries(checkout, "", rels...); len(e) > 0 {
			dirty = true
		}
	}
	return files, dirty
}

// scaffoldAcceptanceMatrix is scaffold_acceptance_matrix at the declared home.
func (f *finalizeRun) scaffoldAcceptanceMatrix() string {
	s := coord.Resolve(f.root, f.slug)
	home, err := s.WriteDir()
	if err != nil {
		home = f.dir
	}
	homePath := filepath.Join(home, "acceptance-matrix.json")
	if _, err := os.Stat(homePath); err == nil {
		return homePath
	}
	if _, err := os.Stat(filepath.Join(f.dir, "acceptance-matrix.json")); err == nil {
		return filepath.Join(f.dir, "acceptance-matrix.json")
	}
	crit := []any{}
	ids := sortedKeysObj(f.functional)
	for _, id := range ids {
		crit = append(crit, jsonx.NewObject("criterion_id", id, "description", "Verify "+id+" is satisfied", "proof_type", "automated_test",
			"evidence", nil, "pass_fail", "pending", "verified_by", nil, "verified_at", nil, "notes", "TODO: replace with a real acceptance criterion"))
	}
	if len(crit) == 0 {
		crit = append(crit, jsonx.NewObject("criterion_id", "AC-001", "description", "TODO: replace with a real acceptance criterion", "proof_type", "automated_test",
			"evidence", nil, "pass_fail", "pending", "verified_by", nil, "verified_at", nil, "notes", "TODO: replace with a real acceptance criterion"))
	}
	doc := jsonx.NewObject("mission_slug", f.slug, "mission_number", "", "mission_type", "", "overall_verdict", "pending", "criteria", crit, "negative_invariants", []any{})
	os.MkdirAll(home, 0o755)
	if err := os.WriteFile(homePath, []byte(jsonx.Dumps(doc, jsonx.Pretty)+"\n"), 0o644); err != nil {
		return ""
	}
	if home == f.dir {
		return homePath
	}
	r := safecommit.CommitForMission(f.root, f.slug, []string{homePath}, fmt.Sprintf("chore(%s): scaffold acceptance-matrix", f.slug), mission.KindAcceptanceMatrix, safecommit.RouterOptions{})
	if r.Status == safecommit.StatusCommitted || r.Status == safecommit.StatusUnchanged {
		return homePath
	}
	return homePath
}

// scandirGlob is Path.glob(pattern) for a flat pattern: directory (scandir)
// order, unsorted, like CPython.
func scandirGlob(dir, pattern string) []string {
	d, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer d.Close()
	names, _ := d.Readdirnames(-1)
	var out []string
	for _, n := range names {
		if ok, _ := filepath.Match(pattern, n); ok {
			out = append(out, filepath.Join(dir, n))
		}
	}
	return out
}
