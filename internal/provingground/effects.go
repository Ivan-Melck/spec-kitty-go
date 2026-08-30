package provingground

import (
	"bytes"
	"context"
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
	ExternalID       string                     `json:"external_id"`
	WorkID           ids.WorkID                 `json:"work_id"`
	ExpectedRevision tracker.Revision           `json:"expected_revision"`
	ExpectedAssignee string                     `json:"expected_assignee"`
	DesiredAssignee  string                     `json:"desired_assignee"`
	Marker           string                     `json:"marker"`
	Diagnostic       string                     `json:"diagnostic"`
	OccurredAt       time.Time                  `json:"occurred_at"`
	Authority        authorityEvidence          `json:"authority"`
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
	c := &EffectCoordinator{runtime: runtime, journal: newWorkflowJournal(runtime.core.config, app, port), provider: provider, faults: faults.Noop{}, maxAttempts: maxAttempts, states: make(map[controljournal.EffectID]effectState)}
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
	state := effectState{SchemaVersion: 1, EffectID: request.EffectID, IntentID: request.IntentID, OperationID: request.OperationID, Provider: request.ExternalRef.Provider, ExternalID: request.ExternalRef.ExternalID, WorkID: request.ExternalRef.WorkID, ExpectedRevision: request.ExpectedRevision, ExpectedAssignee: request.ExpectedAssignee, DesiredAssignee: request.DesiredAssignee, Marker: request.Marker, Diagnostic: boundedDiagnostic(request.Diagnostic), OccurredAt: request.OccurredAt.UTC(), Authority: evidenceFromCapsule(request.Authority)}
	c.mu.Lock()
	prior, exists := c.states[request.EffectID]
	c.mu.Unlock()
	if exists && !sameEffectIntent(prior, state) {
		return EffectResolution{}, refusal(OutcomeAuthorityConflict, "effect-intent", "effect identity was reused for a different request")
	}
	if exists && prior.Resolution != nil {
		return *prior.Resolution, nil
	}
	expected, _ := json.Marshal(map[string]string{"assignee": request.ExpectedAssignee})
	desired, _ := json.Marshal(map[string]string{"assignee": request.DesiredAssignee})
	intent, err := controljournal.NewEffectIntentV1(request.IntentID, request.EffectID, request.OperationID, request.ExternalRef.Provider, strconv.FormatUint(uint64(request.ExpectedRevision), 10), request.Marker, expected, desired)
	if err != nil {
		return EffectResolution{}, refusal(OutcomeInvalidCommand, "effect-intent", "effect intent is malformed")
	}
	if _, err := c.journal.publish(ctx, journalPublication{OperationID: request.OperationID, CommandType: "effect.intent", ResultType: "effect-intent-v1", AggregateKind: "external-effect", AggregateID: controljournal.AggregateID(request.EffectID), EventType: "effect.intent_recorded", Payload: state, Result: state, EffectIntents: []controljournal.EffectIntentV1{intent}}); err != nil {
		return EffectResolution{}, err
	}
	c.mu.Lock()
	c.states[request.EffectID] = state
	c.mu.Unlock()
	if err := c.faults.Reach(ctx, "GJ-05"); err != nil {
		return EffectResolution{}, err
	}
	if exists {
		observed, observeErr := c.provider.ObserveAssignment(ctx, request.ExternalRef)
		return c.observeAndRecord(ctx, request.EffectID, observed, observeErr)
	}
	mutation, mutationErr := c.provider.ProjectAssignment(ctx, tracker.ProjectAssignmentRequest{ExternalRef: request.ExternalRef, OperationID: ids.OperationID(request.OperationID), Assignee: request.DesiredAssignee, ExpectedObservation: request.ExpectedRevision, RequiredGuarantee: tracker.AssignmentAtomicClaim})
	return c.observeAndRecord(ctx, request.EffectID, mutation.Observation, mutationErr)
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
		ref := tracker.ExternalWorkRef{ProjectID: ids.ProjectID(c.runtime.core.config.ProjectID), WorkID: state.WorkID, Provider: state.Provider, ExternalID: state.ExternalID, MetadataSchema: 1, CreatedByOperation: ids.OperationID(state.OperationID), LastObservation: state.ExpectedRevision}
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
	ref := tracker.ExternalWorkRef{ProjectID: ids.ProjectID(c.runtime.core.config.ProjectID), WorkID: state.WorkID, Provider: state.Provider, ExternalID: state.ExternalID, MetadataSchema: 1, CreatedByOperation: ids.OperationID(state.OperationID), LastObservation: state.ExpectedRevision}
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
	if mutationErr == nil && (mutation.Observation.Assignee != state.ExpectedAssignee || mutation.Observation.OperationMarker != "" || mutation.Observation.Observation.Revision == 0) {
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
	case observed.Assignee == state.DesiredAssignee && observed.OperationMarker == state.Marker:
		resolution.Outcome, resolution.Diagnostic = EffectApplied, "provider state proves the intended attributable postcondition"
	case observed.Observation.Revision == state.ExpectedRevision && observed.Assignee == state.ExpectedAssignee && observed.OperationMarker == "":
		resolution.Outcome, resolution.Diagnostic = EffectAbsent, "provider still exposes the attributable precondition"
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
	return o.ExternalRef.Provider == state.Provider && o.ExternalRef.ExternalID == state.ExternalID && o.ExternalRef.WorkID == state.WorkID && o.Observation.ExternalID == state.ExternalID && o.Observation.Revision != 0 && !o.Observation.ObservedAt.IsZero()
}
func coherentEffectObservation(state effectState, o tracker.AssignmentObservation) bool {
	return coherentProviderIdentity(state, o) && o.Observation.Revision == state.Resolution.ProviderRevision && o.Assignee == state.DesiredAssignee && o.OperationMarker == state.Marker
}
func terminalEffectOutcome(o EffectOutcome) bool {
	return o == EffectApplied || o == EffectAbsent || o == EffectAttributionLost || o == EffectCompensated || o == EffectCompensationUnsafe || o == EffectReconciliationLimit
}
func sameEffectIntent(a, b effectState) bool {
	a.Resolution = nil
	b.Resolution = nil
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(aa, bb)
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
		if json.Unmarshal(event.Payload(), &state) != nil || state.EffectID == "" {
			return refusal(OutcomeIndeterminate, "effect-restore", "effect event payload is malformed")
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
		if decodeErr != nil || constructErr != nil || encodeErr != nil || operationErr != nil || operationDecodeErr != nil || stored.Key != intentKey || stored.Revision == 0 || stored.Revision != operationStored.Revision || operation.OperationID != state.OperationID || !containsEffect(operation.EffectIDs(), state.EffectID) || !bytes.Equal(stored.Value, expectedEncoded) || intent.EffectID != expectedIntent.EffectID {
			return refusal(OutcomeIndeterminate, "effect-restore", "durable effect intent contradicts journal state")
		}
		c.states[state.EffectID] = state
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
	if stored, getErr := j.port.Get(ctx, headKey); getErr == nil {
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

func (j *workflowJournal) scanPrefixAt(ctx context.Context, prefix string, want uint64) ([]storegapdb.Record, uint64, error) {
	var out []storegapdb.Record
	cursor := ""
	bound := want
	for pageNo := 0; pageNo < 32; pageNo++ {
		page, err := j.port.Scan(ctx, storegapdb.ScanRequest{Prefix: prefix, Limit: storegapdb.MaxScanRecords, Cursor: cursor, MaxBytes: storegapdb.MaxScanBytes})
		if err != nil {
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
	return nil, 0, refusal(OutcomeIndeterminate, "scan", "provider scan exceeded the page bound")
}

// loadAggregateEvents reconstructs one aggregate kind from a fixed provider
// observation. It validates logical order, terminal heads, operation/event
// membership, and terminal payload digest before exposing any state.
func (j *workflowJournal) loadAggregateEvents(ctx context.Context, kind controljournal.AggregateKind) ([]controljournal.EventEnvelopeV1, error) {
	journalRecords, bound, err := j.scanPrefixAt(ctx, j.prefix()+"journal/", 0)
	if err != nil {
		return nil, err
	}
	headRecords, _, err := j.scanPrefixAt(ctx, j.prefix()+"heads/"+string(kind)+"/", bound)
	if err != nil {
		return nil, err
	}
	byAggregate := map[controljournal.AggregateID][]controljournal.EventEnvelopeV1{}
	for _, stored := range journalRecords {
		event, decodeErr := controljournal.DecodeEventEnvelopeV1(stored.Value)
		if decodeErr != nil {
			return nil, refusal(OutcomeIndeterminate, "restore", "journal contains a malformed event")
		}
		if event.AggregateKind != kind {
			continue
		}
		operationKey := j.prefix() + "operations/" + string(event.OperationID)
		operationStored, getErr := j.port.Get(ctx, operationKey)
		if getErr != nil || operationStored.Revision == 0 || operationStored.Revision > bound {
			return nil, refusal(OutcomeIndeterminate, "restore", "event operation is unavailable within the observation")
		}
		operation, opErr := controljournal.DecodeOperationRecordV1(operationStored.Value)
		if opErr != nil || operation.OperationID != event.OperationID || !containsEvent(operation.EventIDs(), event.EventID) {
			return nil, refusal(OutcomeIndeterminate, "restore", "event is not a member of its durable operation")
		}
		byAggregate[event.AggregateID] = append(byAggregate[event.AggregateID], event)
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
		if !ok || head.LogicalVersion != last.AggregateLogicalVersion || head.LastEventID != last.EventID || head.RuntimeEpochID != j.config.RuntimeEpochID || head.StateDigest != sha256.Sum256(last.Payload()) {
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
func containsEvent(values []controljournal.EventID, want controljournal.EventID) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsEffect(values []controljournal.EffectID, want controljournal.EffectID) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func derivedOperationID(parts ...string) controljournal.OperationID {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return controljournal.OperationID("operation-" + hex.EncodeToString(h[:16]))
}

// M1EventRegistryV1 and M1ReducerRegistryV1 are the complete playback
// registries after WP08. Keeping them explicit prevents a later campaign from
// silently replaying only the authority subset.
func M1EventRegistryV1() (controljournal.EventRegistryV1, error) {
	types := []string{"effect.intent_recorded", "effect.observed", "effect.reconciliation_limited", "effect.compensation_recorded", "candidate.created", "candidate.refused", "review.recorded", "integration.plan", "integration.plan_completed", "integration.intent", "integration.activated", "integration.workspace", "integration.revalidated", "integration.result", "integration.repair", "integration.completed", "integration.cleanup_eligible", "integration.cleanup_completed"}
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
	}{{"external-effect", "effect.intent_recorded"}, {"external-effect", "effect.observed"}, {"external-effect", "effect.reconciliation_limited"}, {"external-effect", "effect.compensation_recorded"}, {"candidate", "candidate.created"}, {"candidate", "candidate.refused"}, {"review", "review.recorded"}, {"integration", "integration.plan"}, {"integration", "integration.plan_completed"}, {"integration", "integration.intent"}, {"integration", "integration.activated"}, {"integration", "integration.workspace"}, {"integration", "integration.revalidated"}, {"integration", "integration.result"}, {"integration", "integration.repair"}, {"integration", "integration.completed"}, {"integration", "integration.cleanup_eligible"}, {"integration", "integration.cleanup_completed"}} {
		entries = append(entries, reducer.EntryV1{AggregateKind: x.k, EventType: x.e, EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1})
	}
	return reducer.NewRegistryV1(entries)
}
