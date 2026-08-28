package handoff

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type EligibilityFact = workgraph.EligibilityFact

type ErrorKind string

const (
	KindInvalid             ErrorKind = "invalid_command"
	KindStaleAuthority      ErrorKind = "stale_authority"
	KindCandidateMismatch   ErrorKind = "candidate_mismatch"
	KindRevisionIneligible  ErrorKind = "revision_ineligible"
	KindIdempotencyConflict ErrorKind = "idempotency_conflict"
	KindConflict            ErrorKind = "handoff_conflict"
	KindNotFound            ErrorKind = "not_found"

	EvidenceRefMissing     = "missing"
	EvidenceRefMutated     = "evidence_ref_mutated"
	EvidenceObjectMissing  = "evidence_object_missing"
	EvidenceRepairConflict = "evidence_repair_conflict"
)

type Error struct {
	Kind            ErrorKind
	Message         string
	Observed        map[string]string
	Expected        map[string]string
	RecoveryActions []string
	Cause           error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}
func (e *Error) Unwrap() error { return e.Cause }
func IsKind(err error, kind ErrorKind) bool {
	var typed *Error
	return errors.As(err, &typed) && typed.Kind == kind
}

type Request struct {
	SchemaVersion   uint16
	CorrelationID   string
	OperationID     ids.OperationID
	HandoffID       ids.HandoffID
	ProjectID       ids.ProjectID
	MissionID       ids.MissionID
	WorkRevisionID  ids.WorkRevisionID
	WorkID          ids.WorkID
	RunID           ids.RunID
	WorkspaceID     ids.WorkspaceID
	LeaseID         ids.LeaseID
	LeaseGeneration int64
	Token           string
	ActorID         ids.ActorID
	ProcessID       ids.ProcessID
	Candidate       string
	Contributors    []ids.ActorID
	EvidenceDigest  string
	SubmittedAt     time.Time
	// ExpectedEligibility is required when the service is configured with the
	// complete mixed-reconciliation reader. It is intentionally absent for the
	// frozen legacy-v1 store path.
	ExpectedEligibility EligibilityFact
}

func (r Request) Validate() error {
	if r.SchemaVersion != execution.ResultSchemaVersion || r.CorrelationID == "" {
		return invalid("schema version and correlation ID are required", nil)
	}
	checks := []error{}
	_, err := ids.ParseOperationID(r.OperationID.String())
	checks = append(checks, err)
	_, err = ids.ParseHandoffID(r.HandoffID.String())
	checks = append(checks, err)
	_, err = ids.ParseProjectID(r.ProjectID.String())
	checks = append(checks, err)
	_, err = ids.ParseMissionID(r.MissionID.String())
	checks = append(checks, err)
	_, err = ids.ParseWorkRevisionID(r.WorkRevisionID.String())
	checks = append(checks, err)
	_, err = ids.ParseWorkID(r.WorkID.String())
	checks = append(checks, err)
	_, err = ids.ParseRunID(r.RunID.String())
	checks = append(checks, err)
	_, err = ids.ParseWorkspaceID(r.WorkspaceID.String())
	checks = append(checks, err)
	_, err = ids.ParseLeaseID(r.LeaseID.String())
	checks = append(checks, err)
	_, err = ids.ParseActorID(r.ActorID.String())
	checks = append(checks, err)
	_, err = ids.ParseProcessID(r.ProcessID.String())
	checks = append(checks, err)
	for _, check := range checks {
		if check != nil {
			return invalid("handoff contains an invalid portable ID", check)
		}
	}
	if r.LeaseGeneration <= 0 || r.Token == "" || r.SubmittedAt.IsZero() {
		return invalid("current lease generation, token, and timestamp are required", nil)
	}
	if !ValidCommit(r.Candidate) || !ValidDigest(r.EvidenceDigest) {
		return invalid("candidate must be an exact commit and evidence must be content-addressed", nil)
	}
	contributors, err := CanonicalContributors(r.Contributors)
	if err != nil || len(contributors) == 0 {
		return invalid("at least one unique contributor ActorID is required", err)
	}
	foundActor := false
	for _, contributor := range contributors {
		foundActor = foundActor || contributor == r.ActorID
	}
	if !foundActor {
		return invalid("submitting ActorID must be present in the contributor set", nil)
	}
	return nil
}

type AuthorityScope struct {
	RepositoryID   ids.RepositoryID
	RepositoryPath string
	OwnedBranch    string
	PinnedBase     string
	LaneID         ids.LaneID
	CriteriaDigest string
	OperationID    ids.OperationID
	HandoffID      ids.HandoffID
	WorkRevisionID ids.WorkRevisionID
	WorkID         ids.WorkID
}

type CandidateEvidence struct {
	Candidate          string `json:"candidate"`
	PinnedBase         string `json:"pinned_base"`
	OwnedBranch        string `json:"owned_branch"`
	ObservedBranchHead string `json:"observed_branch_head"`
}

// Refusal is the immutable result of one handoff OperationID that observed an
// owned-branch divergence after preserving both Git facts. It makes a refused
// command replayable even after lease or branch state changes.
type Refusal struct {
	OperationID     ids.OperationID   `json:"operation_id"`
	Kind            ErrorKind         `json:"kind"`
	Message         string            `json:"message"`
	Observed        map[string]string `json:"observed"`
	Expected        map[string]string `json:"expected"`
	RecoveryActions []string          `json:"recovery_actions"`
	OccurredAt      time.Time         `json:"occurred_at"`
	RepositoryPath  string            `json:"-"`
}

func (r Refusal) Error() *Error {
	return &Error{
		Kind: r.Kind, Message: r.Message,
		Observed: cloneStrings(r.Observed), Expected: cloneStrings(r.Expected),
		RecoveryActions: append([]string(nil), r.RecoveryActions...),
	}
}

func cloneStrings(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

type Record struct {
	HandoffID         ids.HandoffID      `json:"handoff_id"`
	HandoffGeneration int64              `json:"handoff_generation"`
	ProjectID         ids.ProjectID      `json:"project_id"`
	MissionID         ids.MissionID      `json:"mission_id"`
	WorkRevisionID    ids.WorkRevisionID `json:"work_revision_id"`
	WorkID            ids.WorkID         `json:"work_id"`
	RunID             ids.RunID          `json:"run_id"`
	WorkspaceID       ids.WorkspaceID    `json:"workspace_id"`
	RepositoryID      ids.RepositoryID   `json:"repository_id"`
	LeaseID           ids.LeaseID        `json:"lease_id"`
	LeaseGeneration   int64              `json:"lease_generation"`
	LaneID            ids.LaneID         `json:"lane_id"`
	Candidate         string             `json:"candidate"`
	PinnedBase        string             `json:"pinned_base"`
	Contributors      []ids.ActorID      `json:"contributors"`
	ContributorDigest string             `json:"contributor_digest"`
	CriteriaDigest    string             `json:"criteria_digest"`
	EvidenceDigest    string             `json:"evidence_digest"`
	Verification      CandidateEvidence  `json:"verification"`
	SubmittedAt       time.Time          `json:"submitted_at"`
}

func CanonicalContributors(input []ids.ActorID) ([]ids.ActorID, error) {
	result := append([]ids.ActorID(nil), input...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	for index, actorID := range result {
		if _, err := ids.ParseActorID(actorID.String()); err != nil {
			return nil, err
		}
		if index > 0 && result[index-1] == actorID {
			return nil, fmt.Errorf("duplicate contributor %s", actorID)
		}
	}
	return result, nil
}

func ValidCommit(value string) bool { return validHex(value, 40) || validHex(value, 64) }
func ValidDigest(value string) bool { return validHex(value, 64) }
func validHex(value string, length int) bool {
	if len(value) != length || strings.ToLower(value) != value {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}
func invalid(message string, cause error) error {
	return &Error{Kind: KindInvalid, Message: message, Cause: cause}
}
