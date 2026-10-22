package prereview

import (
	"fmt"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
)

// PreReviewHandler is the one registered gate handler (GATE_REGISTRY).
const PreReviewHandler = "spec-kitty-pre-review"

// BindingResolution is GateBindingResolution.
type BindingResolution struct {
	Active   bool
	Reason   string
	Handlers []string // active handlers in resolver order
}

// owningAction is OWNING_ACTION_FOR_EDGE (every edge defaults to review).
func owningAction(edge string) string { return "review" }

// ResolveGateBindings is resolve_gate_bindings_for_transition: the review
// step contract of the mission type, its gates for edge, and whether the
// contract survives the DRG activation filter.
func ResolveGateBindings(root, missionType, edge string) (*BindingResolution, error) {
	action := owningAction(edge)
	urn := fmt.Sprintf("mission_step_contract:%s/%s", missionType, action)
	orgRoots := dct.OrgRoots(root)
	off := dct.NewOffering(root, orgRoots)
	var gates []any
	found := false
	for _, a := range off.Repo(dct.MissionStepContract).List() {
		if a.Data.GetString("mission") == missionType && a.Data.GetString("action") == action {
			gates, found = a.Data.GetList("gates"), true
			break
		}
	}
	if !found {
		return &BindingResolution{Reason: fmt.Sprintf("NO_COVERAGE: no gate contract for (%s, %s) governing edge %s", missionType, action, edge)}, nil
	}
	var handlers []string
	for _, g := range gates {
		if o, ok := g.(interface{ GetString(string) string }); ok && o.GetString("on_transition") == edge {
			handlers = append(handlers, o.GetString("handler"))
		}
	}
	if len(handlers) == 0 {
		return &BindingResolution{Reason: fmt.Sprintf("NO_COVERAGE: gate contract (%s, %s) declares no binding for edge %s", missionType, action, edge)}, nil
	}
	g, err := dct.LoadGraph(root, orgRoots)
	if err != nil {
		return nil, err
	}
	if act, err := dct.LoadActivation(root); err == nil {
		g = dct.FilterByActivation(g, act, off)
	}
	if !g.NodeURNs()[urn] {
		return &BindingResolution{Reason: fmt.Sprintf("gate binding present for edge %s but owning contract %s is not activated", edge, urn)}, nil
	}
	return &BindingResolution{Active: true, Handlers: handlers,
		Reason: fmt.Sprintf("%d active gate binding(s) for edge %s on %s", len(handlers), edge, urn)}, nil
}
