package mission

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

const ActionCommandSchemaVersion uint16 = 1
const actionReceiptDomain = "spec-kitty/mission-action-receipt/v1\x00"
const actionRequestDomain = "spec-kitty/mission-action-request/v1\x00"

type ActionCommand struct {
	SchemaVersion    uint16
	CorrelationID    string
	OperationID      ids.OperationID
	MissionID        ids.MissionID
	ExpectedMission  MissionSnapshot
	ActionKey        string
	Successor        StepKey
	GovernanceAction string
	ActorID          ids.ActorID
	ProcessID        ids.ProcessID
	Governance       governancecontext.MissionAction
	PayloadDigest    string
	OccurredAt       time.Time
}

type ActionOutcome struct {
	Mission     MissionSnapshot
	Decision    *Decision
	Blocker     *Blocker
	AffectedIDs []string
}
type ActionReceipt string

func (r ActionReceipt) Validate() error {
	if !lowerHexDigest(string(r)) {
		return refusal(KindPersistenceContract, RecoveryRepairStoredState, "action.receipt", "action receipt is malformed")
	}
	return nil
}

type ActionResult struct {
	OperationID   ids.OperationID
	MissionID     ids.MissionID
	ActionKey     string
	RequestDigest string
	Outcome       ActionOutcome
	Receipt       ActionReceipt
	Replayed      bool
}
type ActionCommit struct {
	OperationID      ids.OperationID
	MissionID        ids.MissionID
	Expected         MissionSnapshot
	ActionKey        string
	Successor        StepKey
	GovernanceAction string
	ActorID          ids.ActorID
	ProcessID        ids.ProcessID
	Governance       governancecontext.MissionAction
	RequestDigest    string
	Outcome          ActionOutcome
	Receipt          ActionReceipt
	OccurredAt       time.Time
}

// ActionStore owns immutable replay and one aggregate CAS publication. It must
// persist the exact supplied result and receipt; it may never regenerate an
// outcome during replay.
type ActionStore interface {
	ReplayAction(context.Context, ids.MissionID, ids.OperationID, string) (ActionResult, bool, error)
	ObserveAction(context.Context, ids.MissionID) (NextSnapshot, error)
	CommitAction(context.Context, ActionCommit) (ActionResult, error)
}

// ActionHandler prepares a domain candidate without persistence or external
// effects. The store is the sole mutation boundary.
type ActionHandler interface {
	PrepareAction(context.Context, ActionCommand, NextSnapshot) (ActionOutcome, error)
}
type ActionHandlerFunc func(context.Context, ActionCommand, NextSnapshot) (ActionOutcome, error)

func (f ActionHandlerFunc) PrepareAction(ctx context.Context, c ActionCommand, s NextSnapshot) (ActionOutcome, error) {
	return f(ctx, c, s)
}

type ActionService struct {
	store    ActionStore
	handlers map[string]ActionHandler
}

func NewActionService(store ActionStore, handlers map[string]ActionHandler) *ActionService {
	copyHandlers := make(map[string]ActionHandler, len(handlers))
	for key, value := range handlers {
		copyHandlers[key] = value
	}
	return &ActionService{store: store, handlers: copyHandlers}
}

func (s *ActionService) Execute(ctx context.Context, command ActionCommand) (ActionResult, error) {
	if s == nil || s.store == nil {
		return ActionResult{}, refusal(KindPersistenceUnavailable, RecoveryRetryPersistence, "action.store", "action persistence is unavailable")
	}
	digest, err := validateActionCommand(command)
	if err != nil {
		return ActionResult{}, err
	}
	prior, found, err := s.store.ReplayAction(ctx, command.MissionID, command.OperationID, digest)
	if err != nil {
		return ActionResult{}, normalizeActionStoreError(err)
	}
	if found {
		if err := validateActionResult(command, digest, prior); err != nil {
			return ActionResult{}, err
		}
		prior = cloneActionResult(prior)
		prior.Replayed = true
		return prior, nil
	}
	snapshot, err := s.store.ObserveAction(ctx, command.MissionID)
	if err != nil {
		return ActionResult{}, normalizeActionStoreError(err)
	}
	if snapshot.Mission.Validate() != nil || !sameMissionSnapshot(snapshot.Mission, command.ExpectedMission) {
		return ActionResult{}, refusal(KindStateVersionConflict, RecoveryRefreshState, "expected_mission", "mission state changed")
	}
	if err := validateCommandGovernance(command, snapshot); err != nil {
		return ActionResult{}, err
	}
	if err := validateRequestedAction(snapshot, command); err != nil {
		return ActionResult{}, err
	}
	handler := s.handlers[command.GovernanceAction]
	if handler == nil {
		return ActionResult{}, refusal(KindUnsupportedAction, RecoveryCorrectInput, "governance_action", "action handler is not registered")
	}
	outcome, err := handler.PrepareAction(ctx, command, cloneNextSnapshot(snapshot))
	if err != nil {
		return ActionResult{}, err
	}
	if err := validateActionOutcome(command, snapshot, outcome); err != nil {
		return ActionResult{}, err
	}
	receipt, err := computeActionReceipt(command.OperationID, command.MissionID, command.ActionKey, digest, outcome)
	if err != nil {
		return ActionResult{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "action.receipt", "action outcome cannot be committed")
	}
	commit := ActionCommit{OperationID: command.OperationID, MissionID: command.MissionID, Expected: command.ExpectedMission.clone(), ActionKey: command.ActionKey, Successor: command.Successor, GovernanceAction: command.GovernanceAction, ActorID: command.ActorID, ProcessID: command.ProcessID, Governance: command.Governance, RequestDigest: digest, Outcome: cloneActionOutcome(outcome), Receipt: receipt, OccurredAt: command.OccurredAt}
	result, err := s.store.CommitAction(ctx, commit)
	if err != nil {
		return ActionResult{}, normalizeActionStoreError(err)
	}
	if err := validateActionResult(command, digest, result); err != nil {
		return ActionResult{}, err
	}
	if result.Receipt != receipt {
		return ActionResult{}, refusal(KindPersistenceContract, RecoveryRepairStoredState, "action.receipt", "action store replaced the prepared outcome")
	}
	return cloneActionResult(result), nil
}

func validateActionCommand(c ActionCommand) (string, error) {
	if c.SchemaVersion != ActionCommandSchemaVersion {
		return "", refusal(KindInvalidRequest, RecoveryCorrectInput, "schema_version", "action command schema is unsupported")
	}
	if !boundedFactText(c.CorrelationID, 128) {
		return "", refusal(KindInvalidRequest, RecoveryCorrectInput, "correlation_id", "correlation identity is invalid")
	}
	if _, err := ids.ParseOperationID(c.OperationID.String()); err != nil {
		return "", refusal(KindInvalidRequest, RecoveryCorrectInput, "operation_id", "operation identity is invalid")
	}
	if _, err := ids.ParseMissionID(c.MissionID.String()); err != nil || c.ExpectedMission.Validate() != nil || c.ExpectedMission.MissionID != c.MissionID {
		return "", refusal(KindInvalidRequest, RecoveryCorrectInput, "expected_mission", "expected mission is invalid")
	}
	if !boundedFactText(c.ActionKey, 128) || c.Successor.Validate() != nil || c.Successor.Reserved() || !boundedFactText(c.GovernanceAction, 128) {
		return "", refusal(KindInvalidRequest, RecoveryCorrectInput, "action", "action identity is invalid")
	}
	if !factGovernanceAction(c.GovernanceAction) {
		destination, governanceAction, ok := actionDestinationLaw(c.ActionKey)
		if !ok || destination != c.Successor || governanceAction != c.GovernanceAction {
			return "", refusal(KindInvalidRequest, RecoveryCorrectInput, "action.destination", "action destination identity is inconsistent")
		}
	}
	if _, err := ids.ParseActorID(c.ActorID.String()); err != nil {
		return "", refusal(KindInvalidRequest, RecoveryCorrectInput, "actor_id", "actor identity is invalid")
	}
	if _, err := ids.ParseProcessID(c.ProcessID.String()); err != nil {
		return "", refusal(KindInvalidRequest, RecoveryCorrectInput, "process_id", "process identity is invalid")
	}
	if c.Governance.Validate() != nil {
		return "", refusal(KindInvalidRequest, RecoveryCorrectInput, "governance", "action governance is invalid")
	}
	if !lowerHexDigest(c.PayloadDigest) {
		return "", refusal(KindInvalidRequest, RecoveryCorrectInput, "payload_digest", "payload digest is invalid")
	}
	if c.OccurredAt.IsZero() || c.OccurredAt.Location() != time.UTC {
		return "", refusal(KindInvalidRequest, RecoveryCorrectInput, "occurred_at", "action time must be non-zero UTC")
	}
	canonical := struct {
		Schema           uint16
		Operation        ids.OperationID
		Mission          ids.MissionID
		Expected         missionSnapshotIdentity
		Action           string
		Successor        StepKey
		GovernanceAction string
		Actor            ids.ActorID
		Process          ids.ProcessID
		Governance       governancecontext.MissionAction
		Payload          string
		OccurredAt       string
	}{c.SchemaVersion, c.OperationID, c.MissionID, canonicalMissionSnapshotIdentity(c.ExpectedMission), c.ActionKey, c.Successor, c.GovernanceAction, c.ActorID, c.ProcessID, c.Governance, c.PayloadDigest, c.OccurredAt.Format(time.RFC3339Nano)}
	return domainDigest(actionRequestDomain, canonical)
}
func factGovernanceAction(action string) bool {
	return action == "mission.resolve-decision" || action == "mission.defer-decision" || action == "mission.cancel-decision" || action == "mission.clear-blocker"
}
func validateCommandGovernance(c ActionCommand, s NextSnapshot) error {
	g := c.Governance
	if g.MissionID() != c.MissionID || g.StepKey() != s.Mission.CurrentStep.String() || g.ActionKey() != c.GovernanceAction || g.ActorID() != c.ActorID {
		return refusal(KindInvalidRequest, RecoveryCorrectInput, "governance", "action governance does not match command authority")
	}
	want := governancecontext.GrantMissionTransition
	if strings.Contains(c.GovernanceAction, "decision") {
		want = governancecontext.GrantDecisionResolve
	} else if c.GovernanceAction == "mission.clear-blocker" {
		want = governancecontext.GrantBlockerClear
	}
	grants := g.Grants()
	if len(grants) != 1 || grants[0] != want {
		return refusal(KindInvalidRequest, RecoveryCorrectInput, "governance.grants", "action grant is insufficient")
	}
	return nil
}
func validateRequestedAction(snapshot NextSnapshot, c ActionCommand) error {
	if snapshot.Definition.ID() != snapshot.Mission.DefinitionID || snapshot.Definition.Version() != snapshot.Mission.DefinitionVersion || snapshot.Definition.Commitment() != snapshot.Mission.DefinitionCommitment || !sameDecisionCensus(snapshot.Mission.UnresolvedDecisions, OpenDecisions(snapshot.Decisions)) || !sameBlockerCensus(snapshot.Mission.ActiveBlockers, ActiveBlockers(snapshot.Blockers)) {
		return refusal(KindInvalidStoredState, RecoveryRepairStoredState, "action.snapshot", "action snapshot authority is inconsistent")
	}
	if c.GovernanceAction == "mission.resolve-decision" || c.GovernanceAction == "mission.defer-decision" || c.GovernanceAction == "mission.cancel-decision" {
		open := OpenDecisions(snapshot.Decisions)
		if len(open) == 0 {
			return refusal(KindUnresolvedDecision, RecoveryResolveDecision, "decision", "open decision is absent")
		}
		for _, d := range open {
			if d.ID().String() == c.ActionKey && c.Successor == snapshot.Mission.CurrentStep {
				return nil
			}
		}
		return refusal(KindUnsupportedAction, RecoveryCorrectInput, "decision", "decision action is outside current authority")
	}
	if c.GovernanceAction == "mission.clear-blocker" {
		active := ActiveBlockers(snapshot.Blockers)
		if len(active) == 0 {
			return refusal(KindActiveBlocker, RecoveryClearBlocker, "blocker", "active blocker is absent")
		}
		for _, b := range active {
			if b.ID().String() == c.ActionKey && c.Successor == snapshot.Mission.CurrentStep {
				return nil
			}
		}
		return refusal(KindUnsupportedAction, RecoveryCorrectInput, "blocker", "blocker action is outside current authority")
	}
	next, err := evaluateNext(snapshot, c.MissionID)
	if err != nil {
		return err
	}
	if next.Kind == NextBlocked {
		return refusal(KindActiveBlocker, RecoveryClearBlocker, "blocker", "active blocker prevents action")
	}
	if next.Kind == NextDecisionRequired {
		return refusal(KindUnresolvedDecision, RecoveryResolveDecision, "decision", "open decision prevents action")
	}
	if next.Kind != NextAction || next.Action.ActionKey != c.ActionKey || next.Action.Destination() != c.Successor || next.Action.GovernanceAction != c.GovernanceAction {
		return refusal(KindUnsupportedAction, RecoveryRefreshState, "action", "action is not legal in current mission state")
	}
	return nil
}
func validateActionOutcome(c ActionCommand, s NextSnapshot, o ActionOutcome) error {
	if o.Mission.Validate() != nil || o.Mission.MissionID != c.MissionID || o.Mission.ProjectID != s.Mission.ProjectID || o.Mission.DefinitionID != s.Mission.DefinitionID || o.Mission.DefinitionVersion != s.Mission.DefinitionVersion || o.Mission.DefinitionCommitment != s.Mission.DefinitionCommitment || o.Mission.StateVersion != s.Mission.StateVersion+1 || o.Mission.CurrentStep != c.Successor || o.Mission.CreatedAt != s.Mission.CreatedAt || o.Mission.UpdatedAt != c.OccurredAt {
		return refusal(KindPersistenceContract, RecoveryRepairStoredState, "action.outcome", "prepared action outcome violates mission postconditions")
	}
	if !canonicalStringSet(o.AffectedIDs) {
		return refusal(KindPersistenceContract, RecoveryRepairStoredState, "action.affected_ids", "affected identities are not canonical")
	}
	switch {
	case strings.Contains(c.GovernanceAction, "decision"):
		if o.Decision == nil || o.Blocker != nil || o.Decision.ID().String() != c.ActionKey || o.Decision.Status() == DecisionOpen || containsDecisionID(o.Mission.UnresolvedDecisions, o.Decision.ID()) {
			return refusal(KindPersistenceContract, RecoveryRepairStoredState, "action.decision", "decision action outcome is incomplete")
		}
	case c.GovernanceAction == "mission.clear-blocker":
		if o.Blocker == nil || o.Decision != nil || o.Blocker.ID().String() != c.ActionKey || o.Blocker.Status() != BlockerCleared || containsBlockerID(o.Mission.ActiveBlockers, o.Blocker.ID()) {
			return refusal(KindPersistenceContract, RecoveryRepairStoredState, "action.blocker", "blocker action outcome is incomplete")
		}
	default:
		if o.Decision != nil || o.Blocker != nil || !sameDecisionIDSet(o.Mission.UnresolvedDecisions, s.Mission.UnresolvedDecisions) || !sameBlockerIDSet(o.Mission.ActiveBlockers, s.Mission.ActiveBlockers) {
			return refusal(KindPersistenceContract, RecoveryRepairStoredState, "action.facts", "lifecycle action rewrote mission facts")
		}
	}
	return nil
}
func validateActionResult(c ActionCommand, digest string, r ActionResult) error {
	if r.OperationID != c.OperationID || r.MissionID != c.MissionID || r.ActionKey != c.ActionKey || r.RequestDigest != digest {
		return refusal(KindPersistenceContract, RecoveryRepairStoredState, "action.result", "action store returned mismatched identity")
	}
	if err := validateActionOutcome(c, NextSnapshot{Mission: c.ExpectedMission}, r.Outcome); err != nil {
		return err
	}
	expected, err := computeActionReceipt(c.OperationID, c.MissionID, c.ActionKey, digest, r.Outcome)
	if err != nil || r.Receipt.Validate() != nil || r.Receipt != expected {
		return refusal(KindPersistenceContract, RecoveryRepairStoredState, "action.receipt", "action store returned substituted outcome evidence")
	}
	return nil
}
func computeActionReceipt(operation ids.OperationID, mission ids.MissionID, action, digest string, outcome ActionOutcome) (ActionReceipt, error) {
	canonical := struct {
		Operation ids.OperationID
		Mission   ids.MissionID
		Action    string
		Request   string
		Outcome   actionOutcomeIdentity
	}{operation, mission, action, digest, canonicalActionOutcome(outcome)}
	value, err := domainDigest(actionReceiptDomain, canonical)
	return ActionReceipt(value), err
}

type actionOutcomeIdentity struct {
	Mission  missionSnapshotIdentity
	Decision *decisionOutcomeIdentity
	Blocker  *blockerOutcomeIdentity
	Affected []string
}
type decisionOutcomeIdentity struct {
	ID                ids.DecisionID
	MissionID         ids.MissionID
	Step              StepKey
	Question          string
	Mode              DecisionMode
	Options           []string
	Status            DecisionStatus
	Version           uint64
	CreatedBy         ids.ActorID
	CreatedGovernance ids.GovernanceContextID
	CreatedAt         time.Time
	Resolution        DecisionResolution
}
type blockerOutcomeIdentity struct {
	ID         ids.BlockerID
	MissionID  ids.MissionID
	WorkID     ids.WorkID
	Step       StepKey
	Kind       BlockerKind
	Summary    string
	Recovery   []BlockerRecovery
	Status     BlockerStatus
	Version    uint64
	ObservedAt time.Time
	Clearance  BlockerClearance
}

func canonicalActionOutcome(o ActionOutcome) actionOutcomeIdentity {
	v := actionOutcomeIdentity{Mission: canonicalMissionSnapshotIdentity(o.Mission), Affected: append([]string(nil), o.AffectedIDs...)}
	if o.Decision != nil {
		d := o.Decision
		v.Decision = &decisionOutcomeIdentity{d.id, d.missionID, d.step, d.question, d.mode, d.Options(), d.status, d.version, d.createdBy, d.governanceContextID, d.createdAt, d.resolution}
	}
	if o.Blocker != nil {
		b := o.Blocker
		v.Blocker = &blockerOutcomeIdentity{b.id, b.missionID, b.workID, b.step, b.kind, b.summary, b.RecoveryActions(), b.status, b.version, b.observedAt, b.clearance}
	}
	return v
}
func domainDigest(domain string, value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(domain), encoded...))
	return hex.EncodeToString(sum[:]), nil
}
func lowerHexDigest(v string) bool {
	if len(v) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(v)
	return err == nil && len(decoded) == 32 && v == strings.ToLower(v)
}
func canonicalStringSet(v []string) bool {
	for i, item := range v {
		if !boundedFactText(item, 128) || (i > 0 && v[i-1] >= item) {
			return false
		}
	}
	return true
}
func cloneActionOutcome(o ActionOutcome) ActionOutcome {
	o.Mission = o.Mission.clone()
	o.AffectedIDs = append([]string(nil), o.AffectedIDs...)
	if o.Decision != nil {
		x := *o.Decision
		x.options = o.Decision.Options()
		o.Decision = &x
	}
	if o.Blocker != nil {
		x := *o.Blocker
		x.recovery = o.Blocker.RecoveryActions()
		o.Blocker = &x
	}
	return o
}
func cloneActionResult(r ActionResult) ActionResult {
	r.Outcome = cloneActionOutcome(r.Outcome)
	return r
}
func cloneNextSnapshot(s NextSnapshot) NextSnapshot {
	s.Mission = s.Mission.clone()
	s.ArtifactObservations = append([]artifact.CurrentApprovalObservation(nil), s.ArtifactObservations...)
	s.WorkDraftFindings = append([]workgraph.Finding(nil), s.WorkDraftFindings...)
	s.Decisions = append([]Decision(nil), s.Decisions...)
	for i := range s.Decisions {
		s.Decisions[i].options = s.Decisions[i].Options()
	}
	s.Blockers = append([]Blocker(nil), s.Blockers...)
	for i := range s.Blockers {
		s.Blockers[i].recovery = s.Blockers[i].RecoveryActions()
	}
	return s
}
func normalizeActionStoreError(err error) error {
	if err == nil {
		return nil
	}
	var typed *Error
	if errors.As(err, &typed) && typed.Validate() == nil {
		return typed
	}
	return refusal(KindPersistenceUnavailable, RecoveryRetryPersistence, "action.store", "action persistence operation failed")
}

// AdvanceActionHandler is a pure candidate builder for definition-declared
// lifecycle advancement. Specialized artifact/workgraph services may use
// their own handlers, but all persistence still crosses ActionStore once.
type AdvanceActionHandler struct{}

func (AdvanceActionHandler) PrepareAction(_ context.Context, c ActionCommand, s NextSnapshot) (ActionOutcome, error) {
	next := s.Mission.clone()
	next.CurrentStep = c.Successor
	next.StateVersion++
	next.UpdatedAt = c.OccurredAt
	if s.Definition.IsTerminal(c.Successor) {
		next.Lifecycle = LifecycleTerminal
		next.TerminalOutcome = OutcomeWorkFinalized
	}
	return ActionOutcome{Mission: next, AffectedIDs: []string{c.MissionID.String()}}, nil
}

// DecisionActionHandler and BlockerActionHandler prepare the fact and mission
// reference update together. ActionStore commits that candidate in one CAS.
type DecisionActionHandler struct{ Transition DecisionTransition }

func (h DecisionActionHandler) PrepareAction(_ context.Context, c ActionCommand, s NextSnapshot) (ActionOutcome, error) {
	var current Decision
	found := false
	for _, candidate := range s.Decisions {
		if candidate.ID().String() == c.ActionKey && candidate.Status() == DecisionOpen {
			current = candidate
			found = true
			break
		}
	}
	if !found {
		return ActionOutcome{}, refusal(KindUnresolvedDecision, RecoveryResolveDecision, "decision", "open decision is absent")
	}
	t := h.Transition
	if t.ActorID != c.ActorID || t.GovernanceContextID != c.Governance.ContextID() || t.OccurredAt != c.OccurredAt {
		return ActionOutcome{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "decision.provenance", "decision provenance does not match action")
	}
	want := DecisionResolved
	if c.GovernanceAction == "mission.defer-decision" {
		want = DecisionDeferred
	} else if c.GovernanceAction == "mission.cancel-decision" {
		want = DecisionCanceled
	}
	if t.Status != want {
		return ActionOutcome{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "decision.status", "decision action and transition disagree")
	}
	updated, err := current.Transition(t)
	if err != nil {
		return ActionOutcome{}, err
	}
	mission := s.Mission.clone()
	mission.UnresolvedDecisions = removeDecisionID(mission.UnresolvedDecisions, current.ID())
	mission.StateVersion++
	mission.UpdatedAt = c.OccurredAt
	affected := []string{current.ID().String(), c.MissionID.String()}
	sort.Strings(affected)
	return ActionOutcome{Mission: mission, Decision: &updated, AffectedIDs: affected}, nil
}

type BlockerActionHandler struct{ Clear BlockerClear }

func (h BlockerActionHandler) PrepareAction(_ context.Context, c ActionCommand, s NextSnapshot) (ActionOutcome, error) {
	var current Blocker
	found := false
	for _, candidate := range s.Blockers {
		if candidate.ID().String() == c.ActionKey && candidate.Status() == BlockerActive {
			current = candidate
			found = true
			break
		}
	}
	if !found {
		return ActionOutcome{}, refusal(KindActiveBlocker, RecoveryClearBlocker, "blocker", "active blocker is absent")
	}
	clear := h.Clear
	if clear.ActorID != c.ActorID || clear.GovernanceContextID != c.Governance.ContextID() || clear.OccurredAt != c.OccurredAt {
		return ActionOutcome{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "blocker.provenance", "blocker provenance does not match action")
	}
	updated, err := current.Clear(clear)
	if err != nil {
		return ActionOutcome{}, err
	}
	mission := s.Mission.clone()
	mission.ActiveBlockers = removeBlockerID(mission.ActiveBlockers, current.ID())
	mission.StateVersion++
	mission.UpdatedAt = c.OccurredAt
	affected := []string{current.ID().String(), c.MissionID.String()}
	sort.Strings(affected)
	return ActionOutcome{Mission: mission, Blocker: &updated, AffectedIDs: affected}, nil
}
func removeDecisionID(values []ids.DecisionID, want ids.DecisionID) []ids.DecisionID {
	result := make([]ids.DecisionID, 0, len(values))
	for _, v := range values {
		if v != want {
			result = append(result, v)
		}
	}
	return result
}
func removeBlockerID(values []ids.BlockerID, want ids.BlockerID) []ids.BlockerID {
	result := make([]ids.BlockerID, 0, len(values))
	for _, v := range values {
		if v != want {
			result = append(result, v)
		}
	}
	return result
}
func containsDecisionID(values []ids.DecisionID, want ids.DecisionID) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
func containsBlockerID(values []ids.BlockerID, want ids.BlockerID) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
func sameDecisionIDSet(a, b []ids.DecisionID) bool {
	left := append([]ids.DecisionID(nil), a...)
	right := append([]ids.DecisionID(nil), b...)
	sort.Slice(left, func(i, j int) bool { return left[i] < left[j] })
	sort.Slice(right, func(i, j int) bool { return right[i] < right[j] })
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
func sameBlockerIDSet(a, b []ids.BlockerID) bool {
	left := append([]ids.BlockerID(nil), a...)
	right := append([]ids.BlockerID(nil), b...)
	sort.Slice(left, func(i, j int) bool { return left[i] < left[j] })
	sort.Slice(right, func(i, j int) bool { return right[i] < right[j] })
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
