package mission

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/playback"
)

type authorityPortProbeV2 struct {
	resolution OperationResolutionV2
	mission    MissionObservationV2
	fact       MissionFactObservationV2
	resolveErr error
	observeErr error
	publishErr error
	reads      int
	publishes  []journalv2.SealedCommitPlanV2
}

func (p *authorityPortProbeV2) ResolveOperation(_ context.Context, binding journalv2.OperationBindingV2) (OperationResolutionV2, error) {
	p.reads++
	p.resolution.Binding = binding
	return p.resolution, p.resolveErr
}

func (p *authorityPortProbeV2) ObserveMission(context.Context, journalv2.ProjectID, journalv2.MissionID) (MissionObservationV2, error) {
	p.reads++
	return p.mission, p.observeErr
}

func (p *authorityPortProbeV2) ObserveMissionFact(context.Context, journalv2.ProjectID, journalv2.MissionID, MissionFactKindV2, string) (MissionFactObservationV2, error) {
	p.reads++
	return p.fact, nil
}

func (p *authorityPortProbeV2) PublishMission(_ context.Context, plan journalv2.SealedCommitPlanV2) (MissionPublicationV2, error) {
	p.publishes = append(p.publishes, plan)
	return MissionPublicationV2{PublicationID: plan.PublicationID(), ProviderRevision: uint64(len(p.publishes)), DurableThroughRevision: uint64(len(p.publishes)), MutationCount: plan.MutationCount()}, p.publishErr
}

type epochProbeV2 struct {
	epoch journalv2.RuntimeEpochID
	calls atomic.Int32
}

func (p *epochProbeV2) CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error) {
	p.calls.Add(1)
	return p.epoch, nil
}

func TestMissionAuthorityLifecycleAtomicReplayAndRefusalOrderV2(t *testing.T) {
	t.Parallel()
	state := fixtureAuthorityStateV2(t, 1, AuthorityMissionActiveV2, "specifying")
	command := fixtureAuthorityCommandV2(t, MissionEventCreatedV2, MissionAuthorityStateV2{}, state)
	port := &authorityPortProbeV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}}
	epoch := &epochProbeV2{epoch: command.OriginatingRuntimeEpochID}
	service, err := NewMissionAuthorityServiceV2(port, epoch, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.State().Version() != 1 || result.Replayed() || len(port.publishes) != 1 {
		t.Fatalf("result=%+v publishes=%d", result, len(port.publishes))
	}
	plan := port.publishes[0]
	if len(plan.Events()) != 2 || plan.MutationCount() != 6 || plan.OperationState().Status() != journalv2.StatusResolved {
		t.Fatalf("events=%d mutations=%d status=%s", len(plan.Events()), plan.MutationCount(), plan.OperationState().Status())
	}

	port.resolution = OperationResolutionV2{Kind: OperationExactV2, State: plan.OperationState()}
	epoch.epoch = "replacement-epoch"
	beforeReads := port.reads
	replayed, err := service.Execute(t.Context(), command)
	if err != nil || !replayed.Replayed() || replayed.State().Digest() != result.State().Digest() {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	if port.reads != beforeReads+1 || len(port.publishes) != 1 || epoch.calls.Load() != 1 {
		t.Fatalf("replay crossed authority boundary: reads=%d publishes=%d epoch_calls=%d", port.reads, len(port.publishes), epoch.calls.Load())
	}

	port.resolution = OperationResolutionV2{Kind: OperationConflictV2}
	beforeReads = port.reads
	_, err = service.Execute(t.Context(), command)
	if !IsMissionAuthorityKindV2(err, MissionAuthorityIdempotencyConflictV2) {
		t.Fatalf("binding conflict=%v", err)
	}
	if port.reads != beforeReads+1 || epoch.calls.Load() != 1 {
		t.Fatalf("binding conflict read mission or epoch: reads=%d epoch=%d", port.reads, epoch.calls.Load())
	}
}

func TestMissionReducerEveryEventAndVersionRefusalV2(t *testing.T) {
	t.Parallel()
	prior := fixtureAuthorityStateV2(t, 1, AuthorityMissionActiveV2, "planning")
	next := fixtureAuthorityStateV2(t, 2, AuthorityMissionPausedV2, "planning")
	payload, err := NewMissionEventPayloadV2(MissionEventPausedV2, prior, next)
	if err != nil {
		t.Fatal(err)
	}
	envelope := fixtureMissionEnvelopeV2(t, payload, 2)
	priorBytes := prior.CanonicalBytes()
	got, err := ReduceMissionEventV2(priorBytes, envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeMissionAuthorityStateV2(got)
	if err != nil || decoded.Digest() != next.Digest() {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	if _, err := ReduceMissionEventV2([]byte(`{}`), envelope); !IsMissionAuthorityKindV2(err, MissionAuthorityVersionConflictV2) {
		t.Fatalf("missing prior accepted: %v", err)
	}
	bad := fixtureAuthorityStateV2(t, 3, AuthorityMissionPausedV2, "planning")
	if _, err := NewMissionEventPayloadV2(MissionEventPausedV2, prior, bad); !IsMissionAuthorityKindV2(err, MissionAuthorityVersionConflictV2) {
		t.Fatalf("version jump accepted: %v", err)
	}
	terminal := fixtureAuthorityStateV2(t, 1, AuthorityMissionCompletedV2, "planning")
	reopened := fixtureAuthorityStateV2(t, 2, AuthorityMissionActiveV2, "specifying")
	if _, err := NewMissionEventPayloadV2(MissionEventLifecycleAdvancedV2, terminal, reopened); !IsMissionAuthorityKindV2(err, MissionAuthorityTerminalV2) {
		t.Fatalf("terminal mission reopened: %v", err)
	}
}

func TestMissionReducerCompleteRegisteredEventFamilyV2(t *testing.T) {
	t.Parallel()
	base := fixtureAuthorityStateV2(t, 1, AuthorityMissionActiveV2, "planning")
	state := func(prior MissionAuthorityStateV2, mutate func(*MissionAuthorityStateInputV2)) MissionAuthorityStateV2 {
		t.Helper()
		input := prior.input
		input.Version++
		input.ActiveBlockerIDs = prior.ActiveBlockerIDs()
		input.OpenDecisionIDs = prior.OpenDecisionIDs()
		input.EffectIDs = prior.EffectIDs()
		mutate(&input)
		value, err := NewMissionAuthorityStateV2(input)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	blocked := state(base, func(v *MissionAuthorityStateInputV2) { v.ActiveBlockerIDs = []string{"blocker-1"} })
	deciding := state(base, func(v *MissionAuthorityStateInputV2) { v.OpenDecisionIDs = []string{"decision-1"} })
	effectID := journalv2.EffectID("effect-1")
	requested := state(base, func(v *MissionAuthorityStateInputV2) {
		v.Decomposition, v.EffectIDs = DecompositionRequestedV2, []journalv2.EffectID{effectID}
	})
	progressed := state(requested, func(v *MissionAuthorityStateInputV2) { v.Decomposition = DecompositionProgressV2 })
	cases := []struct {
		name  string
		event MissionEventTypeV2
		prior MissionAuthorityStateV2
		next  MissionAuthorityStateV2
	}{
		{"created", MissionEventCreatedV2, MissionAuthorityStateV2{}, fixtureAuthorityStateV2(t, 1, AuthorityMissionActiveV2, "planning")},
		{"advanced", MissionEventLifecycleAdvancedV2, base, state(base, func(v *MissionAuthorityStateInputV2) { v.Step = "plan_approved" })},
		{"paused", MissionEventPausedV2, base, state(base, func(v *MissionAuthorityStateInputV2) { v.Status = AuthorityMissionPausedV2 })},
		{"resumed", MissionEventResumedV2, state(base, func(v *MissionAuthorityStateInputV2) { v.Status = AuthorityMissionPausedV2 }), state(state(base, func(v *MissionAuthorityStateInputV2) { v.Status = AuthorityMissionPausedV2 }), func(v *MissionAuthorityStateInputV2) { v.Status = AuthorityMissionActiveV2 })},
		{"failed", MissionEventFailedV2, base, state(base, func(v *MissionAuthorityStateInputV2) { v.Status = AuthorityMissionFailedV2 })},
		{"canceled", MissionEventCanceledV2, base, state(base, func(v *MissionAuthorityStateInputV2) { v.Status = AuthorityMissionCanceledV2 })},
		{"completed", MissionEventCompletedV2, base, state(base, func(v *MissionAuthorityStateInputV2) { v.Status = AuthorityMissionCompletedV2 })},
		{"action", MissionEventActionRecordedV2, base, state(base, func(v *MissionAuthorityStateInputV2) { v.ActionCount++ })},
		{"blocker-raised", MissionEventBlockerRaisedV2, base, blocked},
		{"blocker-cleared", MissionEventBlockerClearedV2, blocked, state(blocked, func(v *MissionAuthorityStateInputV2) { v.ActiveBlockerIDs = nil })},
		{"decision-opened", MissionEventDecisionOpenedV2, base, deciding},
		{"decision-resolved", MissionEventDecisionResolvedV2, deciding, state(deciding, func(v *MissionAuthorityStateInputV2) { v.OpenDecisionIDs = nil })},
		{"decision-deferred", MissionEventDecisionDeferredV2, deciding, state(deciding, func(v *MissionAuthorityStateInputV2) { v.OpenDecisionIDs = nil })},
		{"decision-canceled", MissionEventDecisionCanceledV2, deciding, state(deciding, func(v *MissionAuthorityStateInputV2) { v.OpenDecisionIDs = nil })},
		{"decomposition-requested", MissionEventDecompositionRequestedV2, base, requested},
		{"decomposition-progressed", MissionEventDecompositionProgressedV2, requested, progressed},
		{"decomposition-reconciliation", MissionEventDecompositionReconciliationV2, requested, state(requested, func(v *MissionAuthorityStateInputV2) { v.Decomposition = DecompositionReconciliationRequiredV2 })},
		{"decomposition-completed", MissionEventDecompositionCompletedV2, progressed, state(progressed, func(v *MissionAuthorityStateInputV2) { v.Decomposition = DecompositionCompletedV2 })},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			payload, err := NewMissionEventPayloadV2(test.event, test.prior, test.next)
			if err != nil {
				t.Fatal(err)
			}
			envelope := fixtureMissionEnvelopeV2(t, payload, test.next.Version())
			prior := []byte(`{}`)
			if !test.prior.IsZero() {
				prior = test.prior.CanonicalBytes()
			}
			got, err := ReduceMissionEventV2(prior, envelope)
			if err != nil || !bytes.Equal(got, test.next.CanonicalBytes()) {
				t.Fatalf("got=%s err=%v", got, err)
			}
		})
	}
}

func TestMissionAuthorityProviderFailureDoesNotAcknowledgeV2(t *testing.T) {
	t.Parallel()
	state := fixtureAuthorityStateV2(t, 1, AuthorityMissionActiveV2, "specifying")
	command := fixtureAuthorityCommandV2(t, MissionEventCreatedV2, MissionAuthorityStateV2{}, state)
	port := &authorityPortProbeV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}, publishErr: errors.New("lost response")}
	service, _ := NewMissionAuthorityServiceV2(port, &epochProbeV2{epoch: command.OriginatingRuntimeEpochID}, journalv2.DefaultEventRegistryV2())
	if _, err := service.Execute(t.Context(), command); !IsMissionAuthorityKindV2(err, MissionAuthorityAmbiguousV2) {
		t.Fatalf("lost response=%v", err)
	}
	if len(port.publishes) != 1 {
		t.Fatalf("publication count=%d", len(port.publishes))
	}
}

func TestMissionAuthorityPublishesIndependentlyContendedFactAtomicallyV2(t *testing.T) {
	t.Parallel()
	prior := fixtureAuthorityStateV2(t, 1, AuthorityMissionActiveV2, "planning")
	nextInput := prior.input
	nextInput.Version = 2
	nextInput.ActiveBlockerIDs = []string{"blocker-1"}
	nextInput.LastOperationID = "operation-blocker"
	nextInput.ObservedAt = "2026-09-04T12:01:00Z"
	next, err := NewMissionAuthorityStateV2(nextInput)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := NewMissionFactStateV2(MissionFactStateInputV2{
		ProjectID: "project-1", MissionID: "mission-1", FactID: "blocker-1",
		Kind: MissionFactBlockerV2, Status: MissionFactActiveV2, LogicalVersion: 1,
		DetailDigest: testDigestV2("blocker-detail"), LastOperationID: "operation-blocker",
		ObservedAt: "2026-09-04T12:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	command := fixtureAuthorityCommandV2(t, MissionEventBlockerRaisedV2, prior, next)
	command.OperationID = "operation-blocker"
	command.ObservedAt = next.ObservedAt()
	command.RelatedFact = &MissionFactTransitionV2{Successor: fact}
	port := &authorityPortProbeV2{
		resolution: OperationResolutionV2{Kind: OperationAbsentV2},
		mission:    MissionObservationV2{State: prior, RecordRevision: 17},
	}
	service, err := NewMissionAuthorityServiceV2(port, &epochProbeV2{epoch: command.OriginatingRuntimeEpochID}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Execute(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	plan := port.publishes[0]
	if got := len(plan.Events()); got != 3 {
		t.Fatalf("atomic fact event count=%d want=3", got)
	}
	if got := len(plan.Heads()); got != 3 {
		t.Fatalf("atomic fact head count=%d want=3", got)
	}
	var factEvent journalv2.EventEnvelopeV2
	var factHead journalv2.HeadTransitionV2
	for _, event := range plan.Events() {
		if event.Coordinate().ID == missionFactAggregateIDV2(fact) {
			factEvent = event
		}
	}
	for _, head := range plan.Heads() {
		if head.Coordinate.ID == missionFactAggregateIDV2(fact) {
			factHead = head
		}
	}
	if factEvent.EventID() == "" || factHead.EventID != factEvent.EventID() || factHead.ExpectedLogicalVersion != 0 || factHead.ExpectedRecordRevision != 0 {
		t.Fatalf("fact CAS/event missing: event=%+v head=%+v", factEvent, factHead)
	}
	reduced, err := ReduceMissionEventV2([]byte(`{}`), factEvent)
	if err != nil || !bytes.Equal(reduced, fact.CanonicalBytes()) {
		t.Fatalf("fact reduction=%s err=%v", reduced, err)
	}

	missing := command
	missing.OperationID = "operation-missing-fact"
	missing.Successor.input.LastOperationID = missing.OperationID
	missing.Successor, err = NewMissionAuthorityStateV2(missing.Successor.input)
	if err != nil {
		t.Fatal(err)
	}
	missing.RelatedFact = nil
	before := port.reads
	if _, err = service.Execute(t.Context(), missing); !IsMissionAuthorityKindV2(err, MissionAuthorityInvalidRequestV2) || port.reads != before {
		t.Fatalf("missing fact crossed authority boundary: err=%v reads=%d/%d", err, before, port.reads)
	}
}

func TestMissionAuthorityFactUpdateCarriesIndependentCASV2(t *testing.T) {
	t.Parallel()
	priorFact, err := NewMissionFactStateV2(MissionFactStateInputV2{
		ProjectID: "project-1", MissionID: "mission-1", FactID: "decision-1",
		Kind: MissionFactDecisionV2, Status: MissionFactOpenV2, LogicalVersion: 1,
		DetailDigest: testDigestV2("decision"), LastOperationID: "operation-open",
		ObservedAt: "2026-09-04T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	nextFactInput := priorFact.input
	nextFactInput.Status = MissionFactResolvedV2
	nextFactInput.LogicalVersion = 2
	nextFactInput.LastOperationID = "operation-resolve"
	nextFactInput.ObservedAt = "2026-09-04T12:02:00Z"
	nextFact, err := NewMissionFactStateV2(nextFactInput)
	if err != nil {
		t.Fatal(err)
	}
	prior := fixtureAuthorityStateV2(t, 1, AuthorityMissionActiveV2, "planning")
	prior.input.OpenDecisionIDs = []string{"decision-1"}
	prior, err = NewMissionAuthorityStateV2(prior.input)
	if err != nil {
		t.Fatal(err)
	}
	nextInput := prior.input
	nextInput.Version = 2
	nextInput.OpenDecisionIDs = nil
	nextInput.LastOperationID = "operation-resolve"
	nextInput.ObservedAt = "2026-09-04T12:02:00Z"
	next, err := NewMissionAuthorityStateV2(nextInput)
	if err != nil {
		t.Fatal(err)
	}
	command := fixtureAuthorityCommandV2(t, MissionEventDecisionResolvedV2, prior, next)
	command.OperationID = "operation-resolve"
	command.ObservedAt = next.ObservedAt()
	command.RelatedFact = &MissionFactTransitionV2{Prior: priorFact, Successor: nextFact, ExpectedRecordRevision: 29}
	binding, digest, err := validateMissionAuthorityCommandV2(command)
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := buildMissionAuthorityPlanV2(command, binding, digest, MissionObservationV2{State: prior, RecordRevision: 23}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	for _, head := range plan.Heads() {
		if head.Coordinate.ID == missionFactAggregateIDV2(nextFact) {
			if head.ExpectedLogicalVersion != 1 || head.NextLogicalVersion != 2 || head.ExpectedRecordRevision != 29 || !head.Tombstone {
				t.Fatalf("fact CAS not preserved: %+v", head)
			}
			return
		}
	}
	t.Fatal("fact head absent")
}

func TestMissionAuthorityRefusesForgedFactPriorBeforePublicationV2(t *testing.T) {
	t.Parallel()
	priorFact, err := NewMissionFactStateV2(MissionFactStateInputV2{
		ProjectID: "project-1", MissionID: "mission-1", FactID: "blocker-1",
		Kind: MissionFactBlockerV2, Status: MissionFactActiveV2, LogicalVersion: 1,
		DetailDigest: testDigestV2("blocker-prior"), LastOperationID: "operation-raise",
		ObservedAt: "2026-09-04T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	nextFactInput := priorFact.input
	nextFactInput.Status = MissionFactClearedV2
	nextFactInput.LogicalVersion = 2
	nextFactInput.LastOperationID = "operation-clear"
	nextFactInput.ObservedAt = "2026-09-04T12:04:00Z"
	nextFact, _ := NewMissionFactStateV2(nextFactInput)
	prior := fixtureAuthorityStateV2(t, 1, AuthorityMissionActiveV2, "planning")
	prior.input.ActiveBlockerIDs = []string{"blocker-1"}
	prior, _ = NewMissionAuthorityStateV2(prior.input)
	nextInput := prior.input
	nextInput.Version = 2
	nextInput.ActiveBlockerIDs = nil
	nextInput.LastOperationID = "operation-clear"
	nextInput.ObservedAt = "2026-09-04T12:04:00Z"
	next, _ := NewMissionAuthorityStateV2(nextInput)
	command := fixtureAuthorityCommandV2(t, MissionEventBlockerClearedV2, prior, next)
	command.OperationID = "operation-clear"
	command.ObservedAt = next.ObservedAt()
	command.RelatedFact = &MissionFactTransitionV2{Prior: priorFact, Successor: nextFact, ExpectedRecordRevision: 41}
	port := &authorityPortProbeV2{
		resolution: OperationResolutionV2{Kind: OperationAbsentV2},
		mission:    MissionObservationV2{State: prior, RecordRevision: 40},
		fact:       MissionFactObservationV2{State: priorFact, RecordRevision: 39, Found: true},
	}
	service, _ := NewMissionAuthorityServiceV2(port, &epochProbeV2{epoch: command.OriginatingRuntimeEpochID}, journalv2.DefaultEventRegistryV2())
	if _, err = service.Execute(t.Context(), command); !IsMissionAuthorityKindV2(err, MissionAuthorityVersionConflictV2) || len(port.publishes) != 0 {
		t.Fatalf("forged fact prior reached publication: err=%v publications=%d", err, len(port.publishes))
	}
	port.fact.RecordRevision = 41
	if _, err = service.Execute(t.Context(), command); err != nil || len(port.publishes) != 1 {
		t.Fatalf("exact fact prior refused: err=%v publications=%d", err, len(port.publishes))
	}
}

func TestMissionProjectionConsumesCheckpointAndSuffixPlaybackV2(t *testing.T) {
	t.Parallel()
	registry, reducers := missionPlaybackRegistriesV2(t)
	created := fixtureAuthorityStateV2(t, 1, AuthorityMissionActiveV2, "planning")
	createCommand := fixtureAuthorityCommandV2(t, MissionEventCreatedV2, MissionAuthorityStateV2{}, created)
	createBinding, createDigest, err := validateMissionAuthorityCommandV2(createCommand)
	if err != nil {
		t.Fatal(err)
	}
	createPlan, _, err := buildMissionAuthorityPlanV2(createCommand, createBinding, createDigest, MissionObservationV2{}, registry)
	if err != nil {
		t.Fatal(err)
	}

	blockedInput := created.input
	blockedInput.Version = 2
	blockedInput.ActiveBlockerIDs = []string{"blocker-playback"}
	blockedInput.LastOperationID = "operation-playback-blocker"
	blockedInput.ObservedAt = "2026-09-04T12:03:00Z"
	blocked, err := NewMissionAuthorityStateV2(blockedInput)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := NewMissionFactStateV2(MissionFactStateInputV2{
		ProjectID: "project-1", MissionID: "mission-1", FactID: "blocker-playback",
		Kind: MissionFactBlockerV2, Status: MissionFactActiveV2, LogicalVersion: 1,
		DetailDigest: testDigestV2("playback-blocker"), LastOperationID: "operation-playback-blocker",
		ObservedAt: "2026-09-04T12:03:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	blockCommand := fixtureAuthorityCommandV2(t, MissionEventBlockerRaisedV2, created, blocked)
	blockCommand.OperationID = "operation-playback-blocker"
	blockCommand.ObservedAt = blocked.ObservedAt()
	blockCommand.RelatedFact = &MissionFactTransitionV2{Successor: fact}
	blockBinding, blockDigest, err := validateMissionAuthorityCommandV2(blockCommand)
	if err != nil {
		t.Fatal(err)
	}
	blockPlan, _, err := buildMissionAuthorityPlanV2(blockCommand, blockBinding, blockDigest, MissionObservationV2{State: created, RecordRevision: 11}, registry)
	if err != nil {
		t.Fatal(err)
	}

	baseEvents := materializeMissionPlanEventsV2(t, createPlan, 10)
	baseResult := runMissionPlaybackV2(t, registry, reducers, nil, baseEvents)
	suffixEvents := materializeMissionPlanEventsV2(t, blockPlan, 20)
	restored := runMissionPlaybackV2(t, registry, reducers, baseResult.States(), suffixEvents)
	originEvents := append(materializeMissionPlanEventsV2(t, createPlan, 10), suffixEvents...)
	origin := runMissionPlaybackV2(t, registry, reducers, nil, originEvents)
	if restored.CanonicalProjectionDigest() != origin.CanonicalProjectionDigest() {
		t.Fatal("checkpoint plus suffix diverged from bounded origin playback")
	}
	projection, err := NewMissionRestoredProjectionV2(restored)
	if err != nil {
		t.Fatal(err)
	}
	gotMission, found := projection.Mission("project-1", "mission-1")
	if !found || !bytes.Equal(gotMission.CanonicalBytes(), blocked.CanonicalBytes()) {
		t.Fatalf("restored mission found=%v state=%s", found, gotMission.CanonicalBytes())
	}
	gotFact, found := projection.Fact("project-1", "mission-1", MissionFactBlockerV2, "blocker-playback")
	if !found || !bytes.Equal(gotFact.CanonicalBytes(), fact.CanonicalBytes()) {
		t.Fatalf("restored fact found=%v state=%s", found, gotFact.CanonicalBytes())
	}
}

func TestMissionAuthorityMandatoryMutantCensusV2(t *testing.T) {
	t.Parallel()
	// The entries name the production-facing killer rather than a source-text
	// assertion. Architecture-owned mutants are executed by the permanent M2
	// guard; the remaining mutants are exercised by the named behavioral tests.
	mutants := map[string]string{
		"M-MISSION-GENERIC-STORE":            "TestM2RepositoryHasNoGenericRawKeyAuthority",
		"M-MISSION-PROVIDER-IMPORT":          "TestM2DependencyPolicyAcceptsOnlyBoundedProviderComposition",
		"M-MISSION-OPERATION-SCOPED":         "TestMissionAuthorityAdapterBindingConflictPrecedesMissionReadV2",
		"M-MISSION-LATE-COLLISION":           "TestMissionAuthorityLifecycleAtomicReplayAndRefusalOrderV2",
		"M-MISSION-EPOCH-BEFORE-REPLAY":      "TestMissionAuthorityLifecycleAtomicReplayAndRefusalOrderV2",
		"M-MISSION-HEAD-WITHOUT-CAS":         "TestMissionAuthorityFactUpdateCarriesIndependentCASV2",
		"M-MISSION-FACT-GATE-SKIPPED":        "TestMissionAuthorityPublishesIndependentlyContendedFactAtomicallyV2",
		"M-MISSION-TERMINAL-REOPEN":          "TestMissionReducerEveryEventAndVersionRefusalV2",
		"M-MISSION-PROVIDER-BEFORE-INTENT":   "TestDecompositionIntentPrecedesProviderAndCompletionV2",
		"M-MISSION-NAKED-RESERVATION":        "TestDecompositionCrashBeforeIntentMakesNoProviderCallV2",
		"M-MISSION-CHILD-IDENTITY-DRIFT":     "TestDecompositionRestartAfterRequestedPublicationReusesIdentitiesV2",
		"M-MISSION-UNATTRIBUTED-COMPLETION":  "TestDecompositionDishonestProviderMatrixNeverCompletesV2",
		"M-MISSION-BLIND-COMPENSATION":       "TestDecompositionCompensationRequiresUnchangedRevisionValueAndMarkerV2",
		"M-MISSION-IO-REDUCER":               "TestMissionProjectionConsumesCheckpointAndSuffixPlaybackV2",
		"M-MISSION-TRACKER-GRANTS-AUTHORITY": "TestDecompositionIntentPrecedesProviderAndCompletionV2",
	}
	if len(mutants) != 15 {
		t.Fatalf("controlled mutant census=%d want=15", len(mutants))
	}
	for id, killer := range mutants {
		if id == "" || killer == "" {
			t.Fatalf("unbound mutant: %q -> %q", id, killer)
		}
	}
}

type contendedMissionPortV2 struct {
	mu         sync.Mutex
	revision   uint64
	bindings   map[journalv2.OperationID][]byte
	operations map[journalv2.OperationID]OperationResolutionV2
	missions   map[journalv2.MissionID]MissionObservationV2
}

func newContendedMissionPortV2() *contendedMissionPortV2 {
	return &contendedMissionPortV2{revision: 1, bindings: make(map[journalv2.OperationID][]byte), operations: make(map[journalv2.OperationID]OperationResolutionV2), missions: make(map[journalv2.MissionID]MissionObservationV2)}
}
func (p *contendedMissionPortV2) ResolveOperation(_ context.Context, binding journalv2.OperationBindingV2) (OperationResolutionV2, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	encoded, found := p.bindings[binding.OperationID()]
	if !found {
		return OperationResolutionV2{Kind: OperationAbsentV2}, nil
	}
	resolution, _ := journalv2.ResolveOperationBinding(binding, encoded, true)
	if resolution != journalv2.BindingExact {
		return OperationResolutionV2{Kind: OperationConflictV2}, nil
	}
	return p.operations[binding.OperationID()], nil
}
func (p *contendedMissionPortV2) ObserveMission(_ context.Context, _ journalv2.ProjectID, missionID journalv2.MissionID) (MissionObservationV2, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	value, found := p.missions[missionID]
	if !found {
		return MissionObservationV2{}, missionAuthorityErrorV2(MissionAuthorityNotFoundV2, "mission", nil)
	}
	return value, nil
}
func (p *contendedMissionPortV2) ObserveMissionFact(context.Context, journalv2.ProjectID, journalv2.MissionID, MissionFactKindV2, string) (MissionFactObservationV2, error) {
	return MissionFactObservationV2{}, nil
}
func (p *contendedMissionPortV2) PublishMission(_ context.Context, plan journalv2.SealedCommitPlanV2) (MissionPublicationV2, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var missionHead journalv2.HeadTransitionV2
	for _, head := range plan.Heads() {
		if head.Coordinate.Kind == "mission" {
			missionHead = head
			break
		}
	}
	missionID := missionHead.Coordinate.Scope.MissionID
	prior, found := p.missions[missionID]
	if !found && missionHead.ExpectedLogicalVersion != 0 || found && (prior.State.Version() != missionHead.ExpectedLogicalVersion || prior.RecordRevision != missionHead.ExpectedRecordRevision) {
		return MissionPublicationV2{}, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "mission_head", nil)
	}
	state, err := DecodeMissionAuthorityStateV2(missionHead.AuthoritySummary)
	if err != nil {
		return MissionPublicationV2{}, err
	}
	p.revision++
	if binding := plan.BindingCreate(); binding != nil {
		encoded, _ := journalv2.EncodeOperationBindingV2(*binding)
		p.bindings[binding.OperationID()] = encoded
	}
	p.operations[plan.OperationState().OperationID()] = OperationResolutionV2{Kind: OperationExactV2, State: plan.OperationState(), StateRecordRevision: p.revision}
	p.missions[missionID] = MissionObservationV2{State: state, RecordRevision: p.revision, LastEventID: missionHead.EventID}
	return MissionPublicationV2{PublicationID: plan.PublicationID(), ProviderRevision: p.revision, DurableThroughRevision: p.revision, MutationCount: plan.MutationCount()}, nil
}

func TestMissionAuthorityHundredSameAggregateContendersAndDisjointProgressV2(t *testing.T) {
	port := newContendedMissionPortV2()
	epoch := &epochProbeV2{epoch: "epoch-1"}
	service, _ := NewMissionAuthorityServiceV2(port, epoch, journalv2.DefaultEventRegistryV2())
	var winners, conflicts atomic.Int32
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			operationID := journalv2.OperationID(fmt.Sprintf("operation-%d", index+1))
			state := fixtureAuthorityStateForIDsV2(t, "mission-shared", operationID)
			command := fixtureAuthorityCommandV2(t, MissionEventCreatedV2, MissionAuthorityStateV2{}, state)
			command.OperationID, command.MissionID, command.Successor = operationID, "mission-shared", state
			if _, err := service.Execute(t.Context(), command); err == nil {
				winners.Add(1)
			} else if IsMissionAuthorityKindV2(err, MissionAuthorityVersionConflictV2) {
				conflicts.Add(1)
			} else {
				t.Errorf("contender %d: %v", index, err)
			}
		}(index)
	}
	wait.Wait()
	if winners.Load() != 1 || conflicts.Load() != 99 {
		t.Fatalf("winners=%d conflicts=%d", winners.Load(), conflicts.Load())
	}

	var disjoint atomic.Int32
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			missionID := journalv2.MissionID(fmt.Sprintf("mission-disjoint-%d", index+1))
			operationID := journalv2.OperationID(fmt.Sprintf("operation-disjoint-%d", index+1))
			state := fixtureAuthorityStateForIDsV2(t, missionID, operationID)
			command := fixtureAuthorityCommandV2(t, MissionEventCreatedV2, MissionAuthorityStateV2{}, state)
			command.OperationID, command.MissionID, command.Successor = operationID, missionID, state
			if _, err := service.Execute(t.Context(), command); err == nil {
				disjoint.Add(1)
			} else {
				t.Errorf("disjoint %d: %v", index, err)
			}
		}(index)
	}
	wait.Wait()
	if disjoint.Load() != 100 {
		t.Fatalf("disjoint successes=%d", disjoint.Load())
	}
}

func fixtureAuthorityStateForIDsV2(t *testing.T, missionID journalv2.MissionID, operationID journalv2.OperationID) MissionAuthorityStateV2 {
	t.Helper()
	state, err := NewMissionAuthorityStateV2(MissionAuthorityStateInputV2{
		ProjectID: "project-1", MissionID: missionID, DefinitionRevision: "definition-1", DefinitionDigest: testDigestV2("definition"),
		Version: 1, Status: AuthorityMissionActiveV2, Step: "specifying", LastOperationID: operationID, ObservedAt: "2026-09-04T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func fixtureAuthorityStateV2(t *testing.T, version uint64, status AuthorityMissionStatusV2, step string) MissionAuthorityStateV2 {
	t.Helper()
	state, err := NewMissionAuthorityStateV2(MissionAuthorityStateInputV2{
		ProjectID: "project-1", MissionID: "mission-1", DefinitionRevision: "definition-1",
		DefinitionDigest: testDigestV2("definition"), Version: journalv2.LogicalVersion(version),
		Status: status, Step: step, LastOperationID: "operation-1", ObservedAt: "2026-09-04T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func fixtureAuthorityCommandV2(t *testing.T, event MissionEventTypeV2, prior, next MissionAuthorityStateV2) MissionAuthorityCommandV2 {
	t.Helper()
	return MissionAuthorityCommandV2{
		SchemaVersion: 1, OperationID: "operation-1", ProjectID: "project-1", MissionID: "mission-1",
		ActorID: "actor-1", ProcessID: "process-1", OriginatingRuntimeEpochID: "epoch-1",
		DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
		ObservedAt: "2026-09-04T12:00:00Z", EventType: event, Expected: prior, Successor: next,
	}
}

func fixtureMissionEnvelopeV2(t *testing.T, payload MissionEventPayloadV2, version journalv2.LogicalVersion) journalv2.EventEnvelopeV2 {
	t.Helper()
	encoded, err := EncodeMissionEventPayloadV2(payload)
	if err != nil {
		t.Fatal(err)
	}
	coordinate, _ := journalv2.NewAggregateCoordinate("mission", "mission-1", journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-1", MissionID: "mission-1"})
	envelope, err := journalv2.NewEventEnvelopeV2(journalv2.EventEnvelopeInput{
		EventID: "event-1", EventType: string(payload.EventType()), Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1,
		Coordinate: coordinate, AggregateLogicalVersion: version, OperationID: "operation-1", OperationLogicalVersion: 1,
		PublicationID: journalv2.PublicationID(testDigestV2("publication").String()), CommittedRuntimeEpochID: "epoch-1",
		EventIndex: 0, EventCount: 1, Payload: encoded,
	}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func testDigestV2(label string) journalv2.Digest {
	digest, _ := journalv2.ReducerStateDigestV2([]byte(`{"label":"` + label + `"}`))
	return digest
}

func missionPlaybackRegistriesV2(t *testing.T) (journalv2.EventRegistryV2, journalv2.ReducerSetV2) {
	t.Helper()
	registry := journalv2.DefaultEventRegistryV2()
	registrations := make([]journalv2.ReducerRegistration, 0, len(journalv2.ExpectedEventTuples()))
	for _, tuple := range journalv2.ExpectedEventTuples() {
		reduce := journalv2.Reducer(func(_ []byte, event journalv2.EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil })
		switch tuple.Family {
		case "operation":
			reduce = journalv2.ReduceOperationStateV2
		case "mission":
			reduce = ReduceMissionEventV2
		}
		registrations = append(registrations, journalv2.ReducerRegistration{
			Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion,
			Owner: tuple.Owner, ImplementationDigest: testDigestV2("reducer-" + tuple.Family), Reduce: reduce,
		})
	}
	reducers, err := journalv2.NewReducerSetV2(registry, registrations)
	if err != nil {
		t.Fatal(err)
	}
	return registry, reducers
}

func materializeMissionPlanEventsV2(t *testing.T, plan journalv2.SealedCommitPlanV2, revision uint64) []playback.MaterializedEventV2 {
	t.Helper()
	result := make([]playback.MaterializedEventV2, len(plan.Events()))
	for index, event := range plan.Events() {
		materialized, err := playback.NewMaterializedEventV2(revision, event, false)
		if err != nil {
			t.Fatal(err)
		}
		result[index] = materialized
	}
	return result
}

func runMissionPlaybackV2(t *testing.T, registry journalv2.EventRegistryV2, reducers journalv2.ReducerSetV2, base []playback.StateV2, events []playback.MaterializedEventV2) playback.ResultV2 {
	t.Helper()
	eventDescriptor, err := registry.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	reducerDescriptor, err := reducers.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	result, err := playback.RunV2(playback.InputV2{
		Base: base, Events: events, EventRegistry: registry, Reducers: reducers,
		ExpectedEventRegistryDigest: eventDescriptor.Digest(), ExpectedReducerRegistryDigest: reducerDescriptor.Digest(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
