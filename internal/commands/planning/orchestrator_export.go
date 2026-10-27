package planning

// Exported seams used by the orchestrator-api verbs (area I). Additive only:
// they reuse this package's record-analysis helpers without changing the
// host command.

import (
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
)

// RecordAnalysisDirtyPreflight runs record-analysis's write preflight
// (_enforce_analysis_report_write_preflight) for mission dir slug and returns
// the JSON payload the host prints on refusal, or nil when the tree is clean.
func RecordAnalysisDirtyPreflight(root, slug string) *jsonx.Object {
	cwdRoot := project.CurrentWorktreeRoot(cwd())
	if cwdRoot == "" {
		cwdRoot = root
	}
	entries, err := safecommit.StatusEntries(cwdRoot, "")
	if err != nil {
		return jsonx.NewObject("error", err.Error(), "success", false)
	}
	s := coord.Resolve(root, slug)
	var dirty []string
	for _, e := range entries {
		sides := []string{e.Path}
		if e.OrigPath != "" {
			sides = []string{e.OrigPath, e.Path}
		}
		for _, p := range sides {
			if isSelfBookkeepingChurn(p) {
				continue
			}
			if coord.RoutesThroughCoordination(s.Topology) && strings.HasPrefix(p, "kitty-specs/"+slug+"/") && mission.IsCoordResidue(p) {
				continue
			}
			dirty = append(dirty, p)
		}
	}
	if len(dirty) == 0 {
		return nil
	}
	return jsonx.NewObject("success", false, "error_code", "DIRTY_WORKTREE",
		"error", "Refusing to record analysis report with pre-existing dirty working tree.",
		"dirty_paths", strList(dirty),
		"remediation", strList(dirtyWorktreeRemediation))
}

// ParseAnalysisVerdict is parse_structured_findings reduced to the verdict:
// "unknown" when the body carries no analysis-findings/v1 carrier, an error
// for a present-but-invalid carrier.
func ParseAnalysisVerdict(body string) (string, error) {
	verdict, _, _, _, ok, err := parseFindingsCarrier(body)
	if err != nil {
		return "", err
	}
	if !ok {
		return "unknown", nil
	}
	return verdict, nil
}
