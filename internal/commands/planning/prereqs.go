package planning

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/branchctx"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missionart"
	"github.com/spec-kitty/go-kitty3/internal/ownedcheckout"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
)

const checkPrereqLong = `Validate mission structure and prerequisites.

This command is designed for AI agents to call programmatically.

Examples:
    spec-kitty agent mission check-prerequisites --json
    spec-kitty agent mission check-prerequisites --mission 020-my-feature --paths-only --json
    spec-kitty agent mission check-prerequisites --mission my-feature --resume-probe --json`

const projectRootNotFound = "Could not locate project root. Run from within spec-kitty repository."

func checkPrereqFlags() []*cli.Flag {
	return []*cli.Flag{
		{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug (e.g., '020-my-mission')"},
		{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
		{Name: "paths-only", Kind: cli.Bool, Help: "Only output path variables"},
		{Name: "resume-probe", Kind: cli.Bool, Help: "Return structured found/not_found/existing/ambiguous/malformed state for safe specify resume"},
		{Name: "include-tasks", Kind: cli.Bool, Help: "Include tasks.md in validation"},
		{Name: "require-tasks", Kind: cli.Bool, Hidden: true, Help: "Deprecated alias for --include-tasks"},
		{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: ownedCheckoutHelp},
	}
}

// aliasPrereqFlags are the flags of the deprecated top-level
// `agent check-prerequisites` alias (upstream never grew --resume-probe or
// --owned-checkout there).
func aliasPrereqFlags() []*cli.Flag {
	return []*cli.Flag{
		{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug"},
		{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
		{Name: "paths-only", Kind: cli.Bool, Help: "Only output path variables"},
		{Name: "include-tasks", Kind: cli.Bool, Help: "Include tasks.md in validation"},
		{Name: "require-tasks", Kind: cli.Bool, Hidden: true, Help: "Deprecated alias for --include-tasks"},
	}
}

func init() {
	addCommands(func(_, agent, missionG, _ *cli.Command) {
		missionG.Add(&cli.Command{
			Name:  "check-prerequisites",
			Short: "Validate mission structure and prerequisites.",
			Long:  checkPrereqLong,
			Flags: checkPrereqFlags(),
			Run:   runCheckPrerequisites,
		})
		agent.Add(&cli.Command{
			Name:   "check-prerequisites",
			Short:  "Deprecated compatibility alias forwarding to agent mission check-prerequisites.",
			Hidden: true,
			Flags:  aliasPrereqFlags(),
			Run:    runCheckPrerequisites,
		})
	})
}

// consoleOrJSONError is _emit_console_or_json_error.
func consoleOrJSONError(ctx *cli.Context, jsonMode bool, msg string) {
	if jsonMode {
		emitJSON(ctx.Stdout, jsonx.NewObject("error", msg))
	} else {
		say(ctx.Stdout, "[red]Error:[/red] "+msg)
	}
}

func runCheckPrerequisites(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	includeTasks := ctx.Bool("include-tasks")
	requireTasks := ctx.Bool("require-tasks")
	if requireTasks && !includeTasks {
		includeTasks = true
		if !jsonMode {
			say(ctx.Stdout, "[yellow]Warning:[/yellow] --require-tasks is deprecated; use --include-tasks.")
		}
	}
	root := project.Locate("")
	if root == "" {
		consoleOrJSONError(ctx, jsonMode, projectRootNotFound)
		return cli.Exit(1)
	}
	feature := ctx.String("mission")
	// Validate --owned-checkout (or adopt the caller's checkout) exactly once;
	// a flagless --resume-probe skips adoption.
	envelope := func(code, msg string) *jsonx.Object {
		return withCLIVersion(ownedcheckout.FlatErrorEnvelope(code, msg))
	}
	var owned *ownedcheckout.Fact
	if !(ctx.Bool("resume-probe") && !ctx.IsSet("owned-checkout")) {
		f, err := resolveOwnedOrRefuse(ctx, root, feature, jsonMode, envelope, ownedcheckout.Options{})
		if err != nil {
			if _, ok := err.(*ownedcheckout.MetaReadError); ok {
				consoleOrJSONError(ctx, jsonMode, err.Error())
				return cli.Exit(1)
			}
			return err
		}
		owned = f
	}
	if owned != nil && ctx.Bool("resume-probe") {
		ownedcheckout.Emit(ctx.Stdout, ctx.Stderr, &ownedcheckout.Error{Code: ownedcheckout.OwnedOptionUnsupported, Message: "--resume-probe is not supported with --owned-checkout."}, jsonMode, envelope)
		return cli.Exit(1)
	}
	if owned != nil {
		root = owned.OwnedRoot
	}
	if ctx.Bool("resume-probe") {
		if strings.TrimSpace(feature) == "" {
			consoleOrJSONError(ctx, jsonMode, "--resume-probe requires --mission <provisional-slug>")
			return cli.Exit(1)
		}
		if ctx.Bool("paths-only") || includeTasks || requireTasks {
			consoleOrJSONError(ctx, jsonMode, "--resume-probe cannot be combined with task/path validation flags")
			return cli.Exit(1)
		}
		payload, err := resumeProbe(root, strings.TrimSpace(feature))
		if err != nil {
			consoleOrJSONError(ctx, jsonMode, err.Error())
			return cli.Exit(1)
		}
		state := payload.GetString("resume_state")
		if jsonMode {
			emitJSON(ctx.Stdout, payload)
		} else if state == "found" || state == "not_found" {
			say(ctx.Stdout, "[green]Resume probe:[/green] "+state)
		} else {
			say(ctx.Stdout, fmt.Sprintf("[red]Resume probe:[/red] %s: %s", state, payload.GetString("error")))
		}
		if state == "existing" || state == "ambiguous" || state == "malformed" {
			return cli.Exit(1)
		}
		return nil
	}
	var featureDir string
	var derr error
	if owned != nil {
		featureDir = owned.MissionDir
	} else {
		featureDir, derr = detectFeatureDir(root, feature)
	}
	if derr != nil {
		var args []string
		if jsonMode {
			args = append(args, "--json")
		}
		if ctx.Bool("paths-only") {
			args = append(args, "--paths-only")
		}
		if includeTasks {
			args = append(args, "--include-tasks")
		}
		payload := detectionError(root, derr.Error(), feature, "MISSION_CONTEXT_UNRESOLVED", "check-prerequisites", args)
		emitDetectionError(ctx, jsonMode, payload)
		return cli.Exit(1)
	}
	st := missionart.ValidateFeatureStructure(featureDir, includeTasks)
	target := branchctx.FeatureTargetBranch(featureDir, root)
	current := branchctx.CurrentBranch(root)
	if current == "" {
		current = target
	}
	if jsonMode {
		var payload *jsonx.Object
		if ctx.Bool("paths-only") {
			payload = pathsOnlyPayload(st)
		} else {
			payload = st.Payload()
		}
		expected := ""
		if owned != nil {
			expected = owned.WriteBranch
		}
		emitJSON(ctx.Stdout, branchctx.Inject(payload, target, current, "", expected))
		return nil
	}
	if st.Valid {
		say(ctx.Stdout, "[green]✓[/green] Prerequisites check passed")
		say(ctx.Stdout, "   Mission: "+filepath.Base(featureDir))
	} else {
		say(ctx.Stdout, "[red]✗[/red] Prerequisites check failed")
		for _, e := range st.Errors {
			say(ctx.Stdout, "   • "+e)
		}
	}
	for i, w := range st.Warnings {
		if i == 0 {
			say(ctx.Stdout, "\n[yellow]Warnings:[/yellow]")
		}
		say(ctx.Stdout, "   • "+w)
	}
	return nil
}

func pathsOnlyPayload(st *missionart.Structure) *jsonx.Object {
	p := st.Paths.Clone()
	p.Set("artifact_files", st.ArtifactFiles)
	p.Set("artifact_dirs", st.ArtifactDirs)
	p.Set("available_docs", strList(st.AvailableDocs))
	get := func(k string) string { return p.GetString(k) }
	fd := get("feature_dir")
	p.Set("FEATURE_DIR", fd)
	p.Set("SPEC_FILE", get("spec_file"))
	p.Set("PLAN_FILE", get("plan_file"))
	p.Set("TASKS_FILE", get("tasks_file"))
	p.Set("FEATURE_SPEC", get("spec_file"))
	p.Set("IMPL_PLAN", get("plan_file"))
	p.Set("TASKS", get("tasks_file"))
	specs := ""
	if fd != "" {
		specs = filepath.Dir(fd)
	}
	p.Set("SPECS_DIR", specs)
	return p
}

// detectionErr carries an ActionContextError-like (code, message).
type detectionErr struct{ code, msg string }

func (e *detectionErr) Error() string { return e.msg }

const notFoundHandleMarker = "Mission not found for handle"

// primaryAnchoredFeatureDir is _primary_anchored_feature_dir.
func primaryAnchoredFeatureDir(root, handle string) (string, error) {
	h := strings.TrimSpace(handle)
	if h == "" {
		return "", nil
	}
	if mission.SafePathSegment(h) {
		if st, err := os.Stat(project.Specs(root, h)); err == nil && st.IsDir() {
			return project.Specs(root, h), nil
		}
	}
	name, err := mission.ResolveHandle(root, h)
	if err != nil {
		switch e := err.(type) {
		case *mission.SelectorAmbiguousError:
			return "", &detectionErr{e.Code(), e.Error()}
		}
		return "", nil
	}
	d := project.Specs(root, name)
	if st, err := os.Stat(d); err == nil && st.IsDir() {
		return d, nil
	}
	return "", nil
}

// detectFeatureDir is the primary-anchored resolution with the coord-aware
// fallback (_find_feature_directory).
func detectFeatureDir(root, handle string) (string, error) {
	d, err := primaryAnchoredFeatureDir(root, handle)
	if err != nil {
		return "", err
	}
	if d != "" {
		return d, nil
	}
	h := strings.TrimSpace(handle)
	if h == "" {
		return "", &detectionErr{"MISSION_CONTEXT_UNRESOLVED", "--mission <slug> is required"}
	}
	// Coordination-only materialization: the coord worktree dir.
	if name, err := mission.ResolveHandle(root, h); err == nil {
		s := coord.Resolve(root, name)
		if s.CoordDir != "" {
			if st, err := os.Stat(s.CoordDir); err == nil && st.IsDir() {
				return s.CoordDir, nil
			}
		}
	}
	return "", &detectionErr{"MISSION_CONTEXT_UNRESOLVED", fmt.Sprintf("%s %s; checked the coordination worktree and the primary checkout.", notFoundHandleMarker, mission.PyRepr(h))}
}

type specCandidate struct {
	slug, featureDir, specFile string
	specExists                 bool
}

// listSpecCandidates is _list_feature_spec_candidates.
func listSpecCandidates(root string) []specCandidate {
	entries, err := os.ReadDir(project.Specs(root))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if st, err := os.Stat(project.Specs(root, e.Name())); err == nil && st.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out []specCandidate
	for _, n := range names {
		d := project.Specs(root, n)
		spec := filepath.Join(d, "spec.md")
		_, e1 := os.Stat(spec)
		_, e2 := os.Stat(filepath.Join(d, "meta.json"))
		if e1 != nil && e2 != nil {
			continue
		}
		out = append(out, specCandidate{slug: n, featureDir: d, specFile: spec, specExists: e1 == nil})
	}
	return out
}

// detectionError is _build_setup_plan_detection_error.
func detectionError(root, baseErr, missionFlag, code, command string, args []string) *jsonx.Object {
	explicit := strings.TrimSpace(missionFlag)
	if explicit != "" && strings.Contains(baseErr, notFoundHandleMarker) {
		return jsonx.NewObject("error_code", code, "mission_flag", missionFlag, "spec_kitty_version", app.Version,
			"error", "Mission not found: "+explicit,
			"remediation", "Re-run with an existing --mission <slug>, or create it: spec-kitty agent mission create <name> --json")
	}
	var flag any = missionFlag
	if missionFlag == "" {
		flag = nil
	}
	payload := jsonx.NewObject("error_code", code, "mission_flag", flag, "spec_kitty_version", app.Version)
	cands := listSpecCandidates(root)
	if len(cands) == 0 {
		payload.Set("error", "No missions found in kitty-specs/")
		payload.Set("remediation", "Run /spec-kitty.specify or: spec-kitty agent mission create <name> --json")
		return payload
	}
	var slugs []string
	for _, c := range cands {
		slugs = append(slugs, c.slug)
	}
	payload.Set("error", fmt.Sprintf("%d missions found, pass --mission <slug> to disambiguate", len(slugs)))
	payload.Set("available_missions", strList(slugs))
	suffix := ""
	if len(args) > 0 {
		suffix = " " + strings.Join(args, " ")
	}
	payload.Set("example_command", fmt.Sprintf("spec-kitty agent mission %s --mission %s%s", command, slugs[0], suffix))
	payload.Set("remediation", "Re-run with --mission <slug>")
	return payload
}

func emitDetectionError(ctx *cli.Context, jsonMode bool, payload *jsonx.Object) {
	if jsonMode {
		emitJSON(ctx.Stdout, payload)
		return
	}
	say(ctx.Stdout, "[red]Error:[/red] "+payload.GetString("error"))
	for i, s := range payload.GetList("available_missions") {
		if i >= 10 {
			break
		}
		say(ctx.Stdout, fmt.Sprintf("  - %v", s))
	}
	if payload.Has("example_command") {
		say(ctx.Stdout, "  "+payload.GetString("example_command"))
	}
}

// --- resume probe ------------------------------------------------------------

var resumeRequiredFields = []string{"mission_id", "slug", "mission_slug", "mission_type", "friendly_name", "purpose_tldr", "purpose_context", "target_branch", "topology", "created_at"}

var createdSnapshotFields = []string{"mission_id", "mission_slug", "mission_number", "mission_type", "friendly_name", "purpose_tldr", "purpose_context", "target_branch", "created_at"}

func resumeMatches(root, handle string) ([]string, error) {
	if msg := unsafeSegment(handle); msg != "" {
		return nil, fmt.Errorf("%s", msg)
	}
	set := map[string]bool{}
	if r, err := mission.ResolveIdentity(root, handle); err == nil {
		set[realpathOr(r.Dir)] = true
	} else if amb, ok := err.(*mission.AmbiguousError); ok {
		for _, c := range amb.Candidates {
			set[realpathOr(c.Dir)] = true
		}
	}
	specs := project.Specs(root)
	entries, err := os.ReadDir(specs)
	if err == nil {
		handleMid8 := mission.Mid8FromSlug(handle)
		human := mission.StripNumericPrefix(handle)
		for _, e := range entries {
			p := filepath.Join(specs, e.Name())
			if st, err := os.Stat(p); err != nil || !st.IsDir() {
				continue
			}
			cm := mission.Mid8FromSlug(e.Name())
			ch := e.Name()
			if cm != "" {
				ch = e.Name()[:len(e.Name())-len(cm)-1]
			}
			if e.Name() == handle || (handleMid8 == "" && mission.StripNumericPrefix(ch) == human) {
				set[realpathOr(p)] = true
			}
		}
	}
	var out []string
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func realpathOr(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func isAssignedNumber(v any) bool {
	switch x := v.(type) {
	case int:
		return x > 0
	case int64:
		return x > 0
	case float64:
		return false
	}
	return false
}

func nonEmptyStr(v any) bool {
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) != ""
}

func resumeMetaProblems(meta *jsonx.Object, dir string) []string {
	var p []string
	for _, f := range resumeRequiredFields {
		v, _ := meta.Get(f)
		if !nonEmptyStr(v) {
			p = append(p, fmt.Sprintf("meta.json field %s is missing or empty", mission.PyRepr(f)))
		}
	}
	for _, f := range []string{"slug", "mission_slug"} {
		v, _ := meta.Get(f)
		if nonEmptyStr(v) && v.(string) != filepath.Base(dir) {
			p = append(p, fmt.Sprintf("meta.json field %s does not match directory name", mission.PyRepr(f)))
		}
	}
	if v, ok := meta.Get("pr_bound"); ok {
		if _, isBool := v.(bool); !isBool {
			p = append(p, "meta.json field 'pr_bound' must be a boolean when present")
		}
	}
	if v, ok := meta.Get("mission_number"); !ok {
		p = append(p, "meta.json field 'mission_number' is missing")
	} else if v != nil && !isAssignedNumber(v) {
		p = append(p, "meta.json field 'mission_number' must be null or a positive integer")
	}
	if v, _ := meta.Get("mission_id"); nonEmptyStr(v) && !ulid.Valid(v.(string)) {
		p = append(p, "meta.json field 'mission_id' is not a canonical ULID")
	}
	if v, _ := meta.Get("created_at"); nonEmptyStr(v) && !tzAwareISO(v.(string)) {
		p = append(p, "meta.json field 'created_at' is not a valid timezone-aware ISO-8601 timestamp")
	}
	if v, _ := meta.Get("topology"); nonEmptyStr(v) {
		switch v.(string) {
		case coord.SingleBranch, coord.Lanes, coord.Coord, coord.LanesWithCoord:
		default:
			p = append(p, "meta.json field 'topology' is not a recognized Mission topology")
		}
	}
	return p
}

func tzAwareISO(s string) bool {
	s = strings.Replace(s, "Z", "+00:00", 1)
	if len(s) < 6 {
		return false
	}
	tail := s[len(s)-6:]
	return (tail[0] == '+' || tail[0] == '-') && tail[3] == ':' && strings.Contains(s, "T")
}

func createdSnapshotProblems(root, dir string, meta *jsonx.Object) []string {
	logPath := filepath.Join(dir, "status.events.jsonl")
	if strings.TrimSpace(meta.GetString("coordination_branch")) != "" {
		s := coord.Resolve(root, filepath.Base(dir))
		logPath = filepath.Join(s.StatusDir(), "status.events.jsonl")
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		return []string{"status.events.jsonl is missing"}
	}
	var rows []*jsonx.Object
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		o, err := jsonx.DecodeObject([]byte(line))
		if err != nil {
			return []string{"status.events.jsonl is unreadable or malformed: " + err.Error()}
		}
		rows = append(rows, o)
	}
	var created []*jsonx.Object
	for _, r := range rows {
		if r.GetString("event_type") == "MissionCreated" {
			created = append(created, r)
		}
	}
	if len(created) != 1 {
		return []string{fmt.Sprintf("expected exactly one MissionCreated event, found %d", len(created))}
	}
	payload := created[0].GetObject("payload")
	if payload == nil {
		return []string{"MissionCreated payload is missing or not an object"}
	}
	var p []string
	c := created[0]
	if c.GetString("aggregate_id") != meta.GetString("mission_id") {
		p = append(p, "MissionCreated aggregate_id disagrees with meta.json mission_id")
	}
	if c.GetString("aggregate_type") != "Mission" {
		p = append(p, "MissionCreated aggregate_type is not 'Mission'")
	}
	if !ulid.Valid(c.GetString("event_id")) {
		p = append(p, "MissionCreated envelope event_id is not a valid ULID or UUID")
	}
	if c.GetString("schema_version") != "5.0.0" {
		p = append(p, "MissionCreated envelope schema_version is not canonical local version '5.0.0'")
	}
	if !tzAwareISO(c.GetString("timestamp")) {
		p = append(p, "MissionCreated envelope timestamp is not a timezone-aware ISO-8601 timestamp")
	}
	if n, ok := payload.GetInt("wp_count"); !ok || n != 0 {
		p = append(p, "MissionCreated wp_count must be zero at creation")
	}
	for _, f := range createdSnapshotFields {
		var expected any
		if f != "mission_number" {
			expected, _ = meta.Get(f)
		}
		got, _ := payload.Get(f)
		if jsonx.Dumps(got, compact) != jsonx.Dumps(expected, compact) {
			p = append(p, fmt.Sprintf("MissionCreated field %s disagrees with canonical creation metadata", mission.PyRepr(f)))
		}
	}
	return p
}

func strOf(meta *jsonx.Object, k string) string {
	v, ok := meta.Get(k)
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// resumeProbe is _build_resume_probe_payload.
func resumeProbe(root, handle string) (*jsonx.Object, error) {
	cands, err := resumeMatches(root, handle)
	if err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return jsonx.NewObject("result", "success", "resume_state", "not_found", "handle", handle), nil
	}
	if len(cands) > 1 {
		var list []any
		for _, c := range cands {
			m := mission.ReadMetaOrEmpty(c)
			list = append(list, jsonx.NewObject("mission_slug", filepath.Base(c), "mission_id", strOf(m, "mission_id"), "mission_dir", c, "feature_dir", c))
		}
		return jsonx.NewObject("error_code", "MISSION_RESUME_AMBIGUOUS",
			"error", fmt.Sprintf("Mission handle %s matches multiple existing or partial scaffolds.", mission.PyRepr(handle)),
			"resume_state", "ambiguous", "handle", handle, "candidates", list,
			"remediation", "Select one candidate by exact mission_slug or mission_id; do not create another Mission."), nil
	}
	dir := cands[0]
	var invalid []string
	meta, merr := mission.ReadMeta(dir)
	if merr != nil {
		if merr == mission.ErrNoMeta {
			merr = nil
		} else {
			invalid = append(invalid, "meta.json is malformed or unreadable: "+merr.Error())
		}
	}
	if meta == nil {
		if merr == nil {
			invalid = append(invalid, "meta.json is missing")
		}
		meta = &jsonx.Object{}
	}
	invalid = append(invalid, resumeMetaProblems(meta, dir)...)
	number, _ := meta.Get("mission_number")
	declaredID := strOf(meta, "mission_id")
	embedded := mission.Mid8FromSlug(filepath.Base(dir))
	if number == nil {
		if embedded == "" {
			invalid = append(invalid, "pre-merge Mission directory is missing its canonical mission-id mid8 suffix")
		} else if declaredID != "" && !strings.HasPrefix(declaredID, embedded) {
			invalid = append(invalid, "directory mid8 does not match meta.json mission_id")
		}
	}
	spec := filepath.Join(dir, "spec.md")
	prBound := false
	if v, ok := meta.Get("pr_bound"); ok {
		prBound = truthyValue(v)
	}
	identity := []any{"handle", handle, "mission_id", declaredID, "mission_slug", filepath.Base(dir),
		"mission_type", strOf(meta, "mission_type"), "friendly_name", strOf(meta, "friendly_name"),
		"purpose_tldr", strOf(meta, "purpose_tldr"), "purpose_context", strOf(meta, "purpose_context"),
		"target_branch", strOf(meta, "target_branch"), "topology", strOf(meta, "topology"), "pr_bound", prBound,
		"created_at", strOf(meta, "created_at"), "mission_dir", dir, "feature_dir", dir, "spec_file", spec,
		"meta_file", filepath.Join(dir, "meta.json")}
	if isAssignedNumber(number) {
		if _, err := os.Stat(spec); err != nil {
			invalid = append(invalid, "spec.md is missing")
		}
		o := jsonx.NewObject("error_code", "MISSION_RESUME_EXISTING",
			"error", fmt.Sprintf("Mission %s is numbered history and cannot be reused as an interrupted specify scaffold.", mission.PyRepr(filepath.Base(dir))),
			"resume_state", "existing", "mission_number", number)
		o2 := jsonx.NewObject(identity...)
		for _, kv := range o2.Pairs() {
			o.Set(kv.Key, kv.Value)
		}
		o.Set("integrity_warnings", strList(nonNil(invalid)))
		o.Set("remediation", "Preserve this Mission; do not repair or remove it. Choose a new slug, or operate on the existing Mission by its exact identity.")
		return o, nil
	}
	if st, err := os.Stat(spec); err != nil || !st.Mode().IsRegular() {
		invalid = append(invalid, "spec.md is missing")
	}
	invalid = append(invalid, createdSnapshotProblems(root, dir, meta)...)
	if len(invalid) > 0 {
		return jsonx.NewObject("error_code", "MISSION_RESUME_MALFORMED",
			"error", fmt.Sprintf("Mission scaffold %s is incomplete or malformed.", mission.PyRepr(filepath.Base(dir))),
			"resume_state", "malformed", "handle", handle, "mission_slug", filepath.Base(dir), "mission_dir", dir, "feature_dir", dir,
			"problems", strList(invalid),
			"remediation", "Repair or remove the partial scaffold explicitly; do not create through it."), nil
	}
	mtype := strOf(meta, "mission_type")
	committed := missionart.IsCommitted(spec, root)
	substantive := committed && (!dct.RequiresSubstantiveSpecFor(root, mtype) || missionart.IsSubstantive(spec, "spec", mtype))
	o := jsonx.NewObject("result", "success", "resume_state", "found")
	for _, kv := range jsonx.NewObject(identity...).Pairs() {
		o.Set(kv.Key, kv.Value)
	}
	o.Set("spec_committed_and_substantive", substantive)
	return o, nil
}

// unsafeSegment is assert_safe_path_segment's refusal message ("" = safe).
func unsafeSegment(v string) string {
	pre := "Not a safe path segment: " + mission.PyRepr(v) + " — "
	st := strings.TrimSpace(v)
	switch {
	case st == "":
		return pre + "value must not be empty or whitespace-only."
	case v != st:
		return pre + "value must not contain leading or trailing whitespace."
	case strings.Contains(st, ".."):
		return pre + "value must not contain '..' (traversal guard)."
	case strings.HasPrefix(st, "."):
		return pre + "value must not begin with '.' (traversal guard)."
	case strings.ContainsAny(st, `/\`):
		return pre + "value must not contain path separators."
	case !mission.SafePathSegment(v):
		return pre + "value must match the canonical segment grammar (ASCII alphanumerics, hyphens, underscores, and interior dots only; must begin with an alphanumeric character)."
	}
	return ""
}

func nonNil(x []string) []string {
	if x == nil {
		return []string{}
	}
	return x
}

func truthyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case *jsonx.Object:
		return x.Len() > 0
	}
	return true
}
