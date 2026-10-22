package orchestrator

import "github.com/spec-kitty/go-kitty3/internal/jsonx"

// Commands is the group's verb list in registration (--help) order.
var Commands = []string{
	"contract-version", "mission-state", "list-ready", "resolve-workspace",
	"start-implementation", "start-review", "transition", "append-history",
	"accept-mission", "consolidate-mission", "specify", "plan", "tasks",
	"check-prerequisites", "record-analysis", "open-decision", "resolve-decision",
	"defer-decision", "cancel-decision", "answer-decision", "design-status",
	"artifact-read", "artifact-submit", "design-validate", "design-context",
	"interview-record", "next",
}

// AllowedErrorCodes is upstream_contract.json orchestrator_api.allowed_error_codes.
var AllowedErrorCodes = []string{
	"USAGE_ERROR", "POLICY_METADATA_REQUIRED", "POLICY_VALIDATION_FAILED", "INVALID_MISSION",
	"MISSION_NOT_FOUND", "STATUS_READ_PATH_NOT_FOUND", "WP_NOT_FOUND", "TRANSITION_REJECTED",
	"WP_ALREADY_CLAIMED", "MISSION_NOT_READY", "WORKFLOW_EVIDENCE_REQUIRED", "PREFLIGHT_FAILED",
	"CONTRACT_VERSION_MISMATCH", "UNSUPPORTED_STRATEGY", "HISTORY_COMMIT_FAILED",
	"DEPENDENCIES_NOT_SATISFIED", "LANE_ALLOCATION_FAILED", "ANCESTRY_NOT_ESTABLISHED",
	"STATUS_LOCK_HELD", "SAFE_COMMIT_BACKSTOP", "SAFE_COMMIT_DESTINATION_NOT_FOUND",
	"SAFE_COMMIT_DESTINATION_REF_SHAPE", "SAFE_COMMIT_EMPTY_CHANGESET", "SAFE_COMMIT_GENERIC",
	"SAFE_COMMIT_HEAD_MISMATCH", "SAFE_COMMIT_NOT_A_WORKTREE", "SAFE_COMMIT_PROTECTED_BRANCH",
	"SAFE_COMMIT_PATH_POLICY", "SAFE_COMMIT_PATH_LOOP", "SAFE_COMMIT_INDEX_DELETION_CONFLICT",
	"SAFE_COMMIT_INDEX_RESIDUE", "SAFE_COMMIT_RECOVERY_FAILED", "SAFE_COMMIT_STAGED_TREE_UNCHANGED",
	"SAFE_COMMIT_UNREQUESTED_PATHS", "MISSION_ALREADY_EXISTS", "MISSION_CREATE_FAILED",
	"PLAN_SETUP_FAILED", "TASKS_FINALIZE_FAILED", "PLACEMENT_RESOLUTION_REQUIRED",
	"CHECK_PREREQUISITES_FAILED", "RECORD_ANALYSIS_EMPTY_BODY", "RECORD_ANALYSIS_INPUT_FILE_NOT_FOUND",
	"RECORD_ANALYSIS_MALFORMED_CARRIER", "RECORD_ANALYSIS_WRITE_NOT_CONFIRMED",
	"RECORD_ANALYSIS_VERDICT_UNRELIABLE", "DIRTY_WORKTREE", "INVALID_ORIGIN_FLOW",
	"DECISION_MISSING_STEP_OR_SLOT", "DECISION_ALREADY_CLOSED", "DECISION_NOT_FOUND",
	"DECISION_TERMINAL_CONFLICT", "DECISION_EVENT_REPAIR_FAILED", "DECISION_OPERATION_FAILED",
	"DESIGN_STATUS_EVENT_LOG_UNREADABLE", "RESULT_REQUIRED", "INVALID_RESULT", "NO_PENDING_DECISION",
	"AMBIGUOUS_PENDING_DECISION", "DECISION_NOT_PENDING", "DESIGN_ARTIFACT_UNSUPPORTED",
	"DESIGN_BOUNDS_EXCEEDED", "DESIGN_COMMIT_FAILED", "DESIGN_CONTEXT_FAILED", "DESIGN_CONTEXT_STALE",
	"DESIGN_FINALIZED", "DESIGN_INTERVIEW_FAILED", "DESIGN_INTERVIEW_INCOMPLETE",
	"DESIGN_INTERVIEW_INVALID", "DESIGN_IO_FAILED", "DESIGN_LOCK_TIMEOUT", "DESIGN_PARENT_STALE",
	"DESIGN_PATH_REFUSED", "DESIGN_PREREQUISITES_FAILED", "DESIGN_RECEIPT_UNREADABLE",
	"DESIGN_REQUEST_INVALID", "DESIGN_REVISION_CONFLICT", "RUNTIME_BLOCKED", "RUNTIME_NEXT_FAILED",
	"UNSUPPORTED_CAPABILITY",
}

var allowedErrorSet = func() map[string]bool {
	m := map[string]bool{}
	for _, c := range AllowedErrorCodes {
		m[c] = true
	}
	return m
}()

// IsAllowedErrorCode is contract_gate.is_allowed_error_code("orchestrator_api", code).
func IsAllowedErrorCode(code string) bool { return allowedErrorSet[code] }

// Design-authoring limits (specify_cli.design.models / design.context).
const (
	MaxArtifactBytes   = 256 * 1024
	MaxBatchBytes      = 1024 * 1024
	MaxRequestBytes    = 2 * MaxBatchBytes
	MaxActorBytes      = 256
	MaxArtifacts       = 64
	ContextBytes       = 1048576
	InterviewAnswerMax = 16384
	InterviewActorMax  = 1024
)

func strs(xs ...string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

// DeliveryProfile is contract-version's delivery_profile block.
func DeliveryProfile() *jsonx.Object {
	return jsonx.NewObject(
		"semantic_contract", "spec-kitty.orchestrator/2",
		"transport", "spec-kitty.orchestrator-api/"+ContractVersion,
		"full_go_conformance", false,
		"supported", strs(Commands...),
		"translated", jsonx.NewObject(
			"CreateMission", "specify: native host-local Mission creation",
			"RecordMissionInterview", "interview-record: canonical decision slots, not aggregate revision CAS",
			"SubmitSpecification", "artifact-submit kind specification: exact-byte SHA-256 CAS",
			"SubmitPlan", "artifact-submit kind plan: accepted specification lineage",
			"SubmitDesignArtifact", "artifact-submit: registered bounded inline support content",
			"SubmitTasksOutline", "artifact-submit kind outline: canonical manifest validation",
			"SubmitWorkPackageDrafts", "artifact-submit kind work_package: declared prompt drafts",
			"ValidateMissionStage", "design-validate: read-only predicates, no durable report revision",
			"CompleteMissionStage", "next --result success: revalidate actually issued native stage",
			"FinalizeWorkPackages", "tasks: native planning pin/status bootstrap, not immutable Go generations",
		),
		"unavailable", strs("GapDB", "leases", "fences", "runtime_epochs", "native_async", "watch",
			"durable_operation_replay", "immutable_work_revisions", "aggregate_revision_cas",
			"opaque_artifact_references", "remote_serving", "tracker_publication", "cross_clone_provenance"),
		"limits", jsonx.NewObject(
			"artifact_bytes", MaxArtifactBytes,
			"batch_bytes", MaxBatchBytes,
			"entries", MaxArtifacts,
			"request_json_bytes", MaxRequestBytes,
			"artifact_actor_bytes", MaxActorBytes,
			"context_bytes", ContextBytes,
			"interview_answer_bytes", InterviewAnswerMax,
			"interview_actor_bytes", InterviewActorMax,
		),
		"persistence", jsonx.NewObject(
			"receipts", "trusted Git common-directory metadata; nonportable between clones",
			"locking", "cooperative API writers only",
			"commit_order", "files and Git commit precede receipt persistence; inspect partial-effect failures",
		),
	)
}
