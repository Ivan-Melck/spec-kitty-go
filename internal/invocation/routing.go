package invocation

import (
	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// CatalogAsset is the packaged model-to-task_type routing catalog.
const CatalogAsset = "charter/offering/model_task_routing/catalog/model-to-task_type.yaml"

// verbTaskType is task_class_map._VERB_TO_TASK_TYPE.
var verbTaskType = map[string]string{
	"generate": "code-generation", "refine": "code-refinement", "implement": "code-implementation",
	"audit": "quality-audit", "assess": "quality-assessment", "review": "code-review",
	"synthesize": "synthesis", "draft": "drafting", "design": "design",
	"plan": "planning", "decompose": "task-decomposition", "prioritize": "prioritization",
	"analyze": "analysis", "investigate": "investigation", "summarize": "summarization",
	"classify": "classification", "curate": "curation", "validate": "validation",
	"coordinate": "coordination", "delegate": "delegation", "monitor": "monitoring",
}

// TaskTypeForVerb maps a dispatch verb to a catalog task_type ("" = none).
func TaskTypeForVerb(verb string) string { return verbTaskType[verb] }

// Candidate is RoutingCandidate.
type Candidate struct {
	ModelID   string
	Source    string // catalog | profile
	Score     *float64
	Rationale *string
	Effort    *string
}

// Recommendation is RoutingRecommendation.
type Recommendation struct {
	TaskType, Objective, OverrideMode string
	Candidates                        []Candidate
}

// CatalogCandidate is the catalog pick (nil when none).
func (r *Recommendation) CatalogCandidate() *Candidate {
	for i := range r.Candidates {
		if r.Candidates[i].Source == "catalog" {
			return &r.Candidates[i]
		}
	}
	return nil
}

// ProfileCandidate is the profile-declared model (nil when none).
func (r *Recommendation) ProfileCandidate() *Candidate {
	for i := range r.Candidates {
		if r.Candidates[i].Source == "profile" {
			return &r.Candidates[i]
		}
	}
	return nil
}

func optStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// Object is dataclasses.asdict(recommendation).
func (r *Recommendation) Object() *jsonx.Object {
	cs := []any{}
	for _, c := range r.Candidates {
		var score any
		if c.Score != nil {
			score = jsonx.Float(*c.Score)
		}
		cs = append(cs, jsonx.NewObject("model_id", c.ModelID, "source", c.Source, "score", score,
			"rationale", optStr(c.Rationale), "effort", optStr(c.Effort)))
	}
	return jsonx.NewObject("task_type", r.TaskType, "objective", r.Objective, "override_mode", r.OverrideMode,
		"candidates", cs, "model_tier", nil)
}

var (
	costDesirability    = map[string]float64{"low": 1.0, "medium": 0.66, "high": 0.33, "premium": 0.0}
	costOrder           = map[string]int{"low": 0, "medium": 1, "high": 2, "premium": 3}
	riskDesirability    = map[string]float64{"high": 1.0, "medium": 0.66, "low": 0.33}
	latencyDesirability = map[string]float64{"low": 1.0, "medium": 0.66, "high": 0.33}
)

const neutral = 0.5

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func loadCatalog() *jsonx.Object {
	b, err := assets.ReadFile(CatalogAsset)
	if err != nil {
		return nil
	}
	o, err := yamlx.ParseObject(b)
	if err != nil {
		return nil
	}
	return o
}

func catalogStale(cat *jsonx.Object) bool {
	pol := cat.GetObject("routing_policy")
	fp := pol.GetObject("freshness_policy")
	if fp == nil {
		return false
	}
	maxAge, ok := fp.Get("max_catalog_age_hours")
	if !ok || maxAge == nil {
		return false
	}
	gen, err := clock.ParseISO(cat.GetString("generated_at"))
	if err != nil {
		return false
	}
	return clock.Now().Sub(gen).Hours() > num(maxAge)
}

// Recommend is executor._compute_recommendation (nil when no advice).
func Recommend(p *dct.Profile, action string) *Recommendation {
	tt := TaskTypeForVerb(action)
	if tt == "" {
		return nil
	}
	cat := loadCatalog()
	if cat == nil || catalogStale(cat) {
		return nil
	}
	pol := cat.GetObject("routing_policy")
	if pol == nil {
		return nil
	}
	w := pol.GetObject("weights")
	objective := pol.GetString("objective")
	maxTier := ""
	for _, x := range pol.GetList("tier_constraints") {
		if tc, ok := x.(*jsonx.Object); ok && tc.GetString("task_type") == tt {
			maxTier = tc.GetString("max_tier")
			break
		}
	}
	var best *Candidate
	var bestKey [2]float64
	for _, x := range cat.GetList("models") {
		m, ok := x.(*jsonx.Object)
		if !ok {
			continue
		}
		cost := ""
		if c := m.GetObject("cost"); c != nil {
			cost = c.GetString("tier")
		}
		if maxTier != "" && costOrder[cost] > costOrder[maxTier] {
			continue
		}
		var fit *jsonx.Object
		for _, f := range m.GetList("task_fit") {
			if fo, ok := f.(*jsonx.Object); ok && fo.GetString("task_type") == tt {
				fit = fo
				break
			}
		}
		if fit == nil {
			continue
		}
		q, _ := fit.Get("score")
		quality := num(q)
		costD, ok := costDesirability[cost]
		if !ok {
			costD = neutral
		}
		risk := neutral
		if c := fit.GetString("confidence"); c != "" {
			if v, ok := riskDesirability[c]; ok {
				risk = v
			}
		}
		lat := neutral
		if l := m.GetString("latency_tier"); l != "" {
			if v, ok := latencyDesirability[l]; ok {
				lat = v
			}
		}
		weighted := wv(w, "quality")*quality + wv(w, "cost")*costD + wv(w, "risk")*risk + wv(w, "latency")*lat
		key := [2]float64{weighted, weighted}
		if objective == "quality_first" {
			key = [2]float64{quality, weighted}
		}
		if best == nil || key[0] > bestKey[0] || (key[0] == bestKey[0] && key[1] > bestKey[1]) {
			sc := weighted
			c := &Candidate{ModelID: m.GetString("id"), Source: "catalog", Score: &sc}
			if r, ok := fit.Get("rationale"); ok && r != nil {
				rs := fit.GetString("rationale")
				c.Rationale = &rs
			}
			best, bestKey = c, key
		}
	}
	rec := &Recommendation{TaskType: tt, Objective: objective}
	if op := pol.GetObject("override_policy"); op != nil {
		rec.OverrideMode = op.GetString("mode")
	}
	if best != nil {
		rec.Candidates = append(rec.Candidates, *best)
	}
	if pm := p.PreferredModel(); pm != "" {
		c := Candidate{ModelID: pm, Source: "profile"}
		if e := p.Effort(); e != "" {
			c.Effort = &e
		}
		rec.Candidates = append(rec.Candidates, c)
	}
	if len(rec.Candidates) == 0 {
		return nil
	}
	return rec
}

func wv(w *jsonx.Object, k string) float64 {
	if w == nil {
		return 0
	}
	v, _ := w.Get(k)
	return num(v)
}
