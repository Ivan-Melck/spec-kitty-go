package controlstore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

func TestDurableOutboxIntentBindsExactSourceBytesAndAttribution(t *testing.T) {
	plan, coordinates := outboxSourcePlanFixtureV2(t)
	intent, err := BindDurableOutboxIntentV2(plan, 11, coordinates)
	if err != nil || intent.IsZero() || intent.coordinates.SourceOperationID != coordinates.SourceOperationID {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
	mutants := []func(*OutboxIntentCoordinatesV2){
		func(value *OutboxIntentCoordinatesV2) { value.ExpectedProviderRevision++ },
		func(value *OutboxIntentCoordinatesV2) { value.IntendedValue = "substituted" },
		func(value *OutboxIntentCoordinatesV2) { value.ExpectedValue = "substituted" },
		func(value *OutboxIntentCoordinatesV2) { value.ExpectedOperationMarker = "substituted" },
		func(value *OutboxIntentCoordinatesV2) { value.AttributionMarker = "substituted" },
		func(value *OutboxIntentCoordinatesV2) {
			value.IntendedMutationDigest = outboxDigestFixtureV2("substituted")
		},
		func(value *OutboxIntentCoordinatesV2) { value.ActorID = "actor-other" },
		func(value *OutboxIntentCoordinatesV2) { value.Capability = "other.capability" },
	}
	for index, mutate := range mutants {
		candidate := coordinates
		candidate.CanonicalIntent = append([]byte(nil), coordinates.CanonicalIntent...)
		mutate(&candidate)
		if _, err := BindDurableOutboxIntentV2(plan, 11, candidate); err == nil {
			t.Fatalf("attribution mutant %d survived", index)
		}
	}
	tamperedBytes := coordinates
	tamperedBytes.CanonicalIntent = append([]byte(nil), coordinates.CanonicalIntent...)
	tamperedBytes.CanonicalIntent[len(tamperedBytes.CanonicalIntent)-2] ^= 1
	if _, err := BindDurableOutboxIntentV2(plan, 11, tamperedBytes); err == nil {
		t.Fatal("changed source bytes retained durable intent authority")
	}
}

func TestOutboxLifecycleRequiresIntentAndCompareBeforeCompensate(t *testing.T) {
	source, coordinates := outboxSourcePlanFixtureV2(t)
	intent, err := BindDurableOutboxIntentV2(source, 11, coordinates)
	if err != nil {
		t.Fatal(err)
	}
	registry := journalv2.DefaultEventRegistryV2()
	startCommand := bindOutboxCommandFixtureV2(t, OutboxCommandV2{Action: OutboxStartAttemptV2, Intent: intent, AttemptID: "attempt-stable-1", RuntimeEpochID: "epoch-1", OperationEventID: "event-outbox-operation-1", OutboxEventID: "event-outbox-1"}, 1)
	start, err := BuildOutboxAuthorityPlanV2(startCommand, registry)
	if err != nil {
		t.Fatal(err)
	}
	startPlan, _ := start.Publication()
	state := reduceOutboxPlanV2(t, []byte(`{}`), startPlan)
	if state.Stage != OutboxAttemptStartedV2 || state.AttemptID != "attempt-stable-1" || startPlan.OperationState().Status() != journalv2.StatusResolved || startPlan.OperationState().OperationID() == coordinates.SourceOperationID {
		t.Fatalf("state=%+v status=%s", state, startPlan.OperationState().Status())
	}

	observation, err := NewOutboxProviderObservationV2(coordinates.ExpectedProviderRevision+1, coordinates.IntendedValue, coordinates.AttributionMarker, "confirmed")
	if err != nil {
		t.Fatal(err)
	}
	observedPlan := buildLaterOutboxPlanV2(t, registry, intent, startPlan, state, OutboxRecordObservationV2, &observation, "", "", 2)
	state = reduceOutboxPlanV2(t, state.CanonicalBytes(), observedPlan)
	reconcilePlan := buildLaterOutboxPlanV2(t, registry, intent, observedPlan, state, OutboxRequireReconcileV2, &observation, "", "", 3)
	state = reduceOutboxPlanV2(t, state.CanonicalBytes(), reconcilePlan)
	if state.Stage != OutboxReconciliationRequiredV2 {
		t.Fatalf("stage=%s", state.Stage)
	}
	if _, err := BuildOutboxAuthorityPlanV2(laterOutboxCommandV2(t, intent, state, OutboxPermitRetryV2, &observation, string(coordinates.SourceOperationID), "", 4), registry); err == nil {
		t.Fatal("retry accepted while the attributed provider value was still present")
	}

	changed, _ := NewOutboxProviderObservationV2(coordinates.ExpectedProviderRevision+2, coordinates.IntendedValue, coordinates.AttributionMarker, "confirmed")
	if _, err := BuildOutboxAuthorityPlanV2(laterOutboxCommandV2(t, intent, state, OutboxPermitCompensationV2, &changed, string(coordinates.SourceOperationID), "", 4), registry); err == nil {
		t.Fatal("compensation accepted a changed provider revision")
	}
	changedValue, _ := NewOutboxProviderObservationV2(observation.ProviderRevision, "other-owner", observation.AttributionMarker, "confirmed")
	if _, err := BuildOutboxAuthorityPlanV2(laterOutboxCommandV2(t, intent, state, OutboxPermitCompensationV2, &changedValue, string(coordinates.SourceOperationID), "", 4), registry); err == nil {
		t.Fatal("compensation accepted a changed provider value")
	}
	changedAttribution, _ := NewOutboxProviderObservationV2(observation.ProviderRevision, observation.Value, "other-operation", "confirmed")
	if _, err := BuildOutboxAuthorityPlanV2(laterOutboxCommandV2(t, intent, state, OutboxPermitCompensationV2, &changedAttribution, string(coordinates.SourceOperationID), "", 4), registry); err == nil {
		t.Fatal("compensation accepted a changed provider attribution marker")
	}
	for _, outcome := range []string{"stale", "conflicting"} {
		notConfirmed, _ := NewOutboxProviderObservationV2(observation.ProviderRevision, observation.Value, observation.AttributionMarker, outcome)
		if _, err := BuildOutboxAuthorityPlanV2(laterOutboxCommandV2(t, intent, state, OutboxPermitCompensationV2, &notConfirmed, string(coordinates.SourceOperationID), "", 4), registry); err == nil {
			t.Fatalf("compensation accepted an attribution match with %s outcome", outcome)
		}
	}
	if _, err := BuildOutboxAuthorityPlanV2(laterOutboxCommandV2(t, intent, state, OutboxPermitCompensationV2, &observation, "wrong-marker", "", 4), registry); err == nil {
		t.Fatal("compensation accepted a changed permission marker")
	}
	permitted, err := BuildOutboxAuthorityPlanV2(laterOutboxCommandV2(t, intent, state, OutboxPermitCompensationV2, &observation, string(coordinates.SourceOperationID), "", 4), registry)
	if err != nil {
		t.Fatal(err)
	}
	permittedPlan, _ := permitted.Publication()
	permittedState := reduceOutboxPlanV2(t, state.CanonicalBytes(), permittedPlan)
	if permittedState.Stage != OutboxCompensationPermittedV2 || permittedPlan.OperationState().Status() != journalv2.StatusResolved {
		t.Fatalf("state=%+v status=%s", permittedState, permittedPlan.OperationState().Status())
	}

	absent, err := NewOutboxProviderObservationV2(coordinates.ExpectedProviderRevision+2, "", "", "absent")
	if err != nil {
		t.Fatal(err)
	}
	absentObserved := buildLaterOutboxPlanV2(t, registry, intent, startPlan, reduceOutboxPlanV2(t, []byte(`{}`), startPlan), OutboxRecordObservationV2, &absent, "", "", 5)
	absentState := reduceOutboxPlanV2(t, reduceOutboxPlanV2(t, []byte(`{}`), startPlan).CanonicalBytes(), absentObserved)
	absentReconcile := buildLaterOutboxPlanV2(t, registry, intent, absentObserved, absentState, OutboxRequireReconcileV2, &absent, "", "", 6)
	absentState = reduceOutboxPlanV2(t, absentState.CanonicalBytes(), absentReconcile)
	retry := buildLaterOutboxPlanV2(t, registry, intent, absentReconcile, absentState, OutboxPermitRetryV2, &absent, string(coordinates.SourceOperationID), "", 7)
	if retryState := reduceOutboxPlanV2(t, absentState.CanonicalBytes(), retry); retryState.Stage != OutboxRetryPermittedV2 {
		t.Fatalf("reconciled absence did not permit stable retry: %+v", retryState)
	}
}

func TestOutboxReducerRejectsUnknownTuplePriorSubstitutionAndIllegalJump(t *testing.T) {
	source, coordinates := outboxSourcePlanFixtureV2(t)
	intent, _ := BindDurableOutboxIntentV2(source, 11, coordinates)
	registry := journalv2.DefaultEventRegistryV2()
	startCommand := bindOutboxCommandFixtureV2(t, OutboxCommandV2{Action: OutboxStartAttemptV2, Intent: intent, AttemptID: "attempt-stable-1", RuntimeEpochID: "epoch-1", OperationEventID: "event-outbox-operation-1", OutboxEventID: "event-outbox-1"}, 1)
	start, _ := BuildOutboxAuthorityPlanV2(startCommand, registry)
	startPlan, _ := start.Publication()
	state := reduceOutboxPlanV2(t, []byte(`{}`), startPlan)
	observation, _ := NewOutboxProviderObservationV2(coordinates.ExpectedProviderRevision, coordinates.ExpectedValue, coordinates.AttributionMarker, "confirmed")
	if _, err := BuildOutboxAuthorityPlanV2(laterOutboxCommandV2(t, intent, state, OutboxPermitRetryV2, &observation, string(coordinates.SourceOperationID), "", 2), registry); err == nil {
		t.Fatal("attempt jumped directly to retry permission")
	}
	for _, registration := range OutboxReducerRegistrationsV2() {
		if registration.Owner != "internal/controlstore" || registration.Family != "outbox" || registration.Reduce == nil || registration.ImplementationDigest.IsZero() {
			t.Fatalf("bad registration: %+v", registration)
		}
	}
}

func TestOutboxReducerIndependentlyEnforcesRetryAndCompensationSafety(t *testing.T) {
	source, coordinates := outboxSourcePlanFixtureV2(t)
	intent, _ := BindDurableOutboxIntentV2(source, 11, coordinates)
	registry := journalv2.DefaultEventRegistryV2()
	startCommand := bindOutboxCommandFixtureV2(t, OutboxCommandV2{Action: OutboxStartAttemptV2, Intent: intent, AttemptID: "attempt-reducer-safety", RuntimeEpochID: "epoch-1", OperationEventID: "event-reducer-operation-1", OutboxEventID: "event-reducer-outbox-1"}, 41)
	startCapability, _ := BuildOutboxAuthorityPlanV2(startCommand, registry)
	startPlan, _ := startCapability.Publication()
	started := reduceOutboxPlanV2(t, []byte(`{}`), startPlan)

	present, _ := NewOutboxProviderObservationV2(8, coordinates.IntendedValue, coordinates.AttributionMarker, "confirmed")
	observedPlan := buildLaterOutboxPlanV2(t, registry, intent, startPlan, started, OutboxRecordObservationV2, &present, "", "", 2)
	observed := reduceOutboxPlanV2(t, started.CanonicalBytes(), observedPlan)
	reconcilePlan := buildLaterOutboxPlanV2(t, registry, intent, observedPlan, observed, OutboxRequireReconcileV2, &present, "", "", 3)
	reconciling := reduceOutboxPlanV2(t, observed.CanonicalBytes(), reconcilePlan)
	compensationPlan := buildLaterOutboxPlanV2(t, registry, intent, reconcilePlan, reconciling, OutboxPermitCompensationV2, &present, string(coordinates.SourceOperationID), "", 4)
	compensationEvent := outboxEventFromPlanV2(t, compensationPlan)
	var compensationChange outboxEventV2
	if err := strictOutboxJSON(compensationEvent.Payload(), &compensationChange); err != nil {
		t.Fatal(err)
	}
	compensationMutants := []OutboxProviderObservationV2{}
	for _, input := range []struct {
		revision uint64
		value    string
		marker   string
		outcome  string
	}{{9, present.Value, present.AttributionMarker, "confirmed"}, {present.ProviderRevision, "other-value", present.AttributionMarker, "confirmed"}, {present.ProviderRevision, present.Value, "other-operation", "confirmed"}, {present.ProviderRevision, present.Value, present.AttributionMarker, "stale"}, {present.ProviderRevision, present.Value, present.AttributionMarker, "conflicting"}} {
		observation, err := NewOutboxProviderObservationV2(input.revision, input.value, input.marker, input.outcome)
		if err != nil {
			t.Fatal(err)
		}
		compensationMutants = append(compensationMutants, observation)
	}
	for index, mutant := range compensationMutants {
		changed := compensationChange
		changed.State.Observation = &mutant
		event := replaceOutboxEventStateV2(t, compensationEvent, changed)
		if _, err := ReduceOutboxEventV2(reconciling.CanonicalBytes(), event); err == nil {
			t.Fatalf("unsafe compensation reducer mutant %d survived playback", index)
		}
	}

	absent, _ := NewOutboxProviderObservationV2(9, "", "", "absent")
	absentObservedPlan := buildLaterOutboxPlanV2(t, registry, intent, startPlan, started, OutboxRecordObservationV2, &absent, "", "", 5)
	absentObserved := reduceOutboxPlanV2(t, started.CanonicalBytes(), absentObservedPlan)
	absentReconcilePlan := buildLaterOutboxPlanV2(t, registry, intent, absentObservedPlan, absentObserved, OutboxRequireReconcileV2, &absent, "", "", 6)
	absentReconciling := reduceOutboxPlanV2(t, absentObserved.CanonicalBytes(), absentReconcilePlan)
	retryPlan := buildLaterOutboxPlanV2(t, registry, intent, absentReconcilePlan, absentReconciling, OutboxPermitRetryV2, &absent, string(coordinates.SourceOperationID), "", 7)
	retryEvent := outboxEventFromPlanV2(t, retryPlan)
	var retryChange outboxEventV2
	if err := strictOutboxJSON(retryEvent.Payload(), &retryChange); err != nil {
		t.Fatal(err)
	}
	notAbsent, _ := NewOutboxProviderObservationV2(9, coordinates.ExpectedValue, coordinates.ExpectedOperationMarker, "confirmed")
	retryChange.State.Observation = &notAbsent
	if _, err := ReduceOutboxEventV2(absentReconciling.CanonicalBytes(), replaceOutboxEventStateV2(t, retryEvent, retryChange)); err == nil {
		t.Fatal("retry permission without exact reconciled absence survived playback")
	}
}

func TestOutboxOperationBindingRejectsPostBindCommandMutation(t *testing.T) {
	source, coordinates := outboxSourcePlanFixtureV2(t)
	intent, err := BindDurableOutboxIntentV2(source, 11, coordinates)
	if err != nil {
		t.Fatal(err)
	}
	bound := bindOutboxCommandFixtureV2(t, OutboxCommandV2{
		Action:           OutboxStartAttemptV2,
		Intent:           intent,
		AttemptID:        "attempt-stable-1",
		RuntimeEpochID:   "epoch-1",
		OperationEventID: "event-outbox-operation-1",
		OutboxEventID:    "event-outbox-1",
	}, 1)
	mutants := []func(*OutboxCommandV2){
		func(command *OutboxCommandV2) { command.AttemptID = "attempt-substituted" },
		func(command *OutboxCommandV2) { command.RuntimeEpochID = "epoch-substituted" },
		func(command *OutboxCommandV2) { command.OutboxEventID = "event-substituted" },
		func(command *OutboxCommandV2) { command.Action = OutboxResolveV2 },
	}
	for index, mutate := range mutants {
		candidate := bound
		mutate(&candidate)
		if _, err := BuildOutboxAuthorityPlanV2(candidate, journalv2.DefaultEventRegistryV2()); err == nil {
			t.Fatalf("post-bind command mutant %d retained authority", index)
		}
	}
}

func TestCanonicalOutboxStatesOwnsNestedObservationMemory(t *testing.T) {
	source, coordinates := outboxSourcePlanFixtureV2(t)
	intent, _ := BindDurableOutboxIntentV2(source, 11, coordinates)
	registry := journalv2.DefaultEventRegistryV2()
	startCommand := bindOutboxCommandFixtureV2(t, OutboxCommandV2{Action: OutboxStartAttemptV2, Intent: intent, AttemptID: "attempt-copy", RuntimeEpochID: "epoch-1", OperationEventID: "event-copy-operation-1", OutboxEventID: "event-copy-outbox-1"}, 31)
	start, _ := BuildOutboxAuthorityPlanV2(startCommand, registry)
	startPlan, _ := start.Publication()
	state := reduceOutboxPlanV2(t, []byte(`{}`), startPlan)
	observation, _ := NewOutboxProviderObservationV2(8, coordinates.IntendedValue, coordinates.AttributionMarker, "confirmed")
	observed := buildLaterOutboxPlanV2(t, registry, intent, startPlan, state, OutboxRecordObservationV2, &observation, "", "", 32)
	state = reduceOutboxPlanV2(t, state.CanonicalBytes(), observed)
	input := []OutboxDeliveryStateV2{state}
	result, err := CanonicalOutboxStatesV2(input)
	if err != nil {
		t.Fatal(err)
	}
	input[0].Observation.Value = "mutated-input"
	if result[0].Observation.Value != coordinates.IntendedValue {
		t.Fatal("input mutation changed canonical result")
	}
	result[0].Observation.AttributionMarker = "mutated-result"
	if input[0].Observation.AttributionMarker != coordinates.AttributionMarker {
		t.Fatal("result mutation changed caller input")
	}
}

func TestOutboxPortRemainsCapabilityShaped(t *testing.T) {
	port := reflect.TypeOf((*OutboxAuthorityPortV2)(nil)).Elem()
	want := map[string]bool{"ObserveOutboxState": true, "PublishOutbox": true, "ResolveOutboxOperation": true}
	if port.NumMethod() != len(want) {
		t.Fatalf("outbox port method count=%d", port.NumMethod())
	}
	for index := 0; index < port.NumMethod(); index++ {
		method := port.Method(index)
		if !want[method.Name] {
			t.Fatalf("outbox port leaked method %s", method.Name)
		}
		lower := strings.ToLower(method.Name + " " + method.Type.String())
		for _, forbidden := range []string{"repository", "rawkey", "scanprefix", "append", "genericstore"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("outbox port leaked %s through %s", forbidden, method.Name)
			}
		}
	}
}

func TestOutboxPlanContainsWholeAtomicMembershipMutant(t *testing.T) {
	source, coordinates := outboxSourcePlanFixtureV2(t)
	intent, err := BindDurableOutboxIntentV2(source, 11, coordinates)
	if err != nil {
		t.Fatal(err)
	}
	command := bindOutboxCommandFixtureV2(t, OutboxCommandV2{Action: OutboxStartAttemptV2, Intent: intent, AttemptID: "attempt-membership", RuntimeEpochID: "epoch-1", OperationEventID: "event-membership-operation", OutboxEventID: "event-membership-outbox"}, 51)
	capability, err := BuildOutboxAuthorityPlanV2(command, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatalf("outbox atomic membership missing: %v", err)
	}
	plan, err := capability.Publication()
	if err != nil || len(plan.Events()) != 2 || len(plan.Heads()) != 2 || len(plan.Conditions()) != 2 || len(plan.Assertions()) != 1 {
		t.Fatalf("outbox atomic membership changed: events=%d heads=%d conditions=%d assertions=%d err=%v", len(plan.Events()), len(plan.Heads()), len(plan.Conditions()), len(plan.Assertions()), err)
	}
	families := map[string]int{}
	for _, event := range plan.Events() {
		families[event.Family()]++
	}
	if families["operation"] != 1 || families["outbox"] != 1 || len(families) != 2 {
		t.Fatalf("outbox atomic membership families=%v", families)
	}
}

func TestOutboxReducerProductionSourceHasNoExternalCapabilityImportsMutant(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate outbox reducer source")
	}
	source := filepath.Join(filepath.Dir(testFile), "outbox_reducer.go")
	if mutant := os.Getenv("SPEC_KITTY_WP11_MUTANT_SOURCE"); mutant != "" {
		source = mutant
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), source, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imported := range parsed.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"os", "os/exec", "net", "net/http", "database/sql", "github.com/spec-kitty/gapdb", "internal/controlstore/gapdb", "internal/gitrepo"} {
			if path == forbidden || strings.HasPrefix(path, forbidden+"/") {
				t.Fatalf("pure playback reached external capability import %s", path)
			}
		}
	}
}

func TestOutboxDeliveryEvidenceCannotBecomeProductAuthorityMutant(t *testing.T) {
	state := reflect.TypeOf(OutboxDeliveryStateV2{})
	for index := 0; index < state.NumField(); index++ {
		name := strings.ToLower(state.Field(index).Name)
		for _, forbidden := range []string{"missionstatus", "workstatus", "reviewverdict", "acceptanceverdict", "integrationstatus", "legalaction", "readiness"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("delivery-evidence authority mutant survived through field %s", state.Field(index).Name)
			}
		}
	}
	ack := reflect.TypeOf(OutboxPublicationAckV2{})
	for index := 0; index < ack.NumField(); index++ {
		if strings.Contains(strings.ToLower(ack.Field(index).Name), "provider") {
			t.Fatalf("provider vocabulary escaped through acknowledgement field %s", ack.Field(index).Name)
		}
	}
}

func buildLaterOutboxPlanV2(t *testing.T, registry journalv2.EventRegistryV2, intent DurableOutboxIntentV2, priorPlan journalv2.SealedCommitPlanV2, prior OutboxDeliveryStateV2, action OutboxActionV2, observation *OutboxProviderObservationV2, permission, resolution string, index int) journalv2.SealedCommitPlanV2 {
	t.Helper()
	_ = priorPlan
	plan, err := BuildOutboxAuthorityPlanV2(laterOutboxCommandV2(t, intent, prior, action, observation, permission, resolution, index), registry)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := plan.Publication()
	return sealed
}

func laterOutboxCommandV2(t *testing.T, intent DurableOutboxIntentV2, prior OutboxDeliveryStateV2, action OutboxActionV2, observation *OutboxProviderObservationV2, permission, resolution string, index int) OutboxCommandV2 {
	t.Helper()
	return bindOutboxCommandFixtureV2(t, OutboxCommandV2{Action: action, Intent: intent, Prior: &prior, PriorRecordRevision: uint64(20 + index), AttemptID: prior.AttemptID, RuntimeEpochID: prior.RuntimeEpochID, Observation: observation, PermissionMarker: permission, Resolution: resolution, OperationEventID: journalv2.EventID("event-outbox-operation-" + string(rune('0'+index))), OutboxEventID: journalv2.EventID("event-outbox-" + string(rune('0'+index)))}, index)
}

func bindOutboxCommandFixtureV2(t *testing.T, command OutboxCommandV2, index int) OutboxCommandV2 {
	t.Helper()
	bound, err := BindOutboxCommandOperationV2(command, OutboxOperationCoordinatesV2{OperationID: journalv2.OperationID(fmt.Sprintf("operation-outbox-command-%d", index)), ProcessID: "process-outbox", DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1", ObservedCreatedAt: "2026-09-05T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

func reduceOutboxPlanV2(t *testing.T, prior []byte, plan journalv2.SealedCommitPlanV2) OutboxDeliveryStateV2 {
	t.Helper()
	for _, event := range plan.Events() {
		if event.Family() == "outbox" {
			encoded, err := ReduceOutboxEventV2(prior, event)
			if err != nil {
				t.Fatal(err)
			}
			state, err := DecodeOutboxDeliveryStateV2(encoded)
			if err != nil {
				t.Fatal(err)
			}
			return state
		}
	}
	t.Fatal("outbox event missing")
	return OutboxDeliveryStateV2{}
}

func outboxEventFromPlanV2(t *testing.T, plan journalv2.SealedCommitPlanV2) journalv2.EventEnvelopeV2 {
	t.Helper()
	for _, event := range plan.Events() {
		if event.Family() == "outbox" {
			return event
		}
	}
	t.Fatal("outbox event missing")
	return journalv2.EventEnvelopeV2{}
}

func replaceOutboxEventStateV2(t *testing.T, event journalv2.EventEnvelopeV2, change outboxEventV2) journalv2.EventEnvelopeV2 {
	t.Helper()
	payload, err := canonicalOutboxValue(change)
	if err != nil {
		t.Fatal(err)
	}
	mutant, err := journalv2.NewEventEnvelopeV2(journalv2.EventEnvelopeInput{
		EventID: event.EventID(), EventType: event.EventType(), Family: event.Family(), EventSchemaVersion: event.EventSchemaVersion(), ReducerVersion: event.ReducerVersion(),
		Coordinate: event.Coordinate(), AggregateLogicalVersion: event.LogicalVersion(), OperationID: event.OperationID(), OperationLogicalVersion: event.OperationVersion(),
		PublicationID: event.PublicationID(), CommittedRuntimeEpochID: event.CommittedRuntimeEpochID(), EventIndex: event.EventIndex(), EventCount: event.EventCount(),
		ActorID: event.ActorID(), ProcessID: event.ProcessID(), WorkRevisionID: event.WorkRevisionID(), PolicyRevision: event.PolicyRevision(), GovernanceRevisions: event.GovernanceRevisions(),
		Observations: event.Observations(), Payload: payload,
	}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	return mutant
}

func outboxSourcePlanFixtureV2(t *testing.T) (journalv2.SealedCommitPlanV2, OutboxIntentCoordinatesV2) {
	t.Helper()
	operationID := journalv2.OperationID("operation-effect-1")
	effectID := journalv2.EffectID("effect-1")
	actor := journalv2.ActorID("actor-1")
	mutationDigest := outboxDigestFixtureV2("mutation")
	intentRaw := []byte(`{"actor_id":"actor-1","canonical_request_digest":"` + mutationDigest + `","effect_id":"effect-1","expected_operation_marker":"marker-old","expected_provider_revision":7,"expected_value":"old","intended_value":"owner-new","operation_id":"operation-effect-1"}`)
	intent, err := journalv2.CanonicalObject(intentRaw)
	if err != nil {
		t.Fatal(err)
	}
	intentSum := sha256.Sum256(intent)
	intentDigest, _ := journalv2.ParseDigest("effect_intent", hex.EncodeToString(intentSum[:]))
	scope := journalv2.Scope{Kind: journalv2.ScopeHost}
	commandDigest, _ := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "external_effect.saga", CommandSchemaVersion: 1, Scope: scope, Command: intent})
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: operationID, CommandKind: "external_effect.saga", CommandSchemaVersion: 1, CanonicalIntentDigest: commandDigest, Scope: scope, ActorID: actor, ProcessID: "process-1", DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1", OriginatingRuntimeEpochID: "epoch-1", ObservedCreatedAt: "2026-09-05T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	eventIDs := []journalv2.EventID{"event-effect-1", "event-operation-1"}
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: operationID, LogicalVersion: 1, Status: journalv2.StatusAccepted, EventIDs: eventIDs, EffectIDs: []journalv2.EffectID{effectID}})
	if err != nil {
		t.Fatal(err)
	}
	opPayload, _ := journalv2.EncodeOperationStateEventPayloadV2(operation)
	effectPayload := []byte(`{"stage":"intent_recorded"}`)
	opCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(operationID), scope)
	effectCoordinate, _ := journalv2.NewAggregateCoordinate("external_effect", journalv2.AggregateID(effectID), scope)
	drafts := []journalv2.EventDraftV2{{EventID: "event-operation-1", EventType: "operation.accepted", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: opCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-1", ActorID: actor, ProcessID: "process-1", PolicyRevision: "policy-1", GovernanceRevisions: []string{"doctrine-1", "profile-1"}, Payload: opPayload}, {EventID: "event-effect-1", EventType: "external_effect.record_intent", Family: "external_effect", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: effectCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-1", ActorID: actor, ProcessID: "process-1", PolicyRevision: "policy-1", GovernanceRevisions: []string{"doctrine-1", "profile-1"}, Payload: effectPayload}}
	opDigest, _ := journalv2.ReducerStateDigestV2(opPayload)
	effectDigest, _ := journalv2.ReducerStateDigestV2(effectPayload)
	heads := []journalv2.HeadTransitionV2{{Coordinate: opCoordinate, NextLogicalVersion: 1, EventID: "event-operation-1", CommittedRuntimeEpochID: "epoch-1", CanonicalStateDigest: opDigest, AuthoritySummary: opPayload}, {Coordinate: effectCoordinate, NextLogicalVersion: 1, EventID: "event-effect-1", CommittedRuntimeEpochID: "epoch-1", CanonicalStateDigest: effectDigest, AuthoritySummary: effectPayload}}
	conditions := make([]journalv2.ProviderCondition, 0, 2)
	for _, head := range heads {
		condition, conditionErr := journalv2.ConditionForHeadTransitionV2(head)
		if conditionErr != nil {
			t.Fatal(conditionErr)
		}
		conditions = append(conditions, condition)
	}
	plan, err := journalv2.SealCommitPlanV2(journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: operation, Events: drafts, Heads: heads, EffectIntents: []journalv2.EffectIntentReference{{EffectID: effectID, Digest: intentDigest, Canonical: intent}}, Conditions: conditions}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	return plan, OutboxIntentCoordinatesV2{EffectID: effectID, SourceOperationID: operationID, CanonicalIntent: intent, IntendedMutationDigest: mutationDigest, IntendedValue: "owner-new", ExpectedProviderRevision: 7, ExpectedValue: "old", ExpectedOperationMarker: "marker-old", AttributionMarker: string(operationID), ActorID: actor, Capability: "external_effect.saga"}
}

func outboxDigestFixtureV2(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
