package mission

// PlanGuardFailures is runtime_bridge_cores._evaluate_plan_guards: the
// artifact guard of one built-in plan-family action. present reports whether
// a planning artifact is a regular file. specify/research/plan each require
// their artifact; review is an approval gate with no filesystem artifact; any
// other action fails closed.
func PlanGuardFailures(action string, present func(name string) bool) []string {
	need := func(name string) []string {
		if present(name) {
			return nil
		}
		return []string{"Required artifact missing: " + name}
	}
	switch action {
	case "specify":
		return need("spec.md")
	case "research":
		return need("research.md")
	case "plan":
		return need("plan.md")
	case "review":
		return nil
	}
	return []string{"No guard registered for plan action: " + action}
}
