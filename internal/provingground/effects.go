package provingground

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/application"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/reducer"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

const (
	EffectApplied             EffectOutcome = "applied"
	EffectAbsent              EffectOutcome = "absent"
	EffectAmbiguous           EffectOutcome = "ambiguous"
	EffectUnavailable         EffectOutcome = "unavailable"
	EffectAttributionLost     EffectOutcome = "attribution_lost"
	EffectCompensated         EffectOutcome = "compensated"
	EffectCompensationUnsafe  EffectOutcome = "safe_compensation_unsupported"
	EffectReconciliationLimit EffectOutcome = "reconciliation_limit"
)

// EffectOutcome deliberately describes the provider-side fact. It is not the
// outcome of the local command that durably recorded that fact.
type EffectOutcome string

// AssignmentEffectIntent is the complete attributable provider mutation. The
// execution capsule is checked before publication but provider state is never
// copied into execution authority.
type AssignmentEffectIntent struct {
	OperationID      controljournal.OperationID
	IntentID         controljournal.IntentID
	EffectID         controljournal.EffectID
	ExternalRef      tracker.ExternalWorkRef
	ExpectedRevision tracker.Revision
	ExpectedAssignee string
	DesiredAssignee  string
	Marker           string
	Diagnostic       string
	OccurredAt       time.Time
	Authority        controljournal.AuthorityCapsuleV1
}

type EffectResolution struct {
	EffectID         controljournal.EffectID    `json:"effect_id"`
	IntentOperation  controljournal.OperationID `json:"intent_operation_id"`
	Outcome          EffectOutcome              `json:"outcome"`
	ProviderRevision tracker.Revision           `json:"provider_revision,omitempty"`
	ProviderValue    string                     `json:"provider_value,omitempty"`
	ProviderMarker   string                     `json:"provider_marker,omitempty"`
	Attempts         int                        `json:"attempts"`
	Diagnostic       string                     `json:"diagnostic"`
	ObservedAt       time.Time                  `json:"observed_at"`
}

type effectState struct {
	SchemaVersion    uint16                     `json:"schema_version"`
	EffectID         controljournal.EffectID    `json:"effect_id"`
	IntentID         controljournal.IntentID    `json:"intent_id"`
	OperationID      controljournal.OperationID `json:"operation_id"`
	Provider         string                     `json:"provider"`
	ProjectID        ids.ProjectID              `json:"project_id"`
	ExternalID       string                     `json:"external_id"`
	WorkID           ids.WorkID                 `json:"work_id"`
	MetadataSchema   uint16                     `json:"metadata_schema"`
	CreatedBy        ids.OperationID            `json:"created_by_operation"`
	ExpectedRevision tracker.Revision           `json:"expected_revision"`
	ExpectedAssignee string                     `json:"expected_assignee"`
	DesiredAssignee  string                     `json:"desired_assignee"`
	Marker           string                     `json:"marker"`
	Diagnostic       string                     `json:"diagnostic"`
	OccurredAt       time.Time                  `json:"occurred_at"`
	Authority        authorityEvidence          `json:"authority"`
	DispatchID       string                     `json:"dispatch_id,omitempty"`
	Resolution       *EffectResolution          `json:"resolution,omitempty"`
}

type authorityEvidence struct {
	ProjectID                   controljournal.ProjectID      `json:"project_id"`
	MissionID                   controljournal.MissionID      `json:"mission_id"`
	WorkRevisionID              controljournal.WorkRevisionID `json:"work_revision_id"`
	ActorID                     controljournal.ActorID        `json:"actor_id"`
	ProcessID                   controljournal.ProcessID      `json:"process_id"`
	RunID                       controljournal.RunID          `json:"run_id"`
	LeaseID                     controljournal.LeaseID        `json:"lease_id"`
	WorkspaceID                 controljournal.WorkspaceID    `json:"workspace_id"`
	RuntimeEpochID              controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
	LeaseGeneration             uint64                        `json:"lease_generation"`
	FenceToken                  uint64                        `json:"fence_token"`
	AuthorityHeadRecordRevision uint64                        `json:"authority_head_record_revision"`
}

func evidenceFromCapsule(c controljournal.AuthorityCapsuleV1) authorityEvidence {
	return authorityEvidence{c.ProjectID, c.MissionID, c.WorkRevisionID, c.ActorID, c.ProcessID, c.RunID, c.LeaseID, c.WorkspaceID, c.RuntimeEpochID, c.LeaseGeneration, c.FenceToken, c.AuthorityHeadRecordRevision}
}
func (a authorityEvidence) capsule() (controljournal.AuthorityCapsuleV1, error) {
	return controljournal.NewAuthorityCapsuleV1(controljournal.AuthorityCapsuleV1{SchemaVersion: 1, ProjectID: a.ProjectID, MissionID: a.MissionID, WorkRevisionID: a.WorkRevisionID, ActorID: a.ActorID, ProcessID: a.ProcessID, RunID: a.RunID, LeaseID: a.LeaseID, WorkspaceID: a.WorkspaceID, RuntimeEpochID: a.RuntimeEpochID, LeaseGeneration: a.LeaseGeneration, FenceToken: a.FenceToken, AuthorityHeadRecordRevision: a.AuthorityHeadRecordRevision})
}

// EffectCoordinator is the admitted effect boundary. Its only durable writer
// is application.Service and its only provider mutation is the tracker port.
type EffectCoordinator struct {
	runtime     *Runtime
	journal     *workflowJournal
	provider    tracker.AssignmentProjection
	faults      faults.Hook
	maxAttempts int
	mu          sync.Mutex
	states      map[controljournal.EffectID]effectState
	dispatcher  string
	lockMu      sync.Mutex
	effectLocks map[controljournal.EffectID]*sync.Mutex
}

func NewEffectCoordinator(ctx context.Context, runtime *Runtime, app *application.Service, port storegapdb.Port, provider tracker.AssignmentProjection, maxAttempts int) (*EffectCoordinator, error) {
	if runtime == nil || app == nil || port == nil || provider == nil || maxAttempts < 1 || maxAttempts > 16 {
		return nil, refusal(OutcomeInvalidCommand, "effects", "runtime, admitted journal, provider, and a bounded attempt policy are required")
	}
	return newEffectCoordinator(ctx, runtime, app, port, provider, maxAttempts)
}
func newEffectCoordinator(ctx context.Context, runtime *Runtime, app batchApplier, port workflowJournalPort, provider tracker.AssignmentProjection, maxAttempts int) (*EffectCoordinator, error) {
	if runtime == nil || app == nil || port == nil || provider == nil || maxAttempts < 1 || maxAttempts > 16 {
		return nil, refusal(OutcomeInvalidCommand, "effects", "complete effect composition is required")
	}
	dispatcherBytes := make([]byte, 16)
	if _, err := rand.Read(dispatcherBytes); err != nil {
		return nil, refusal(OutcomeIndeterminate, "effects", "create process-scoped effect dispatcher identity")
	}
	c := &EffectCoordinator{runtime: runtime, journal: newWorkflowJournal(runtime.core.config, app, port), provider: provider, faults: faults.Noop{}, maxAttempts: maxAttempts, states: make(map[controljournal.EffectID]effectState), dispatcher: hex.EncodeToString(dispatcherBytes), effectLocks: make(map[controljournal.EffectID]*sync.Mutex)}
	if err := c.restore(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *EffectCoordinator) WithFaults(h faults.Hook) *EffectCoordinator {
	if h != nil {
		c.faults = h
	}
	return c
}

// PublishAssignment records the immutable intent in the same accepted batch as
// its product event, reaches GJ-05, and only then touches the provider.
func (c *EffectCoordinator) PublishAssignment(ctx context.Context, request AssignmentEffectIntent) (EffectResolution, error) {
	if err := c.validateIntent(request); err != nil {
		return EffectResolution{}, err
	}
	lock := c.effectLock(request.EffectID)
	lock.Lock()
	defer lock.Unlock()

	state := effectState{SchemaVersion: 1, EffectID: request.EffectID, IntentID: request.IntentID, OperationID: request.OperationID, Provider: request.ExternalRef.Provider, ProjectID: request.ExternalRef.ProjectID, ExternalID: request.ExternalRef.ExternalID, WorkID: request.ExternalRef.WorkID, MetadataSchema: request.ExternalRef.MetadataSchema, CreatedBy: request.ExternalRef.CreatedByOperation, ExpectedRevision: request.ExpectedRevision, ExpectedAssignee: request.ExpectedAssignee, DesiredAssignee: request.DesiredAssignee, Marker: request.Marker, Diagnostic: boundedDiagnostic(request.Diagnostic), OccurredAt: request.OccurredAt.UTC(), Authority: evidenceFromCapsule(request.Authority)}
	c.mu.Lock()
	prior, exists := c.states[request.EffectID]
	c.mu.Unlock()
	if exists && !sameEffectIntent(prior, state) {
		return EffectResolution{}, refusal(OutcomeAuthorityConflict, "effect-intent", "effect identity was reused for a different request")
	}
	if exists && prior.Resolution != nil && terminalEffectOutcome(prior.Resolution.Outcome) {
		return *prior.Resolution, nil
	}
	if exists {
		state = prior
	}
	expected, _ := json.Marshal(map[string]string{"assignee": request.ExpectedAssignee})
	desired, _ := json.Marshal(map[string]string{"assignee": request.DesiredAssignee})
	intent, err := controljournal.NewEffectIntentV1(request.IntentID, request.EffectID, request.OperationID, request.ExternalRef.Provider, strconv.FormatUint(uint64(request.ExpectedRevision), 10), request.Marker, expected, desired)
	if err != nil {
		return EffectResolution{}, refusal(OutcomeInvalidCommand, "effect-intent", "effect intent is malformed")
	}
	publication, err := c.journal.publish(ctx, journalPublication{OperationID: request.OperationID, CommandType: "effect.intent", ResultType: "effect-intent-v1", AggregateKind: "external-effect", AggregateID: controljournal.AggregateID(request.EffectID), EventType: "effect.intent_recorded", Payload: state, Result: state, EffectIntents: []controljournal.EffectIntentV1{intent}})
	if err != nil {
		return EffectResolution{}, err
	}
	c.mu.Lock()
	c.states[request.EffectID] = state
	c.mu.Unlock()
	if err := c.faults.Reach(ctx, "GJ-05"); err != nil {
		return EffectResolution{}, err
	}
	if exists || publication.Kind == application.OutcomeAlreadyCommitted {
		observed, observeErr := c.provider.ObserveAssignment(ctx, request.ExternalRef)
		if observeErr != nil || state.DispatchID != "" || !coherentProviderIdentity(state, observed) || observed.Observation.Revision != state.ExpectedRevision || observed.Assignee != state.ExpectedAssignee || observed.OperationMarker != "" {
			return c.observeAndRecord(ctx, request.EffectID, observed, observeErr)
		}
	}
	reserved, reserveErr := c.reserveInitialDispatch(ctx, state)
	if reserveErr != nil {
		return EffectResolution{}, reserveErr
	}
	if !reserved {
		observed, observeErr := c.provider.ObserveAssignment(ctx, request.ExternalRef)
		return c.observeAndRecord(ctx, request.EffectID, observed, observeErr)
	}
	// EF-01 models process death after durable dispatch authority but before the
	// provider call. Recovery may observe attribution, but it must never blindly
	// repeat the mutation merely because the provider still shows the precondition.
	if err := c.faults.Reach(ctx, "EF-01"); err != nil {
		return EffectResolution{}, err
	}
	mutation, mutationErr := c.provider.ProjectAssignment(ctx, tracker.ProjectAssignmentRequest{ExternalRef: request.ExternalRef, OperationID: ids.OperationID(request.OperationID), Assignee: request.DesiredAssignee, ExpectedObservation: request.ExpectedRevision, RequiredGuarantee: tracker.AssignmentAtomicClaim})
	if mutationErr == nil && mutation.Guarantee != tracker.AssignmentAtomicClaim {
		mutationErr = &tracker.Error{Kind: tracker.ErrorAmbiguous, Message: "provider assignment response omitted the required atomic claim guarantee"}
	}
	return c.observeAndRecord(ctx, request.EffectID, mutation.Observation, mutationErr)
}

func (c *EffectCoordinator) effectLock(id controljournal.EffectID) *sync.Mutex {
	c.lockMu.Lock()
	defer c.lockMu.Unlock()
	if c.effectLocks[id] == nil {
		c.effectLocks[id] = &sync.Mutex{}
	}
	return c.effectLocks[id]
}

func (c *EffectCoordinator) reserveInitialDispatch(ctx context.Context, state effectState) (bool, error) {
	// Reservation is an empty -> reserved transition, not a general append. Read
	// and validate the exact unreserved aggregate head, then pin publication to
	// that observed head revision. A contender cannot read the winner's newer
	// head and append a second reservation under a different dispatcher.
	headKey := c.journal.prefix() + "heads/external-effect/" + string(state.EffectID)
	headRecord, err := c.journal.port.Get(ctx, headKey)
	if err != nil {
		return false, err
	}
	head, err := controljournal.DecodeAggregateHeadV1(headRecord.Value)
	if err != nil || headRecord.Key != headKey || headRecord.Revision == 0 || head.AggregateKind != "external-effect" || head.AggregateID != controljournal.AggregateID(state.EffectID) {
		return false, refusal(OutcomeIndeterminate, "effect-dispatch", "effect head is malformed before dispatch reservation")
	}
	eventKey := c.journal.prefix() + "journal/" + string(head.LastEventID)
	eventRecord, err := c.journal.port.Get(ctx, eventKey)
	if err != nil {
		return false, err
	}
	event, err := controljournal.DecodeEventEnvelopeV1(eventRecord.Value)
	var current effectState
	if err != nil || eventRecord.Key != eventKey || eventRecord.Revision != headRecord.Revision || event.AggregateKind != "external-effect" || event.AggregateID != controljournal.AggregateID(state.EffectID) || event.AggregateLogicalVersion != head.LogicalVersion || event.EventID != head.LastEventID || sha256.Sum256(event.Payload()) != head.StateDigest || json.Unmarshal(event.Payload(), &current) != nil || !sameEffectIntent(current, state) {
		return false, refusal(OutcomeAuthorityConflict, "effect-dispatch", "durable effect intent changed before dispatch reservation")
	}
	if current.DispatchID != "" {
		c.mu.Lock()
		c.states[state.EffectID] = current
		c.mu.Unlock()
		return false, nil
	}
	state.DispatchID = c.dispatcher
	operationID := derivedOperationID("effect-dispatch-reserve", string(state.EffectID), c.dispatcher)
	result, err := c.journal.publish(ctx, journalPublication{OperationID: operationID, CommandType: "effect.dispatch.reserve", ResultType: "effect-dispatch-v1", AggregateKind: "external-effect", AggregateID: controljournal.AggregateID(state.EffectID), EventType: "effect.dispatch_reserved", Payload: state, Result: state, PinHead: true, ExpectedLogical: head.LogicalVersion, ExpectedRevision: headRecord.Revision})
	if err == nil && result.Kind == application.OutcomeCommitted {
		c.mu.Lock()
		c.states[state.EffectID] = state
		c.mu.Unlock()
		return true, nil
	}
	if refreshErr := c.refreshEffect(ctx, state.EffectID); refreshErr != nil {
		if err != nil {
			return false, err
		}
		return false, refreshErr
	}
	return false, nil
}

func (c *EffectCoordinator) refreshEffect(ctx context.Context, effectID controljournal.EffectID) error {
	events, err := c.journal.loadAggregateEvents(ctx, "external-effect")
	if err != nil {
		return err
	}
	var found effectState
	for _, event := range events {
		if event.AggregateID != controljournal.AggregateID(effectID) {
			continue
		}
		if json.Unmarshal(event.Payload(), &found) != nil || found.EffectID != effectID {
			return refusal(OutcomeIndeterminate, "effect-refresh", "effect event payload is malformed")
		}
	}
	if found.EffectID == "" {
		return refusal(OutcomeIndeterminate, "effect-refresh", "effect aggregate disappeared after dispatch contention")
	}
	c.mu.Lock()
	c.states[effectID] = found
	c.mu.Unlock()
	return nil
}

// RecoverUnresolved enumerates durable intents and observes every unresolved
// provider fact. It never repeats ProjectAssignment.
func (c *EffectCoordinator) RecoverUnresolved(ctx context.Context) ([]EffectResolution, error) {
	c.mu.Lock()
	idsToRecover := make([]controljournal.EffectID, 0)
	for id, state := range c.states {
		if state.Resolution == nil || !terminalEffectOutcome(state.Resolution.Outcome) {
			idsToRecover = append(idsToRecover, id)
		}
	}
	c.mu.Unlock()
	sort.Slice(idsToRecover, func(i, j int) bool { return idsToRecover[i] < idsToRecover[j] })
	results := make([]EffectResolution, 0, len(idsToRecover))
	for _, id := range idsToRecover {
		state, _ := c.State(id)
		if state.Resolution != nil && state.Resolution.Attempts >= c.maxAttempts {
			resolution := *state.Resolution
			resolution.Outcome = EffectReconciliationLimit
			resolution.Diagnostic = "bounded reconciliation attempts exhausted"
			state.Resolution = &resolution
			op := derivedOperationID("effect-reconciliation-limit", string(id), strconv.Itoa(resolution.Attempts))
			if _, err := c.journal.publish(ctx, journalPublication{OperationID: op, CommandType: "effect.reconciliation_limit", ResultType: "effect-observation-v1", AggregateKind: "external-effect", AggregateID: controljournal.AggregateID(id), EventType: "effect.reconciliation_limited", Payload: state, Result: resolution}); err != nil {
				return results, err
			}
			c.mu.Lock()
			c.states[id] = state
			c.mu.Unlock()
			results = append(results, resolution)
			continue
		}
		ref := externalRefFromState(state)
		observed, err := c.provider.ObserveAssignment(ctx, ref)
		resolution, recordErr := c.observeAndRecord(ctx, id, observed, err)
		if recordErr != nil && !isProviderRecoverable(recordErr) {
			return results, recordErr
		}
		results = append(results, resolution)
	}
	return results, nil
}

// Compensate performs a coherent observation, then delegates one atomic
// revision+value+marker guarded mutation to the provider. Missing guard
// capability and every attribution change are durable safe refusals.
func (c *EffectCoordinator) Compensate(ctx context.Context, effectID controljournal.EffectID, operationID controljournal.OperationID, now time.Time) (EffectResolution, error) {
	state, ok := c.State(effectID)
	if !ok {
		return EffectResolution{}, refusal(OutcomeInvalidCommand, "compensate", "effect intent is unknown")
	}
	if state.Resolution == nil || state.Resolution.Outcome != EffectApplied {
		return EffectResolution{}, refusal(OutcomeAuthorityConflict, "compensate", "only an attributable applied effect can be compensated")
	}
	ref := externalRefFromState(state)
	observed, err := c.provider.ObserveAssignment(ctx, ref)
	if err != nil {
		return c.recordCompensation(ctx, state, operationID, EffectUnavailable, tracker.AssignmentObservation{}, err, now)
	}
	if !coherentEffectObservation(state, observed) {
		return c.recordCompensation(ctx, state, operationID, EffectAttributionLost, observed, nil, now)
	}
	mutation, mutationErr := c.provider.CompensateAssignment(ctx, tracker.CompensateAssignmentRequest{ExternalRef: ref, OperationID: ids.OperationID(operationID), ExpectedObservation: observed.Observation.Revision, ExpectedAssignee: state.DesiredAssignee, ExpectedMarker: state.Marker, RestoreAssignee: state.ExpectedAssignee})
	outcome := EffectCompensated
	if mutationErr != nil {
		switch {
		case tracker.IsKind(mutationErr, tracker.ErrorSafeCompensationUnsupported), tracker.IsKind(mutationErr, tracker.ErrorGuardedUpdateUnsupported):
			outcome = EffectCompensationUnsafe
		case tracker.IsKind(mutationErr, tracker.ErrorAttributionLost), tracker.IsKind(mutationErr, tracker.ErrorRevisionConflict):
			outcome = EffectAttributionLost
		default:
			outcome = EffectAmbiguous
		}
	}
	if mutationErr == nil && (mutation.Guarantee != tracker.AssignmentAtomicClaim || !coherentProviderIdentity(state, mutation.Observation) || mutation.Observation.Assignee != state.ExpectedAssignee || mutation.Observation.OperationMarker != "" || mutation.Observation.Observation.Revision == observed.Observation.Revision) {
		outcome = EffectAmbiguous
	}
	return c.recordCompensation(ctx, state, operationID, outcome, mutation.Observation, mutationErr, now)
}

func (c *EffectCoordinator) State(id controljournal.EffectID) (effectState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, ok := c.states[id]
	return state, ok
}

func (c *EffectCoordinator) validateIntent(r AssignmentEffectIntent) error {
	if _, err := controljournal.NewIntentID(string(r.IntentID)); err != nil {
		return refusal(OutcomeInvalidCommand, "effect-intent", "intent identity is invalid")
	}
	if _, err := controljournal.NewEffectID(string(r.EffectID)); err != nil {
		return refusal(OutcomeInvalidCommand, "effect-intent", "effect identity is invalid")
	}
	if _, err := controljournal.NewOperationID(string(r.OperationID)); err != nil || r.ExternalRef.Validate() != nil || r.ExpectedRevision == 0 || r.DesiredAssignee == "" || r.Marker != string(r.OperationID) || r.OccurredAt.IsZero() {
		return refusal(OutcomeInvalidCommand, "effect-intent", "complete attributable assignment fields are required")
	}
	if r.ExternalRef.Provider == "" || r.ExternalRef.ProjectID.String() != string(c.runtime.core.config.ProjectID) || r.ExternalRef.WorkID.String() == "" {
		return refusal(OutcomeInvalidCommand, "effect-intent", "provider binding differs from the admitted project")
	}
	return c.runtime.Authorize(r.Authority, "effect-publication", r.OccurredAt)
}

func (c *EffectCoordinator) observeAndRecord(ctx context.Context, effectID controljournal.EffectID, observed tracker.AssignmentObservation, providerErr error) (EffectResolution, error) {
	state, ok := c.State(effectID)
	if !ok {
		return EffectResolution{}, refusal(OutcomeInvalidCommand, "effect-observe", "effect intent is unknown")
	}
	attempts := 1
	if state.Resolution != nil {
		attempts = state.Resolution.Attempts + 1
	}
	resolution := EffectResolution{EffectID: effectID, IntentOperation: state.OperationID, Attempts: attempts, ObservedAt: state.OccurredAt.Add(time.Duration(attempts) * time.Nanosecond)}
	if !observed.Observation.ObservedAt.IsZero() {
		resolution.ObservedAt = observed.Observation.ObservedAt.UTC()
	}
	resolution.ProviderRevision, resolution.ProviderValue, resolution.ProviderMarker = observed.Observation.Revision, observed.Assignee, observed.OperationMarker
	switch {
	case providerErr != nil && (tracker.IsKind(providerErr, tracker.ErrorUnavailable) || tracker.IsKind(providerErr, tracker.ErrorTimeout)):
		resolution.Outcome, resolution.Diagnostic = EffectUnavailable, boundedDiagnostic(providerErr.Error())
	case providerErr != nil:
		resolution.Outcome, resolution.Diagnostic = EffectAmbiguous, boundedDiagnostic(providerErr.Error())
	case !coherentProviderIdentity(state, observed):
		resolution.Outcome, resolution.Diagnostic = EffectAmbiguous, "provider returned incoherent binding or observation identity"
	case observed.Assignee == state.DesiredAssignee && observed.OperationMarker == state.Marker && observed.Observation.Revision != state.ExpectedRevision:
		resolution.Outcome, resolution.Diagnostic = EffectApplied, "provider state proves the intended attributable postcondition"
	case state.DispatchID == "" && observed.Observation.Revision == state.ExpectedRevision && observed.Assignee == state.ExpectedAssignee && observed.OperationMarker == "":
		resolution.Outcome, resolution.Diagnostic = EffectAbsent, "provider still exposes the attributable precondition"
	case state.DispatchID != "" && observed.Observation.Revision == state.ExpectedRevision && observed.Assignee == state.ExpectedAssignee && observed.OperationMarker == "":
		resolution.Outcome, resolution.Diagnostic = EffectAmbiguous, "dispatch authority exists but the provider still exposes the precondition; blind repeat is forbidden"
	default:
		resolution.Outcome, resolution.Diagnostic = EffectAttributionLost, "provider state belongs to another writer or revision"
	}
	state.Resolution = &resolution
	op := derivedOperationID("effect-observe", string(effectID), strconv.Itoa(attempts), string(resolution.Outcome), strconv.FormatUint(uint64(resolution.ProviderRevision), 10), resolution.ProviderValue, resolution.ProviderMarker)
	if _, err := c.journal.publish(ctx, journalPublication{OperationID: op, CommandType: "effect.observation", ResultType: "effect-observation-v1", AggregateKind: "external-effect", AggregateID: controljournal.AggregateID(effectID), EventType: "effect.observed", Payload: state, Result: resolution}); err != nil {
		return EffectResolution{}, err
	}
	c.mu.Lock()
	c.states[effectID] = state
	c.mu.Unlock()
	if providerErr != nil {
		return resolution, providerErr
	}
	return resolution, nil
}

func (c *EffectCoordinator) recordCompensation(ctx context.Context, state effectState, operationID controljournal.OperationID, outcome EffectOutcome, observed tracker.AssignmentObservation, providerErr error, now time.Time) (EffectResolution, error) {
	resolution := EffectResolution{EffectID: state.EffectID, IntentOperation: state.OperationID, Outcome: outcome, Attempts: state.Resolution.Attempts + 1, ProviderRevision: observed.Observation.Revision, ProviderValue: observed.Assignee, ProviderMarker: observed.OperationMarker, ObservedAt: now.UTC(), Diagnostic: boundedDiagnostic("compensation classified from guarded provider facts")}
	if providerErr != nil {
		resolution.Diagnostic = boundedDiagnostic(providerErr.Error())
	}
	state.Resolution = &resolution
	if _, err := c.journal.publish(ctx, journalPublication{OperationID: operationID, CommandType: "effect.compensation", ResultType: "effect-compensation-v1", AggregateKind: "external-effect", AggregateID: controljournal.AggregateID(state.EffectID), EventType: "effect.compensation_recorded", Payload: state, Result: resolution}); err != nil {
		return EffectResolution{}, err
	}
	c.mu.Lock()
	c.states[state.EffectID] = state
	c.mu.Unlock()
	if providerErr != nil {
		return resolution, providerErr
	}
	return resolution, nil
}

func coherentProviderIdentity(state effectState, o tracker.AssignmentObservation) bool {
	return o.ExternalRef.ProjectID == state.ProjectID && o.ExternalRef.WorkID == state.WorkID && o.ExternalRef.Provider == state.Provider && o.ExternalRef.ExternalID == state.ExternalID && o.ExternalRef.MetadataSchema == state.MetadataSchema && o.ExternalRef.CreatedByOperation == state.CreatedBy && o.ExternalRef.LastObservation == o.Observation.Revision && o.Observation.ExternalID == state.ExternalID && o.Observation.Revision != 0 && !o.Observation.ObservedAt.IsZero() && len(o.Observation.CanonicalFields()) != 0 && o.Assignee == o.Observation.Assignee && o.OperationMarker == o.Observation.OperationMarker
}
func coherentEffectObservation(state effectState, o tracker.AssignmentObservation) bool {
	return coherentProviderIdentity(state, o) && o.Observation.Revision == state.Resolution.ProviderRevision && o.Observation.Revision != state.ExpectedRevision && o.Assignee == state.DesiredAssignee && o.OperationMarker == state.Marker
}
func terminalEffectOutcome(o EffectOutcome) bool {
	return o == EffectApplied || o == EffectAbsent || o == EffectAttributionLost || o == EffectCompensated || o == EffectCompensationUnsafe || o == EffectReconciliationLimit
}
func sameEffectIntent(a, b effectState) bool {
	a.Resolution = nil
	b.Resolution = nil
	a.DispatchID = ""
	b.DispatchID = ""
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(aa, bb)
}

func externalRefFromState(state effectState) tracker.ExternalWorkRef {
	return tracker.ExternalWorkRef{ProjectID: state.ProjectID, WorkID: state.WorkID, Provider: state.Provider, ExternalID: state.ExternalID, MetadataSchema: state.MetadataSchema, CreatedByOperation: state.CreatedBy, LastObservation: state.ExpectedRevision}
}
func isProviderRecoverable(err error) bool {
	return tracker.IsKind(err, tracker.ErrorUnavailable) || tracker.IsKind(err, tracker.ErrorTimeout) || tracker.IsKind(err, tracker.ErrorAmbiguous)
}
func boundedDiagnostic(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 512 {
		return v[:512]
	}
	return v
}

func (c *EffectCoordinator) restore(ctx context.Context) error {
	events, err := c.journal.loadAggregateEvents(ctx, "external-effect")
	if err != nil {
		return err
	}
	for _, event := range events {
		var state effectState
		if json.Unmarshal(event.Payload(), &state) != nil || !completeEffectState(state) || event.AggregateID != controljournal.AggregateID(state.EffectID) {
			return refusal(OutcomeIndeterminate, "effect-restore", "effect event payload is malformed")
		}
		if err := c.validateEffectPublication(ctx, event, state); err != nil {
			return err
		}
		intentKey := c.journal.prefix() + "effects/" + string(state.EffectID) + "/intent"
		stored, getErr := c.journal.port.Get(ctx, intentKey)
		if getErr != nil {
			return refusal(OutcomeIndeterminate, "effect-restore", "effect event lacks its durable intent")
		}
		intent, decodeErr := controljournal.DecodeEffectIntentV1(stored.Value)
		expectedValue, _ := json.Marshal(map[string]string{"assignee": state.ExpectedAssignee})
		desiredValue, _ := json.Marshal(map[string]string{"assignee": state.DesiredAssignee})
		expectedIntent, constructErr := controljournal.NewEffectIntentV1(state.IntentID, state.EffectID, state.OperationID, state.Provider, strconv.FormatUint(uint64(state.ExpectedRevision), 10), state.Marker, expectedValue, desiredValue)
		expectedEncoded, encodeErr := controljournal.EncodeEffectIntentV1(expectedIntent)
		operationStored, operationErr := c.journal.port.Get(ctx, c.journal.prefix()+"operations/"+string(state.OperationID))
		operation, operationDecodeErr := controljournal.DecodeOperationRecordV1(operationStored.Value)
		if decodeErr != nil || constructErr != nil || encodeErr != nil || operationErr != nil || operationDecodeErr != nil || stored.Key != intentKey || stored.Revision == 0 || stored.Revision != operationStored.Revision || operation.OperationID != state.OperationID || !reflectEffectIDs(operation.EffectIDs(), []controljournal.EffectID{state.EffectID}) || !bytes.Equal(stored.Value, expectedEncoded) || intent.EffectID != expectedIntent.EffectID {
			return refusal(OutcomeIndeterminate, "effect-restore", "durable effect intent contradicts journal state")
		}
		c.states[state.EffectID] = state
	}
	return nil
}

func completeEffectState(state effectState) bool {
	return state.SchemaVersion == 1 && state.EffectID != "" && state.IntentID != "" && state.OperationID != "" && state.Provider != "" && state.ProjectID != "" && state.ExternalID != "" && state.WorkID != "" && state.MetadataSchema != 0 && state.CreatedBy != "" && state.ExpectedRevision != 0 && state.DesiredAssignee != "" && state.Marker == string(state.OperationID) && !state.OccurredAt.IsZero() && state.Authority.RuntimeEpochID != ""
}

func (c *EffectCoordinator) validateEffectPublication(ctx context.Context, event controljournal.EventEnvelopeV1, state effectState) error {
	commandType, resultType := "", ""
	var result any
	effects := []controljournal.EffectID(nil)
	switch event.EventType {
	case "effect.intent_recorded":
		commandType, resultType, result = "effect.intent", "effect-intent-v1", state
		effects = []controljournal.EffectID{state.EffectID}
	case "effect.dispatch_reserved":
		commandType, resultType, result = "effect.dispatch.reserve", "effect-dispatch-v1", state
	case "effect.observed":
		commandType, resultType, result = "effect.observation", "effect-observation-v1", state.Resolution
	case "effect.reconciliation_limited":
		commandType, resultType, result = "effect.reconciliation_limit", "effect-observation-v1", state.Resolution
	case "effect.compensation_recorded":
		commandType, resultType, result = "effect.compensation", "effect-compensation-v1", state.Resolution
	default:
		return refusal(OutcomeIndeterminate, "effect-restore", "effect event type is not admitted")
	}
	if result == nil {
		return refusal(OutcomeIndeterminate, "effect-restore", "effect result is absent")
	}
	canonicalResult, err := json.Marshal(result)
	if err != nil {
		return err
	}
	canonicalResult, err = controljournal.CanonicalJSONObject(canonicalResult)
	if err != nil {
		return err
	}
	commandPayload, _ := json.Marshal(struct {
		OperationID controljournal.OperationID `json:"operation_id"`
		Result      json.RawMessage            `json:"result"`
	}{event.OperationID, canonicalResult})
	command, err := application.ValidateCommandEnvelopeV1(application.CommandEnvelopeInputV1{SchemaVersion: 1, ProjectID: c.journal.config.ProjectID, MissionID: c.journal.config.MissionID, OperationID: event.OperationID, CommandType: commandType, RuntimeEpochID: c.journal.config.RuntimeEpochID, Payload: commandPayload})
	if err != nil {
		return err
	}
	key := c.journal.prefix() + "operations/" + string(event.OperationID)
	stored, err := c.journal.port.Get(ctx, key)
	if err != nil {
		return refusal(OutcomeIndeterminate, "effect-restore", "effect operation is unavailable")
	}
	operation, err := controljournal.DecodeOperationRecordV1(stored.Value)
	if err != nil || stored.Key != key || stored.Revision == 0 || operation.OperationID != event.OperationID || operation.CommandType != commandType || operation.RuntimeEpochID != c.journal.config.RuntimeEpochID || operation.RequestDigest != command.Digest() || operation.ResultType != resultType || !bytes.Equal(operation.CanonicalResult(), canonicalResult) || !reflectEventIDs(operation.EventIDs(), []controljournal.EventID{event.EventID}) || !reflectEffectIDs(operation.EffectIDs(), effects) {
		return refusal(OutcomeIndeterminate, "effect-restore", "effect operation membership or canonical identity is contradictory")
	}
	return nil
}

type journalPublication struct {
	OperationID             controljournal.OperationID
	CommandType, ResultType string
	AggregateKind           controljournal.AggregateKind
	AggregateID             controljournal.AggregateID
	EventType               controljournal.EventType
	Payload, Result         any
	EffectIntents           []controljournal.EffectIntentV1
	PinHead                 bool
	ExpectedLogical         uint64
	ExpectedRevision        uint64
}

type workflowJournalPort interface {
	Get(context.Context, string) (storegapdb.Record, error)
	Scan(context.Context, storegapdb.ScanRequest) (storegapdb.ScanPage, error)
}
type workflowJournal struct {
	config Config
	app    batchApplier
	port   workflowJournalPort
}

func newWorkflowJournal(config Config, app batchApplier, port workflowJournalPort) *workflowJournal {
	return &workflowJournal{config, app, port}
}
func (j *workflowJournal) prefix() string {
	return "spk/v1/" + string(j.config.ProjectID) + "/" + string(j.config.MissionID) + "/"
}

func (j *workflowJournal) publish(ctx context.Context, p journalPublication) (application.ResultV1, error) {
	if j == nil || j.app == nil || j.port == nil || p.OperationID == "" || p.AggregateKind == "" || p.AggregateID == "" {
		return application.ResultV1{}, refusal(OutcomeInvalidCommand, "journal", "complete admitted publication is required")
	}
	payload, err := json.Marshal(p.Payload)
	if err != nil {
		return application.ResultV1{}, err
	}
	payload, err = controljournal.CanonicalJSONObject(payload)
	if err != nil {
		return application.ResultV1{}, err
	}
	resultBytes, err := json.Marshal(p.Result)
	if err != nil {
		return application.ResultV1{}, err
	}
	resultBytes, err = controljournal.CanonicalJSONObject(resultBytes)
	if err != nil {
		return application.ResultV1{}, err
	}
	commandPayload, _ := json.Marshal(struct {
		OperationID controljournal.OperationID `json:"operation_id"`
		Result      json.RawMessage            `json:"result"`
	}{p.OperationID, resultBytes})
	command, err := application.ValidateCommandEnvelopeV1(application.CommandEnvelopeInputV1{SchemaVersion: 1, ProjectID: j.config.ProjectID, MissionID: j.config.MissionID, OperationID: p.OperationID, CommandType: p.CommandType, RuntimeEpochID: j.config.RuntimeEpochID, Payload: commandPayload})
	if err != nil {
		return application.ResultV1{}, err
	}
	if replay, found, replayErr := j.replay(ctx, p, command, resultBytes, payload); found || replayErr != nil {
		return replay, replayErr
	}
	headKey := j.prefix() + "heads/" + string(p.AggregateKind) + "/" + string(p.AggregateID)
	logical, revision := uint64(1), uint64(0)
	if p.PinHead {
		if p.ExpectedLogical == 0 || p.ExpectedRevision == 0 {
			return application.ResultV1{}, refusal(OutcomeInvalidCommand, p.CommandType, "pinned publication requires an exact observed aggregate head")
		}
		logical, revision = p.ExpectedLogical+1, p.ExpectedRevision
	} else if stored, getErr := j.port.Get(ctx, headKey); getErr == nil {
		head, decodeErr := controljournal.DecodeAggregateHeadV1(stored.Value)
		if decodeErr != nil || head.AggregateKind != p.AggregateKind || head.AggregateID != p.AggregateID {
			return application.ResultV1{}, refusal(OutcomeIndeterminate, "journal", "aggregate head is malformed")
		}
		logical, revision = head.LogicalVersion+1, stored.Revision
	} else if !storegapdb.IsKind(getErr, storegapdb.OutcomeNotFound) {
		return application.ResultV1{}, getErr
	}
	count, _ := controljournal.NewEventCountV1(1)
	index, _ := controljournal.NewEventIndexV1(0)
	eventID := stableIndexedEventID(p.OperationID, 0)
	event, err := controljournal.NewEventEnvelopeV1(controljournal.EventEnvelopeInputV1{EnvelopeVersion: 1, EventID: eventID, EventType: p.EventType, EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", ProjectID: j.config.ProjectID, AggregateKind: p.AggregateKind, AggregateID: p.AggregateID, AggregateLogicalVersion: logical, OperationID: p.OperationID, RuntimeEpochID: j.config.RuntimeEpochID, EventIndex: index, EventCount: count, WorkRevisionID: j.config.WorkRevisionID, PolicyRevision: "m1", DoctrineRevision: "m1", ProfileRevision: "implementer-ivan", Payload: payload})
	if err != nil {
		return application.ResultV1{}, err
	}
	head, err := controljournal.ConstructAggregateHeadV1(p.AggregateKind, p.AggregateID, logical, j.config.RuntimeEpochID, eventID, sha256.Sum256(payload))
	if err != nil {
		return application.ResultV1{}, err
	}
	expected := []controljournal.ExpectedAggregateHeadV1{{AggregateKind: p.AggregateKind, AggregateID: p.AggregateID, LogicalVersion: logical - 1, ObservedHeadRevision: revision}}
	preconditions, err := application.CanonicalPreconditionsDigestV1(expected)
	if err != nil {
		return application.ResultV1{}, err
	}
	batch, err := application.NewValidatedBatchV1(application.BatchInputV1{Command: command, ExpectedHeads: expected, NextHeads: []controljournal.NewAggregateHeadV1{{Head: head}}, Events: []controljournal.EventEnvelopeV1{event}, EffectIntents: p.EffectIntents, ExpectedPreconditionsDigest: preconditions, ResultType: p.ResultType, ResultSchemaVersion: 1, CanonicalResult: resultBytes})
	if err != nil {
		return application.ResultV1{}, err
	}
	applied, err := j.app.Apply(ctx, batch)
	if err != nil {
		return application.ResultV1{}, err
	}
	if applied.Kind != application.OutcomeCommitted && applied.Kind != application.OutcomeAlreadyCommitted {
		return applied, refusal(OutcomeAuthorityConflict, p.CommandType, "journal publication did not durably commit")
	}
	return applied, nil
}

func (j *workflowJournal) replay(ctx context.Context, p journalPublication, command application.ValidatedCommandEnvelopeV1, resultBytes, payload []byte) (application.ResultV1, bool, error) {
	key := j.prefix() + "operations/" + string(p.OperationID)
	stored, err := j.port.Get(ctx, key)
	if storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
		return application.ResultV1{}, false, nil
	}
	if err != nil {
		return application.ResultV1{}, true, err
	}
	record, err := controljournal.DecodeOperationRecordV1(stored.Value)
	if err != nil || stored.Key != key || stored.Revision == 0 {
		return application.ResultV1{}, true, refusal(OutcomeIndeterminate, p.CommandType, "stored operation is malformed")
	}
	effectIDs := make([]controljournal.EffectID, len(p.EffectIntents))
	for i, intent := range p.EffectIntents {
		effectIDs[i] = intent.EffectID
	}
	sort.Slice(effectIDs, func(i, j int) bool { return effectIDs[i] < effectIDs[j] })
	events := record.EventIDs()
	effects := record.EffectIDs()
	identityMatches := record.OperationID == p.OperationID && record.CommandType == p.CommandType && record.RuntimeEpochID == j.config.RuntimeEpochID && record.RequestDigest == command.Digest() && record.ResultType == p.ResultType && bytes.Equal(record.CanonicalResult(), resultBytes) && len(events) == 1 && events[0] == stableIndexedEventID(p.OperationID, 0) && reflectEffectIDs(effects, effectIDs)
	if !identityMatches {
		return application.ResultV1{Kind: application.OutcomeOperationIdentityCollision, OperationID: p.OperationID, Recovery: application.RecoveryOperator}, true, refusal(OutcomeAuthorityConflict, p.CommandType, "operation identity was reused for changed input")
	}
	eventKey := j.prefix() + "journal/" + string(events[0])
	eventRecord, getErr := j.port.Get(ctx, eventKey)
	if getErr != nil {
		return application.ResultV1{}, true, getErr
	}
	event, decodeErr := controljournal.DecodeEventEnvelopeV1(eventRecord.Value)
	if decodeErr != nil || eventRecord.Key != eventKey || event.OperationID != p.OperationID || event.AggregateKind != p.AggregateKind || event.AggregateID != p.AggregateID || event.EventType != p.EventType || !bytes.Equal(event.Payload(), payload) {
		return application.ResultV1{}, true, refusal(OutcomeIndeterminate, p.CommandType, "operation event membership or payload is malformed")
	}
	for _, intent := range p.EffectIntents {
		intentKey := j.prefix() + "effects/" + string(intent.EffectID) + "/intent"
		intentRecord, getErr := j.port.Get(ctx, intentKey)
		if getErr != nil {
			return application.ResultV1{}, true, refusal(OutcomeIndeterminate, p.CommandType, "operation effect intent is unavailable")
		}
		expected, encodeErr := controljournal.EncodeEffectIntentV1(intent)
		if encodeErr != nil || intentRecord.Key != intentKey || intentRecord.Revision == 0 || intentRecord.Revision != stored.Revision || !bytes.Equal(intentRecord.Value, expected) {
			return application.ResultV1{}, true, refusal(OutcomeIndeterminate, p.CommandType, "operation effect intent membership or value is malformed")
		}
	}
	return application.ResultV1{Kind: application.OutcomeAlreadyCommitted, OperationID: p.OperationID, CommittedRevision: stored.Revision, ResultType: record.ResultType, CanonicalResult: record.CanonicalResult(), Recovery: application.RecoveryNone}, true, nil
}

func (j *workflowJournal) validateOperationIdentity(ctx context.Context, event controljournal.EventEnvelopeV1, commandType, resultType string, canonicalResult []byte, effectIDs []controljournal.EffectID) error {
	result, err := controljournal.CanonicalJSONObject(canonicalResult)
	if err != nil {
		return refusal(OutcomeIndeterminate, "restore", "stored operation result is not canonical")
	}
	commandPayload, _ := json.Marshal(struct {
		OperationID controljournal.OperationID `json:"operation_id"`
		Result      json.RawMessage            `json:"result"`
	}{event.OperationID, result})
	command, err := application.ValidateCommandEnvelopeV1(application.CommandEnvelopeInputV1{SchemaVersion: 1, ProjectID: j.config.ProjectID, MissionID: j.config.MissionID, OperationID: event.OperationID, CommandType: commandType, RuntimeEpochID: j.config.RuntimeEpochID, Payload: commandPayload})
	if err != nil {
		return err
	}
	key := j.prefix() + "operations/" + string(event.OperationID)
	stored, err := j.port.Get(ctx, key)
	if err != nil {
		return refusal(OutcomeIndeterminate, "restore", "stored operation identity is unavailable")
	}
	operation, err := controljournal.DecodeOperationRecordV1(stored.Value)
	if err != nil || stored.Key != key || stored.Revision == 0 || operation.OperationID != event.OperationID || operation.CommandType != commandType || operation.RuntimeEpochID != j.config.RuntimeEpochID || operation.RequestDigest != command.Digest() || operation.ResultType != resultType || operation.ResultSchemaVersion != 1 || !bytes.Equal(operation.CanonicalResult(), result) || !reflectEventIDs(operation.EventIDs(), []controljournal.EventID{event.EventID}) || !reflectEffectIDs(operation.EffectIDs(), effectIDs) {
		return refusal(OutcomeIndeterminate, "restore", "stored operation command, result, or exact membership is contradictory")
	}
	return nil
}

func reflectEffectIDs(a, b []controljournal.EffectID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

const (
	maxWorkflowRestorePages  = 32
	maxWorkflowRestoreEvents = 16_000
	maxWorkflowRestoreBytes  = 48 << 20
)

type restoreBudget struct {
	pages  int
	events int
	bytes  int
}

func (b *restoreBudget) consume(records []storegapdb.Record, countEvents bool) error {
	b.pages++
	if b.pages > maxWorkflowRestorePages {
		return refusal(OutcomeIndeterminate, "scan", "provider scan exceeded the global page bound")
	}
	for _, record := range records {
		b.bytes += len(record.Key) + len(record.Value)
		if countEvents {
			b.events++
		}
	}
	if b.bytes > maxWorkflowRestoreBytes || b.events > maxWorkflowRestoreEvents {
		return refusal(OutcomeIndeterminate, "scan", "provider scan exceeded the global event or 48MiB byte bound")
	}
	return nil
}

func (j *workflowJournal) scanPrefixAt(ctx context.Context, prefix string, want uint64, budget *restoreBudget, countEvents bool) ([]storegapdb.Record, uint64, error) {
	var out []storegapdb.Record
	cursor := ""
	bound := want
	for {
		page, err := j.port.Scan(ctx, storegapdb.ScanRequest{Prefix: prefix, Limit: storegapdb.MaxScanRecords, Cursor: cursor, MaxBytes: storegapdb.MaxScanBytes})
		if err != nil {
			return nil, 0, err
		}
		if err := budget.consume(page.Records, countEvents); err != nil {
			return nil, 0, err
		}
		if bound == 0 {
			bound = page.ObservedRevision
		}
		if bound == 0 || page.ObservedRevision != bound {
			return nil, 0, refusal(OutcomeIndeterminate, "scan", "provider observation moved during bounded traversal")
		}
		for _, record := range page.Records {
			if record.Revision == 0 || record.Revision > bound || !strings.HasPrefix(record.Key, prefix) {
				return nil, 0, refusal(OutcomeIndeterminate, "scan", "provider returned a foreign or post-bound record")
			}
		}
		out = append(out, page.Records...)
		if !page.Truncated {
			return out, bound, nil
		}
		if page.Cursor == "" || page.Cursor == cursor {
			return nil, 0, refusal(OutcomeIndeterminate, "scan", "provider cursor did not advance")
		}
		cursor = page.Cursor
	}
}

// loadAggregateEvents reconstructs one aggregate kind from a fixed provider
// observation. It validates logical order, terminal heads, operation/event
// membership, and terminal payload digest before exposing any state.
func (j *workflowJournal) loadAggregateEvents(ctx context.Context, kind controljournal.AggregateKind) ([]controljournal.EventEnvelopeV1, error) {
	return j.loadAggregateEventsWithBudget(ctx, kind, &restoreBudget{})
}

func (j *workflowJournal) loadAggregateEventsWithBudget(ctx context.Context, kind controljournal.AggregateKind, budget *restoreBudget) ([]controljournal.EventEnvelopeV1, error) {
	journalRecords, bound, err := j.scanPrefixAt(ctx, j.prefix()+"journal/", 0, budget, true)
	if err != nil {
		return nil, err
	}
	headRecords, _, err := j.scanPrefixAt(ctx, j.prefix()+"heads/"+string(kind)+"/", bound, budget, false)
	if err != nil {
		return nil, err
	}
	byAggregate := map[controljournal.AggregateID][]controljournal.EventEnvelopeV1{}
	byOperation := map[controljournal.OperationID][]controljournal.EventEnvelopeV1{}
	operationRevisions := map[controljournal.OperationID]uint64{}
	operationEpochs := map[controljournal.OperationID]controljournal.RuntimeEpochID{}
	decoded := make([]workflowStoredEvent, 0, len(journalRecords))
	for _, stored := range journalRecords {
		event, decodeErr := controljournal.DecodeEventEnvelopeV1(stored.Value)
		wp08Kind := event.AggregateKind == "external-effect" || event.AggregateKind == "candidate" || event.AggregateKind == "review" || event.AggregateKind == "integration"
		if decodeErr != nil || stored.Key != j.prefix()+"journal/"+string(event.EventID) || stored.Revision == 0 || stored.Revision > bound || event.ProjectID != j.config.ProjectID || wp08Kind && event.WorkRevisionID != j.config.WorkRevisionID {
			return nil, refusal(OutcomeIndeterminate, "restore", "journal contains a malformed event")
		}
		decoded = append(decoded, workflowStoredEvent{record: stored, event: event})
	}
	admissibleEpochs, err := j.admissibleRuntimeEpochs(ctx, decoded, bound, budget)
	if err != nil {
		return nil, err
	}
	for _, stored := range decoded {
		event := stored.event
		if !admissibleEpochs[event.RuntimeEpochID] {
			return nil, refusal(OutcomeIndeterminate, "restore", "journal event runtime epoch is not linked to the sealed owner history")
		}
		byOperation[event.OperationID] = append(byOperation[event.OperationID], event)
		if revision, exists := operationRevisions[event.OperationID]; exists && revision != stored.record.Revision {
			return nil, refusal(OutcomeIndeterminate, "restore", "operation event group spans durable revisions")
		}
		if epoch, exists := operationEpochs[event.OperationID]; exists && epoch != event.RuntimeEpochID {
			return nil, refusal(OutcomeIndeterminate, "restore", "operation event group spans runtime epochs")
		}
		operationRevisions[event.OperationID] = stored.record.Revision
		operationEpochs[event.OperationID] = event.RuntimeEpochID
		if event.AggregateKind == kind {
			byAggregate[event.AggregateID] = append(byAggregate[event.AggregateID], event)
		}
	}
	// Seal every operation group in the observed journal, including foreign
	// aggregate kinds. A malformed unrelated event must never be ignored while
	// reconstructing a supposedly trustworthy fixed-revision projection.
	for operationID, events := range byOperation {
		sort.Slice(events, func(i, k int) bool { return events[i].EventIndex < events[k].EventIndex })
		ids := make([]controljournal.EventID, len(events))
		for index, event := range events {
			if int(event.EventIndex) != index || int(event.EventCount) != len(events) || event.OperationID != operationID {
				return nil, refusal(OutcomeIndeterminate, "restore", "operation event group is missing, extra, reordered, or malformed")
			}
			ids[index] = event.EventID
		}
		operationKey := j.prefix() + "operations/" + string(operationID)
		operationStored, getErr := j.port.Get(ctx, operationKey)
		budget.bytes += len(operationKey) + len(operationStored.Value)
		if getErr != nil || budget.bytes > maxWorkflowRestoreBytes || operationStored.Key != operationKey || operationStored.Revision == 0 || operationStored.Revision > bound || operationStored.Revision != operationRevisions[operationID] {
			return nil, refusal(OutcomeIndeterminate, "restore", "event operation is unavailable within the bounded observation")
		}
		operation, opErr := controljournal.DecodeOperationRecordV1(operationStored.Value)
		if opErr != nil || operation.OperationID != operationID || operation.RuntimeEpochID != operationEpochs[operationID] || !reflectEventIDs(operation.EventIDs(), ids) {
			return nil, refusal(OutcomeIndeterminate, "restore", "operation event membership is not exact")
		}
	}
	heads := map[controljournal.AggregateID]controljournal.AggregateHeadV1{}
	for _, stored := range headRecords {
		head, decodeErr := controljournal.DecodeAggregateHeadV1(stored.Value)
		if decodeErr != nil || head.AggregateKind != kind {
			return nil, refusal(OutcomeIndeterminate, "restore", "aggregate head is malformed")
		}
		heads[head.AggregateID] = head
	}
	var ordered []controljournal.EventEnvelopeV1
	for id, events := range byAggregate {
		sort.Slice(events, func(i, j int) bool { return events[i].AggregateLogicalVersion < events[j].AggregateLogicalVersion })
		for i, event := range events {
			if event.AggregateLogicalVersion != uint64(i+1) {
				return nil, refusal(OutcomeIndeterminate, "restore", "aggregate history is non-contiguous or duplicated")
			}
		}
		head, ok := heads[id]
		last := events[len(events)-1]
		if !ok || head.LogicalVersion != last.AggregateLogicalVersion || head.LastEventID != last.EventID || head.RuntimeEpochID != last.RuntimeEpochID || !admissibleEpochs[head.RuntimeEpochID] || head.StateDigest != sha256.Sum256(last.Payload()) {
			return nil, refusal(OutcomeIndeterminate, "restore", "aggregate head contradicts terminal journal state")
		}
		ordered = append(ordered, events...)
	}
	for id := range heads {
		if len(byAggregate[id]) == 0 {
			return nil, refusal(OutcomeIndeterminate, "restore", "aggregate head has no journal history")
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].AggregateID != ordered[j].AggregateID {
			return ordered[i].AggregateID < ordered[j].AggregateID
		}
		return ordered[i].AggregateLogicalVersion < ordered[j].AggregateLogicalVersion
	})
	return ordered, nil
}

type workflowStoredEvent struct {
	record storegapdb.Record
	event  controljournal.EventEnvelopeV1
}

// admissibleRuntimeEpochs derives the complete epoch set from the sealed
// runtime-composition aggregate. Historical events remain readable only when
// an exact owner-restart chain links them to the currently admitted epoch.
func (j *workflowJournal) admissibleRuntimeEpochs(ctx context.Context, events []workflowStoredEvent, bound uint64, budget *restoreBudget) (map[controljournal.RuntimeEpochID]bool, error) {
	restarts := make([]controljournal.EventEnvelopeV1, 0)
	for _, stored := range events {
		event := stored.event
		if event.EventType != "runtime.owner_restarted" {
			continue
		}
		if event.AggregateKind != "runtime-composition" || event.AggregateID != controljournal.AggregateID(j.config.RunID) || event.WorkRevisionID != j.config.WorkRevisionID {
			return nil, refusal(OutcomeIndeterminate, "restore", "owner restart event is foreign to the configured run")
		}
		restarts = append(restarts, event)
	}
	headRecords, _, err := j.scanPrefixAt(ctx, j.prefix()+"heads/runtime-composition/", bound, budget, false)
	if err != nil {
		return nil, err
	}
	if len(restarts) == 0 {
		if len(headRecords) != 0 {
			return nil, refusal(OutcomeIndeterminate, "restore", "runtime-composition head has no sealed restart history")
		}
		return map[controljournal.RuntimeEpochID]bool{j.config.RuntimeEpochID: true}, nil
	}
	if len(headRecords) != 1 || headRecords[0].Key != j.prefix()+"heads/runtime-composition/"+string(j.config.RunID) || headRecords[0].Revision == 0 || headRecords[0].Revision > bound {
		return nil, refusal(OutcomeIndeterminate, "restore", "runtime-composition head is absent, duplicated, or foreign")
	}
	sort.Slice(restarts, func(i, k int) bool { return restarts[i].AggregateLogicalVersion < restarts[k].AggregateLogicalVersion })
	admissible := make(map[controljournal.RuntimeEpochID]bool, len(restarts)+1)
	var previous controljournal.RuntimeEpochID
	for index, event := range restarts {
		var state ownerRestartState
		if json.Unmarshal(event.Payload(), &state) != nil || state.OperationID != event.OperationID || state.RunID != j.config.RunID || state.PredecessorEpoch == "" || state.RuntimeEpochID == "" || state.RuntimeEpochID != event.RuntimeEpochID || state.RuntimeEpochID == state.PredecessorEpoch || state.OccurredAt.IsZero() || state.CommandDigest == "" || event.AggregateLogicalVersion != uint64(index+1) {
			return nil, refusal(OutcomeIndeterminate, "restore", "runtime-composition history contains a malformed owner transition")
		}
		digest, digestErr := ownerRestartCommandDigest(state.OperationID, state.RunID, state.PredecessorEpoch, state.RuntimeEpochID, state.OccurredAt)
		if digestErr != nil || digest != state.CommandDigest || index > 0 && state.PredecessorEpoch != previous || admissible[state.RuntimeEpochID] {
			return nil, refusal(OutcomeIndeterminate, "restore", "runtime-composition history is unsealed, discontinuous, or cyclic")
		}
		admissible[state.PredecessorEpoch] = true
		admissible[state.RuntimeEpochID] = true
		previous = state.RuntimeEpochID
	}
	last := restarts[len(restarts)-1]
	head, decodeErr := controljournal.DecodeAggregateHeadV1(headRecords[0].Value)
	if decodeErr != nil || previous != j.config.RuntimeEpochID || head.AggregateKind != "runtime-composition" || head.AggregateID != controljournal.AggregateID(j.config.RunID) || head.LogicalVersion != last.AggregateLogicalVersion || head.LastEventID != last.EventID || head.RuntimeEpochID != j.config.RuntimeEpochID || head.StateDigest != sha256.Sum256(last.Payload()) {
		return nil, refusal(OutcomeIndeterminate, "restore", "runtime-composition terminal head does not seal the admitted epoch lineage")
	}
	if err := j.validateRuntimeEpochAdmissions(ctx, events, admissible, bound, budget); err != nil {
		return nil, err
	}
	return admissible, nil
}

func (j *workflowJournal) validateRuntimeEpochAdmissions(ctx context.Context, events []workflowStoredEvent, lineage map[controljournal.RuntimeEpochID]bool, bound uint64, budget *restoreBudget) error {
	admissions := make([]controljournal.EventEnvelopeV1, 0, len(lineage))
	for _, stored := range events {
		event := stored.event
		if event.EventType != "runtime.epoch_admitted" {
			continue
		}
		if event.AggregateKind != "runtime" || event.AggregateID != "host-runtime" {
			return refusal(OutcomeIndeterminate, "restore", "runtime admission event has a foreign aggregate identity")
		}
		var payload struct {
			RuntimeEpochID controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
		}
		if json.Unmarshal(event.Payload(), &payload) != nil || payload.RuntimeEpochID == "" || payload.RuntimeEpochID != event.RuntimeEpochID {
			return refusal(OutcomeIndeterminate, "restore", "runtime admission event does not bind its epoch")
		}
		admissions = append(admissions, event)
	}
	sort.Slice(admissions, func(i, k int) bool {
		return admissions[i].AggregateLogicalVersion < admissions[k].AggregateLogicalVersion
	})
	admitted := make(map[controljournal.RuntimeEpochID]bool, len(admissions))
	for index, event := range admissions {
		if event.AggregateLogicalVersion != uint64(index+1) || admitted[event.RuntimeEpochID] {
			return refusal(OutcomeIndeterminate, "restore", "runtime admission history is non-contiguous or duplicated")
		}
		admitted[event.RuntimeEpochID] = true
	}
	for epoch := range lineage {
		if !admitted[epoch] {
			return refusal(OutcomeIndeterminate, "restore", "owner history names an epoch without sealed runtime admission")
		}
	}
	headRecords, _, err := j.scanPrefixAt(ctx, j.prefix()+"heads/runtime/", bound, budget, false)
	if err != nil {
		return err
	}
	if len(admissions) == 0 || len(headRecords) != 1 || headRecords[0].Key != j.prefix()+"heads/runtime/host-runtime" || headRecords[0].Revision == 0 || headRecords[0].Revision > bound {
		return refusal(OutcomeIndeterminate, "restore", "runtime admission head is absent, duplicated, or foreign")
	}
	last := admissions[len(admissions)-1]
	head, decodeErr := controljournal.DecodeAggregateHeadV1(headRecords[0].Value)
	if decodeErr != nil || head.AggregateKind != "runtime" || head.AggregateID != "host-runtime" || head.LogicalVersion != last.AggregateLogicalVersion || head.LastEventID != last.EventID || head.RuntimeEpochID != j.config.RuntimeEpochID || last.RuntimeEpochID != j.config.RuntimeEpochID || head.StateDigest != sha256.Sum256(last.Payload()) {
		return refusal(OutcomeIndeterminate, "restore", "runtime admission head does not seal the current lineage terminal")
	}
	return nil
}
func reflectEventIDs(a, b []controljournal.EventID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func derivedOperationID(parts ...string) controljournal.OperationID {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return controljournal.OperationID("operation-" + hex.EncodeToString(h[:16]))
}

// M1EventRegistryV1 and M1ReducerRegistryV1 are the complete playback
// registries after WP08. Keeping them explicit prevents a later campaign from
// silently replaying only the authority subset.
func M1EventRegistryV1() (controljournal.EventRegistryV1, error) {
	types := []string{"effect.intent_recorded", "effect.dispatch_reserved", "effect.observed", "effect.reconciliation_limited", "effect.compensation_recorded", "candidate.created", "candidate.refused", "review.recorded", "integration.plan", "integration.plan_completed", "integration.intent", "integration.activated", "integration.workspace", "integration.revalidated", "integration.result", "integration.repair", "integration.completed", "integration.cleanup_eligible", "integration.cleanup_completed"}
	base := []controljournal.EventVersionV1{{EventType: "runtime.epoch_admitted", EventSchemaVersion: 1, ReducerVersion: "runtime-v1"}, {EventType: "authority.claimed", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1"}, {EventType: "authority.heartbeat", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1"}, {EventType: "authority.expired", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1"}, {EventType: "authority.predecessor_invalidated", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1"}, {EventType: "workspace.bound", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1"}, {EventType: "workspace.quarantined", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1"}, {EventType: "runtime.owner_restarted", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1"}}
	for _, eventType := range types {
		base = append(base, controljournal.EventVersionV1{EventType: eventType, EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1"})
	}
	return controljournal.NewEventRegistryV1(base)
}
func M1ReducerRegistryV1() (reducer.RegistryV1, error) {
	entries := []reducer.EntryV1{{AggregateKind: "runtime", EventType: "runtime.epoch_admitted", EventSchemaVersion: 1, ReducerVersion: "runtime-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1}, {AggregateKind: "authority", EventType: "authority.claimed", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1}, {AggregateKind: "authority", EventType: "authority.heartbeat", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1}, {AggregateKind: "authority", EventType: "authority.expired", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1}, {AggregateKind: "authority", EventType: "authority.predecessor_invalidated", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1}, {AggregateKind: "workspace", EventType: "workspace.bound", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1}, {AggregateKind: "workspace", EventType: "workspace.quarantined", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1}, {AggregateKind: "runtime-composition", EventType: "runtime.owner_restarted", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1}}
	for _, x := range []struct {
		k controljournal.AggregateKind
		e controljournal.EventType
	}{{"external-effect", "effect.intent_recorded"}, {"external-effect", "effect.dispatch_reserved"}, {"external-effect", "effect.observed"}, {"external-effect", "effect.reconciliation_limited"}, {"external-effect", "effect.compensation_recorded"}, {"candidate", "candidate.created"}, {"candidate", "candidate.refused"}, {"review", "review.recorded"}, {"integration", "integration.plan"}, {"integration", "integration.plan_completed"}, {"integration", "integration.intent"}, {"integration", "integration.activated"}, {"integration", "integration.workspace"}, {"integration", "integration.revalidated"}, {"integration", "integration.result"}, {"integration", "integration.repair"}, {"integration", "integration.completed"}, {"integration", "integration.cleanup_eligible"}, {"integration", "integration.cleanup_completed"}} {
		entries = append(entries, reducer.EntryV1{AggregateKind: x.k, EventType: x.e, EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1})
	}
	return reducer.NewRegistryV1(entries)
}
