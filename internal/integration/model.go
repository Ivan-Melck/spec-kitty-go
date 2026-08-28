package integration

import (
	"errors"
	"fmt"
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
	KindNotApproved         ErrorKind = "candidate_not_approved"
	KindRevisionIneligible  ErrorKind = "revision_ineligible"
	KindAuthorityBusy       ErrorKind = "target_authority_busy"
	KindAuthorityExpired    ErrorKind = "target_authority_expired"
	KindTargetMoved         ErrorKind = "target_changed"
	KindConflict            ErrorKind = "integration_conflict"
	KindRepairRequired      ErrorKind = "repair_required"
	KindIdempotencyConflict ErrorKind = "idempotency_conflict"
	KindStoreFailure        ErrorKind = "control_store_failure"
	KindGitFailure          ErrorKind = "git_failure"
)

type Error struct {
	Kind     ErrorKind
	Message  string
	Observed map[string]string
	Expected map[string]string
	Cause    error
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

type Status string

type CleanupStatus string

const (
	StatusAuthorizing    Status = "authorizing"
	StatusPrepared       Status = "prepared"
	StatusResultReady    Status = "result_ready"
	StatusDone           Status = "done"
	StatusRepairRequired Status = "repair_required"
)

const (
	CleanupPending  CleanupStatus = "pending"
	CleanupEligible CleanupStatus = "eligible"
	CleanupComplete CleanupStatus = "complete"
)

type Request struct {
	SchemaVersion uint16
	CorrelationID string
	OperationID   ids.OperationID
	ReviewID      ids.ReviewID
	RepositoryID  ids.RepositoryID
	TargetRef     string
	ActorID       ids.ActorID
	ProcessID     ids.ProcessID
	Candidate     string
	LeaseTTL      time.Duration
	StartedAt     time.Time
}

func (r Request) Validate() error {
	if r.SchemaVersion != execution.ResultSchemaVersion || r.CorrelationID == "" {
		return invalid("schema version and correlation ID are required", nil)
	}
	checks := []error{}
	_, err := ids.ParseOperationID(r.OperationID.String())
	checks = append(checks, err)
	_, err = ids.ParseReviewID(r.ReviewID.String())
	checks = append(checks, err)
	_, err = ids.ParseRepositoryID(r.RepositoryID.String())
	checks = append(checks, err)
	_, err = ids.ParseActorID(r.ActorID.String())
	checks = append(checks, err)
	_, err = ids.ParseProcessID(r.ProcessID.String())
	checks = append(checks, err)
	for _, check := range checks {
		if check != nil {
			return invalid("integration contains an invalid portable ID", check)
		}
	}
	if !validTargetRef(r.TargetRef) || !validCommit(r.Candidate) || r.LeaseTTL <= 0 || r.StartedAt.IsZero() {
		return invalid("explicit target ref, exact candidate, positive lease lifetime, and timestamp are required", nil)
	}
	return nil
}

func validTargetRef(value string) bool {
	if !strings.HasPrefix(value, "refs/") || strings.HasPrefix(value, "refs/-") || strings.Contains(value, "..") || strings.Contains(value, "//") ||
		strings.ContainsAny(value, " ~^:?*[\\") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") || strings.HasSuffix(value, ".lock") || strings.Contains(value, "@{") {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	for _, component := range strings.Split(value, "/") {
		if strings.HasPrefix(component, "-") {
			return false
		}
	}
	return true
}

type Record struct {
	OperationID        ids.OperationID    `json:"operation_id"`
	ReviewID           ids.ReviewID       `json:"review_id"`
	HandoffID          ids.HandoffID      `json:"handoff_id"`
	MissionID          ids.MissionID      `json:"mission_id"`
	WorkRevisionID     ids.WorkRevisionID `json:"work_revision_id"`
	WorkID             ids.WorkID         `json:"work_id"`
	RepositoryID       ids.RepositoryID   `json:"repository_id"`
	TargetRef          string             `json:"target_ref"`
	ActorID            ids.ActorID        `json:"actor_id"`
	ProcessID          ids.ProcessID      `json:"process_id"`
	Candidate          string             `json:"candidate"`
	PreflightTarget    string             `json:"preflight_target"`
	WorkspacePath      string             `json:"workspace_path,omitempty"`
	ResultCommit       string             `json:"result_commit,omitempty"`
	ObservedTarget     string             `json:"observed_target,omitempty"`
	MutationGeneration int64              `json:"mutation_generation"`
	FenceRef           string             `json:"fence_ref"`
	FenceObject        string             `json:"fence_object"`
	FencePredecessor   string             `json:"-"`
	FenceRequestDigest string             `json:"-"`
	Token              string             `json:"-"`
	LeaseDeadline      time.Time          `json:"lease_deadline"`
	Status             Status             `json:"status"`
	RepairKind         string             `json:"repair_kind,omitempty"`
	RepairAction       string             `json:"repair_action,omitempty"`
	Evidence           ConflictEvidence   `json:"evidence,omitempty"`
	StartedAt          time.Time          `json:"started_at"`
	DoneAt             time.Time          `json:"done_at,omitempty"`
	CleanupStatus      CleanupStatus      `json:"cleanup_status"`
	CleanupEligibleAt  time.Time          `json:"cleanup_eligible_at,omitempty"`
	CleanupCompletedAt time.Time          `json:"cleanup_completed_at,omitempty"`
}

type WorkspaceFacts struct {
	Path               string
	Registered         bool
	Detached           bool
	Clean              bool
	Conflicted         bool
	Head               string
	CandidateReachable bool
}

type FenceFacts struct {
	Ref                  string
	Object               string
	PredecessorObject    string
	SupersededOperations []ids.OperationID
	Generation           int64
	OperationID          ids.OperationID
	RepositoryID         ids.RepositoryID
	TargetRef            string
	RequestDigest        string
	PreflightTarget      string
	TokenDigest          string
	Deadline             time.Time
}

type ConflictEvidence struct {
	Base          string   `json:"base,omitempty"`
	Target        string   `json:"target,omitempty"`
	Candidate     string   `json:"candidate,omitempty"`
	Result        string   `json:"result,omitempty"`
	Workspace     string   `json:"workspace,omitempty"`
	ConflictFiles []string `json:"conflict_files,omitempty"`
	GitMessage    string   `json:"git_message,omitempty"`
}

func invalid(message string, cause error) error {
	return &Error{Kind: KindInvalid, Message: message, Cause: cause}
}

func validCommit(value string) bool {
	if (len(value) != 40 && len(value) != 64) || strings.ToLower(value) != value {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}
