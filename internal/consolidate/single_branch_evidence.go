package consolidate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

var singleBranchEvidenceCodes = []string{"SINGLE_BRANCH_IMPLEMENTATION_MISSING", "SINGLE_BRANCH_REVIEW_NOT_INDEPENDENT", "SINGLE_BRANCH_EVIDENCE_UNREADABLE", "SINGLE_BRANCH_EVIDENCE_WINDOW_UNCOMPUTABLE"}

func SingleBranchRefusalCodes(text string) []string {
	var result []string
	for _, line := range strings.Split(text, "\n") {
		for _, code := range singleBranchEvidenceCodes {
			if strings.HasPrefix(line, code+": ") && !contains(result, code) {
				result = append(result, code)
			}
		}
	}
	return result
}

func singleBranchRefusalText(slug string, lines []string) string {
	return "Mission " + slug + ": single_branch work packages cannot be landed as reviewed:\n" + strings.Join(append(lines, "Recovery: implement the work package (commit a real change in its owned_files on the mission branch) and have a reviewer who is not the implementer approve it, then re-run consolidate."), "\n")
}

// SingleBranchEvidenceRefusal also covers the orchestrator planning-lane path,
// which does not construct a multi-lane reconciliation claim.
func SingleBranchEvidenceRefusal(root, featureDir string, m *lanes.Manifest, excluded map[string]bool) (string, error) {
	meta := mission.ReadMetaOrEmpty(project.Specs(root, m.MissionSlug))
	if meta.GetString("topology") != coord.SingleBranch {
		return "", nil
	}
	wps, err := snapshotLanes(featureDir)
	if err != nil {
		return "", err
	}
	return singleBranchEvidenceRefusal(root, featureDir, m, wps, excluded), nil
}

func singleBranchEvidenceRefusal(root, featureDir string, m *lanes.Manifest, wps map[string]string, excluded map[string]bool) string {
	if mission.ReadMetaOrEmpty(project.Specs(root, m.MissionSlug)).GetString("topology") != coord.SingleBranch {
		return ""
	}
	var approved []string
	for id, lane := range wps {
		if !excluded[id] && approvedLane(lane) {
			approved = append(approved, id)
		}
	}
	sort.Strings(approved)
	if len(approved) == 0 {
		return ""
	}
	if m.PlanningCommitSHA == nil || *m.PlanningCommitSHA == "" || m.MissionBranch == "" {
		return singleBranchRefusalText(m.MissionSlug, []string{"SINGLE_BRANCH_EVIDENCE_WINDOW_UNCOMPUTABLE: the implementation-evidence window (planning_commit..mission_branch) cannot be computed for this single_branch mission (no recorded planning commit), so implementation evidence cannot be verified. Recovery: run `spec-kitty agent mission finalize-tasks` to record the planning commit, then re-run."})
	}
	base, tip := *m.PlanningCommitSHA, m.MissionBranch
	changed, err := changedPathsInRange(root, base, tip)
	if err != nil {
		return singleBranchRefusalText(m.MissionSlug, []string{fmt.Sprintf("SINGLE_BRANCH_EVIDENCE_UNREADABLE: the implementation-evidence window %s..%s could not be diffed, so implementation evidence cannot be verified.", base, tip)})
	}
	owned := map[string][]string{}
	for _, p := range wp.Files(featureDir) {
		w, e := wp.Read(p)
		if e != nil {
			continue
		}
		for _, raw := range w.Meta().GetList("owned_files") {
			pattern, ok := raw.(string)
			if !ok {
				continue
			}
			if strings.TrimSpace(pattern) != "" {
				owned[w.String("work_package_id")] = append(owned[w.String("work_package_id")], strings.TrimPrefix(strings.ReplaceAll(pattern, `\`, "/"), "./"))
			}
		}
	}
	events, _ := readEvents(featureDir)
	var refusals []string
	for _, id := range approved {
		implemented := false
		for _, entry := range changed {
			for _, pattern := range owned[id] {
				p := strings.TrimPrefix(strings.ReplaceAll(entry.path, `\`, "/"), "./")
				prefix := strings.TrimRight(strings.ReplaceAll(strings.ReplaceAll(pattern, "/**", ""), "/*", ""), "/")
				if lanes.Fnmatch(p, pattern) || prefix != "" && strings.HasPrefix(p, prefix+"/") {
					implemented = true
				}
			}
		}
		if !implemented {
			refusals = append(refusals, fmt.Sprintf("SINGLE_BRANCH_IMPLEMENTATION_MISSING: %s was approved with no implementation commit touching its owned_files in the mission branch window %s..%s.", id, base, tip))
			continue
		}
		implementer := status.LatestImplementer(events, id)
		reviewer := ""
		for _, e := range events {
			if e.WPID != id || e.To != status.Approved || isMigration(e) {
				continue
			}
			if isApprovedReviewedAttestation(e) {
				continue
			}
			if identity := e.Actor.Identity(); identity != "" {
				reviewer = identity
			}
		}
		if implementer == "" || reviewer == "" || implementer == reviewer {
			refusals = append(refusals, fmt.Sprintf("SINGLE_BRANCH_REVIEW_NOT_INDEPENDENT: %s was approved without a confirmable independent review (the implementer of record and the approving actor are the same identity, or one of them is not recorded).", id))
		}
	}
	if len(refusals) == 0 {
		return ""
	}
	return singleBranchRefusalText(m.MissionSlug, refusals)
}
