package prereview

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// Budget classifications.
const (
	BudgetBounded   = "bounded"
	BudgetOversized = "oversized"
	BudgetUnknown   = "unknown"
)

const (
	policyNamespace = "spec-kitty.pre-review-budget/v1"
	identityPrefix  = "budget-v1:sha256:"
	unknownGuidance = "No reviewed budget metadata matches this scope; run it under the configured timeout and treat any timeout as a retrospective classification candidate."
)

// Assessment is ScopeBudgetAssessment.
type Assessment struct {
	Classification    string
	NormalizedTargets []string
	IdentityValue     string
	EffectiveBudget   float64
	MatchedRule       string // "" = None
	Evidence          string
	Guidance          string
}

type budgetRule struct {
	id, class, evidence, guidance string
	atoms                         []string
}

var budgetRules = []budgetRule{
	{id: "spec-kitty-architectural-full-directory", atoms: []string{"tests/architectural"}, class: BudgetOversized,
		evidence: "issue #2573 dogfood, approximately 26 minutes per leg",
		guidance: "Select a bounded test scope, or explicitly skip the pre-review gate when the operator accepts that tradeoff."},
	{id: "spec-kitty-gate-budget-pinned-vector", atoms: []string{"tests/review/test_gate_budget.py::test_identity_matches_pinned_vector"}, class: BudgetBounded,
		evidence: "reviewed isolated contract node with no network, repository mutation, or subprocess fan-out",
		guidance: "Run this stable isolated contract node under the configured timeout."},
}

func normalizeTarget(t string) string {
	n := strings.ReplaceAll(t, `\`, "/")
	for strings.HasPrefix(n, "./") {
		n = n[2:]
	}
	return strings.TrimRight(n, "/")
}

// AssessBudget is assess_scope_budget.
func AssessBudget(targets []string, budget float64) *Assessment {
	set := map[string]bool{}
	for _, t := range targets {
		set[normalizeTarget(t)] = true
	}
	norm := make([]string, 0, len(set))
	for t := range set {
		norm = append(norm, t)
	}
	sort.Strings(norm)
	ts := make([]any, len(norm))
	for i, t := range norm {
		ts[i] = t
	}
	canonical := jsonx.Dumps(jsonx.NewObject("namespace", policyNamespace, "targets", ts),
		jsonx.Options{Indent: -1, SortKeys: true, EnsureASCII: true, Compact: true})
	sum := sha256.Sum256([]byte(canonical))
	a := &Assessment{NormalizedTargets: norm, IdentityValue: identityPrefix + hex.EncodeToString(sum[:]), EffectiveBudget: budget}
	for _, r := range budgetRules {
		match := false
		if r.class == BudgetOversized {
			match = true
			for _, at := range r.atoms {
				if !set[at] {
					match = false
				}
			}
		} else {
			match = len(set) == len(r.atoms)
			for _, at := range r.atoms {
				if !set[at] {
					match = false
				}
			}
		}
		if match {
			a.Classification, a.MatchedRule, a.Evidence, a.Guidance = r.class, r.id, r.evidence, r.guidance
			return a
		}
	}
	a.Classification, a.Guidance = BudgetUnknown, unknownGuidance
	return a
}
