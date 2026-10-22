package agentcore

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/prereview"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

const preReviewRunningNotice = "[cyan]Pre-review regression gate: running scoped tests at head (may take a few minutes)...[/cyan]"

// runPreReviewGate is _mt_run_transition_gates: the doctrine-resolved
// pre-review regression gate on every for_review move (escape hatch, lazy
// baseline capture, fail-open resolution + dispatch, aggregation, the two
// hard-stops and the byproduct compensator).
func (st *moveState) runPreReviewGate() error {
	if st.target != string(status.ForReview) {
		return nil
	}
	skip := ""
	if st.skipGate {
		skip = "--skip-pre-review-gate flag"
	} else if truthyEnv(os.Getenv("SPEC_KITTY_SKIP_PRE_REVIEW_GATE")) {
		skip = "SPEC_KITTY_SKIP_PRE_REVIEW_GATE is set"
	}
	if skip != "" {
		st.gateMeta = prereview.Metadata(prereview.EmptyScopeVerdict("gate skipped — "+skip, nil), false, false, false)
		st.console(fmt.Sprintf("Pre-review regression gate: SKIPPED (%s)", skip))
		return nil
	}
	st.captureBaselineIfMissing()
	wt, dirtyBefore, verdicts := st.resolveGateVerdicts()
	created := gateByproducts(wt, dirtyBefore)
	eff := prereview.Translate(verdicts, prereview.BlockEnabled(st.root), st.force)
	if len(created) > 0 && eff.ShouldExit {
		for _, p := range created {
			os.Remove(p)
		}
	}
	st.gateMeta = eff.Metadata
	for _, l := range eff.ConsoleLines {
		st.console(l)
	}
	if eff.Terminal {
		outcome := eff.Representative.Outcome
		return st.outDiag(fmt.Sprintf("Pre-review regression gate %s; transition not applied", outcome),
			jsonx.NewObject("result", "error", "error", "pre-review gate "+outcome, "transition_applied", false, "pre_review_gate", st.gateMeta))
	}
	if eff.Blocked {
		msg := prereview.BlockMessage(eff.Representative)
		return st.outDiag(msg, jsonx.NewObject("result", "error", "error", msg, "transition_applied", false, "pre_review_gate", st.gateMeta))
	}
	return nil
}

// logWarn is a logger.warning line (stderr; suppressed in --json mode).
func (st *moveState) logWarn(msg string) {
	if !st.json {
		warnLog(st.ctx.Stderr, msg)
	}
}

// laneWorkspace is _mt_resolve_pre_review_workspace ("" = none).
func (st *moveState) laneWorkspace() string {
	ws, err := lanes.ResolveWorkspace(st.root, st.m.Slug, st.taskID)
	if err != nil || !ws.Exists() {
		return ""
	}
	return ws.WorktreePath
}

func (st *moveState) wpSlug() string {
	return strings.TrimSuffix(filepath.Base(st.w.Path), filepath.Ext(st.w.Path))
}

// captureBaselineIfMissing is _mt_capture_baseline_if_missing +
// capture_and_commit_baseline on the in_progress -> for_review edge.
func (st *moveState) captureBaselineIfMissing() {
	if st.oldLane != string(status.InProgress) {
		return
	}
	dir := st.m.PrimaryDir()
	artifact := prereview.ArtifactPath(dir, st.wpSlug())
	if _, err := os.Stat(artifact); err == nil {
		return
	}
	ref := st.targetBranch
	if wt := st.laneWorkspace(); wt != "" {
		if out, err := gitx.Run(wt, "merge-base", "HEAD", st.targetBranch); err == nil && strings.TrimSpace(out) != "" {
			ref = strings.TrimSpace(out)
		}
	}
	if ref == st.targetBranch {
		st.logWarn(fmt.Sprintf("Could not resolve the merge-base of %s's lane with %s; capturing the baseline at the base branch tip.", st.taskID, st.targetBranch))
	}
	b, err := prereview.Capture(prereview.CaptureOptions{RepoRoot: st.root, BaseBranch: ref, WPID: st.taskID, Artifact: artifact,
		Source: prereview.NewDeclaredSource(st.root), Warn: st.logWarn})
	if err != nil {
		st.logWarn("Baseline capture error: " + err.Error())
		return
	}
	if b != nil && b.Failed > 0 {
		st.console(fmt.Sprintf("Baseline: %d pre-existing test failure(s) captured", b.Failed))
	} else if b != nil && b.Failed == -1 {
		st.console("Warning: baseline test capture failed — no baseline context available")
	}
	if b == nil || b.Failed == -1 {
		return
	}
	if _, err := os.Stat(artifact); err != nil {
		return
	}
	rel, _ := filepath.Rel(st.root, artifact)
	if es, err := safecommit.StatusEntries(st.root, "", filepath.ToSlash(rel)); err == nil && len(es) == 0 {
		return
	}
	if _, err := safecommit.Commit(st.root, st.root, st.targetBranch, "chore: Capture baseline tests for "+st.taskID, []string{artifact}, safecommit.Options{}); err != nil {
		st.logWarn("Baseline artifact commit failed: " + err.Error())
		if !st.json {
			richPrint(st.ctx.Stdout, fmt.Sprintf("Warning: baseline artifact was not committed (%s); a later move to for_review may block on it.", err.Error()))
		}
	}
}

// gateDirtyPaths is _mt_pre_review_dirty_paths (nil = unknown).
func gateDirtyPaths(wt string) []string {
	es, err := statusEntries(wt)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, e := range filterRuntimeState(es) {
		for _, p := range []string{e.origPath, e.path} {
			if p != "" && !isRuntimeStatePath(p) {
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

// gateChangedFiles is _mt_pre_review_changed_files.
func gateChangedFiles(wt, base string) []string {
	set := map[string]bool{}
	if mb, err := gitx.Run(wt, "merge-base", "HEAD", base); err == nil && strings.TrimSpace(mb) != "" {
		if out, err := gitx.Run(wt, "diff", "--name-only", strings.TrimSpace(mb), "HEAD"); err == nil {
			for _, l := range strings.Split(out, "\n") {
				if l = strings.TrimSpace(l); l != "" {
					set[l] = true
				}
			}
		}
	}
	for _, p := range gateDirtyPaths(wt) {
		set[p] = true
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// gateByproducts is _mt_enrol_gate_byproducts: paths the gate subprocess
// created inside the worktree (absolute).
func gateByproducts(wt string, before []string) []string {
	if wt == "" || before == nil {
		return nil
	}
	after := gateDirtyPaths(wt)
	if after == nil {
		return nil
	}
	had := map[string]bool{}
	for _, p := range before {
		had[p] = true
	}
	var out []string
	for _, p := range after {
		if !had[p] {
			out = append(out, filepath.Join(wt, p))
		}
	}
	return out
}

func (st *moveState) gateObserver() *prereview.Observer {
	if st.json {
		return nil
	}
	return &prereview.Observer{
		ScopeAssessed: func(a *prereview.Assessment) {
			targets := strings.Join(a.NormalizedTargets, ", ")
			if targets == "" {
				targets = "(empty)"
			}
			suffix := ""
			if a.Classification == prereview.BudgetUnknown {
				suffix = "; no reviewed metadata matches, so validation will run under the existing timeout"
			}
			st.console(fmt.Sprintf("[cyan]Pre-review gate scope assessment: %s; targets=%s; effective budget=%ss%s[/cyan]",
				a.Classification, targets, prereview.PyG(a.EffectiveBudget), suffix))
		},
		Heartbeat: func(phase string, elapsed float64) {
			if elapsed <= 0 {
				st.console(fmt.Sprintf("[cyan]Pre-review gate validation started; phase=%s; elapsed=%ss[/cyan]", phase, prereview.PyG(elapsed)))
				return
			}
			st.console(fmt.Sprintf("[cyan]Pre-review gate still running; phase=%s; elapsed=%ss[/cyan]", phase, prereview.PyG(elapsed)))
		},
	}
}

// scopeOverride is _mt_pre_review_scope_override (frontmatter
// pre_review_test_scope > config review.pre_review_test_command).
func (st *moveState) scopeOverride() ([]string, bool) {
	front, _ := splitFrontmatterText(strings.TrimPrefix(st.w.Raw, "\ufeff"))
	if v, ok := extractScalar(front, prereview.FrontmatterScopeKey); ok && v != "" {
		return strings.Fields(v), true
	}
	if v, _ := prereview.ReviewSection(st.root).Get(prereview.KeyPreReviewTestCommand); prereview.Truthy(v) {
		return strings.Fields(status.PyStr(v)), true
	}
	return nil, false
}

// failOpenEval runs one gate-execution callable under the three-catch
// fail-open (_mt_fail_open_gate).
func failOpenEval(run func() (*prereview.Verdict, error)) *prereview.Verdict {
	v, err := run()
	if err != nil {
		return prereview.EmptyScopeVerdict("pre-review gate evaluation failed — unverified: "+err.Error(), nil)
	}
	return v
}

// resolveGateVerdicts is _mt_resolve_transition_gate_verdicts +
// _mt_collect_transition_gate_verdicts.
func (st *moveState) resolveGateVerdicts() (string, []string, []*prereview.Verdict) {
	wt := st.laneWorkspace()
	dirtyBefore := []string{}
	var changed []string
	if wt != "" {
		dirtyBefore = gateDirtyPaths(wt)
		changed = gateChangedFiles(wt, st.targetBranch)
	}
	gateRoot := wt
	if gateRoot == "" {
		gateRoot = st.root
	}
	obs := st.gateObserver()
	artifact := prereview.ArtifactPath(st.m.PrimaryDir(), st.wpSlug())
	if targets, ok := st.scopeOverride(); ok {
		st.console(preReviewRunningNotice)
		return wt, dirtyBefore, []*prereview.Verdict{failOpenEval(func() (*prereview.Verdict, error) {
			b, err := prereview.LoadBaseline(artifact)
			if err != nil {
				return nil, err
			}
			if len(targets) == 0 {
				return prereview.EmptyScopeVerdict("override test scope is empty", nil), nil
			}
			return prereview.EvaluateWithScope(prereview.Scope{TestTargets: targets}, prereview.EvalOptions{Dir: gateRoot, Baseline: b, Observer: obs})
		})}
	}
	if len(changed) == 0 {
		return wt, dirtyBefore, []*prereview.Verdict{prereview.EmptyScopeVerdict("no changed files detected for this WP — skipping the gate cheaply", nil)}
	}
	resolutionFailed := func(err error) (string, []string, []*prereview.Verdict) {
		return "", []string{}, []*prereview.Verdict{prereview.EmptyScopeVerdict("pre-review gate resolution failed — unverified: "+err.Error(), nil)}
	}
	mt := mission.MissionType(mission.ReadMetaOrEmpty(st.m.PrimaryDir()))
	res, err := prereview.ResolveGateBindings(st.root, mt, st.oldLane+"->"+st.target)
	if err != nil {
		return resolutionFailed(err)
	}
	if !res.Active {
		return wt, dirtyBefore, []*prereview.Verdict{prereview.EmptyScopeVerdict(res.Reason, nil)}
	}
	src := prereview.NewDeclaredSource(st.root)
	if _, ok := src.TestCommand(); !ok {
		if _, present := prereview.TestCommandTemplate(st.root); !present {
			return wt, dirtyBefore, []*prereview.Verdict{prereview.NotDeclaredVerdict()}
		}
	}
	st.console(preReviewRunningNotice)
	baseline, err := prereview.LoadBaseline(artifact)
	if err != nil {
		return resolutionFailed(err)
	}
	var out []*prereview.Verdict
	for _, h := range res.Handlers {
		if h != prereview.PreReviewHandler {
			msg := fmt.Sprintf("Unknown gate handler: %s. Registered handlers: %s", pyjson.ReprString(h), prereview.PreReviewHandler)
			out = append(out, prereview.EmptyScopeVerdict("pre-review gate evaluation failed — unverified: "+pyjson.ReprString(msg), nil))
			continue
		}
		out = append(out, failOpenEval(func() (*prereview.Verdict, error) {
			return prereview.EvaluatePreReview(changed, prereview.EvalOptions{Dir: gateRoot, Baseline: baseline, Observer: obs,
				Source: prereview.NewDeclaredSource(st.root)})
		}))
	}
	return wt, dirtyBefore, out
}
