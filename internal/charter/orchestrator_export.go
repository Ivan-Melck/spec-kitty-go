package charter

// Exported seam used by orchestrator-api design-context (area I). Additive.

import "github.com/spec-kitty/go-kitty3/internal/jsonx"

// ActionGovernanceDisclosure is resolve_action_governance_bundle (depth 0,
// no refresh / mark-loaded effects) rendered by build_disclosure_payload with
// include_all=True: directives, tactics, styleguides, toolguides, procedures
// and references.
func ActionGovernanceDisclosure(root, action, missionType string) *jsonx.Object {
	b := ResolveActionBundle(root, action, missionType, 0)
	if b.Merged == nil {
		return jsonx.NewObject("directives", []any{}, "tactics", []any{}, "styleguides", []any{},
			"toolguides", []any{}, "procedures", []any{}, "references", []any{})
	}
	return b.disclosure(true)
}
