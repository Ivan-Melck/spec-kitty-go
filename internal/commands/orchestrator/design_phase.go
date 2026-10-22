package orchestrator

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/commands/planning"
	"github.com/spec-kitty/go-kitty3/internal/fm"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	orch "github.com/spec-kitty/go-kitty3/internal/orchestrator"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
)

// ── specify ───────────────────────────────────────────────────────────────

func specifyCmd() *cli.Command {
	return &cli.Command{
		Name:  "specify",
		Short: "Create a mission scaffold, matching the host CLI's enriched ``specify --json`` contract.",
		Long: "Create a mission scaffold, matching the host CLI's enriched ``specify --json`` contract.\n\n" +
			"In-process only (FR-001): calls\n``specify_cli.cli.commands.lifecycle._create_mission_for_specify_json``,\n" +
			"the SAME enrichment step the host CLI's ``--json`` path runs (adds\n" +
			"``scaffold_only``/``spec_state``/``next_action``/``next_step`` on top of\n" +
			"``agent_feature.create_mission``'s raw payload) -- never the unenriched\npayload one layer beneath it.",
		Flags: []*cli.Flag{
			missionFlag(),
			str("mission-type", "Mission type (e.g., software-dev)", true),
			{Name: "topology", Kind: cli.String, Metavar: "<single_branch|lanes|coord|lanes_with_coord>",
				Choices: []string{"single_branch", "lanes", "coord", "lanes_with_coord"},
				Help:    "Create-time mission shape: single_branch | lanes | coord | lanes_with_coord. Default: context-derived (matches the host CLI's own --topology default)."},
			str("policy", helpPolicy, false),
		},
		Run: runSpecify,
	}
}

func scaffoldNextAction(slug string) string {
	return "Open spec_file and replace the scaffold with a complete specification; then run `spec-kitty plan --mission " + slug + "`."
}

func runSpecify(ctx *cli.Context) error {
	v := newVerb(ctx, "specify")
	if _, err := v.requirePolicy(ctx.String("policy"), "--policy is required for specify"); err != nil {
		return err
	}
	handle := ctx.String("mission")
	argv := []string{"--mission-type", ctx.String("mission-type")}
	if ctx.IsSet("topology") {
		argv = append(argv, "--topology", ctx.String("topology"))
	}
	argv = append(argv, "--json", "--", handle)
	raw, code := delegate(ctx, []string{"agent", "mission", "create"}, argv...)
	if code != 0 {
		payload := extractJSONPayload(raw)
		ec, msg, data := classifyDelegateError(payload, raw, "MISSION_CREATE_FAILED", "mission creation failed")
		return v.fail(ec, msg, data)
	}
	payload := extractJSONPayload(raw)
	if payload == nil {
		return v.fail("MISSION_CREATE_FAILED", "specify produced no parseable JSON payload", jsonx.NewObject("raw_output", raw))
	}
	slug := payload.GetString("mission_slug")
	if slug == "" {
		slug = handle
	}
	payload.Set("scaffold_only", true)
	payload.Set("spec_state", "scaffold_only")
	payload.Set("next_action", scaffoldNextAction(slug))
	payload.Set("next_step", scaffoldNextAction(slug))
	if !payload.Has("mission_slug") {
		root := v.root()
		m, err := v.mission(root, handle)
		if err != nil {
			return err
		}
		payload.Set("mission_slug", orch.Identity(m.StatusDir).GetString("mission_slug"))
	}
	payload.Delete("mission_branch")
	payload.Delete("commit_to_target")
	return v.ok(payload)
}

// ── plan ──────────────────────────────────────────────────────────────────

func planCmd() *cli.Command {
	return &cli.Command{
		Name:  "plan",
		Short: "Scaffold plan.md for a mission -- an unenriched pass-through of ``setup_plan``.",
		Long: "Scaffold plan.md for a mission -- an unenriched pass-through of ``setup_plan``.\n\n" +
			"FR-002 / Clarification 1: deliberately asymmetric with ``specify`` -- the\n" +
			"host CLI's own ``--json`` path returns ``agent_feature.setup_plan``'s raw\n" +
			"dict verbatim (``lifecycle.py`` adds no enrichment here), so this verb\n" +
			"does the same. Do not add fields ``setup_plan`` does not already return.",
		Flags: []*cli.Flag{missionFlag(), str("policy", helpPolicy, false)},
		Run:   runPlan,
	}
}

// planContractError is _plan_contract_error.
func planContractError(code string, data *jsonx.Object) (string, *jsonx.Object) {
	if orch.IsAllowedErrorCode(code) {
		return code, data
	}
	d := data.Clone()
	d.Set("reason", code)
	return "PLAN_SETUP_FAILED", d
}

func (v *verb) designErr(err error, identityData *jsonx.Object) error {
	var de *orch.DesignError
	if errors.As(err, &de) {
		data := jsonx.NewObject()
		if identityData != nil {
			data = identityData.Clone()
		}
		if de.Details != nil {
			for _, kv := range de.Details.Pairs() {
				data.Set(kv.Key, kv.Value)
			}
		}
		return v.fail(de.Code, de.Message, data)
	}
	return v.fail("DESIGN_IO_FAILED", "The host could not read or persist design content", identityData)
}

func runPlan(ctx *cli.Context) error {
	v := newVerb(ctx, "plan")
	if _, err := v.requirePolicy(ctx.String("policy"), "--policy is required for plan"); err != nil {
		return err
	}
	root := v.root()
	handle := ctx.String("mission")
	m, err := v.mission(root, handle)
	if err != nil {
		return err
	}
	if orch.AuthoringEnabled(root, filepath.Base(m.StatusDir)) {
		if err := requireUnfinalized(m); err != nil {
			return v.designErr(err, nil)
		}
	}
	raw, code := delegate(ctx, []string{"agent", "mission", "setup-plan"}, "--mission", handle, "--json")
	if code != 0 {
		payload := extractJSONPayload(raw)
		ec, msg, data := classifyDelegateError(payload, raw, "PLAN_SETUP_FAILED", "plan scaffolding failed")
		ec, data = planContractError(ec, data)
		return v.fail(ec, msg, data)
	}
	payload := extractJSONPayload(raw)
	if payload == nil {
		return v.fail("PLAN_SETUP_FAILED", "plan produced no parseable JSON payload", jsonx.NewObject("raw_output", raw))
	}
	setDefault(payload, "mission_slug", orch.Identity(m.StatusDir).GetString("mission_slug"))
	res := payload.GetString("result")
	if sv, ok := payload.Get("success"); res == "blocked" || res == "error" || (ok && sv == false) {
		ec, msg, data := classifyDelegateError(payload, raw, "PLAN_SETUP_FAILED", "plan prerequisites are blocked")
		ec, data = planContractError(ec, data)
		return v.fail(ec, msg, data)
	}
	return v.ok(payload)
}

// requireUnfinalized is design.validation.require_unfinalized.
func requireUnfinalized(m *orch.Mission) error {
	fin, err := orch.TasksFinalized(m.StatusDir)
	if err != nil {
		return orch.NewDesignError("DESIGN_STATUS_EVENT_LOG_UNREADABLE", "Canonical mission status cannot be trusted")
	}
	if fin {
		return orch.NewDesignError("DESIGN_FINALIZED", "Finalized planning content requires the native reconciliation workflow")
	}
	return nil
}

// ── tasks ─────────────────────────────────────────────────────────────────

func tasksCmd() *cli.Command {
	return &cli.Command{
		Name:  "tasks",
		Short: "Finalize WP task metadata -- an unenriched pass-through of ``finalize_tasks``.",
		Long: "Finalize WP task metadata -- an unenriched pass-through of ``finalize_tasks``.\n\n" +
			"FR-003 / Clarification 1: same deliberate asymmetry as ``plan`` -- the\n" +
			"host CLI's own ``--json`` path returns ``agent_feature.finalize_tasks``'s\n" +
			"raw dict verbatim, so this verb does the same.",
		Flags: []*cli.Flag{missionFlag(), str("policy", helpPolicy, false)},
		Run:   runTasks,
	}
}

func runTasks(ctx *cli.Context) error {
	v := newVerb(ctx, "tasks")
	if _, err := v.requirePolicy(ctx.String("policy"), "--policy is required for tasks"); err != nil {
		return err
	}
	root := v.root()
	handle := ctx.String("mission")
	m, err := v.mission(root, handle)
	if err != nil {
		return err
	}
	slug := filepath.Base(m.StatusDir)
	orch.TouchAuthoringLock(root, slug)
	receipts, err := orch.ReadReceipts(root, slug)
	if err != nil {
		return v.designErr(err, nil)
	}
	if receipts != nil {
		if _, err := newDesign(m).validateStage("tasks"); err != nil {
			return v.designErr(err, nil)
		}
	}
	raw, code := delegate(ctx, []string{"agent", "mission", "finalize-tasks"}, "--mission", handle, "--json")
	if code != 0 {
		payload := extractJSONPayload(raw)
		ec, msg, data := classifyDelegateError(payload, raw, "TASKS_FINALIZE_FAILED", "tasks finalization failed")
		return v.fail(ec, msg, data)
	}
	payload := extractJSONPayload(raw)
	if receipts != nil && payload != nil && payload.GetString("result") == "success" && !pyjson.Truthy(mustGet(payload, "error")) {
		if sv, ok := payload.Get("success"); !ok || sv != false {
			d := newDesign(m)
			for _, k := range receipts.Keys {
				r := receipts.Receipts[k]
				if r.Kind == "work_package" {
					if p, err := d.artifactPath(r.Kind, r.ArtifactID, nil); err == nil {
						r.SHA256 = contentDigestOrAbsent(p)
					}
				}
			}
			if err := orch.SaveReceipts(root, slug, receipts); err != nil {
				return v.designErr(err, nil)
			}
		}
	}
	if payload == nil {
		return v.fail("TASKS_FINALIZE_FAILED", "tasks produced no parseable JSON payload", jsonx.NewObject("raw_output", raw))
	}
	setDefault(payload, "mission_slug", orch.Identity(m.StatusDir).GetString("mission_slug"))
	return v.ok(payload)
}

func mustGet(o *jsonx.Object, k string) any {
	v, _ := o.Get(k)
	return v
}

// ── check-prerequisites ───────────────────────────────────────────────────

func checkPrerequisitesCmd() *cli.Command {
	return &cli.Command{
		Name:  "check-prerequisites",
		Short: "Read-only mission-prerequisite context for ``/spec-kitty.analyze`` (FR-004).",
		Flags: []*cli.Flag{missionFlag(),
			boolFlag("include-tasks", "Include tasks.md validation (matches the host CLI's own --include-tasks default)")},
		Run: runCheckPrerequisites,
	}
}

// sanitizeForbidden is _sanitize_forbidden_error_code.
func sanitizeForbidden(v any, forbidden, replacement string) any {
	switch t := v.(type) {
	case *jsonx.Object:
		out := jsonx.NewObject()
		for _, kv := range t.Pairs() {
			out.Set(strings.ReplaceAll(kv.Key, forbidden, replacement), sanitizeForbidden(kv.Value, forbidden, replacement))
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = sanitizeForbidden(x, forbidden, replacement)
		}
		return out
	case string:
		return strings.ReplaceAll(t, forbidden, replacement)
	}
	return v
}

func runCheckPrerequisites(ctx *cli.Context) error {
	v := newVerb(ctx, "check-prerequisites")
	root := v.root()
	handle := ctx.String("mission")
	m, err := v.mission(root, handle)
	if err != nil {
		return err
	}
	argv := []string{"--mission", handle, "--json"}
	if ctx.Bool("include-tasks") {
		argv = append(argv, "--include-tasks")
	}
	raw, code := delegate(ctx, []string{"agent", "mission", "check-prerequisites"}, argv...)
	if code != 0 {
		payload := extractJSONPayload(raw)
		if payload == nil {
			msg := strings.TrimSpace(raw)
			if msg == "" {
				msg = "check-prerequisites failed"
			}
			return v.fail("CHECK_PREREQUISITES_FAILED", msg, jsonx.NewObject("raw_output", raw))
		}
		ec, msg, data := classifyDelegateError(payload, raw, "CHECK_PREREQUISITES_FAILED", "check-prerequisites failed")
		if ec == "MISSION_CONTEXT_UNRESOLVED" {
			return v.fail("MISSION_NOT_FOUND", msg, sanitizeForbidden(data, "MISSION_CONTEXT_UNRESOLVED", "MISSION_NOT_FOUND").(*jsonx.Object))
		}
		return v.fail(ec, msg, data)
	}
	payload := extractJSONPayload(raw)
	if payload == nil {
		return v.fail("CHECK_PREREQUISITES_FAILED", "check-prerequisites produced no parseable JSON payload", jsonx.NewObject("raw_output", raw))
	}
	setDefault(payload, "mission_slug", orch.Identity(m.StatusDir).GetString("mission_slug"))
	return v.ok(payload)
}

// ── record-analysis ───────────────────────────────────────────────────────

func recordAnalysisCmd() *cli.Command {
	return &cli.Command{
		Name:  "record-analysis",
		Short: "Persist an ``/spec-kitty.analyze`` report, verified against disk (FR-005).",
		Flags: []*cli.Flag{
			missionFlag(),
			{Name: "input-file", Kind: cli.String, Metavar: "<str>", Default: "-", Help: "Markdown report path, or '-' to read the report body from stdin"},
			str("agent", "Agent name that produced the analysis report", false),
			str("policy", helpPolicy, false),
		},
		Run: runRecordAnalysis,
	}
}

func runRecordAnalysis(ctx *cli.Context) error {
	v := newVerb(ctx, "record-analysis")
	if _, err := v.requirePolicy(ctx.String("policy"), "--policy is required for record-analysis"); err != nil {
		return err
	}
	root := v.root()
	m, err := v.mission(root, ctx.String("mission"))
	if err != nil {
		return err
	}
	slug := orch.Identity(m.StatusDir).GetString("mission_slug")
	if pl := planning.RecordAnalysisDirtyPreflight(root, m.Slug); pl != nil {
		ec, msg, data := classifyDelegateError(pl, "", "DIRTY_WORKTREE", "record-analysis dirty-tree preflight failed")
		setDefault(data, "mission_slug", slug)
		return v.fail(ec, msg, data)
	}
	in := ctx.String("input-file")
	var body []byte
	if in == "-" {
		body, err = io.ReadAll(ctx.Stdin)
	} else {
		body, err = os.ReadFile(in)
	}
	if err != nil {
		return v.fail("RECORD_ANALYSIS_INPUT_FILE_NOT_FOUND",
			fmt.Sprintf("Could not read --input-file %s: %s", pyjson.ReprString(in), osErrorText(err, in)), jsonx.NewObject("mission_slug", slug))
	}
	if strings.TrimSpace(string(body)) == "" {
		return v.fail("RECORD_ANALYSIS_EMPTY_BODY", "Analysis report body is empty", jsonx.NewObject("mission_slug", slug))
	}
	submitted, perr := planning.ParseAnalysisVerdict(string(body))
	if perr != nil {
		return v.fail("RECORD_ANALYSIS_MALFORMED_CARRIER", perr.Error(), jsonx.NewObject("mission_slug", slug))
	}
	callStart := clock.Now().UTC()
	argv := []string{"--mission", m.Slug, "--input-file", "-", "--json"}
	if a := ctx.String("agent"); a != "" {
		argv = append(argv, "--agent", a)
	}
	wctx := *ctx
	wctx.Stdin = strings.NewReader(string(body))
	// _run_write_with_timeout: the write runs bounded by a real wall-clock
	// limit; a still-running call is left behind (a leaked daemon thread
	// upstream) and the re-read below stays the sole success signal.
	w := runWithTimeout(func() (string, int) {
		return delegate(&wctx, []string{"agent", "mission", "record-analysis"}, argv...)
	}, recordAnalysisTimeout)
	raw, code := w.raw, w.code
	var out *jsonx.Object
	if w.completed {
		out = extractJSONPayload(raw)
	}
	path := filepath.Join(m.PrimaryDir, "analysis-report.md")
	verdict, generatedAt, exists := rereadReport(path)
	confirmed := exists && generatedAt != nil && strictlyAfter(*generatedAt, callStart)
	if confirmed && submitted != "unknown" && verdict != nil && *verdict == submitted {
		data := identity(m.StatusDir, "path", path, "verdict", *verdict, "generated_at", *generatedAt)
		if out != nil {
			if s, ok := out.Get("surfaces"); ok {
				if l, ok := s.([]any); ok && len(l) > 0 {
					data.Set("commit_surfaces", s)
					if w := surfaceWarnings(l); w != nil {
						data.Set("warnings", strList(w))
					}
				}
			}
		}
		return v.ok(data)
	}
	var ec, msg string
	switch {
	case !confirmed:
		ec, msg = "RECORD_ANALYSIS_WRITE_NOT_CONFIRMED", "record-analysis could not confirm a fresh write: no analysis-report.md with a generated_at timestamp later than this call's start was found on disk."
	case submitted == "unknown":
		ec, msg = "RECORD_ANALYSIS_VERDICT_UNRELIABLE", "The submitted analysis report carried no valid analysis-findings/v1 carrier, so no reliable verdict could be recorded (verdict: unknown is never reported as success)."
	default:
		ec, msg = "RECORD_ANALYSIS_VERDICT_UNRELIABLE", fmt.Sprintf("analysis-report.md was written but its verdict (%s) does not match the submitted verdict (%s).", optRepr(verdict), pyjson.ReprString(submitted))
	}
	data := jsonx.NewObject("mission_slug", slug, "submitted_verdict", submitted)
	if exists {
		data.Set("reread_verdict", optAny(verdict))
		data.Set("reread_generated_at", optAny(generatedAt))
	}
	if !w.completed {
		data.Set("underlying_call_timed_out", true)
	} else if code != 0 && out != nil {
		if e := out.GetString("error"); e != "" {
			data.Set("underlying_call_error", e)
		}
	}
	return v.fail(ec, msg, data)
}

// surfaceWarnings is render_commit_outcome(result) when some surface is
// neither committed nor unchanged (nil otherwise), rebuilt from the
// commit_outcome_payload surfaces.
func surfaceWarnings(surfaces []any) []string {
	r := &safecommit.RouterResult{}
	warn := false
	fates := func(v any) []safecommit.PathFate {
		var out []safecommit.PathFate
		l, _ := v.([]any)
		for _, x := range l {
			if o, ok := x.(*jsonx.Object); ok {
				out = append(out, safecommit.PathFate{Path: o.GetString("path"), Reason: o.GetString("reason"), OwningPath: o.GetString("owning_path")})
			}
		}
		return out
	}
	for _, x := range surfaces {
		o, ok := x.(*jsonx.Object)
		if !ok {
			continue
		}
		so := safecommit.SurfaceOutcome{Surface: o.GetString("surface"), Branch: o.GetString("branch"), Status: o.GetString("status"),
			CommitHash: o.GetString("commit_hash"), Skipped: fates(mustGet(o, "skipped")), Refused: fates(mustGet(o, "refused")),
			Diagnostic: o.GetString("diagnostic")}
		if l, ok := mustGet(o, "committed").([]any); ok {
			for _, c := range l {
				so.Committed = append(so.Committed, pyStr(c))
			}
		}
		if so.Status != safecommit.StatusCommitted && so.Status != safecommit.StatusUnchanged {
			warn = true
		}
		r.Surfaces = append(r.Surfaces, so)
	}
	if !warn {
		return nil
	}
	return r.Render()
}

func optRepr(s *string) string {
	if s == nil {
		return "None"
	}
	return pyjson.ReprString(*s)
}

func optAny(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// osErrorText renders an OSError like CPython's str(exc).
func osErrorText(err error, path string) string {
	switch {
	case os.IsNotExist(err):
		return fmt.Sprintf("[Errno 2] No such file or directory: %s", pyjson.ReprString(path))
	case os.IsPermission(err):
		return fmt.Sprintf("[Errno 13] Permission denied: %s", pyjson.ReprString(path))
	}
	if st, e := os.Stat(path); e == nil && st.IsDir() {
		return fmt.Sprintf("[Errno 21] Is a directory: %s", pyjson.ReprString(path))
	}
	return err.Error()
}

// rereadReport reads verdict / generated_at from analysis-report.md.
func rereadReport(path string) (verdict, generatedAt *string, exists bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, false
	}
	doc, err := fm.Parse(string(b))
	if err != nil || doc == nil {
		return nil, nil, true
	}
	get := func(k string) *string {
		v, ok := doc.Meta.Get(k)
		if !ok || v == nil {
			return nil
		}
		s := pyStr(v)
		return &s
	}
	return get("verdict"), get("generated_at"), true
}

func strictlyAfter(candidate string, ref time.Time) bool {
	t, err := clock.ParseISO(candidate)
	if err != nil {
		return false
	}
	return t.After(ref)
}

// recordAnalysisTimeout is _RECORD_ANALYSIS_TIMEOUT_SECONDS.
var recordAnalysisTimeout = 10 * time.Second

// timedOutcome is _TimedWriteOutcome.
type timedOutcome struct {
	completed bool
	raw       string
	code      int
}

// runWithTimeout is _run_write_with_timeout: fn runs in its own goroutine;
// control returns after timeout whether or not it finished.
func runWithTimeout(fn func() (string, int), timeout time.Duration) timedOutcome {
	done := make(chan timedOutcome, 1)
	go func() {
		raw, code := fn()
		done <- timedOutcome{true, raw, code}
	}()
	select {
	case o := <-done:
		return o
	case <-time.After(timeout):
		return timedOutcome{}
	}
}
