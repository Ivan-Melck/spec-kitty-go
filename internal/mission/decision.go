package mission

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

type DecisionMode string

const (
	DecisionClosed   DecisionMode = "closed"
	DecisionFreeform DecisionMode = "freeform"
)

type DecisionStatus string

const (
	DecisionOpen     DecisionStatus = "open"
	DecisionResolved DecisionStatus = "resolved"
	DecisionDeferred DecisionStatus = "deferred"
	DecisionCanceled DecisionStatus = "canceled"
)

type DecisionParams struct {
	ID                  ids.DecisionID
	MissionID           ids.MissionID
	Step                StepKey
	Question            string
	Mode                DecisionMode
	Options             []string
	CreatedBy           ids.ActorID
	GovernanceContextID ids.GovernanceContextID
	CreatedAt           time.Time
}

type DecisionResolution struct {
	Status              DecisionStatus
	Answer              string
	Rationale           string
	ActorID             ids.ActorID
	GovernanceContextID ids.GovernanceContextID
	OccurredAt          time.Time
}

type Decision struct {
	id                  ids.DecisionID
	missionID           ids.MissionID
	step                StepKey
	question            string
	mode                DecisionMode
	options             []string
	status              DecisionStatus
	version             uint64
	createdBy           ids.ActorID
	governanceContextID ids.GovernanceContextID
	createdAt           time.Time
	resolution          DecisionResolution
}

func NewDecision(p DecisionParams) (Decision, error) {
	if _, err := ids.ParseDecisionID(p.ID.String()); err != nil {
		return Decision{}, decisionRefusal(KindInvalidRequest, "decision_id")
	}
	if _, err := ids.ParseMissionID(p.MissionID.String()); err != nil {
		return Decision{}, decisionRefusal(KindInvalidRequest, "mission_id")
	}
	if p.Step.Validate() != nil || p.Step.Reserved() {
		return Decision{}, decisionRefusal(KindInvalidRequest, "step")
	}
	if !boundedFactText(p.Question, 512) || containsPrivateEvidence(p.Question) {
		return Decision{}, decisionRefusal(KindInvalidRequest, "question")
	}
	if _, err := ids.ParseActorID(p.CreatedBy.String()); err != nil {
		return Decision{}, decisionRefusal(KindInvalidRequest, "created_by")
	}
	if _, err := ids.ParseGovernanceContextID(p.GovernanceContextID.String()); err != nil {
		return Decision{}, decisionRefusal(KindInvalidRequest, "governance_context_id")
	}
	if p.CreatedAt.IsZero() || p.CreatedAt.Location() != time.UTC {
		return Decision{}, decisionRefusal(KindInvalidRequest, "created_at")
	}
	options := append([]string(nil), p.Options...)
	sort.Strings(options)
	switch p.Mode {
	case DecisionClosed:
		if len(options) < 2 || len(options) > 32 {
			return Decision{}, decisionRefusal(KindInvalidRequest, "options")
		}
	case DecisionFreeform:
		if len(options) != 0 {
			return Decision{}, decisionRefusal(KindInvalidRequest, "options")
		}
	default:
		return Decision{}, decisionRefusal(KindInvalidRequest, "mode")
	}
	for i, option := range options {
		if !boundedFactText(option, 128) || containsPrivateEvidence(option) || (i > 0 && options[i-1] == option) {
			return Decision{}, decisionRefusal(KindInvalidRequest, "options")
		}
	}
	return Decision{id: p.ID, missionID: p.MissionID, step: p.Step, question: p.Question, mode: p.Mode, options: options, status: DecisionOpen, version: 1, createdBy: p.CreatedBy, governanceContextID: p.GovernanceContextID, createdAt: p.CreatedAt}, nil
}

func RestoreDecision(p DecisionParams, status DecisionStatus, version uint64, resolution DecisionResolution) (Decision, error) {
	value, err := NewDecision(p)
	if err != nil {
		return Decision{}, err
	}
	if status == DecisionOpen {
		if version != 1 || resolution != (DecisionResolution{}) {
			return Decision{}, decisionRefusal(KindInvalidStoredState, "decision")
		}
		return value, nil
	}
	if version != 2 {
		return Decision{}, decisionRefusal(KindInvalidStoredState, "decision.version")
	}
	return value.Transition(DecisionTransition{ExpectedStatus: DecisionOpen, ExpectedVersion: 1, Status: status, Answer: resolution.Answer, Rationale: resolution.Rationale, ActorID: resolution.ActorID, GovernanceContextID: resolution.GovernanceContextID, OccurredAt: resolution.OccurredAt})
}

func (d Decision) ID() ids.DecisionID                           { return d.id }
func (d Decision) MissionID() ids.MissionID                     { return d.missionID }
func (d Decision) Step() StepKey                                { return d.step }
func (d Decision) Question() string                             { return d.question }
func (d Decision) Mode() DecisionMode                           { return d.mode }
func (d Decision) Options() []string                            { return append([]string(nil), d.options...) }
func (d Decision) Status() DecisionStatus                       { return d.status }
func (d Decision) Version() uint64                              { return d.version }
func (d Decision) CreatedBy() ids.ActorID                       { return d.createdBy }
func (d Decision) GovernanceContextID() ids.GovernanceContextID { return d.governanceContextID }
func (d Decision) CreatedAt() time.Time                         { return d.createdAt }
func (d Decision) Resolution() DecisionResolution               { return d.resolution }

func (d Decision) Validate() error {
	base, err := NewDecision(DecisionParams{ID: d.id, MissionID: d.missionID, Step: d.step, Question: d.question, Mode: d.mode, Options: d.Options(), CreatedBy: d.createdBy, GovernanceContextID: d.governanceContextID, CreatedAt: d.createdAt})
	if err != nil {
		return err
	}
	if d.status == DecisionOpen {
		if d.version != 1 || d.resolution != (DecisionResolution{}) {
			return decisionRefusal(KindInvalidStoredState, "decision")
		}
		return nil
	}
	if d.version != 2 {
		return decisionRefusal(KindInvalidStoredState, "decision.version")
	}
	_, err = base.Transition(DecisionTransition{ExpectedStatus: DecisionOpen, ExpectedVersion: 1, Status: d.status, Answer: d.resolution.Answer, Rationale: d.resolution.Rationale, ActorID: d.resolution.ActorID, GovernanceContextID: d.resolution.GovernanceContextID, OccurredAt: d.resolution.OccurredAt})
	if err != nil {
		return decisionRefusal(KindInvalidStoredState, "decision.resolution")
	}
	return nil
}

type DecisionTransition struct {
	ExpectedStatus      DecisionStatus
	ExpectedVersion     uint64
	Status              DecisionStatus
	Answer              string
	Rationale           string
	ActorID             ids.ActorID
	GovernanceContextID ids.GovernanceContextID
	OccurredAt          time.Time
}

func (d Decision) Transition(t DecisionTransition) (Decision, error) {
	if d.status != DecisionOpen {
		return Decision{}, decisionRefusal(KindIllegalTransition, "decision_status")
	}
	if t.ExpectedStatus != d.status || t.ExpectedVersion != d.version {
		return Decision{}, decisionRefusal(KindStateVersionConflict, "decision_version")
	}
	if t.Status != DecisionResolved && t.Status != DecisionDeferred && t.Status != DecisionCanceled {
		return Decision{}, decisionRefusal(KindIllegalTransition, "decision_status")
	}
	if !boundedFactText(t.Rationale, 512) || containsPrivateEvidence(t.Rationale) {
		return Decision{}, decisionRefusal(KindInvalidRequest, "rationale")
	}
	if _, err := ids.ParseActorID(t.ActorID.String()); err != nil {
		return Decision{}, decisionRefusal(KindInvalidRequest, "actor_id")
	}
	if _, err := ids.ParseGovernanceContextID(t.GovernanceContextID.String()); err != nil {
		return Decision{}, decisionRefusal(KindInvalidRequest, "governance_context_id")
	}
	if t.OccurredAt.IsZero() || t.OccurredAt.Location() != time.UTC || t.OccurredAt.Before(d.createdAt) {
		return Decision{}, decisionRefusal(KindInvalidRequest, "occurred_at")
	}
	if t.Status == DecisionResolved {
		if !boundedFactText(t.Answer, 512) || containsPrivateEvidence(t.Answer) {
			return Decision{}, decisionRefusal(KindInvalidRequest, "answer")
		}
		if d.mode == DecisionClosed && !containsString(d.options, t.Answer) {
			return Decision{}, decisionRefusal(KindInvalidRequest, "answer")
		}
	} else if t.Answer != "" {
		return Decision{}, decisionRefusal(KindInvalidRequest, "answer")
	}
	result := d
	result.options = d.Options()
	result.status = t.Status
	result.version++
	result.resolution = DecisionResolution{Status: t.Status, Answer: t.Answer, Rationale: t.Rationale, ActorID: t.ActorID, GovernanceContextID: t.GovernanceContextID, OccurredAt: t.OccurredAt}
	return result, nil
}

type BlockerKind string

const (
	BlockerDependency     BlockerKind = "dependency"
	BlockerGovernance     BlockerKind = "governance"
	BlockerEvidence       BlockerKind = "evidence"
	BlockerReconciliation BlockerKind = "reconciliation"
)

type BlockerRecovery string

const (
	RecoveryResolveDependency       BlockerRecovery = "resolve_dependency"
	RecoveryCorrectGovernance       BlockerRecovery = "correct_governance"
	RecoveryRefreshAuthority        BlockerRecovery = "refresh_authority"
	RecoveryProvideArtifactEvidence BlockerRecovery = "provide_artifact_evidence"
	RecoveryReconcileWork           BlockerRecovery = "reconcile_work"
	RecoveryRetryOperation          BlockerRecovery = "retry_operation"
)

type BlockerStatus string

const (
	BlockerActive  BlockerStatus = "active"
	BlockerCleared BlockerStatus = "cleared"
)

type BlockerParams struct {
	ID              ids.BlockerID
	MissionID       ids.MissionID
	WorkID          ids.WorkID
	Step            StepKey
	Kind            BlockerKind
	Summary         string
	RecoveryActions []BlockerRecovery
	ObservedAt      time.Time
}
type BlockerClear struct {
	ExpectedStatus      BlockerStatus
	ExpectedVersion     uint64
	ActorID             ids.ActorID
	GovernanceContextID ids.GovernanceContextID
	Reason              string
	OccurredAt          time.Time
}
type BlockerClearance struct {
	ActorID             ids.ActorID
	GovernanceContextID ids.GovernanceContextID
	Reason              string
	OccurredAt          time.Time
}
type Blocker struct {
	id         ids.BlockerID
	missionID  ids.MissionID
	workID     ids.WorkID
	step       StepKey
	kind       BlockerKind
	summary    string
	recovery   []BlockerRecovery
	status     BlockerStatus
	version    uint64
	observedAt time.Time
	clearance  BlockerClearance
}

func NewBlocker(p BlockerParams) (Blocker, error) {
	if _, err := ids.ParseBlockerID(p.ID.String()); err != nil {
		return Blocker{}, decisionRefusal(KindInvalidRequest, "blocker_id")
	}
	if _, err := ids.ParseMissionID(p.MissionID.String()); err != nil {
		return Blocker{}, decisionRefusal(KindInvalidRequest, "mission_id")
	}
	if p.WorkID != "" {
		if _, err := ids.ParseWorkID(p.WorkID.String()); err != nil {
			return Blocker{}, decisionRefusal(KindInvalidRequest, "work_id")
		}
	}
	if p.Step.Validate() != nil || p.Step.Reserved() {
		return Blocker{}, decisionRefusal(KindInvalidRequest, "step")
	}
	if !boundedFactText(p.Summary, 512) || containsPrivateEvidence(p.Summary) {
		return Blocker{}, decisionRefusal(KindInvalidRequest, "summary")
	}
	if p.ObservedAt.IsZero() || p.ObservedAt.Location() != time.UTC {
		return Blocker{}, decisionRefusal(KindInvalidRequest, "observed_at")
	}
	recovery := append([]BlockerRecovery(nil), p.RecoveryActions...)
	sort.Slice(recovery, func(i, j int) bool { return recovery[i] < recovery[j] })
	if len(recovery) == 0 || len(recovery) > 8 {
		return Blocker{}, decisionRefusal(KindInvalidRequest, "recovery_actions")
	}
	for i, action := range recovery {
		if !blockerRecoveryAllowed(p.Kind, action) || (i > 0 && recovery[i-1] == action) {
			return Blocker{}, decisionRefusal(KindInvalidRequest, "recovery_actions")
		}
	}
	return Blocker{id: p.ID, missionID: p.MissionID, workID: p.WorkID, step: p.Step, kind: p.Kind, summary: p.Summary, recovery: recovery, status: BlockerActive, version: 1, observedAt: p.ObservedAt}, nil
}
func RestoreBlocker(p BlockerParams, status BlockerStatus, version uint64, clearance BlockerClearance) (Blocker, error) {
	value, err := NewBlocker(p)
	if err != nil {
		return Blocker{}, err
	}
	if status == BlockerActive {
		if version != 1 || clearance != (BlockerClearance{}) {
			return Blocker{}, decisionRefusal(KindInvalidStoredState, "blocker")
		}
		return value, nil
	}
	if status != BlockerCleared || version != 2 {
		return Blocker{}, decisionRefusal(KindInvalidStoredState, "blocker.status")
	}
	return value.Clear(BlockerClear{ExpectedStatus: BlockerActive, ExpectedVersion: 1, ActorID: clearance.ActorID, GovernanceContextID: clearance.GovernanceContextID, Reason: clearance.Reason, OccurredAt: clearance.OccurredAt})
}
func blockerRecoveryAllowed(kind BlockerKind, recovery BlockerRecovery) bool {
	switch kind {
	case BlockerDependency:
		return recovery == RecoveryResolveDependency || recovery == RecoveryRetryOperation
	case BlockerGovernance:
		return recovery == RecoveryCorrectGovernance || recovery == RecoveryRefreshAuthority
	case BlockerEvidence:
		return recovery == RecoveryProvideArtifactEvidence || recovery == RecoveryRefreshAuthority
	case BlockerReconciliation:
		return recovery == RecoveryReconcileWork
	default:
		return false
	}
}
func (b Blocker) ID() ids.BlockerID        { return b.id }
func (b Blocker) MissionID() ids.MissionID { return b.missionID }
func (b Blocker) WorkID() ids.WorkID       { return b.workID }
func (b Blocker) Step() StepKey            { return b.step }
func (b Blocker) Kind() BlockerKind        { return b.kind }
func (b Blocker) Summary() string          { return b.summary }
func (b Blocker) RecoveryActions() []BlockerRecovery {
	return append([]BlockerRecovery(nil), b.recovery...)
}
func (b Blocker) Status() BlockerStatus       { return b.status }
func (b Blocker) Version() uint64             { return b.version }
func (b Blocker) ObservedAt() time.Time       { return b.observedAt }
func (b Blocker) Clearance() BlockerClearance { return b.clearance }
func (b Blocker) Validate() error {
	base, err := NewBlocker(BlockerParams{ID: b.id, MissionID: b.missionID, WorkID: b.workID, Step: b.step, Kind: b.kind, Summary: b.summary, RecoveryActions: b.RecoveryActions(), ObservedAt: b.observedAt})
	if err != nil {
		return err
	}
	if b.status == BlockerActive {
		if b.version != 1 || b.clearance != (BlockerClearance{}) {
			return decisionRefusal(KindInvalidStoredState, "blocker")
		}
		return nil
	}
	if b.status != BlockerCleared || b.version != 2 {
		return decisionRefusal(KindInvalidStoredState, "blocker.status")
	}
	_, err = base.Clear(BlockerClear{ExpectedStatus: BlockerActive, ExpectedVersion: 1, ActorID: b.clearance.ActorID, GovernanceContextID: b.clearance.GovernanceContextID, Reason: b.clearance.Reason, OccurredAt: b.clearance.OccurredAt})
	if err != nil {
		return decisionRefusal(KindInvalidStoredState, "blocker.clearance")
	}
	return nil
}
func (b Blocker) Clear(c BlockerClear) (Blocker, error) {
	if b.status != BlockerActive {
		return Blocker{}, decisionRefusal(KindIllegalTransition, "blocker_status")
	}
	if c.ExpectedStatus != b.status || c.ExpectedVersion != b.version {
		return Blocker{}, decisionRefusal(KindStateVersionConflict, "blocker_version")
	}
	if _, err := ids.ParseActorID(c.ActorID.String()); err != nil {
		return Blocker{}, decisionRefusal(KindInvalidRequest, "actor_id")
	}
	if _, err := ids.ParseGovernanceContextID(c.GovernanceContextID.String()); err != nil {
		return Blocker{}, decisionRefusal(KindInvalidRequest, "governance_context_id")
	}
	if !boundedFactText(c.Reason, 512) || containsPrivateEvidence(c.Reason) {
		return Blocker{}, decisionRefusal(KindInvalidRequest, "reason")
	}
	if c.OccurredAt.IsZero() || c.OccurredAt.Location() != time.UTC || c.OccurredAt.Before(b.observedAt) {
		return Blocker{}, decisionRefusal(KindInvalidRequest, "occurred_at")
	}
	result := b
	result.recovery = b.RecoveryActions()
	result.status = BlockerCleared
	result.version++
	result.clearance = BlockerClearance{c.ActorID, c.GovernanceContextID, c.Reason, c.OccurredAt}
	return result, nil
}

func ActiveBlockers(values []Blocker) []Blocker {
	result := make([]Blocker, 0, len(values))
	for _, b := range values {
		if b.status == BlockerActive {
			b.recovery = b.RecoveryActions()
			result = append(result, b)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result
}
func OpenDecisions(values []Decision) []Decision {
	result := make([]Decision, 0, len(values))
	for _, d := range values {
		if d.status == DecisionOpen {
			d.options = d.Options()
			result = append(result, d)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result
}

// MissionFactStore is the narrow WP08 persistence boundary. A fact mutation,
// mission reference removal, mission version increment, operation record, and
// exact result are one aggregate CAS; no sequential repository writes exist.
type MissionFactStore interface {
	ReplayMissionFact(context.Context, ids.MissionID, ids.OperationID, string) (MissionFactResult, bool, error)
	CommitMissionFact(context.Context, MissionFactCommit) (MissionFactResult, error)
}
type MissionFactCommit struct {
	Expected      MissionSnapshot
	Successor     MissionSnapshot
	Decision      *Decision
	Blocker       *Blocker
	OperationID   ids.OperationID
	RequestDigest string
}
type MissionFactResult struct {
	Mission       MissionSnapshot
	Decision      *Decision
	Blocker       *Blocker
	OperationID   ids.OperationID
	RequestDigest string
	Replayed      bool
}

func decisionRefusal(kind ErrorKind, field string) error {
	recovery := RecoveryCorrectInput
	if kind == KindStateVersionConflict {
		recovery = RecoveryRefreshState
	}
	return refusal(kind, recovery, field, "mission fact mutation refused")
}
func boundedFactText(v string, n int) bool {
	if v == "" || len(v) > n || strings.TrimSpace(v) != v {
		return false
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
func containsPrivateEvidence(v string) bool {
	lower := strings.ToLower(v)
	for _, needle := range []string{"password=", "secret=", "token=", "panic:", "stack trace", "/home/"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}
func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
