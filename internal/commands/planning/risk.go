package planning

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

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// riskPolicy is policy.config.RiskPolicyConfig.
type riskPolicy struct {
	mode      string
	threshold float64
}

// loadRiskPolicy is load_policy_config(repo_root).risk.
func loadRiskPolicy(root string) riskPolicy {
	p := riskPolicy{mode: "warn", threshold: 0.6}
	b, err := os.ReadFile(filepath.Join(root, ".kittify", "config.yaml"))
	if err != nil {
		return p
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return p
	}
	cfg, _ := v.(*jsonx.Object)
	if cfg == nil {
		return p
	}
	pol, _ := mustGetAny(cfg, "policy").(*jsonx.Object)
	if pol == nil {
		return p
	}
	rk, _ := mustGetAny(pol, "risk").(*jsonx.Object)
	if rk == nil {
		return p
	}
	if raw, ok := rk.Get("mode"); ok && raw != nil {
		c := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw)))
		switch c {
		case "warn", "block", "off":
			p.mode = c
		default:
			p.mode = "block"
		}
	}
	switch t := mustGetAny(rk, "threshold").(type) {
	case float64:
		p.threshold = t
	case int:
		p.threshold = float64(t)
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
			p.threshold = f
		}
	}
	return p
}

func mustGetAny(o *jsonx.Object, k string) any {
	v, _ := o.Get(k)
	return v
}

type lanePairRisk struct {
	laneA, laneB  string
	sharedParents []string
	coupling      []string
	score         float64
}

func riskCleanScope(p string) string {
	p = strings.ReplaceAll(p, "/**", "")
	p = strings.ReplaceAll(p, "/*", "")
	p = strings.ReplaceAll(p, "**", "")
	return strings.TrimRight(p, "/")
}

func riskParentDirs(scope []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range scope {
		c := riskCleanScope(s)
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

func riskModules(scope []string) []string {
	set := map[string]bool{}
	for _, s := range scope {
		c := riskCleanScope(s)
		if c == "" {
			continue
		}
		c = strings.TrimPrefix(c, "src/")
		c = strings.TrimSuffix(c, ".py")
		set[strings.ReplaceAll(c, "/", ".")] = true
	}
	return sortedSet(set)
}

var riskTestRe = regexp.MustCompile(`(?i)tests?/[\w/]*test_\w+`)

func riskTestRefs(l *lanes.Lane, bodies map[string]string) map[string]bool {
	out := map[string]bool{}
	for _, s := range l.WriteScope {
		if strings.Contains(strings.ToLower(s), "test") {
			if c := strings.TrimRight(strings.ReplaceAll(strings.ReplaceAll(s, "/**", ""), "/*", ""), "/"); c != "" {
				out[c] = true
			}
		}
	}
	for _, id := range l.WPIDs {
		for _, m := range riskTestRe.FindAllString(bodies[id], -1) {
			out[m] = true
		}
	}
	return out
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func intersectSorted(a, b map[string]bool) []string {
	set := map[string]bool{}
	for k := range a {
		if b[k] {
			set[k] = true
		}
	}
	return sortedSet(set)
}

func joinBodies(l *lanes.Lane, bodies map[string]string) string {
	parts := make([]string, len(l.WPIDs))
	for i, id := range l.WPIDs {
		parts[i] = bodies[id]
	}
	return strings.Join(parts, " ")
}

// computeRiskReport is policy.risk_scorer.compute_risk_report. Module sets
// are iterated sorted (upstream iterates a hash-randomized Python set).
func computeRiskReport(m *lanes.Manifest, bodies map[string]string) ([]lanePairRisk, float64) {
	groups := map[int][]*lanes.Lane{}
	var order []int
	for i := range m.Lanes {
		l := &m.Lanes[i]
		if _, ok := groups[l.ParallelGroup]; !ok {
			order = append(order, l.ParallelGroup)
		}
		groups[l.ParallelGroup] = append(groups[l.ParallelGroup], l)
	}
	var pairs []lanePairRisk
	overall := 0.0
	for _, g := range order {
		ls := groups[g]
		for i := 0; i < len(ls); i++ {
			for j := i + 1; j < len(ls); j++ {
				a, b := ls[i], ls[j]
				shared := intersectSorted(riskParentDirs(a.WriteScope), riskParentDirs(b.WriteScope))
				var coupling []string
				ba, bb := joinBodies(a, bodies), joinBodies(b, bodies)
				for _, mod := range riskModules(b.WriteScope) {
					if strings.Contains(ba, mod) {
						coupling = append(coupling, fmt.Sprintf("%s references %s (owned by %s)", a.LaneID, mod, b.LaneID))
					}
				}
				for _, mod := range riskModules(a.WriteScope) {
					if strings.Contains(bb, mod) {
						coupling = append(coupling, fmt.Sprintf("%s references %s (owned by %s)", b.LaneID, mod, a.LaneID))
					}
				}
				tests := intersectSorted(riskTestRefs(a, bodies), riskTestRefs(b, bodies))
				raw := float64(min(len(shared), 5))*0.12 + float64(min(len(coupling), 5))*0.10 + float64(min(len(tests), 3))*0.08
				score, _ := strconv.ParseFloat(strconv.FormatFloat(math.Min(1, raw), 'f', 3, 64), 64)
				pairs = append(pairs, lanePairRisk{a.LaneID, b.LaneID, shared, coupling, score})
				if score > overall {
					overall = score
				}
			}
		}
	}
	return pairs, overall
}

// reportParallelizationRisk is _report_parallelization_risk.
func (f *finalizeRun) reportParallelizationRisk(m *lanes.Manifest) error {
	pol := loadRiskPolicy(f.root)
	pairs, overall := computeRiskReport(m, f.laneBodies)
	f2 := func(x float64) string { return strconv.FormatFloat(x, 'f', 2, 64) }
	if overall > 0 && !f.json {
		say(f.ctx.Stdout, fmt.Sprintf("[yellow]⚠[/yellow] Parallelization risk: %s (threshold: %s)", f2(overall), f2(pol.threshold)))
		for _, pr := range pairs {
			if pr.score <= 0 {
				continue
			}
			say(f.ctx.Stdout, fmt.Sprintf("  %s ↔ %s: %s", pr.laneA, pr.laneB, f2(pr.score)))
			for i, d := range pr.sharedParents {
				if i < 3 {
					say(f.ctx.Stdout, "    shared dir: "+d)
				}
			}
			for i, c := range pr.coupling {
				if i < 3 {
					say(f.ctx.Stdout, "    coupling: "+c)
				}
			}
		}
	}
	if overall > pol.threshold && pol.mode == "block" {
		msg := fmt.Sprintf("Parallelization risk %s exceeds threshold %s. Adjust the risk policy to proceed.", f2(overall), f2(pol.threshold))
		if f.json {
			emitJSON(f.ctx.Stdout, jsonx.NewObject("error", msg, "risk_report", jsonx.NewObject("overall_score", overall, "threshold", pol.threshold)))
		} else {
			say(f.ctx.Stdout, "[red]Error:[/red] "+msg)
		}
		return cli.Exit(1)
	}
	return nil
}
