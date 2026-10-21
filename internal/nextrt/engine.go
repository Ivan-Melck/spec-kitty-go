package nextrt

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/invocation"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/workflow"
)

// MissionNotFoundError is runtime_bridge_query.MissionNotFoundError.
type MissionNotFoundError struct{ Handle string }

func (e *MissionNotFoundError) Error() string {
	return fmt.Sprintf("Mission not found: '%s'", e.Handle)
}

// NextStep is the error's remediation.
func (e *MissionNotFoundError) NextStep() string {
	return fmt.Sprintf("Run 'spec-kitty doctor topology' to see available missions, then re-run with a valid handle (attempted: '%s').", e.Handle)
}

// QueryValidationError is QueryModeValidationError.
type QueryValidationError struct{ Msg string }

func (e *QueryValidationError) Error() string { return e.Msg }

func base(m *missionCtx, kind, agent, state string) *Decision {
	return &Decision{Kind: kind, Agent: agent, MissionSlug: m.Slug, Mission: m.MissionType, MissionState: state,
		Timestamp: clock.NowUTCISO(), MissionNumber: m.MissionNumber, MissionType: m.MissionType}
}

func mergedShortCircuit(root, slug, agent, kind string) *Decision {
	v := terminalVerdict(root, slug)
	if v == "none" {
		return nil
	}
	m := newMissionCtx(root, slug)
	if v == "terminal" {
		d := base(m, kind, agent, "done")
		d.Reason = sp(mergedDoneReason)
		return materialize(d)
	}
	bk := KindBlocked
	if kind == KindQuery {
		bk = KindQuery
	}
	d := base(m, bk, agent, "blocked")
	d.Reason = sp(fmt.Sprintf("Merged mission has committed work packages that are not an acceptable ending (conflict). Inspect the committed board with 'spec-kitty agent tasks status --mission %s' and resolve the straggling work package(s) — e.g. 'spec-kitty agent tasks move-task <wp> --to approved --mission %s'.", slug, slug))
	return materialize(d)
}

// analysisFailures evaluates the analyze-step currency guard.
func analysisFailures(m *missionCtx) []string {
	f := workflow.CheckAnalysisReport(m.Root, m.Slug)
	v := f.Verdict()
	switch v.GetString("status") {
	case "current":
		return nil
	case "missing":
		return []string{AnalysisMissingMessage}
	}
	var out []string
	for _, n := range strList(v, "stale_inputs") {
		out = append(out, fmt.Sprintf(AnalysisStaleMessage, n))
	}
	if len(out) == 0 {
		out = []string{fmt.Sprintf(AnalysisStaleMessage, "analysis-report.md")}
	}
	return out
}

func analysisErrorCode(fs []string) *string {
	for _, f := range fs {
		if f == AnalysisMissingMessage {
			return sp(AnalysisReportMissing)
		}
		if strings.HasPrefix(f, fmt.Sprintf(AnalysisStaleMessage, "")) {
			return sp(AnalysisReportStale)
		}
	}
	return nil
}

// QueryCurrentState is runtime_bridge_query.query_current_state.
func QueryCurrentState(root, slug, agent string) (*Decision, error) {
	if d := mergedShortCircuit(root, slug, agent, KindQuery); d != nil {
		return d, nil
	}
	m := newMissionCtx(root, slug)
	if st, err := os.Stat(m.Primary); err != nil || !st.IsDir() {
		return nil, &MissionNotFoundError{Handle: slug}
	}
	if e := taskSurfaceError(m); e != "" {
		d := base(m, KindQuery, agent, "blocked")
		d.Reason = sp(e)
		return materialize(d), nil
	}
	progress := computeProgress(m.Primary, m.StatusDir)
	ref := ExistingRun(root, slug, m.MissionID, m.MissionType)
	var snap *Snapshot
	var tpl *Template
	var emittedRunID *string
	if ref == nil {
		tb, _, err := templateSource(root, m.MissionType)
		if err != nil {
			return nil, &QueryValidationError{fmt.Sprintf("Could not read query state for mission '%s': %v", slug, err)}
		}
		tpl, err = ParseTemplate(tb)
		if err != nil {
			return nil, &QueryValidationError{fmt.Sprintf("Could not read query state for mission '%s': %v", slug, err)}
		}
		snap = &Snapshot{Obj: jsonx.NewObject("completed_steps", []any{}, "decisions", &jsonx.Object{}, "pending_decisions", &jsonx.Object{})}
	} else {
		var err error
		snap, err = ReadSnapshot(ref.RunDir)
		if err == nil {
			tpl, err = loadTemplate(ref.RunDir)
		}
		if err != nil {
			return nil, &QueryValidationError{fmt.Sprintf("Could not read query state for mission '%s': %v", slug, err)}
		}
		emittedRunID = sp(ref.RunID)
	}
	plan := planNext(snap, tpl)
	if bs := boardStep(m, progress); bs != "" {
		if bs == "implement" && tpl.HasStep(AnalyzeStepID) {
			if fs := analysisFailures(m); len(fs) > 0 {
				d := base(m, KindQuery, agent, AnalyzeStepID)
				d.Progress, d.RunID, d.PreviewStep = progress, emittedRunID, sp(AnalyzeStepID)
				d.GuardFailures, d.ErrorCode = fs, analysisErrorCode(fs)
				return materialize(d), nil
			}
		}
		var d *Decision
		switch {
		case bs == "done":
			d = base(m, KindQuery, agent, "done")
			d.Reason = sp(mergedDoneReason)
		case strings.HasPrefix(bs, "blocked:"):
			d = base(m, KindQuery, agent, "blocked")
			d.Reason = sp(strings.ReplaceAll(strings.TrimPrefix(bs, "blocked:"), "_", " "))
		default:
			d = base(m, KindQuery, agent, bs)
			d.PreviewStep = sp(bs)
			if bs == "implement" {
				id, reason := workflow.PreviewClaimableWP(root, slug)
				d.WPID = spOrNil(id)
				if id == "" && reason != "" {
					d.Reason = sp(reason)
				}
			}
		}
		d.Progress, d.RunID = progress, emittedRunID
		return materialize(d), nil
	}
	if snap.untouched() {
		if (plan.Kind == KindStep || plan.Kind == KindDecisionRequired) && plan.StepID != "" {
			d := base(m, KindQuery, agent, "not_started")
			d.Progress, d.RunID, d.PreviewStep = progress, emittedRunID, sp(plan.StepID)
			return materialize(d), nil
		}
		return nil, &QueryValidationError{fmt.Sprintf("Mission '%s' has no issuable first step for run '%s'", m.MissionType, slug)}
	}
	issued := snap.issued()
	if plan.Kind == KindDecisionRequired {
		state := issued
		if state == "" {
			state = plan.StepID
		}
		if state == "" {
			state = "unknown"
		}
		d := base(m, KindQuery, agent, state)
		d.Progress, d.RunID = progress, emittedRunID
		d.StepID = spOrNil(firstNonEmpty(issued, plan.StepID))
		d.DecisionID, d.InputKey, d.Question = spOrNil(plan.DecisionID), spOrNil(plan.InputKey), spOrNil(plan.Question)
		if plan.Options != nil {
			d.Options = plan.Options
		}
		return materialize(d), nil
	}
	state := firstNonEmpty(plan.StepID, "unknown")
	var reason *string
	switch plan.Kind {
	case KindTerminal:
		state = "done"
	case KindBlocked:
		state = firstNonEmpty(issued, plan.StepID, "blocked")
		reason = spOrNil(firstNonEmpty(snap.Obj.GetString("blocked_reason"), plan.Reason))
	}
	d := base(m, KindQuery, agent, state)
	d.Progress, d.RunID, d.Reason = progress, emittedRunID, reason
	d.StepID = spOrNil(firstNonEmpty(issued, plan.StepID))
	return materialize(d), nil
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// missionOrigin is the resolved mission config tier/path.
func missionOrigin(root, missionType string) *jsonx.Object {
	k := filepath.Join(root, ".kittify")
	for _, c := range []struct{ p, tier string }{
		{filepath.Join(k, "overrides", "missions", missionType, "mission.yaml"), "override"},
		{filepath.Join(k, "missions", missionType, "mission.yaml"), "legacy"},
		{filepath.Join(project.GlobalKittify(), "missions", missionType, "mission.yaml"), "global_mission"},
	} {
		if _, err := os.Stat(c.p); err == nil {
			return jsonx.NewObject("mission_tier", c.tier, "mission_path", filepath.Dir(c.p))
		}
	}
	return jsonx.NewObject("mission_tier", "package_default", "mission_path", "packs/built-in/missions/"+missionType)
}

type advanceCtx struct {
	m          *missionCtx
	agent      string
	featureDir string
	progress   *jsonx.Object
	origin     *jsonx.Object
	ref        *RunRef
}

func (c *advanceCtx) decision(kind, state string) *Decision {
	d := base(c.m, kind, c.agent, state)
	d.Progress, d.Origin, d.RunID = c.progress, c.origin, sp(c.ref.RunID)
	return d
}

// wpBoardAction is _resolve_wp_board_action: (step, action, wp, workspace, blocked reason).
func wpBoardAction(m *missionCtx) (string, string, string, string, string) {
	if e := taskSurfaceError(m); e != "" {
		return "", "", "", "", e
	}
	progress := computeProgress(m.Primary, m.StatusDir)
	bs := boardStep(m, progress)
	if bs == "" || bs == "accept" || bs == "done" {
		return "", "", "", "", ""
	}
	inspect := fmt.Sprintf("spec-kitty agent tasks status --mission %s", m.Slug)
	if strings.HasPrefix(bs, "blocked:") {
		return bs, "", "", "", fmt.Sprintf("No actionable work package (%s). Inspect the board: `%s`.", strings.ReplaceAll(strings.TrimPrefix(bs, "blocked:"), "_", " "), inspect)
	}
	var id string
	if bs == "implement" {
		var reason string
		id, reason = workflow.PreviewClaimableWP(m.Root, m.Slug)
		if id == "" {
			if reason == "" {
				reason = "no claimable work package"
			}
			return bs, "", "", "", fmt.Sprintf("%s. Inspect the board: `%s`.", reason, inspect)
		}
	} else {
		id = firstWPByLane(m.Primary, m.StatusDir, "for_review")
		if id == "" {
			return bs, "", "", "", fmt.Sprintf("Board reported a reviewable work package but none was found on re-read. Inspect the board: `%s`.", inspect)
		}
	}
	ws, err := lanes.ResolveWorkspace(m.Root, m.Slug, id)
	if err != nil {
		return bs, "", "", "", err.Error()
	}
	return bs, bs, id, ws.WorktreePath, ""
}

// wpIterationDecision is _build_wp_iteration_decision.
func (c *advanceCtx) wpIterationDecision(stepID string, guards []string) *Decision {
	bs, action, wpID, wsPath, blocked := wpBoardAction(c.m)
	if blocked != "" {
		d := c.decision(KindBlocked, stepID)
		d.Reason, d.StepID, d.GuardFailures = sp(blocked), sp(stepID), guards
		return materialize(d)
	}
	state := stepID
	if action == "" {
		// fall back to _state_to_action
		action, wpID, wsPath = c.stateToAction(stepID)
	} else {
		state = bs
	}
	d := c.decision(KindStep, state)
	d.StepID, d.GuardFailures = sp(state), guards
	if action == "" {
		d.Kind = KindBlocked
		d.Reason = sp(fmt.Sprintf("No work packages available for step '%s'", stepID))
		return materialize(d)
	}
	d.Action, d.WPID, d.WorkspacePath = sp(action), spOrNil(wpID), spOrNil(wsPath)
	var p string
	var err error
	if wpID != "" {
		p, err = buildWPPrompt(c.m, action, wpID, c.agent)
	} else {
		p, err = buildTemplatePrompt(c.m, c.featureDir, action, c.agent)
	}
	if err != nil {
		d.Reason = sp(err.Error())
	} else {
		d.PromptFile = spOrNil(p)
	}
	return materialize(d)
}

// stateToAction is decision._state_to_action.
func (c *advanceCtx) stateToAction(state string) (string, string, string) {
	m := c.m
	switch state {
	case "implement":
		id, _ := workflow.PreviewClaimableWP(m.Root, m.Slug)
		if id == "" {
			id = firstWPByLane(m.Primary, m.StatusDir, "in_progress")
		}
		action := "implement"
		if id == "" {
			id = firstWPByLane(m.Primary, m.StatusDir, "for_review")
			action = "review"
			if id == "" {
				return "", "", ""
			}
		}
		ws, err := lanes.ResolveWorkspace(m.Root, m.Slug, id)
		if err != nil {
			return "", "", ""
		}
		return action, id, ws.WorktreePath
	case "review":
		if id := firstWPByLane(m.Primary, m.StatusDir, "for_review"); id != "" {
			if ws, err := lanes.ResolveWorkspace(m.Root, m.Slug, id); err == nil {
				return "review", id, ws.WorktreePath
			}
		}
	case "done":
		return "accept", "", ""
	}
	return templateAction(state, m.Root, m.MissionType), "", ""
}

// stepDecision issues a (non-WP or WP) step decision with guard failures.
func (c *advanceCtx) stepDecision(stepID string, guards []string, errorCode *string) *Decision {
	if stepID == "implement" || stepID == "review" {
		return c.wpIterationDecision(stepID, guards)
	}
	action, wpID, wsPath := c.stateToAction(stepID)
	d := c.decision(KindStep, stepID)
	d.StepID, d.GuardFailures, d.ErrorCode = sp(stepID), guards, errorCode
	d.Action, d.WPID, d.WorkspacePath = spOrNil(action), spOrNil(wpID), spOrNil(wsPath)
	if action == "" {
		d.Reason = sp(fmt.Sprintf("no action mapped for step '%s'; cannot resolve prompt", stepID))
		return materialize(d)
	}
	p, err := buildTemplatePrompt(c.m, c.featureDir, action, c.agent)
	if err != nil {
		d.Reason = sp(err.Error())
	} else {
		d.PromptFile = spOrNil(p)
	}
	d = materialize(d)
	c.guardPaths(d)
	return d
}

func (c *advanceCtx) guardPaths(d *Decision) {
	if len(d.GuardFailures) == 0 {
		return
	}
	o := &jsonx.Object{}
	for _, g := range d.GuardFailures {
		var name string
		if n, err := fmt.Sscanf(g, MissingArtifactMessage, &name); err == nil && n == 1 {
			o.Set(name, filepath.Join(c.m.Primary, name))
		}
	}
	d.GuardFailurePaths = o
}

// softwareDevGuards is _evaluate_software_dev_guards for a non-WP step.
func (c *advanceCtx) softwareDevGuards(step string) []string {
	exists := func(n string) bool {
		st, err := os.Stat(filepath.Join(c.m.Primary, n))
		return err == nil && st.Mode().IsRegular()
	}
	switch step {
	case AnalyzeStepID:
		return analysisFailures(c.m)
	case "specify":
		if !exists("spec.md") {
			return []string{fmt.Sprintf(MissingArtifactMessage, "spec.md")}
		}
	case "plan":
		if !exists("plan.md") {
			return []string{fmt.Sprintf(MissingArtifactMessage, "plan.md")}
		}
	case "tasks":
		var out []string
		if !exists("tasks.md") {
			out = append(out, fmt.Sprintf(MissingArtifactMessage, "tasks.md"))
		}
		if len(wpFiles(c.m.Primary)) == 0 {
			out = append(out, MissingTaskFilesMessage)
		}
		return out
	}
	return nil
}

// shouldAdvanceWPStep is _should_advance_wp_step.
func (c *advanceCtx) shouldAdvanceWPStep(step string) bool {
	snap, _ := snapshotOf(c.m.StatusDir)
	files := wpFiles(c.m.Primary)
	if len(files) == 0 {
		return true
	}
	for _, f := range files {
		l := snap.Lane(wpIDOf(f))
		switch step {
		case "implement":
			if l != status.ForReview && l != status.InReview && l != status.Approved && l != status.Done && l != status.Canceled {
				return false
			}
		default:
			if l != status.Approved && l != status.Done && l != status.Canceled {
				return false
			}
		}
	}
	return true
}

// DecideNext is runtime_bridge.decide_next_via_runtime (advancing mode).
func DecideNext(root, slug, agent, result string) *Decision {
	if d := mergedShortCircuit(root, slug, agent, KindTerminal); d != nil {
		return d
	}
	m := newMissionCtx(root, slug)
	featureDir := m.StatusDir
	if st, err := os.Stat(featureDir); err != nil || !st.IsDir() {
		d := base(m, KindBlocked, agent, "unknown")
		d.Mission, d.MissionType = "unknown", "unknown"
		d.Reason = sp("Feature directory not found: " + featureDir)
		return materialize(d)
	}
	c := &advanceCtx{m: m, agent: agent, featureDir: featureDir, progress: computeProgress(featureDir, featureDir),
		origin: missionOrigin(root, m.MissionType)}
	ref, err := GetOrStartRun(root, slug, m.MissionID, m.MissionType)
	if err != nil {
		d := base(m, KindBlocked, agent, "unknown")
		d.Reason = sp(fmt.Sprintf("Failed to start/load runtime run: %v", err))
		d.Progress, d.Origin = c.progress, c.origin
		return materialize(d)
	}
	c.ref = ref
	snap, err := ReadSnapshot(ref.RunDir)
	current := ""
	if err == nil {
		current = snap.issued()
	}
	tpl, _ := loadTemplate(ref.RunDir)
	// finalized-board override (first contact)
	if result == "success" && snap != nil && snap.untouched() {
		bs, action, _, _, blocked := wpBoardAction(m)
		if action != "" {
			if action == "implement" && tpl != nil && tpl.HasStep(AnalyzeStepID) {
				if fs := analysisFailures(m); len(fs) > 0 {
					return c.stepDecision(AnalyzeStepID, fs, analysisErrorCode(fs))
				}
			}
			return c.wpIterationDecision(bs, nil)
		}
		if blocked != "" && strings.HasPrefix(bs, "blocked:") {
			d := c.decision(KindBlocked, "blocked")
			d.Reason = sp(blocked)
			return materialize(d)
		}
	}
	// dependency gate
	if result == "success" && current != "" {
		if current == "implement" || current == "review" {
			if !c.shouldAdvanceWPStep(current) {
				return c.wpIterationDecision(current, nil)
			}
		} else if m.MissionType == "software-dev" {
			if fs := c.softwareDevGuards(current); len(fs) > 0 {
				return c.stepDecision(current, fs, analysisErrorCode(fs))
			}
		}
	}
	// composition dispatch
	if result == "success" && current != "" && shouldDispatchViaComposition(root, m.MissionType, current, tpl) {
		action := normalizeComposedAction(current)
		hint, contract := compositionInputs(root, m.MissionType, current, action, tpl)
		var failures []string
		if _, err := ExecuteStepContract(root, m.MissionType, action, agent, hint, "", contract); err != nil {
			if _, ok := err.(*StepContractError); ok {
				failures = []string{fmt.Sprintf("composition failed for %s/%s: %v", m.MissionType, action, err)}
			} else {
				failures = []string{fmt.Sprintf("composition crashed for %s/%s: %s: %v", m.MissionType, action, pyErrType(err), err)}
			}
		} else if m.MissionType == "software-dev" && action != "implement" && action != "review" {
			failures = c.softwareDevGuards(action)
		}
		if len(failures) > 0 {
			return c.compositionBlocked(current, failures)
		}
	}
	plan, err := advanceRun(ref, agent, result)
	if err != nil {
		d := c.decision(KindBlocked, firstNonEmpty(current, "unknown"))
		d.Reason = sp("Runtime engine error: " + err.Error())
		d.RunID = nil
		return materialize(d)
	}
	switch plan.Kind {
	case KindStep:
		return c.stepDecision(plan.StepID, nil, nil)
	case KindTerminal:
		d := c.decision(KindTerminal, "done")
		d.Reason = sp(plan.Reason)
		return materialize(d)
	case KindDecisionRequired:
		d := c.decision(KindDecisionRequired, firstNonEmpty(plan.StepID, "unknown"))
		d.StepID, d.DecisionID, d.InputKey, d.Question = spOrNil(plan.StepID), spOrNil(plan.DecisionID), spOrNil(plan.InputKey), spOrNil(plan.Question)
		d.Options = plan.Options
		d.PromptFile = spOrNil(buildDecisionPrompt(m, plan, agent))
		return materialize(d)
	}
	snap2, _ := ReadSnapshot(ref.RunDir)
	state := current
	if snap2 != nil && snap2.issued() != "" {
		state = snap2.issued()
	}
	d := c.decision(KindBlocked, firstNonEmpty(state, "blocked"))
	d.Reason = spOrNil(plan.Reason)
	d.StepID = spOrNil(state)
	return materialize(d)
}

// compositionBlocked is _dn_composition_blocked_decision.
func (c *advanceCtx) compositionBlocked(step string, failures []string) *Decision {
	action, wpID, wsPath := c.stateToAction(step)
	d := c.decision(KindBlocked, step)
	d.Reason, d.StepID, d.GuardFailures = sp(failures[0]), sp(step), failures
	d.Action, d.WPID, d.WorkspacePath = spOrNil(action), spOrNil(wpID), spOrNil(wsPath)
	if action != "" {
		var p string
		var err error
		if wpID != "" {
			p, err = buildWPPrompt(c.m, action, wpID, c.agent)
		} else {
			p, err = buildTemplatePrompt(c.m, c.featureDir, action, c.agent)
		}
		if err == nil {
			d.PromptFile = spOrNil(p)
		}
	}
	return materialize(d)
}

// pyErrType names an error class for the composition crash surface.
func pyErrType(err error) string {
	switch err.(type) {
	case *invocation.WriteError:
		return "InvocationWriteError"
	case *invocation.Error:
		return "InvocationError"
	}
	if strings.Contains(err.Error(), "not found") {
		return "ProfileNotFoundError"
	}
	return "RuntimeError"
}

func buildDecisionPrompt(m *missionCtx, p planned, agent string) string {
	bar := strings.Repeat("=", 80)
	l := []string{bar, "DECISION REQUIRED", bar, "", "Mission: " + m.Slug, "Agent: " + agent, "Decision ID: " + p.DecisionID, "", "Question: " + p.Question, ""}
	if len(p.Options) > 0 {
		l = append(l, "Options:")
		for i, o := range p.Options {
			l = append(l, fmt.Sprintf("  %d. %s", i+1, o))
		}
		l = append(l, "")
	}
	l = append(l, "To answer:", fmt.Sprintf("  spec-kitty next --agent %s --mission %s --answer \"<your answer>\" --decision-id \"%s\"", agent, m.Slug, p.DecisionID))
	return writeNextPrompt(m.Root, "decision", "", agent, m.Slug, strings.Join(l, "\n"))
}

// ---------------------------------------------------------------------------
// Lifecycle records (kitty-ops/lifecycle.jsonl) and MissionNextInvoked
// ---------------------------------------------------------------------------

func lifecyclePath(root string) string { return filepath.Join(root, "kitty-ops", "lifecycle.jsonl") }

func readLifecycle(root string) []*jsonx.Object {
	f, err := os.Open(lifecyclePath(root))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []*jsonx.Object
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		if o, err := jsonx.DecodeObject(sc.Bytes()); err == nil && o != nil {
			out = append(out, o)
		}
	}
	return out
}

func appendLifecycle(root string, rec *jsonx.Object) {
	os.MkdirAll(filepath.Dir(lifecyclePath(root)), 0o755)
	f, err := os.OpenFile(lifecyclePath(root), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(jsonx.Dumps(rec, jsonx.Options{Indent: -1, SortKeys: true, EnsureASCII: true}) + "\n")
}

// PairPreviousLifecycleRecord is pair_previous_lifecycle_record.
func PairPreviousLifecycleRecord(root, slug, agent, result string) {
	mid := mission.ReadMetaOrEmpty(project.Specs(root, slug)).GetString("mission_id")
	if mid == "" {
		return
	}
	recs := readLifecycle(root)
	var started *jsonx.Object
	for i := len(recs) - 1; i >= 0; i-- {
		r := recs[i]
		if r.GetString("phase") != "started" || r.GetString("agent") != agent || r.GetString("mission_id") != mid {
			continue
		}
		paired := false
		for _, q := range recs[i+1:] {
			if q.GetString("phase") != "started" && q.GetString("canonical_action_id") == r.GetString("canonical_action_id") &&
				q.GetString("agent") == agent && q.GetString("mission_id") == mid {
				paired = true
				break
			}
		}
		if !paired {
			started = r
		}
		break
	}
	if started == nil {
		return
	}
	phase := "completed"
	var reason any
	if result != "success" {
		phase, reason = "failed", result
	}
	wp, _ := started.Get("wp_id")
	appendLifecycle(root, jsonx.NewObject("agent", agent, "at", clock.NowUTCISO(), "canonical_action_id", started.GetString("canonical_action_id"),
		"mission_id", mid, "phase", phase, "reason", reason, "wp_id", wp))
}

// WriteIssuanceRecord is write_issuance_lifecycle_record.
func WriteIssuanceRecord(root, slug, agent string, d *Decision) {
	if d.Kind != KindStep || d.Action == nil || d.MissionState == "" {
		return
	}
	mid := mission.ReadMetaOrEmpty(project.Specs(root, slug)).GetString("mission_id")
	if mid == "" {
		return
	}
	appendLifecycle(root, jsonx.NewObject("agent", agent, "at", clock.NowUTCISO(), "canonical_action_id", d.MissionState+"::"+*d.Action,
		"mission_id", mid, "phase", "started", "reason", nil, "wp_id", pv(d.WPID)))
}

// EmitMissionNextInvoked appends MissionNextInvoked to mission-events.jsonl
// on the status surface.
func EmitMissionNextInvoked(root, slug, agent, result string, d *Decision) {
	m := newMissionCtx(root, slug)
	dir := m.StatusDir
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return
	}
	ev := jsonx.NewObject("mission", d.Mission, "payload", jsonx.NewObject("action", pv(d.Action), "agent", agent,
		"decision_kind", d.Kind, "mission_state", d.MissionState, "result_input", result, "wp_id", pv(d.WPID)),
		"timestamp", clock.NowUTCISO(), "type", "MissionNextInvoked")
	f, err := os.OpenFile(filepath.Join(dir, "mission-events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(jsonx.Dumps(ev, jsonx.Options{Indent: -1, SortKeys: true, EnsureASCII: true}) + "\n")
}
