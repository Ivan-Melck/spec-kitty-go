// Package mission owns mission-definition selection and lifecycle authority.
// It deliberately has no knowledge of trackers, files, Git, transports, or
// persistence implementations.
package mission

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

// DefinitionVersion is the exact, positive contract version of a mission
// definition. Callers must select one explicitly; there is no latest version.
type DefinitionVersion uint64

func (v DefinitionVersion) Validate() error {
	if v == 0 {
		return errors.New("definition version must be positive")
	}
	return nil
}

// DefinitionCommitment binds a mission instance to canonical definition law.
type DefinitionCommitment string

func (c DefinitionCommitment) String() string { return string(c) }

func (c DefinitionCommitment) Validate() error {
	if len(c) != 64 {
		return errors.New("definition commitment must be a lowercase SHA-256 digest")
	}
	decoded, err := hex.DecodeString(string(c))
	if err != nil || len(decoded) != 32 || string(c) != fmt.Sprintf("%x", decoded) {
		return errors.New("definition commitment must be a lowercase SHA-256 digest")
	}
	return nil
}

// StepKey is the closed lifecycle vocabulary understood by the Phase 3
// authority. Reserved keys are parseable for forward-compatible refusals but
// are not actionable in this phase.
type StepKey string

const (
	StepInitialized            StepKey = "initialized"
	StepSpecifying             StepKey = "specifying"
	StepSpecificationApproved  StepKey = "specification_approved"
	StepPlanning               StepKey = "planning"
	StepPlanApproved           StepKey = "plan_approved"
	StepDecomposing            StepKey = "decomposing"
	StepWorkDraftReady         StepKey = "work_draft_ready"
	StepReconciliationRequired StepKey = "reconciliation_required"
	StepWorkFinalized          StepKey = "work_finalized"

	StepExecuteReserved   StepKey = "execute_reserved"
	StepReviewReserved    StepKey = "review_reserved"
	StepIntegrateReserved StepKey = "integrate_reserved"
)

// Phase3StepKeys returns a defensive copy of the actionable lifecycle keys.
func Phase3StepKeys() []StepKey {
	return []StepKey{
		StepInitialized,
		StepSpecifying,
		StepSpecificationApproved,
		StepPlanning,
		StepPlanApproved,
		StepDecomposing,
		StepWorkDraftReady,
		StepReconciliationRequired,
		StepWorkFinalized,
	}
}

func (s StepKey) String() string { return string(s) }

func (s StepKey) Validate() error {
	switch s {
	case StepInitialized, StepSpecifying, StepSpecificationApproved, StepPlanning,
		StepPlanApproved, StepDecomposing, StepWorkDraftReady,
		StepReconciliationRequired, StepWorkFinalized,
		StepExecuteReserved, StepReviewReserved, StepIntegrateReserved:
		return nil
	default:
		return fmt.Errorf("unsupported mission step %q", s)
	}
}

func ParseStepKey(v string) (StepKey, error) {
	step := StepKey(v)
	return step, step.Validate()
}

func (s StepKey) Reserved() bool {
	switch s {
	case StepExecuteReserved, StepReviewReserved, StepIntegrateReserved:
		return true
	default:
		return false
	}
}

// LifecycleStatus is the coarse closed state of a mission instance. StepKey
// carries the authoritative fine-grained state.
type LifecycleStatus string

const (
	LifecycleOpen     LifecycleStatus = "open"
	LifecycleTerminal LifecycleStatus = "terminal"
)

func (s LifecycleStatus) Validate() error {
	switch s {
	case LifecycleOpen, LifecycleTerminal:
		return nil
	default:
		return fmt.Errorf("unsupported lifecycle status %q", s)
	}
}

// TerminalOutcome is set only when a definition-declared terminal step is
// entered.
type TerminalOutcome string

const OutcomeWorkFinalized TerminalOutcome = "work_finalized"

func (o TerminalOutcome) Validate() error {
	if o != OutcomeWorkFinalized {
		return fmt.Errorf("unsupported terminal outcome %q", o)
	}
	return nil
}

// ArtifactKind is a typed description of a definition input or output. It is
// not a path or a mutable file observation.
type ArtifactKind string

const (
	ArtifactIntent        ArtifactKind = "intent"
	ArtifactSpecification ArtifactKind = "specification"
	ArtifactPlan          ArtifactKind = "plan"
	ArtifactWorkDraft     ArtifactKind = "work_draft"
	ArtifactWorkRevision  ArtifactKind = "work_revision"
)

func (k ArtifactKind) Validate() error {
	switch k {
	case ArtifactIntent, ArtifactSpecification, ArtifactPlan, ArtifactWorkDraft, ArtifactWorkRevision:
		return nil
	default:
		return fmt.Errorf("unsupported artifact kind %q", k)
	}
}

// ValidationRequirement names a closed validator result consumed by a step.
type ValidationRequirement string

const (
	ValidationSpecificationComplete ValidationRequirement = "specification_complete"
	ValidationPlanComplete          ValidationRequirement = "plan_complete"
	ValidationWorkGraphComplete     ValidationRequirement = "work_graph_complete"
)

func (v ValidationRequirement) Validate() error {
	switch v {
	case ValidationSpecificationComplete, ValidationPlanComplete, ValidationWorkGraphComplete:
		return nil
	default:
		return fmt.Errorf("unsupported validation requirement %q", v)
	}
}

// ApprovalPolicy is a closed declaration made by a mission definition.
type ApprovalPolicy string

const (
	ApprovalNone          ApprovalPolicy = "none"
	ApprovalSpecification ApprovalPolicy = "specification"
	ApprovalPlan          ApprovalPolicy = "plan"
)

func (p ApprovalPolicy) Validate() error {
	switch p {
	case ApprovalNone, ApprovalSpecification, ApprovalPlan:
		return nil
	default:
		return fmt.Errorf("unsupported approval policy %q", p)
	}
}

// EvidenceKind is closed gate evidence. Values intentionally describe domain
// facts rather than filenames, provider statuses, or presentation prose.
type EvidenceKind string

const (
	EvidenceIntent             EvidenceKind = "intent"
	EvidenceScope              EvidenceKind = "scope"
	EvidenceRequirements       EvidenceKind = "requirements"
	EvidenceAcceptanceFraming  EvidenceKind = "acceptance_framing"
	EvidenceAssumptions        EvidenceKind = "assumptions"
	EvidenceBoundaries         EvidenceKind = "boundaries"
	EvidenceSpecificationOK    EvidenceKind = "specification_approval"
	EvidenceArchitecture       EvidenceKind = "architecture"
	EvidenceDataFlow           EvidenceKind = "data_flow"
	EvidenceRisk               EvidenceKind = "risk"
	EvidenceCompatibility      EvidenceKind = "compatibility"
	EvidenceMigration          EvidenceKind = "migration"
	EvidenceValidationStrategy EvidenceKind = "validation_strategy"
	EvidencePlanOK             EvidenceKind = "plan_approval"
	EvidenceWorkGraph          EvidenceKind = "work_graph"
)

func (k EvidenceKind) Validate() error {
	switch k {
	case EvidenceIntent, EvidenceScope, EvidenceRequirements, EvidenceAcceptanceFraming,
		EvidenceAssumptions, EvidenceBoundaries, EvidenceSpecificationOK,
		EvidenceArchitecture, EvidenceDataFlow, EvidenceRisk, EvidenceCompatibility,
		EvidenceMigration, EvidenceValidationStrategy, EvidencePlanOK, EvidenceWorkGraph:
		return nil
	default:
		return fmt.Errorf("unsupported evidence kind %q", k)
	}
}

type EvidenceStatus string

const (
	EvidenceAccepted EvidenceStatus = "accepted"
	EvidenceRejected EvidenceStatus = "rejected"
	EvidenceStale    EvidenceStatus = "stale"
)

func (s EvidenceStatus) Validate() error {
	switch s {
	case EvidenceAccepted, EvidenceRejected, EvidenceStale:
		return nil
	default:
		return fmt.Errorf("unsupported evidence status %q", s)
	}
}

// EvidenceRef is a bounded reference scoped to the exact mission and current
// step. Artifact services can add freshness proofs without exposing bodies.
type EvidenceRef struct {
	ID           ids.EvidenceID
	Kind         EvidenceKind
	Status       EvidenceStatus
	MissionID    ids.MissionID
	ForStep      StepKey
	StateVersion uint64
}

func (e EvidenceRef) Validate() error {
	if _, err := ids.ParseEvidenceID(e.ID.String()); err != nil {
		return err
	}
	if err := e.Kind.Validate(); err != nil {
		return err
	}
	if err := e.Status.Validate(); err != nil {
		return err
	}
	if _, err := ids.ParseMissionID(e.MissionID.String()); err != nil {
		return err
	}
	if err := e.ForStep.Validate(); err != nil {
		return err
	}
	if e.StateVersion == 0 {
		return errors.New("evidence state version must be positive")
	}
	return nil
}

// ErrorKind and RecoveryCategory form the stable branchable refusal contract.
type ErrorKind string

const (
	KindInvalidRequest               ErrorKind = "invalid_request"
	KindDefinitionMalformed          ErrorKind = "definition_malformed"
	KindDefinitionNotFound           ErrorKind = "definition_not_found"
	KindDefinitionVersionUnsupported ErrorKind = "definition_version_unsupported"
	KindDuplicateDefinition          ErrorKind = "duplicate_definition"
	KindDefinitionConflict           ErrorKind = "definition_conflict"
	KindDefinitionMismatch           ErrorKind = "definition_mismatch"
	KindIllegalTransition            ErrorKind = "illegal_transition"
	KindStateVersionConflict         ErrorKind = "state_version_conflict"
	KindMissingEvidence              ErrorKind = "missing_evidence"
	KindApprovalStale                ErrorKind = "approval_stale"
	KindActiveBlocker                ErrorKind = "active_blocker"
	KindUnresolvedDecision           ErrorKind = "unresolved_decision"
	KindTerminalMission              ErrorKind = "terminal_mission"
	KindUnsupportedAction            ErrorKind = "unsupported_action"
	KindNotFound                     ErrorKind = "not_found"
	KindAlreadyExists                ErrorKind = "already_exists"
	KindIdempotencyConflict          ErrorKind = "idempotency_conflict"
	KindInvalidStoredState           ErrorKind = "invalid_stored_state"
	KindPersistenceUnavailable       ErrorKind = "persistence_unavailable"
	KindPersistenceContract          ErrorKind = "persistence_contract_violation"
)

func (k ErrorKind) Validate() error {
	switch k {
	case KindInvalidRequest, KindDefinitionMalformed, KindDefinitionNotFound,
		KindDefinitionVersionUnsupported, KindDuplicateDefinition,
		KindDefinitionConflict, KindDefinitionMismatch, KindIllegalTransition,
		KindStateVersionConflict, KindMissingEvidence, KindApprovalStale,
		KindActiveBlocker, KindUnresolvedDecision, KindTerminalMission,
		KindUnsupportedAction, KindNotFound, KindAlreadyExists,
		KindIdempotencyConflict, KindInvalidStoredState,
		KindPersistenceUnavailable, KindPersistenceContract:
		return nil
	default:
		return fmt.Errorf("unsupported mission error kind %q", k)
	}
}

type RecoveryCategory string

const (
	RecoveryCorrectInput      RecoveryCategory = "correct_input"
	RecoverySelectDefinition  RecoveryCategory = "select_supported_definition"
	RecoveryRefreshState      RecoveryCategory = "refresh_state"
	RecoverySupplyEvidence    RecoveryCategory = "supply_evidence"
	RecoveryRefreshApproval   RecoveryCategory = "refresh_approval"
	RecoveryClearBlocker      RecoveryCategory = "clear_blocker"
	RecoveryResolveDecision   RecoveryCategory = "resolve_decision"
	RecoveryNoFurtherAction   RecoveryCategory = "no_further_action"
	RecoveryRetryPersistence  RecoveryCategory = "retry_persistence"
	RecoveryRepairStoredState RecoveryCategory = "repair_stored_state"
)

func (r RecoveryCategory) Validate() error {
	switch r {
	case RecoveryCorrectInput, RecoverySelectDefinition, RecoveryRefreshState,
		RecoverySupplyEvidence, RecoveryRefreshApproval, RecoveryClearBlocker,
		RecoveryResolveDecision, RecoveryNoFurtherAction,
		RecoveryRetryPersistence, RecoveryRepairStoredState:
		return nil
	default:
		return fmt.Errorf("unsupported mission recovery category %q", r)
	}
}

// Error contains only bounded categorical diagnostics. Cause is retained for
// errors.Is/errors.As but is deliberately excluded from Error output.
type Error struct {
	Kind     ErrorKind
	Recovery RecoveryCategory
	Field    string
	Message  string
	Cause    error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if err := e.Validate(); err != nil {
		return "kind=persistence_unavailable recovery=retry_persistence field=diagnostic message=invalid bounded diagnostic"
	}
	return fmt.Sprintf("kind=%s recovery=%s field=%s message=%s", e.Kind, e.Recovery, e.Field, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

func IsKind(err error, kind ErrorKind) bool {
	var target *Error
	return errors.As(err, &target) && target.Kind == kind
}

// Validate ensures a branchable diagnostic itself remains within the bounded
// mission contract. It does not accept arbitrary adapter prose.
func (e *Error) Validate() error {
	if e == nil {
		return errors.New("nil mission error")
	}
	if err := e.Kind.Validate(); err != nil {
		return err
	}
	if err := e.Recovery.Validate(); err != nil {
		return err
	}
	if !boundedDiagnosticText(e.Field, 64) || !boundedDiagnosticText(e.Message, 160) {
		return errors.New("mission diagnostic is not bounded")
	}
	return nil
}

func refusal(kind ErrorKind, recovery RecoveryCategory, field, message string) *Error {
	if err := recovery.Validate(); err != nil {
		recovery = RecoveryCorrectInput
	}
	if !boundedDiagnosticText(field, 64) {
		field = "diagnostic"
	}
	if !boundedDiagnosticText(message, 160) {
		message = "mission operation refused"
	}
	return &Error{Kind: kind, Recovery: recovery, Field: field, Message: message}
}

func boundedDiagnosticText(value string, limit int) bool {
	if value == "" || len(value) > limit || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// MissionSnapshot is the reconstruction boundary for one mission aggregate.
// Constructors and accessors copy all collections.
type MissionSnapshot struct {
	MissionID             ids.MissionID
	ProjectID             ids.ProjectID
	DefinitionID          ids.MissionDefinitionID
	DefinitionVersion     DefinitionVersion
	DefinitionCommitment  DefinitionCommitment
	Lifecycle             LifecycleStatus
	CurrentStep           StepKey
	CurrentWorkRevisionID ids.WorkRevisionID
	StateVersion          uint64
	CreatedAt             time.Time
	UpdatedAt             time.Time
	TerminalOutcome       TerminalOutcome
	ActiveBlockers        []ids.BlockerID
	UnresolvedDecisions   []ids.DecisionID
}

func (s MissionSnapshot) clone() MissionSnapshot {
	s.ActiveBlockers = append([]ids.BlockerID(nil), s.ActiveBlockers...)
	s.UnresolvedDecisions = append([]ids.DecisionID(nil), s.UnresolvedDecisions...)
	return s
}

func (s MissionSnapshot) Validate() error {
	if _, err := ids.ParseMissionID(s.MissionID.String()); err != nil {
		return err
	}
	if _, err := ids.ParseProjectID(s.ProjectID.String()); err != nil {
		return err
	}
	if _, err := ids.ParseMissionDefinitionID(s.DefinitionID.String()); err != nil {
		return err
	}
	if err := s.DefinitionVersion.Validate(); err != nil {
		return err
	}
	if err := s.DefinitionCommitment.Validate(); err != nil {
		return err
	}
	if err := s.Lifecycle.Validate(); err != nil {
		return err
	}
	if err := s.CurrentStep.Validate(); err != nil || s.CurrentStep.Reserved() {
		return fmt.Errorf("invalid current step %q", s.CurrentStep)
	}
	if s.StateVersion == 0 {
		return errors.New("mission state version must be positive")
	}
	if s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() || s.CreatedAt.Location() != time.UTC || s.UpdatedAt.Location() != time.UTC {
		return errors.New("mission timestamps must be non-zero UTC values")
	}
	if s.UpdatedAt.Before(s.CreatedAt) {
		return errors.New("mission updated time precedes creation time")
	}
	if s.CurrentWorkRevisionID != "" {
		if _, err := ids.ParseWorkRevisionID(s.CurrentWorkRevisionID.String()); err != nil {
			return err
		}
		if s.CurrentStep != StepWorkFinalized && s.CurrentStep != StepReconciliationRequired {
			return errors.New("current work revision is not legal before finalization")
		}
	}
	if s.CurrentStep == StepWorkFinalized && s.CurrentWorkRevisionID == "" {
		return errors.New("finalized mission requires a current work revision")
	}
	if s.Lifecycle == LifecycleTerminal {
		if err := s.TerminalOutcome.Validate(); err != nil {
			return errors.New("terminal mission requires a valid outcome")
		}
		if s.CurrentStep != StepWorkFinalized {
			return errors.New("terminal outcome is inconsistent with current step")
		}
	} else {
		if s.TerminalOutcome != "" {
			return errors.New("nonterminal mission cannot carry a terminal outcome")
		}
		if s.CurrentStep == StepWorkFinalized {
			return errors.New("nonterminal mission cannot occupy a terminal step")
		}
	}
	blockers := make(map[ids.BlockerID]bool, len(s.ActiveBlockers))
	for _, blocker := range s.ActiveBlockers {
		if _, err := ids.ParseBlockerID(blocker.String()); err != nil {
			return err
		}
		if blockers[blocker] {
			return errors.New("mission contains duplicate active blocker reference")
		}
		blockers[blocker] = true
	}
	decisions := make(map[ids.DecisionID]bool, len(s.UnresolvedDecisions))
	for _, decision := range s.UnresolvedDecisions {
		if _, err := ids.ParseDecisionID(decision.String()); err != nil {
			return err
		}
		if decisions[decision] {
			return errors.New("mission contains duplicate unresolved decision reference")
		}
		decisions[decision] = true
	}
	return nil
}

// MissionInstance is an immutable validated aggregate value.
type MissionInstance struct{ snapshot MissionSnapshot }

func NewMissionInstance(snapshot MissionSnapshot) (MissionInstance, error) {
	snapshot = snapshot.clone()
	if err := snapshot.Validate(); err != nil {
		return MissionInstance{}, err
	}
	return MissionInstance{snapshot: snapshot}, nil
}

func (m MissionInstance) Snapshot() MissionSnapshot { return m.snapshot.clone() }

func (m MissionInstance) Validate() error { return m.snapshot.Validate() }
