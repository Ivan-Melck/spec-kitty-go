package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// Charter preflight advisory warnings (charter_runtime.preflight).
const (
	freshProjectMissingCharter = "project charter is not initialized; run `spec-kitty charter generate` when this project is ready for charter-governed workflows"
	legacyCharterBundle        = "a legacy charter.md-only bundle was detected (.kittify/charter/charter.md exists but .kittify/charter/charter.yaml does not); this project has governance intent that predates the charter.yaml bundle format — run `spec-kitty charter generate --no-from-interview` to migrate it forward"
)

// preflightConfig is load_preflight_config: preflight.enabled (default
// true; null = true) and preflight.auto_refresh (default false).
func preflightConfig(root string) (enabled, autoRefresh bool) {
	enabled = true
	cfg := mission.ProjectConfig(root)
	if cfg == nil {
		return
	}
	sec := cfg.GetObject("preflight")
	if sec == nil {
		return
	}
	if v, ok := sec.Get("enabled"); ok && v != nil {
		enabled = pyTruthy(v)
	}
	if v, ok := sec.Get("auto_refresh"); ok {
		autoRefresh = pyTruthy(v)
	}
	return
}

func pyTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int:
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

// PreflightCheck is CharterPreflightCheck.
type PreflightCheck struct{ Name, State, Detail, Remediation string }

// PreflightResult is CharterPreflightResult.
type PreflightResult struct {
	Passed        bool
	Checks        []PreflightCheck
	BlockedReason string
	Warnings      []string
}

var passStates = map[string]bool{"fresh": true, "skipped": true, "built_in_only": true}

var layerOrder = [][2]string{{"charter_source", "charter source"}, {"synced_bundle", "synced bundle"}, {"synthesized_drg", "synthesized DRG"}}

func buildChecks(fr *jsonx.Object) []PreflightCheck {
	var out []PreflightCheck
	for _, l := range layerOrder {
		sub := fr.GetObject(l[0])
		state := "invalid"
		if sub != nil && sub.GetString("state") != "" {
			state = sub.GetString("state")
		}
		detail := ""
		rem := ""
		if sub != nil {
			detail, rem = sub.GetString("detail"), sub.GetString("remediation")
		}
		if detail == "" {
			detail = l[1] + " is " + state
			if sub != nil && sub.GetString("last_change") != "" {
				detail += " (last_change=" + sub.GetString("last_change") + ")"
			}
		}
		out = append(out, PreflightCheck{Name: l[0], State: state, Detail: detail, Remediation: rem})
	}
	return out
}

func blockedReason(checks []PreflightCheck) string {
	var lines []string
	for _, c := range checks {
		if passStates[c.State] {
			continue
		}
		if c.Remediation != "" {
			lines = append(lines, fmt.Sprintf("%s %s; run `%s`", c.Name, c.State, c.Remediation))
		} else {
			lines = append(lines, fmt.Sprintf("%s %s: %s", c.Name, c.State, c.Detail))
		}
	}
	if len(lines) == 0 {
		return "charter preflight failed; no non-passing check found (internal inconsistency)"
	}
	return strings.Join(lines, "\n")
}

func allPass(checks []PreflightCheck) bool {
	for _, c := range checks {
		if !passStates[c.State] {
			return false
		}
	}
	return true
}

// RunCharterPreflight is run_charter_preflight(allow_missing_charter=True):
// the three-layer charter freshness checks (auto-refresh when configured)
// plus the deactivated mission-default-profile advisories. nil means the
// preflight is disabled by project config.
func RunCharterPreflight(root string) *PreflightResult {
	enabled, autoRefresh := preflightConfig(root)
	if !enabled {
		return nil
	}
	r := preflightFreshness(root, autoRefresh)
	r.Warnings = append(r.Warnings, deactivatedDefaultProfileWarnings(root)...)
	return r
}

func preflightFreshness(root string, autoRefresh bool) *PreflightResult {
	fr := charter.ComputeFreshness(root)
	checks := buildChecks(fr)
	st := map[string]string{}
	for _, c := range checks {
		st[c.Name] = c.State
	}
	if st["charter_source"] == "missing" && st["synced_bundle"] == "missing" && (st["synthesized_drg"] == "missing" || st["synthesized_drg"] == "built_in_only") {
		detail, warn := "project charter is not initialized", freshProjectMissingCharter
		if _, err := os.Stat(filepath.Join(root, ".kittify", "charter", "charter.md")); err == nil {
			detail, warn = "legacy charter.md-only bundle; charter.yaml not yet migrated", legacyCharterBundle
		}
		var sk []PreflightCheck
		for _, c := range checks {
			sk = append(sk, PreflightCheck{Name: c.Name, State: "skipped", Detail: detail})
		}
		return &PreflightResult{Passed: true, Checks: sk, Warnings: []string{warn}}
	}
	if allPass(checks) {
		return &PreflightResult{Passed: true, Checks: checks}
	}
	if autoRefresh {
		return attemptAutoRefresh(root, fr, checks)
	}
	return &PreflightResult{Checks: checks, BlockedReason: blockedReason(checks)}
}

var actionProfileDefaults = [][3]string{
	{"documentation", "accept", "reviewer-renata"}, {"documentation", "audit", "researcher-robbie"},
	{"documentation", "design", "architect-alphonso"}, {"documentation", "discover", "researcher-robbie"},
	{"documentation", "generate", "implementer-ivan"}, {"documentation", "publish", "reviewer-renata"},
	{"documentation", "validate", "reviewer-renata"}, {"plan", "plan", "architect-alphonso"},
	{"plan", "research", "researcher-robbie"}, {"plan", "review", "reviewer-renata"},
	{"plan", "specify", "researcher-robbie"}, {"research", "gathering", "researcher-robbie"},
	{"research", "methodology", "researcher-robbie"}, {"research", "output", "reviewer-renata"},
	{"research", "scoping", "researcher-robbie"}, {"research", "synthesis", "researcher-robbie"},
	{"software-dev", "implement", "implementer-ivan"}, {"software-dev", "plan", "architect-alphonso"},
	{"software-dev", "review", "reviewer-renata"}, {"software-dev", "specify", "researcher-robbie"},
	{"software-dev", "tasks", "architect-alphonso"},
}

var defaultProfileRoles = map[string]string{"researcher-robbie": "researcher", "architect-alphonso": "architect", "implementer-ivan": "implementer", "reviewer-renata": "reviewer"}

// missionDefaultProfileWarning is mission_default_profile_warning.
func missionDefaultProfileWarning(profileID string) string {
	var bound []string
	for _, d := range actionProfileDefaults {
		if d[2] == profileID {
			bound = append(bound, d[0]+"/"+d[1])
		}
	}
	if len(bound) == 0 {
		return ""
	}
	note := "same-role"
	if r, ok := defaultProfileRoles[profileID]; ok {
		note = "'" + r + "'-role"
	}
	return fmt.Sprintf("agent profile '%s' is the built-in default profile for the mission steps: %s. With it deactivated, those steps dispatch through the next available %s profile, or block when no available profile carries that role — activate a profile with that role to keep them running.",
		profileID, strings.Join(bound, ", "), note)
}

// deactivatedDefaultProfileWarnings is
// _deactivated_mission_default_profile_warnings.
func deactivatedDefaultProfileWarnings(root string) []string {
	act, err := dct.ActivatedProfiles(root)
	if err != nil || act == nil {
		return nil
	}
	set := map[string]bool{}
	for _, d := range actionProfileDefaults {
		set[d[2]] = true
	}
	var ids []string
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []string
	for _, id := range ids {
		if act[id] {
			continue
		}
		if w := missionDefaultProfileWarning(id); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// --- auto refresh ------------------------------------------------------------

func refreshTimeout() time.Duration {
	if raw := os.Getenv("SPEC_KITTY_PREFLIGHT_TIMEOUT_SECS"); raw != "" {
		if v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil && v > 0 {
			return time.Duration(v * float64(time.Second))
		}
	}
	return 30 * time.Second
}

type stepRun struct {
	code           int
	stdout, stderr string
	err            error
	timedOut       bool
}

func runStep(root string, timeout time.Duration, cmd []string) stepRun {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Dir = root
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	r := stepRun{stdout: out.String(), stderr: errb.String()}
	if ctx.Err() == context.DeadlineExceeded {
		r.timedOut = true
		return r
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.code = ee.ExitCode()
	} else if err != nil {
		r.err = err
	}
	return r
}

// runRefreshStep is _run_refresh_step.
func runRefreshStep(root string, cmd []string, timeout time.Duration) (bool, string) {
	label := cmd[0]
	if len(cmd) > 1 {
		label = strings.Join(cmd[1:], " ")
	}
	r := runStep(root, timeout, cmd)
	if r.err != nil {
		return false, fmt.Sprintf("%s not on PATH; cannot run `%s`", cmd[0], strings.Join(cmd, " "))
	}
	if r.timedOut {
		return false, fmt.Sprintf("`%s` timed out after %.0fs", strings.Join(cmd, " "), timeout.Seconds())
	}
	if r.code == 0 {
		return true, ""
	}
	first := ""
	if ls := strings.Split(strings.TrimRight(r.stderr, "\n"), "\n"); r.stderr != "" && len(ls) > 0 {
		first = ls[0]
	}
	if first == "" && r.stdout != "" {
		ls := strings.Split(strings.TrimRight(r.stdout, "\n"), "\n")
		first = ls[len(ls)-1]
	}
	return false, strings.TrimRight(fmt.Sprintf("%s failed (exit %d): %s", label, r.code, first), ": ")
}

// shlexSplit is a posix shlex.split for the remediation commands.
func shlexSplit(s string) []string {
	var out []string
	var cur strings.Builder
	in := false
	quote := rune(0)
	for _, c := range s {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, in = c, true
		case c == ' ' || c == '\t' || c == '\n':
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(c)
			in = true
		}
	}
	if in {
		out = append(out, cur.String())
	}
	return out
}

func dirtyCharterArtifacts(root string) ([]string, string) {
	res, err := gitStatusPaths(root, ".kittify/charter/", dct.ProjectPackRoot+"/")
	if err != nil {
		return nil, err.Error()
	}
	return res, ""
}

func gitStatusPaths(root string, specs ...string) ([]string, error) {
	cmd := exec.Command("git", append([]string{"status", "--porcelain=v1", "-z", "--"}, specs...)...)
	cmd.Dir = root
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			first := strings.SplitN(strings.TrimSpace(errb.String()), "\n", 2)[0]
			return nil, errors.New(strings.TrimRight(fmt.Sprintf("git status failed (exit %d): %s", ee.ExitCode(), first), ": "))
		}
		return nil, errors.New("git CLI not available; cannot determine worktree cleanliness")
	}
	var paths []string
	parts := strings.Split(out.String(), "\x00")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if len(p) < 4 {
			continue
		}
		paths = append(paths, p[3:])
		if p[0] == 'R' || p[0] == 'C' {
			i++
		}
	}
	return paths, nil
}

var richErrorLine = regexp.MustCompile(`^(?:Unexpected error|Error):`)

// referencesRefresh is refresh_references_if_needed: a synthesized_drg
// staleness re-runs `charter generate` so catalog references stay in parity.
func referencesRefresh(root, cause string) (attempted, ok bool, detail string) {
	hit := false
	for _, n := range strings.Split(cause, ",") {
		if n == "synthesized_drg" {
			hit = true
		}
	}
	if !hit {
		return false, false, ""
	}
	cmd := []string{"spec-kitty", "charter", "generate", "--no-from-interview", "--json"}
	if doc, err := os.ReadFile(filepath.Join(root, ".kittify", "charter", "charter.yaml")); err == nil {
		if o, err := yamlx.ParseObject(doc); err == nil && o != nil {
			if cat := o.GetObject("catalog"); cat != nil {
				if m, _ := cat.Get("mission"); m != nil {
					if s, ok := m.(string); ok && s != "" {
						cmd = append(cmd, "--mission-type", s)
					}
				}
				if t, _ := cat.Get("template_set"); t != nil {
					if s, ok := t.(string); ok && s != "" {
						cmd = append(cmd, "--template-set", s)
					}
				}
			}
		}
	}
	r := runStep(root, 30*time.Second, cmd)
	if r.err != nil || r.timedOut {
		why := "timed out"
		if r.err != nil {
			why = r.err.Error()
		}
		return true, false, fmt.Sprintf("`%s` invocation failed: %s", strings.Join(cmd, " "), why)
	}
	if r.code == 0 {
		return true, true, ""
	}
	return true, false, extractFailureDetail(r)
}

func extractFailureDetail(r stepRun) string {
	cands := append([]string{r.stdout}, reverse(strings.Split(r.stdout, "\n"))...)
	for _, c := range cands {
		var payload map[string]any
		if json.Unmarshal([]byte(c), &payload) == nil {
			if e, ok := payload["error"].(string); ok && strings.TrimSpace(e) != "" {
				return strings.TrimSpace(e)
			}
		}
	}
	for _, s := range []string{r.stdout, r.stderr} {
		var lines []string
		for _, l := range strings.Split(s, "\n") {
			if t := strings.TrimSpace(l); t != "" {
				lines = append(lines, t)
			}
		}
		last := -1
		for i, l := range lines {
			if richErrorLine.MatchString(l) {
				last = i
			}
		}
		if last >= 0 {
			return strings.Join(lines[last:], " ")
		}
	}
	for _, s := range []string{r.stderr, r.stdout} {
		var lines []string
		for _, l := range strings.Split(s, "\n") {
			if strings.TrimSpace(l) != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) > 0 {
			return lines[len(lines)-1]
		}
	}
	return fmt.Sprintf("generate exited %d with no captured output", r.code)
}

func reverse(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[len(xs)-1-i] = x
	}
	return out
}

// attemptAutoRefresh is _attempt_auto_refresh.
func attemptAutoRefresh(root string, fr *jsonx.Object, checks []PreflightCheck) *PreflightResult {
	dirty, derr := dirtyCharterArtifacts(root)
	if derr != "" {
		return &PreflightResult{Checks: checks, BlockedReason: derr}
	}
	if len(dirty) > 0 {
		var cd, pd []string
		for _, p := range dirty {
			if strings.HasPrefix(p, ".kittify/charter/") {
				cd = append(cd, p)
			} else if strings.HasPrefix(p, dct.ProjectPackRoot+"/") {
				pd = append(pd, p)
			}
		}
		var ann []PreflightCheck
		for _, c := range checks {
			if (c.Name == "charter_source" || c.Name == "synced_bundle") && len(cd) > 0 {
				c.Detail += "; uncommitted: " + strings.Join(cd, ", ")
			} else if c.Name == "synthesized_drg" && len(pd) > 0 {
				c.Detail += "; uncommitted: " + strings.Join(pd, ", ")
			}
			ann = append(ann, c)
		}
		return &PreflightResult{Checks: ann, BlockedReason: "uncommitted generated artifacts; commit or stash and retry"}
	}
	timeout := refreshTimeout()
	state := func(k string) *jsonx.Object { return fr.GetObject(k) }
	if !(state("charter_source").GetString("state") == "fresh" && state("synced_bundle").GetString("state") == "fresh") {
		rem := state("charter_source").GetString("remediation")
		if rem == "" {
			rem = state("synced_bundle").GetString("remediation")
		}
		if rem == "" {
			return &PreflightResult{Checks: checks, BlockedReason: blockedReason(checks)}
		}
		if ok, why := runRefreshStep(root, shlexSplit(rem), timeout); !ok {
			return &PreflightResult{Checks: checks, BlockedReason: why}
		}
	}
	if state("synthesized_drg").GetString("state") != "fresh" {
		if ok, why := runRefreshStep(root, []string{"spec-kitty", "charter", "synthesize"}, timeout); !ok {
			return &PreflightResult{Checks: checks, BlockedReason: why}
		}
	}
	if ok, why := runRefreshStep(root, []string{"spec-kitty", "charter", "bundle", "validate"}, timeout); !ok {
		return &PreflightResult{Checks: checks, BlockedReason: why}
	}
	causes := map[string]bool{}
	for _, c := range checks {
		if !passStates[c.State] {
			causes[c.Name] = true
		}
	}
	var cl []string
	for c := range causes {
		cl = append(cl, c)
	}
	sort.Strings(cl)
	attempted, ok, detail := referencesRefresh(root, strings.Join(cl, ","))
	if attempted && !ok {
		return &PreflightResult{Checks: checks, BlockedReason: "references-parity refresh failed: " + detail}
	}
	if attempted && ok {
		if ok, why := runRefreshStep(root, []string{"spec-kitty", "charter", "synthesize"}, timeout); !ok {
			return &PreflightResult{Checks: checks, BlockedReason: why}
		}
	}
	post := buildChecks(charter.ComputeFreshness(root))
	if allPass(post) {
		return &PreflightResult{Passed: true, Checks: post}
	}
	return &PreflightResult{Checks: post, BlockedReason: blockedReason(post)}
}

// --- surfaces -----------------------------------------------------------------

var (
	surfacedMu sync.Mutex
	surfaced   = map[string]bool{}
)

// emitAdvisoryWarnings is emit_advisory_warnings: each distinct ambient
// warning once per process, scoped to the consumer and repo root.
func emitAdvisoryWarnings(errw io.Writer, r *PreflightResult, root, consumer string) {
	if r == nil {
		return
	}
	surfacedMu.Lock()
	defer surfacedMu.Unlock()
	seen := map[string]bool{}
	for _, w := range r.Warnings {
		if seen[w] || surfaced[w] {
			continue
		}
		seen[w], surfaced[w] = true, true
		fmt.Fprintf(errw, "Warning: %s (scope: charter preflight, consumer '%s', repo root '%s'; advisory only — the command continues; shown once per command run)\n", w, consumer, root)
	}
}

// CharterPreflightOrAbort is run_preflight_or_abort: advisory warnings on a
// pass; "Error: <blocked reason>" on errw and Exit 1 on a failure.
func CharterPreflightOrAbort(errw io.Writer, root, consumer string) error {
	r := RunCharterPreflight(root)
	if r == nil {
		return nil
	}
	if r.Passed {
		emitAdvisoryWarnings(errw, r, root, consumer)
		return nil
	}
	reason := r.BlockedReason
	if reason == "" {
		reason = "charter preflight failed"
	}
	fmt.Fprintf(errw, "Error: %s\n", reason)
	return &Exit{Code: 1}
}

// CharterPreflightWarnOnly is run_preflight_warn_only + the advisory
// emission: a failed preflight is logged (human mode) and the command goes on.
func CharterPreflightWarnOnly(errw io.Writer, root, consumer string, logFailure func(string)) {
	r := RunCharterPreflight(root)
	if r == nil {
		return
	}
	if !r.Passed && logFailure != nil {
		logFailure(fmt.Sprintf("charter preflight failed (consumer=%s): %s", consumer, r.BlockedReason))
	}
	emitAdvisoryWarnings(errw, r, root, consumer)
}

// EmitCharterPreflight is the warn-only charter preflight of a consumer
// (failures are not logged).
func EmitCharterPreflight(errw io.Writer, root, consumer string) {
	CharterPreflightWarnOnly(errw, root, consumer, nil)
}

// CharterPreflightWarning returns the first advisory warning text the
// charter preflight surfaces for root ("" when none).
func CharterPreflightWarning(root string) string {
	r := RunCharterPreflight(root)
	if r == nil || len(r.Warnings) == 0 {
		return ""
	}
	return r.Warnings[0]
}
