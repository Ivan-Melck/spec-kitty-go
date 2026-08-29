package review

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type EligibilityFact = workgraph.EligibilityFact

type ErrorKind string

const (
	KindInvalid                ErrorKind = "invalid_command"
	KindStaleAuthority         ErrorKind = "stale_authority"
	KindCandidateMismatch      ErrorKind = "candidate_mismatch"
	KindRevisionIneligible     ErrorKind = "revision_ineligible"
	KindReviewerNotIndependent ErrorKind = "reviewer_not_independent"
	KindIdempotencyConflict    ErrorKind = "idempotency_conflict"
	KindConflict               ErrorKind = "review_conflict"
	KindNotFound               ErrorKind = "not_found"
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

type Verdict string
type CriterionStatus string

const (
	Approved         Verdict         = "approved"
	ChangesRequested Verdict         = "changes_requested"
	Passed           CriterionStatus = "passed"
	Failed           CriterionStatus = "failed"
)

type CriterionResult struct {
	CriterionID string          `json:"criterion_id"`
	Status      CriterionStatus `json:"status"`
	Evidence    string          `json:"evidence"`
}

type Request struct {
	SchemaVersion   uint16
	CorrelationID   string
	OperationID     ids.OperationID
	ReviewID        ids.ReviewID
	HandoffID       ids.HandoffID
	WorkRevisionID  ids.WorkRevisionID
	WorkID          ids.WorkID
	LeaseID         ids.LeaseID
	LeaseGeneration int64
	Token           string
	ActorID         ids.ActorID
	ProcessID       ids.ProcessID
	Candidate       string
	Verdict         Verdict
	Criteria        []CriterionResult
	EvidenceDigest  string
	SubmittedAt     time.Time
	// ExpectedEligibility binds this review to complete mixed-reconciliation
	// authority. The zero value selects only the frozen legacy-v1 store path.
	ExpectedEligibility EligibilityFact
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
	_, err = ids.ParseHandoffID(r.HandoffID.String())
	checks = append(checks, err)
	_, err = ids.ParseWorkRevisionID(r.WorkRevisionID.String())
	checks = append(checks, err)
	_, err = ids.ParseWorkID(r.WorkID.String())
	checks = append(checks, err)
	_, err = ids.ParseLeaseID(r.LeaseID.String())
	checks = append(checks, err)
	_, err = ids.ParseActorID(r.ActorID.String())
	checks = append(checks, err)
	_, err = ids.ParseProcessID(r.ProcessID.String())
	checks = append(checks, err)
	for _, check := range checks {
		if check != nil {
			return invalid("review contains an invalid portable ID", check)
		}
	}
	if r.LeaseGeneration <= 0 || r.Token == "" || r.SubmittedAt.IsZero() || !handoff.ValidCommit(r.Candidate) || !handoff.ValidDigest(r.EvidenceDigest) {
		return invalid("current authority, exact candidate, evidence digest, and timestamp are required", nil)
	}
	if r.Verdict != Approved && r.Verdict != ChangesRequested {
		return invalid("verdict must be approved or changes_requested", nil)
	}
	if len(r.Criteria) == 0 {
		return invalid("criterion results are required", nil)
	}
	seen := map[string]struct{}{}
	failed := false
	for _, criterion := range r.Criteria {
		if criterion.CriterionID == "" || criterion.Evidence == "" || (criterion.Status != Passed && criterion.Status != Failed) {
			return invalid("every criterion needs an ID, status, and evidence", nil)
		}
		if _, exists := seen[criterion.CriterionID]; exists {
			return invalid("criterion IDs must be unique", nil)
		}
		seen[criterion.CriterionID] = struct{}{}
		failed = failed || criterion.Status == Failed
	}
	if r.Verdict == Approved && failed {
		return invalid("approved verdict requires every criterion to pass", nil)
	}
	if r.Verdict == ChangesRequested && !failed {
		return invalid("changes_requested verdict requires a failed criterion", nil)
	}
	return nil
}

type Scope struct {
	Handoff      handoff.Record
	CriterionIDs []string
}
type Record struct {
	ReviewID          ids.ReviewID       `json:"review_id"`
	HandoffID         ids.HandoffID      `json:"handoff_id"`
	WorkRevisionID    ids.WorkRevisionID `json:"work_revision_id"`
	WorkID            ids.WorkID         `json:"work_id"`
	Candidate         string             `json:"candidate"`
	ReviewerActorID   ids.ActorID        `json:"reviewer_actor_id"`
	ReviewerProcessID ids.ProcessID      `json:"reviewer_process_id"`
	Verdict           Verdict            `json:"verdict"`
	Criteria          []CriterionResult  `json:"criteria"`
	EvidenceDigest    string             `json:"evidence_digest"`
	SubmittedAt       time.Time          `json:"submitted_at"`
}

func ValidateCriterionSet(results []CriterionResult, canonical []string) error {
	actual := make([]string, len(results))
	for index := range results {
		actual[index] = results[index].CriterionID
	}
	expected := append([]string(nil), canonical...)
	sort.Strings(actual)
	sort.Strings(expected)
	if len(actual) != len(expected) {
		return invalid("review must evaluate every canonical criterion exactly once", nil)
	}
	for index := range actual {
		if actual[index] != expected[index] {
			return invalid("review criterion set does not match the immutable WorkRevision", nil)
		}
	}
	return nil
}
func invalid(message string, cause error) error {
	return &Error{Kind: KindInvalid, Message: message, Cause: cause}
}
