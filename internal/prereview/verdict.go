package prereview

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// Gate outcomes (GateOutcome).
const (
	NoCoverage         = "no_coverage"
	NoNewFailures      = "no_new_failures"
	NewFailures        = "new_failures"
	UnverifiedBaseline = "unverified_baseline"
	SourceMismatch     = "source_mismatch"
	TimedOut           = "timed_out"
	Cancelled          = "cancelled"
	Skipped            = "skipped"
	ScopeOversized     = "scope_oversized"
)

// Scope is ScopeResult.
type Scope struct {
	TestTargets, MatchedShardGroups, MatchedCompositeDirs, EmptyConeCompositeDirs, ExcludedScopeFiles []string
}

// Empty is ScopeResult.is_empty.
func (s Scope) Empty() bool { return len(s.TestTargets) == 0 }

// DescribeEmptyReason is ScopeResult.describe_empty_reason.
func (s Scope) DescribeEmptyReason() string {
	if len(s.EmptyConeCompositeDirs) > 0 {
		return "unmapped composite dir(s) with no test cone_roots — unverified (SC-007): " + strings.Join(s.EmptyConeCompositeDirs, ", ")
	}
	return "excluded scope — unverified: every changed file landed only in a catch-all group (core_misc/e2e/any_src) or matched no dorny group at all"
}

// Verdict is GateVerdict (Reason nil = None).
type Verdict struct {
	Outcome                 string
	Scope                   Scope
	Reason                  *string
	NewFailures             []Failure
	PreExisting             []Failure
	RunState                string
	Budget                  *Assessment
	ClassificationCandidate bool
	ObservedElapsed         *float64
}

func strp(s string) *string { return &s }

// NewVerdict builds a verdict with run_state completed.
func NewVerdict(outcome, reason string) *Verdict {
	return &Verdict{Outcome: outcome, Reason: strp(reason), RunState: RunCompleted}
}

// Observer receives the engine-owned status events (scope assessed;
// heartbeat while the head run is in flight).
type Observer struct {
	ScopeAssessed func(a *Assessment)
	Heartbeat     func(phase string, elapsed float64)
}

func classify(current []Failure, scope Scope, b *Baseline) *Verdict {
	if b == nil || b.Failed == -1 {
		return &Verdict{Outcome: UnverifiedBaseline, Scope: scope, RunState: RunCompleted,
			Reason: strp("baseline uncomputable — surfacing all current failures as unverified"), NewFailures: current}
	}
	pre, newF, _ := Diff(b, current)
	if len(newF) > 0 {
		return &Verdict{Outcome: NewFailures, Scope: scope, RunState: RunCompleted, NewFailures: newF, PreExisting: pre}
	}
	return &Verdict{Outcome: NoNewFailures, Scope: scope, RunState: RunCompleted, PreExisting: pre}
}

func fmtG(f float64) string { return strconv.FormatFloat(f, 'g', 6, 64) }

// PyG renders a float like Python's "{:g}".
func PyG(f float64) string {
	s := fmtG(f)
	if strings.Contains(s, "e") {
		mant, exp, _ := strings.Cut(s, "e")
		sign := exp[0]
		digits := strings.TrimLeft(exp[1:], "0")
		if len(digits) < 2 {
			digits = strings.Repeat("0", 2-len(digits)) + digits
		}
		return mant + "e" + string(sign) + digits
	}
	return s
}

func finalize(v *Verdict, a *Assessment, elapsed *float64) *Verdict {
	candidate := v.Outcome == TimedOut && a.Classification == BudgetUnknown
	if candidate {
		et := "unavailable"
		if elapsed != nil {
			et = fmt.Sprintf("%.3fs", *elapsed)
		}
		targets := strings.Join(a.NormalizedTargets, ", ")
		if targets == "" {
			targets = "(empty)"
		}
		prefix := ""
		if v.Reason != nil && *v.Reason != "" {
			prefix = *v.Reason + "; "
		}
		v.Reason = strp(fmt.Sprintf("%sbudget classification unknown; scope identity %s; normalized targets: %s; configured budget: %ss; monotonic observed elapsed: %s; lane remains unchanged. This is a classification candidate for a reviewed metadata update; runtime policy was not modified.",
			prefix, a.IdentityValue, targets, PyG(a.EffectiveBudget), et))
	}
	v.Budget, v.ClassificationCandidate, v.ObservedElapsed = a, candidate, elapsed
	return v
}

const noTestCommandReason = "no test command configured for the injected ScopeSource — review proceeds without it"

// EvalOptions configures one head evaluation.
type EvalOptions struct {
	Dir      string
	Baseline *Baseline
	Timeout  int // seconds
	Observer *Observer
	Source   *DeclaredSource // nil = override tier (pytest at head)
}

// EvaluateWithScope is pre_review_gate.evaluate_with_scope.
func EvaluateWithScope(scope Scope, o EvalOptions) (*Verdict, error) {
	if o.Timeout == 0 {
		o.Timeout = CaptureTimeoutSeconds
	}
	a := AssessBudget(scope.TestTargets, float64(o.Timeout))
	if o.Observer != nil && o.Observer.ScopeAssessed != nil {
		o.Observer.ScopeAssessed(a)
	}
	if a.Classification == BudgetOversized {
		return &Verdict{Outcome: ScopeOversized, Scope: scope, RunState: RunNotStarted, Budget: a,
			Reason: strp(fmt.Sprintf("validation did not start because the selected scope is explicitly oversized for the %ss budget; %s", PyG(a.EffectiveBudget), a.Guidance))}, nil
	}
	var progress func(float64)
	if o.Observer != nil && o.Observer.Heartbeat != nil {
		progress = func(e float64) { o.Observer.Heartbeat("candidate_head", e) }
	}
	var elapsed *float64
	observe := func(e float64) { elapsed = &e }
	if o.Source == nil {
		if scope.Empty() {
			return finalize(&Verdict{Outcome: NoCoverage, Scope: scope, RunState: RunCompleted, Reason: strp(scope.DescribeEmptyReason())}, a, nil), nil
		}
		r := RunScopedTestsAtHead(scope.TestTargets, o.Dir, o.Timeout, progress)
		switch r.State {
		case RunTimedOut:
			return finalize(&Verdict{Outcome: TimedOut, Scope: scope, Reason: strp(r.Error), RunState: r.State}, a, r.Elapsed), nil
		case RunCancelled:
			return finalize(&Verdict{Outcome: Cancelled, Scope: scope, Reason: strp(r.Error), RunState: r.State}, a, r.Elapsed), nil
		}
		if !r.Ran {
			return finalize(&Verdict{Outcome: NoCoverage, Scope: scope, Reason: strp("scoped test run did not complete: " + r.Error), RunState: r.State}, a, r.Elapsed), nil
		}
		return finalize(classify(r.Failures, scope, o.Baseline), a, r.Elapsed), nil
	}
	v, err := evaluateViaSource(scope, o, progress, observe)
	if err != nil {
		return nil, err
	}
	return finalize(v, a, elapsed), nil
}

func evaluateViaSource(scope Scope, o EvalOptions, progress, observe func(float64)) (*Verdict, error) {
	cmd, ok := o.Source.TestCommand()
	if !ok {
		return &Verdict{Outcome: NoCoverage, Scope: scope, RunState: RunCompleted, Reason: strp(noTestCommandReason)}, nil
	}
	state, raw, errText := RunRaw(append(cmd, scope.TestTargets...), RunOptions{Dir: o.Dir, Timeout: time.Duration(o.Timeout) * time.Second,
		Progress: progress, Elapsed: observe, Serial: true})
	switch state {
	case RunTimedOut:
		return &Verdict{Outcome: TimedOut, Scope: scope, Reason: strp(errText), RunState: state}, nil
	case RunCancelled:
		return &Verdict{Outcome: Cancelled, Scope: scope, Reason: strp(errText), RunState: state}, nil
	}
	if raw == nil {
		return &Verdict{Outcome: NoCoverage, Scope: scope, Reason: strp("scoped test run did not complete: " + errText), RunState: state}, nil
	}
	failures, err := o.Source.ParseResults(raw)
	if err != nil {
		return nil, err
	}
	head := o.Source.Identity(raw)
	if b := o.Baseline; b != nil && b.SourceIdentity != UnknownSourceIdentity && b.SourceIdentity != head {
		return &Verdict{Outcome: SourceMismatch, Scope: scope, RunState: RunCompleted,
			Reason: strp(fmt.Sprintf("baseline captured under %s; head ran under %s — failure identities are not comparable", b.SourceIdentity, head))}, nil
	}
	return classify(failures, scope, o.Baseline), nil
}

// EvaluatePreReview is evaluate_pre_review_gate for the declared source
// (no per-file narrowing: the scope is always empty).
func EvaluatePreReview(changed []string, o EvalOptions) (*Verdict, error) {
	return EvaluateWithScope(Scope{}, o)
}

// HeadRun is HeadRunResult (the override tier's pytest run).
type HeadRun struct {
	Ran      bool
	Failures []Failure
	Error    string
	State    string
	Elapsed  *float64
}

// PythonExecutable stands in for upstream's sys.executable when the
// override tier runs pytest without uv.
var PythonExecutable = "python3"

// resolvePytestCommand is review._interpreter.resolve_pytest_command.
func resolvePytestCommand(args []string, root string) []string {
	if _, err := exec.LookPath("uv"); err == nil {
		if st, err := os.Stat(filepath.Join(root, "pyproject.toml")); err == nil && !st.IsDir() {
			cmd := []string{"uv", "run", "--frozen"}
			cmd = append(cmd, testDependencyFlags(filepath.Join(root, "pyproject.toml"))...)
			cmd = append(cmd, "--project", root, "python", "-m", "pytest")
			return append(cmd, args...)
		}
	}
	return append([]string{PythonExecutable, "-m", "pytest"}, args...)
}

// testDependencyFlags looks for a `test` optional-dependency / dependency
// group in pyproject.toml (best-effort table scan).
func testDependencyFlags(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	section := ""
	inOptional, inGroups := false, false
	for _, raw := range strings.Split(string(b), "\n") {
		l := strings.TrimSpace(raw)
		if strings.HasPrefix(l, "[") {
			section = strings.Trim(l, "[] ")
			inOptional = section == "project.optional-dependencies"
			inGroups = section == "dependency-groups"
			continue
		}
		key, _, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		if strings.Trim(strings.TrimSpace(key), `"'`) == "test" {
			if inOptional {
				return []string{"--extra", "test"}
			}
			if inGroups {
				return []string{"--group", "test"}
			}
		}
	}
	return nil
}

// RunScopedTestsAtHead is run_scoped_tests_at_head (pytest with a JUnit
// artifact in a private temp dir).
func RunScopedTestsAtHead(targets []string, dir string, timeout int, progress func(float64)) HeadRun {
	if len(targets) == 0 {
		return HeadRun{Error: "empty test scope — nothing to run", State: RunIncompleteOutput}
	}
	tmp, err := os.MkdirTemp("", "")
	if err != nil {
		return HeadRun{Error: "scoped test run failed to launch: " + err.Error(), State: RunLaunchFailed}
	}
	defer os.RemoveAll(tmp)
	junit := filepath.Join(tmp, "pre-review-junit.xml")
	cmd := resolvePytestCommand(append(append([]string{}, targets...), "--junitxml="+junit, "-q"), dir)
	var elapsed *float64
	state, raw, errText := RunRaw(cmd, RunOptions{Dir: dir, Timeout: time.Duration(timeout) * time.Second, Progress: progress,
		Elapsed: func(e float64) { elapsed = &e }, Serial: true})
	switch state {
	case RunLaunchFailed:
		return HeadRun{Error: errText, State: state}
	case RunTimedOut:
		if elapsed == nil {
			f := float64(timeout)
			elapsed = &f
		}
		return HeadRun{Error: errText, State: state, Elapsed: elapsed}
	case RunCancelled:
		return HeadRun{Error: errText, State: state}
	}
	if !exists(junit) {
		return HeadRun{State: RunIncompleteOutput, Error: fmt.Sprintf("no JUnit XML produced by the scoped run (exit=%d); stderr tail: %s", raw.ReturnCode, tail(raw.Stderr, stderrTailChars))}
	}
	_, _, _, _, failures, err := ParseJUnit(junit)
	if err != nil {
		return HeadRun{State: RunIncompleteOutput, Error: "failed to parse scoped-run JUnit XML: " + err.Error()}
	}
	return HeadRun{Ran: true, Failures: failures, State: RunCompleted}
}

// --- aggregation / rendering ----------------------------------------------

// Aggregate is verdict_aggregation.aggregate_verdicts.
type Aggregate struct {
	Decision string // terminal | block | warn_proceed
	Warnings []*Verdict
	Blocking []*Verdict
	Terminal *Verdict
}

// ShouldExit is AggregateVerdict.should_exit.
func (a *Aggregate) ShouldExit() bool { return a.Decision == "terminal" || a.Decision == "block" }

// AggregateVerdicts is aggregate_verdicts.
func AggregateVerdicts(vs []*Verdict, blockEnabled, force bool) *Aggregate {
	for _, v := range vs {
		if v.Outcome == TimedOut || v.Outcome == Cancelled || v.Outcome == ScopeOversized {
			return &Aggregate{Decision: "terminal", Warnings: vs, Terminal: v}
		}
	}
	var blocking []*Verdict
	for _, v := range vs {
		if v.Outcome == NewFailures {
			blocking = append(blocking, v)
		}
	}
	if blockEnabled && len(blocking) > 0 && !force {
		return &Aggregate{Decision: "block", Warnings: vs, Blocking: blocking}
	}
	return &Aggregate{Decision: "warn_proceed", Warnings: vs, Blocking: blocking}
}

func strList(ss []string) []any {
	out := []any{}
	for _, s := range ss {
		out = append(out, s)
	}
	return out
}

// Metadata is _mt_pre_review_gate_metadata (policy_metadata.pre_review_gate).
func Metadata(v *Verdict, blockEnabled, blocked, forceBypassed bool) *jsonx.Object {
	var reason any
	if v.Reason != nil {
		reason = *v.Reason
	}
	ids := []any{}
	for _, f := range v.NewFailures {
		ids = append(ids, f.Test)
	}
	m := jsonx.NewObject("outcome", v.Outcome, "reason", reason, "new_failure_count", len(v.NewFailures),
		"new_failure_nodeids", ids, "pre_existing_failure_count", len(v.PreExisting),
		"affected_shard_count", len(v.Scope.MatchedShardGroups)+len(v.Scope.MatchedCompositeDirs),
		"matched_shard_groups", strList(v.Scope.MatchedShardGroups), "matched_composite_dirs", strList(v.Scope.MatchedCompositeDirs),
		"test_targets", strList(v.Scope.TestTargets), "block_enabled", blockEnabled, "blocked", blocked,
		"force_bypassed", forceBypassed, "run_state", v.RunState)
	if a := v.Budget; a != nil {
		var rule, elapsed any
		if a.MatchedRule != "" {
			rule = a.MatchedRule
		}
		if v.ObservedElapsed != nil {
			elapsed = jsonx.Float(*v.ObservedElapsed)
		}
		m.Set("budget_classification", a.Classification)
		m.Set("scope_identity", a.IdentityValue)
		m.Set("effective_budget_seconds", jsonx.Float(a.EffectiveBudget))
		m.Set("matched_budget_rule", rule)
		m.Set("classification_candidate", v.ClassificationCandidate)
		m.Set("observed_elapsed_seconds", elapsed)
		m.Set("classification_guidance", a.Guidance)
		if v.Outcome == ScopeOversized {
			m.Set("recovery_choices", []any{"Select a bounded pre_review_test_scope", "Use --skip-pre-review-gate explicitly"})
		}
	}
	return m
}

const blockUnenforceableHint = "block requested via review.fail_on_pre_review_regression but COULD NOT be enforced — no verified new-failure verdict exists to block on. A baseline must be captured (on the first for_review move; configure review.test_command in .kittify/config.yaml) before this block can ever take effect."

func reasonOr(v *Verdict, def string) string {
	if v.Reason != nil && *v.Reason != "" {
		return *v.Reason
	}
	return def
}

func nodeIDs(v *Verdict) string {
	var ids []string
	for i, f := range v.NewFailures {
		if i >= 5 {
			break
		}
		ids = append(ids, f.Test)
	}
	s := strings.Join(ids, ", ")
	if len(v.NewFailures) > 5 {
		s += fmt.Sprintf(" (+%d more)", len(v.NewFailures)-5)
	}
	return s
}

// ConsoleWarning is _mt_pre_review_gate_console_warning (rich markup;
// ok=false renders no line).
func ConsoleWarning(v *Verdict, blockEnabled bool) (string, bool) {
	switch v.Outcome {
	case NewFailures:
		shards := len(v.Scope.MatchedShardGroups) + len(v.Scope.MatchedCompositeDirs)
		return fmt.Sprintf("[yellow]Pre-review regression gate:[/yellow] %d new failure(s) across %d affected shard(s) — %s", len(v.NewFailures), shards, nodeIDs(v)), true
	case NoCoverage, UnverifiedBaseline:
		if blockEnabled {
			return fmt.Sprintf("[yellow]Pre-review regression gate:[/yellow] %s (outcome=%s: %s)", blockUnenforceableHint, v.Outcome, reasonOr(v, "unverified")), true
		}
		return fmt.Sprintf("[dim]Pre-review regression gate: %s — %s[/dim]", v.Outcome, reasonOr(v, "unverified")), true
	case Skipped:
		if blockEnabled {
			return fmt.Sprintf("[yellow]Pre-review regression gate:[/yellow] %s (outcome=%s: %s)", blockUnenforceableHint, v.Outcome, reasonOr(v, "gate skipped")), true
		}
		return "", false
	case SourceMismatch:
		return fmt.Sprintf("[yellow]Pre-review regression gate: %s — %s[/yellow]", v.Outcome, reasonOr(v, "unverified")), true
	case TimedOut, Cancelled:
		return fmt.Sprintf("[red]Pre-review regression gate: %s — %s[/red]", v.Outcome, reasonOr(v, "interrupted")), true
	case ScopeOversized:
		t := strings.Join(v.Scope.TestTargets, ", ")
		if t == "" {
			t = "(empty)"
		}
		return fmt.Sprintf("[red]Pre-review regression gate: scope_oversized — validation did not start; targets=%s; %s. The work package remains in its prior lane. Recovery choices: select a bounded pre_review_test_scope or use --skip-pre-review-gate explicitly.[/red]",
			t, reasonOr(v, "scope exceeds the effective budget")), true
	case NoNewFailures:
		return "[dim]Pre-review regression gate: no new failures[/dim]", true
	}
	return "[dim]Pre-review regression gate: " + v.Outcome + "[/dim]", true
}

// BlockMessage is _mt_pre_review_gate_block_message.
func BlockMessage(v *Verdict) string {
	return fmt.Sprintf("Pre-review regression gate BLOCKED this for_review move: %d new failure(s) introduced — %s. Fix the regression, or re-run with --force to override (recorded in the transition evidence).",
		len(v.NewFailures), nodeIDs(v))
}

// Effect is _TransitionGateEffect.
type Effect struct {
	Metadata       *jsonx.Object
	ConsoleLines   []string
	Representative *Verdict
	Blocked        bool
	Terminal       bool
	ShouldExit     bool
}

// Translate is _mt_translate_gate_verdicts.
func Translate(vs []*Verdict, blockEnabled, force bool) *Effect {
	agg := AggregateVerdicts(vs, blockEnabled, force)
	var rep *Verdict
	switch {
	case agg.Terminal != nil:
		rep = agg.Terminal
	case len(agg.Blocking) > 0:
		rep = agg.Blocking[0]
	case len(vs) > 0:
		rep = vs[len(vs)-1]
	default:
		rep = EmptyScopeVerdict("no active gate bindings for this transition", nil)
	}
	blocked := agg.Decision == "block"
	terminal := agg.Decision == "terminal"
	md := Metadata(rep, blockEnabled, blocked, blockEnabled && force && len(agg.Blocking) > 0)
	if terminal {
		md.Set("transition_applied", false)
	}
	var lines []string
	if len(agg.Warnings) > 0 {
		for _, v := range agg.Warnings {
			if l, ok := ConsoleWarning(v, blockEnabled); ok {
				lines = append(lines, l)
			}
		}
	} else if l, ok := ConsoleWarning(rep, blockEnabled); ok {
		lines = append(lines, l)
	}
	return &Effect{Metadata: md, ConsoleLines: lines, Representative: rep, Blocked: blocked, Terminal: terminal, ShouldExit: agg.ShouldExit()}
}

// EmptyScopeVerdict is _mt_empty_scope_verdict (a no_coverage verdict).
func EmptyScopeVerdict(reason string, excluded []string) *Verdict {
	return &Verdict{Outcome: NoCoverage, Scope: Scope{ExcludedScopeFiles: excluded}, Reason: strp(reason), RunState: RunCompleted}
}

// NotDeclaredReason is the quiet skip recorded when the repo declares no gate.
const NotDeclaredReason = "pre-review regression gate not declared by this repo — skipped (non-blocking; configure review.test_command in .kittify/config.yaml to declare one)"

// NotDeclaredVerdict is _mt_not_declared_skip_verdict.
func NotDeclaredVerdict() *Verdict {
	return &Verdict{Outcome: Skipped, Reason: strp(NotDeclaredReason), RunState: RunCompleted}
}
