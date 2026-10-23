package charter

import (
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
)

const LegacyAppliesReason = "legacy applies edge replaced by suggests"
const DeadMissionTypeEdgeReason = "dead mission-type requires edge removed (#6443)"

var reachOnlyKinds = []string{"tactic", "styleguide"}

func synthesizedEdgeReason(kind, slug string) string {
	return "Synthesized " + kind + " " + dct.PyRepr(slug) + " is how this project applies the directive"
}
func synthesizedSourceEdge(e dct.Edge) bool {
	kind, slug, ok := strings.Cut(e.Target, ":")
	return ok && contains(reachOnlyKinds, kind) && e.Relation == dct.RelSuggests && strings.HasPrefix(e.Source, "directive:") && e.Reason != nil && *e.Reason == synthesizedEdgeReason(kind, slug)
}
func synthEdgeOwner(e dct.Edge, overlay map[string]bool) string {
	if synthesizedSourceEdge(e) && overlay[e.Target] {
		return e.Target
	}
	if overlay[e.Source] {
		return e.Source
	}
	return ""
}
func deadMissionTypeEdge(e dct.Edge) bool {
	return e.Relation == dct.RelRequires && strings.HasPrefix(e.Source, "directive:PROJECT_") && strings.HasPrefix(e.Target, "mission_type:")
}
func legacySynthEdge(e dct.Edge, synthesized, regenerated map[string]bool) bool {
	if regenerated != nil && !regenerated[e.Source] {
		return false
	}
	kind, _, _ := strings.Cut(e.Source, ":")
	return deadMissionTypeEdge(e) || e.Relation == dct.RelApplies && contains(reachOnlyKinds, kind) && synthesized[e.Source]
}
func strippedEdgeReason(e dct.Edge) string {
	if deadMissionTypeEdge(e) {
		return DeadMissionTypeEdgeReason
	}
	return LegacyAppliesReason
}
func legacyStripNotice(e dct.Edge) string {
	return strippedEdgeReason(e) + ": " + e.Source + " --" + e.Relation + "--> " + e.Target
}
func synthesizedNodeURNs(m *SynthManifest) map[string]bool {
	out := map[string]bool{}
	for _, entry := range m.Artifacts {
		if entry.Kind != "directive" {
			out[entry.Kind+":"+entry.Slug] = true
		}
	}
	return out
}
func withShippedEndpoints(base, overlay *dct.Graph) *dct.Graph {
	known := base.NodeURNs()
	for urn := range overlay.NodeURNs() {
		known[urn] = true
	}
	wanted := map[string]bool{}
	for _, edge := range overlay.Edges {
		if !known[edge.Source] {
			wanted[edge.Source] = true
		}
		if !known[edge.Target] {
			wanted[edge.Target] = true
		}
	}
	if len(wanted) == 0 {
		return base
	}
	shipped, err := dct.BuiltInGraph()
	if err != nil {
		return base
	}
	copied := *base
	copied.Nodes = append([]dct.Node(nil), base.Nodes...)
	for _, node := range shipped.Nodes {
		if wanted[node.URN] {
			copied.Nodes = append(copied.Nodes, node)
		}
	}
	return &copied
}
func StrippedLegacyEdges(delta *ReconDelta) []NodeOrEdgeRef {
	var out []NodeOrEdgeRef
	if delta != nil {
		for _, ref := range delta.Removable {
			if ref.Reason != "" {
				out = append(out, ref)
			}
		}
	}
	return out
}
