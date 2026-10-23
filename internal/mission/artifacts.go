package mission

import (
	"path"
	"strings"
)

// Artifact kinds (mission_runtime.MissionArtifactKind) used for placement.
const (
	KindSpec                   = "spec"
	KindPlan                   = "finalized_execution_plan"
	KindTasksIndex             = "tasks_index"
	KindLaneState              = "lane_state"
	KindAcceptanceMatrix       = "acceptance_matrix"
	KindIssueMatrix            = "issue_matrix"
	KindStatusState            = "status_state"
	KindAnalysisReport         = "analysis_report"
	KindDataModel              = "data_model"
	KindResearch               = "research"
	KindChecklist              = "checklist"
	KindRetrospective          = "retrospective"
	KindWorkPackageTask        = "work_package_task"
	KindDecisionLog            = "decision_log"
	KindTracerFile             = "tracer_file"
	KindDecisionLedger         = "decision_ledger"
	KindContract               = "contract"
	KindResearchOutput         = "research_output"
	KindOccurrenceMap          = "occurrence_map"
	KindMissionGoals           = "mission_goals"
	KindReleaseRecord          = "release_record"
	KindPrimaryMetadata        = "primary_metadata"
	KindReviewCycle            = "review_cycle"
	KindPrimaryMetadataUnknown = ""
)

var kindByBasename = map[string]string{
	"plan.md": KindPlan, "tasks.md": KindTasksIndex, "wps.yaml": KindTasksIndex, "lanes.json": KindLaneState,
	"acceptance-matrix.json": KindAcceptanceMatrix, "issue-matrix.md": KindIssueMatrix, "issue-matrix.json": KindIssueMatrix,
	"status.events.jsonl": KindStatusState, "status.json": KindStatusState, "analysis-report.md": KindAnalysisReport,
	"spec.md": KindSpec, "data-model.md": KindDataModel, "research.md": KindResearch, "quickstart.md": KindChecklist,
	"retrospective.yaml": KindRetrospective, "baseline-tests.json": KindWorkPackageTask, "decisions.events.jsonl": KindDecisionLog,
	"findings.md": KindResearchOutput, "report.md": KindResearchOutput, "methodology.md": KindResearchOutput,
	"synthesis.md": KindResearchOutput, "literature-review.md": KindResearchOutput,
	"gap-analysis.md": KindAnalysisReport, "audit-report.md": KindAnalysisReport,
	"occurrence_map.yaml": KindOccurrenceMap, "goals.md": KindMissionGoals, "release.md": KindReleaseRecord,
	"meta.json": KindPrimaryMetadata,
}

var kindByDir = map[string]string{
	"tasks": KindWorkPackageTask, "checklists": KindChecklist, "traces": KindTracerFile,
	"decisions": KindDecisionLedger, "contracts": KindContract, "research": KindResearch,
}

// CoordinationKinds is the COORD partition (_PLACEMENT_ARTIFACT_KINDS).
var CoordinationKinds = map[string]bool{
	KindAcceptanceMatrix: true, KindIssueMatrix: true, KindStatusState: true,
	KindDecisionLog: true, KindTracerFile: true, KindReviewCycle: true,
}

// ArtifactKindForPath is kind_for_mission_file: classify a
// kitty-specs/<mission>/... path ("" when unrecognised).
func ArtifactKindForPath(p string) string {
	parts := strings.Split(strings.TrimRight(strings.ReplaceAll(p, `\`, "/"), "/"), "/")
	idx := -1
	for i, s := range parts {
		if s == "kitty-specs" {
			idx = i
			break
		}
	}
	if idx < 0 || idx+2 >= len(parts) {
		return ""
	}
	rel := parts[idx+2:]
	if len(rel) == 1 {
		if k := kindByBasename[rel[0]]; k != "" {
			return k
		}
		return kindByDir[rel[0]]
	}
	if rel[0] == "tasks" {
		if ok, _ := path.Match("review-cycle-*.md", rel[len(rel)-1]); ok {
			return KindReviewCycle
		}
	}
	return kindByDir[rel[0]]
}

// IsCoordResidue is is_coord_residue_churn with the default (coord)
// topology: the path classifies to a COORD-partition kind.
func IsCoordResidue(p string) bool { return CoordinationKinds[ArtifactKindForPath(p)] }
