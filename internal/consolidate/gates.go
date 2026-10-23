package consolidate

// Evidence-based merge gates (specify_cli.policy.merge_gates) and the
// parallelization risk scorer (policy.risk_scorer), configured from the
// `policy` section of .kittify/config.yaml.

import (
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/issuematrix"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// GatePolicy is the merge_gates + risk policy.
type GatePolicy struct {
	Enabled                                 bool
	Mode                                    string
	RequireReview, RequireRisk, RequireDeps bool
	RiskThreshold                           float64
}

func policyMode(raw any, present bool) string {
	if !present || raw == nil {
		return "warn"
	}
	c := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw)))
	switch c {
	case "warn", "block", "off":
		return c
	}
	return "block"
}

func pyBool(o *jsonx.Object, k string) bool {
	v, ok := o.Get(k)
	if !ok {
		return true
	}
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	}
	return true
}

// LoadGatePolicy is policy.config.load_policy_config (merge gates + risk).
func LoadGatePolicy(root string) GatePolicy {
	p := GatePolicy{Enabled: true, Mode: "warn", RequireReview: true, RequireRisk: true, RequireDeps: true, RiskThreshold: 0.6}
	b, err := os.ReadFile(filepath.Join(root, ".kittify", "config.yaml"))
	if err != nil {
		return p
	}
	doc, err := yamlx.ParseObject(b)
	if err != nil || doc == nil {
		return p
	}
	pol := doc.GetObject("policy")
	if pol == nil {
		return p
	}
	if mg := pol.GetObject("merge_gates"); mg != nil {
		p.Enabled = pyBool(mg, "enabled")
		raw, ok := mg.Get("mode")
		p.Mode = policyMode(raw, ok)
		p.RequireReview, p.RequireRisk, p.RequireDeps = pyBool(mg, "require_review_approval"), pyBool(mg, "require_risk_check"), pyBool(mg, "require_deps_complete")
	}
	if rk := pol.GetObject("risk"); rk != nil {
		if v, ok := rk.Get("threshold"); ok {
			switch t := v.(type) {
			case float64:
				p.RiskThreshold = t
			case int:
				p.RiskThreshold = float64(t)
			case int64:
				p.RiskThreshold = float64(t)
			case string:
				if f, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
					p.RiskThreshold = f
				}
			default:
				if f, err := strconv.ParseFloat(fmt.Sprint(t), 64); err == nil {
					p.RiskThreshold = f
				}
			}
		}
	}
	return p
}

// GateResult is one gate outcome.
type GateResult struct {
	Name, Verdict, Details string
	Blocking               bool
}

func acceptableLane(st *jsonx.Object) (string, bool) {
	if st == nil {
		return "unknown", false
	}
	l := st.GetString("lane")
	return l, l == "approved" || l == "done" || (l == "canceled" && st.GetString("reason_source") == "operator")
}

// ---------------------------------------------------------------------------
// risk scorer

var reTestRef = regexp.MustCompile(`(?i)tests?/[\w/]*test_\w+`)

func cleanScope(p string) string {
	p = strings.ReplaceAll(p, "/**", "")
	p = strings.ReplaceAll(p, "/*", "")
	p = strings.ReplaceAll(p, "**", "")
	return strings.TrimRight(p, "/")
}

func parentDirs(scope []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range scope {
		c := cleanScope(s)
		if c == "" {
			continue
		}
		c = path.Clean(c)
		for d := path.Dir(c); d != "." && d != "/"; d = path.Dir(d) {
			if len(strings.Split(strings.Trim(d, "/"), "/")) >= 2 {
				out[d] = true
			}
		}
	}
	return out
}

func testRefs(l *lanes.Lane) map[string]bool {
	out := map[string]bool{}
	for _, s := range l.WriteScope {
		if strings.Contains(strings.ToLower(s), "test") {
			c := strings.TrimRight(strings.ReplaceAll(strings.ReplaceAll(s, "/**", ""), "/*", ""), "/")
			if c != "" {
				out[c] = true
			}
		}
	}
	return out
}

func intersect(a, b map[string]bool) int {
	n := 0
	for k := range a {
		if b[k] {
			n++
		}
	}
	return n
}

// RiskScore is compute_risk_report(manifest).overall_score (no WP bodies).
func RiskScore(m *lanes.Manifest) float64 {
	groups := map[int][]*lanes.Lane{}
	var order []int
	for _, l := range m.Lanes {
		if _, ok := groups[l.ParallelGroup]; !ok {
			order = append(order, l.ParallelGroup)
		}
		ll := l
		groups[l.ParallelGroup] = append(groups[l.ParallelGroup], &ll)
	}
	overall := 0.0
	for _, g := range order {
		ls := groups[g]
		for i := 0; i < len(ls); i++ {
			for j := i + 1; j < len(ls); j++ {
				shared := intersect(parentDirs(ls[i].WriteScope), parentDirs(ls[j].WriteScope))
				tests := intersect(testRefs(ls[i]), testRefs(ls[j]))
				raw := float64(minInt(shared, 5))*0.12 + float64(minInt(tests, 3))*0.08
				score := math.Round(math.Min(1, raw)*1000) / 1000
				if score > overall {
					overall = score
				}
			}
		}
	}
	return overall
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// pyFmt2 is Python's "{:.2f}" (round-half-even on the binary value).
func pyFmt2(f float64) string { return strconv.FormatFloat(f, 'f', 2, 64) }

// ---------------------------------------------------------------------------

// EvaluateGates is evaluate_merge_gates.
func EvaluateGates(root, primaryDir, statusDir, matrixDir string, wpIDs []string, pol GatePolicy) []GateResult {
	if !pol.Enabled || pol.Mode == "off" {
		return nil
	}
	blocking := pol.Mode == "block"
	var out []GateResult
	snap, snapErr := status.ReduceDir(statusDir)
	if pol.RequireReview {
		if snapErr != nil {
			out = append(out, GateResult{"evidence", "fail", "Could not read event log: " + snapErr.Error(), blocking})
		} else {
			var missing []string
			for _, id := range wpIDs {
				if _, ok := acceptableLane(snap.WP(id)); !ok {
					missing = append(missing, id)
				}
			}
			if len(missing) > 0 {
				out = append(out, GateResult{"evidence", "fail", fmt.Sprintf("WPs missing review approval: %s (informational — the terminal-lane invariant is enforced unconditionally before any mutation, regardless of gate mode; see merge.executor._assert_mission_terminal_ready)", strings.Join(missing, ", ")), false})
			} else {
				out = append(out, GateResult{"evidence", "pass", fmt.Sprintf("All %d WPs have review approval", len(wpIDs)), false})
			}
		}
	}
	if pol.RequireRisk {
		m, err := lanes.Read(primaryDir)
		switch {
		case err != nil:
			out = append(out, GateResult{"risk", "skip", "Risk assessment unavailable: " + err.Error(), false})
		case m == nil:
			out = append(out, GateResult{"risk", "skip", "No lanes.json — risk gate skipped", false})
		default:
			score := RiskScore(m)
			if score > pol.RiskThreshold {
				out = append(out, GateResult{"risk", "fail", fmt.Sprintf("Risk score %s exceeds threshold %s", pyFmt2(score), pyFmt2(pol.RiskThreshold)), blocking})
			} else {
				out = append(out, GateResult{"risk", "pass", fmt.Sprintf("Risk score %s within threshold", pyFmt2(score)), false})
			}
		}
	}
	if pol.RequireDeps {
		graph, graphErr := dependencyGraph(primaryDir)
		if graphErr == nil {
			graphErr = snapErr
		}
		if graphErr != nil {
			out = append(out, GateResult{"dependency", "skip", "Dependency check unavailable: " + graphErr.Error(), false})
		} else {
			var incomplete []string
			for _, id := range wpIDs {
				for _, d := range graph[id] {
					if lane, ok := acceptableLane(snap.WP(d)); !ok {
						incomplete = append(incomplete, fmt.Sprintf("%s (lane=%s)", d, lane))
					}
				}
			}
			if len(incomplete) > 0 {
				out = append(out, GateResult{"dependency", "fail", "Incomplete dependencies: " + strings.Join(incomplete, ", "), blocking})
			} else {
				out = append(out, GateResult{"dependency", "pass", "All dependencies complete", false})
			}
		}
	}
	gating := map[string]bool{}
	refs, refErr := issuematrix.DiscoverReferences(primaryDir)
	for _, r := range refs {
		if r.Gating() {
			gating[fmt.Sprintf("#%d", r.Number)] = true
		}
	}
	nothing := "No gating issue references discovered — nothing to enforce"
	if refErr != nil {
		out = append(out, GateResult{"issue_matrix_completeness", "fail", "Could not evaluate issue-matrix completeness: " + refErr.Error(), blocking},
			GateResult{"issue_matrix_verdict_terminality", "fail", "Could not evaluate issue-matrix verdict terminality: " + refErr.Error(), blocking})
		return out
	}
	if len(gating) == 0 {
		return append(out, GateResult{"issue_matrix_completeness", "pass", nothing, false},
			GateResult{"issue_matrix_verdict_terminality", "pass", nothing, false})
	}
	have := map[string]bool{}
	if rows := issuematrix.LoadRows(matrixDir); rows != nil {
		for _, k := range rows.Order {
			have[k] = true
		}
	}
	var missing []string
	for k := range gating {
		if !have[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		out = append(out, GateResult{"issue_matrix_completeness", "fail", "Issue-matrix is missing rows for referenced issue(s): " + strings.Join(missing, ", "), blocking})
	} else {
		out = append(out, GateResult{"issue_matrix_completeness", "pass", fmt.Sprintf("All %d referenced issue(s) have matrix rows", len(gating)), false})
	}
	if msg := issuematrix.ApprovalBlocker(matrixDir, "done", primaryDir); msg != "" {
		out = append(out, GateResult{"issue_matrix_verdict_terminality", "fail", msg, blocking})
	} else {
		out = append(out, GateResult{"issue_matrix_verdict_terminality", "pass", "No unresolved (in-mission/unknown) issue-matrix rows", false})
	}
	return out
}
