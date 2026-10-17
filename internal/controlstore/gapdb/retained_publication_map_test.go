package gapdb

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// These observations are fixture-port responses, not native provider receipts.
// The ACKs themselves are genuine opaque values minted ONLY by the coordinator.
type retainedFixturePortV2 struct{ revision uint64 }

func (p *retainedFixturePortV2) Commit(_ context.Context, plan journalv2.SealedCommitPlanV2) (journalv2.CommitObservationV2, error) {
	return journalv2.NewCommitObservationWithAssertionsV2(plan.PublicationID(), plan.Digest(), plan.RecordSetDigest(), p.revision, p.revision+1, plan.MutationCount(), plan.AssertionCount())
}

func retainedCoordinatorV2(t *testing.T, port journalv2.DurablePublicationPortV2) *journalv2.PublicationCoordinatorV2 {
	t.Helper()
	registry := journalv2.DefaultEventRegistryV2()
	var registrations []journalv2.ReducerRegistration
	for _, tuple := range journalv2.ExpectedEventTuples() {
		reduce := journalv2.Reducer(func(_ []byte, event journalv2.EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil })
		if tuple.Family == "operation" {
			reduce = journalv2.ReduceOperationStateV2
		}
		registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: v2TestDigest(tuple.Family), Reduce: reduce})
	}
	reducers, err := journalv2.NewReducerSetV2(registry, registrations)
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(port, tracker)
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

// Both first and later plans have actual reducer digests, not the older codec
// fixture's synthetic head digests. All ten roles occur in the first plan.
func retainedFixtureInputV2(t *testing.T, label string, prior *journalv2.OperationStateV2, predecessor uint64) journalv2.CommitPlanInput {
	t.Helper()
	op := journalv2.OperationID("retained-op-" + label)
	mission := journalv2.MissionID("retained-mission-" + label)
	version := journalv2.LogicalVersion(1)
	status := journalv2.StatusAccepted
	if prior != nil {
		version = prior.LogicalVersion() + 1
		status = journalv2.StatusInProgress
	}
	opEvent := journalv2.EventID(fmt.Sprintf("retained-op-event-%s-%d", label, version))
	missionEvent := journalv2.EventID(fmt.Sprintf("retained-mission-event-%s-%d", label, version))
	effect := journalv2.EffectID("retained-effect-" + label)
	events := []journalv2.EventID{opEvent, missionEvent}
	if prior != nil {
		events = append(events, prior.EventIDs()...)
	}
	slices.Sort(events)
	state, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: op, LogicalVersion: version, Status: status, EventIDs: events, EffectIDs: []journalv2.EffectID{effect}})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := journalv2.EncodeOperationStateEventPayloadV2(state)
	if err != nil {
		t.Fatal(err)
	}
	opCoordinate, err := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(op), journalv2.Scope{Kind: journalv2.ScopeHost})
	if err != nil {
		t.Fatal(err)
	}
	missionCoordinate, err := journalv2.NewAggregateCoordinate("mission", journalv2.AggregateID(mission), journalv2.Scope{Kind: journalv2.ScopeHost})
	if err != nil {
		t.Fatal(err)
	}
	missionPayload := []byte(fmt.Sprintf(`{"stage":%d}`, version))
	opDigest, err := journalv2.ReducerStateDigestV2(payload)
	if err != nil {
		t.Fatal(err)
	}
	missionDigest, err := journalv2.ReducerStateDigestV2(missionPayload)
	if err != nil {
		t.Fatal(err)
	}
	opType := "operation.accepted"
	if prior != nil {
		opType = "operation.progressed"
	}
	heads := []journalv2.HeadTransitionV2{
		{Coordinate: opCoordinate, ExpectedLogicalVersion: version - 1, NextLogicalVersion: version, EventID: opEvent, ExpectedRecordRevision: predecessor, CommittedRuntimeEpochID: "retained-epoch", CanonicalStateDigest: opDigest, AuthoritySummary: payload},
		{Coordinate: missionCoordinate, ExpectedLogicalVersion: version - 1, NextLogicalVersion: version, EventID: missionEvent, ExpectedRecordRevision: predecessor, CommittedRuntimeEpochID: "retained-epoch", CanonicalStateDigest: missionDigest, AuthoritySummary: missionPayload},
	}
	guards := []journalv2.EligibilityGuardTransition{{MissionID: mission, ShardID: "retained-shard", ExpectedRecordRevision: predecessor, Canonical: missionPayload}}
	checkpointID := journalv2.CheckpointID(fmt.Sprintf("retained-checkpoint-%s-%d", label, version))
	checkpoints := []journalv2.CheckpointRecord{{CheckpointID: checkpointID, Canonical: missionPayload, SlotID: journalv2.CheckpointSlotID("retained-slot-" + label), SlotCanonical: []byte(fmt.Sprintf(`{"checkpoint_id":%q}`, checkpointID)), ExpectedSlotRecordRevision: predecessor}}
	input := journalv2.CommitPlanInput{PriorOperationState: prior, OperationState: state,
		Events: []journalv2.EventDraftV2{
			{EventID: opEvent, EventType: opType, Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: opCoordinate, AggregateLogicalVersion: version, CommittedRuntimeEpochID: "retained-epoch", Payload: payload},
			{EventID: missionEvent, EventType: "mission.changed", Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: missionCoordinate, AggregateLogicalVersion: version, CommittedRuntimeEpochID: "retained-epoch", Payload: missionPayload},
		}, Heads: heads, EligibilityGuards: guards, Checkpoints: checkpoints,
		Conditions: v2TestConditions(t, heads, guards, checkpoints),
	}
	if prior == nil {
		binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: op, CommandKind: "retained.proof", CommandSchemaVersion: 1, CanonicalIntentDigest: v2TestDigest(label), Scope: journalv2.Scope{Kind: journalv2.ScopeHost}, ActorID: "retained-actor", ProcessID: "retained-process", OriginatingRuntimeEpochID: "retained-epoch", ObservedCreatedAt: "2026-09-07T00:00:00Z"})
		if err != nil {
			t.Fatal(err)
		}
		claim, err := journalv2.NewWorkspacePathClaimV2(v2TestDigest("retained-claim-"+label), []byte(fmt.Sprintf(`{"workspace":%q}`, label)))
		if err != nil {
			t.Fatal(err)
		}
		input.OperationBinding = &binding
		input.WorkspacePathClaim = &claim
		input.EffectIntents = []journalv2.EffectIntentReference{{EffectID: effect, Digest: v2TestDigest("effect-" + label), Canonical: []byte(`{"intent":"keep exactly"}`)}}
	}
	return input
}

func retainedPublishV2(t *testing.T, coordinator *journalv2.PublicationCoordinatorV2, input journalv2.CommitPlanInput) journalv2.AcknowledgedPublicationV2 {
	t.Helper()
	plan, err := journalv2.SealCommitPlanV2(input, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	ack, err := coordinator.Publish(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	return ack
}
func retainedACKV2(t *testing.T, label string, revision uint64) journalv2.AcknowledgedPublicationV2 {
	t.Helper()
	return retainedPublishV2(t, retainedCoordinatorV2(t, &retainedFixturePortV2{revision}), retainedFixtureInputV2(t, label, nil, 0))
}

// This independent copy oracle runs OUTSIDE the builder/meter allocation scope.
type retainedOracleMapV2 map[string]retainedPublicationEntryV2

func retainedDenseOracleViewV2(rows retainedPublicationMapV2) retainedOracleMapV2 {
	if rows == nil {
		return nil
	}
	view := make(retainedOracleMapV2, len(rows))
	for _, row := range rows {
		view[row.key] = row.retainedPublicationEntryV2
	}
	return view
}

func retainedCopiedOracleV2(t *testing.T, acks []journalv2.AcknowledgedPublicationV2) retainedOracleMapV2 {
	t.Helper()
	want := make(retainedOracleMapV2)
	chronological := append([]journalv2.AcknowledgedPublicationV2(nil), acks...)
	slices.SortFunc(chronological, func(a, b journalv2.AcknowledgedPublicationV2) int {
		if a.ProviderRevision() < b.ProviderRevision() {
			return -1
		}
		if a.ProviderRevision() > b.ProviderRevision() {
			return 1
		}
		return 0
	})
	for _, ack := range chronological {
		for _, record := range ack.Plan().Records() {
			key, err := v2KeyForSealedRecord(record)
			if err != nil {
				t.Fatal(err)
			}
			next := retainedPublicationEntryV2{record.CanonicalValue(), ack.ProviderRevision(), record.Role(), record.Target()}
			prior, exists := want[key.String()]
			condition := record.Condition()
			if !exists {
				if condition.Kind() != journalv2.ConditionAbsent || condition.ObservedRevision() != 0 {
					t.Fatal("oracle first condition")
				}
			} else {
				// Independent chronological map law, not production collapse.
				mutable := next.role == journalv2.RecordOperationHead || next.role == journalv2.RecordAggregateHead || next.role == journalv2.RecordEligibilityGuard || next.role == journalv2.RecordCheckpointSlot
				if !mutable || prior.role != next.role || prior.target != next.target || prior.revision >= next.revision || condition.Kind() != journalv2.ConditionRecordRevision || condition.ObservedRevision() != prior.revision {
					t.Fatal("oracle successor condition")
				}
			}
			want[key.String()] = next
		}
	}
	return want
}

func TestRetainedPublicationMapAllRolesExactBytesAndIsolation(t *testing.T) {
	ack := retainedACKV2(t, "all-roles", 17)
	want := retainedCopiedOracleV2(t, []journalv2.AcknowledgedPublicationV2{ack})
	got, err := buildRetainedPublicationMapV2(t.Context(), []journalv2.AcknowledgedPublicationV2{ack})
	if err != nil || !reflect.DeepEqual(retainedDenseOracleViewV2(got), want) || len(got) != 11 || got.maxProviderRevision() != 17 {
		t.Fatalf("inexact map: %v", err)
	}
	roles := make(map[journalv2.SealedRecordRole]bool)
	for i, entry := range got {
		roles[entry.role] = true
		entry.canonical[0] = '!'
		got[i] = retainedPublicationOccurrenceV2{}
	}
	if len(roles) != 10 {
		t.Fatal("missing role")
	}
	again, err := buildRetainedPublicationMapV2(t.Context(), []journalv2.AcknowledgedPublicationV2{ack})
	if err != nil || !reflect.DeepEqual(retainedDenseOracleViewV2(again), want) {
		t.Fatal("map aliased ACK storage")
	}
}

func TestRetainedPublicationMapChainedMutablesReverseInputAndHistory(t *testing.T) {
	port := &retainedFixturePortV2{7}
	coordinator := retainedCoordinatorV2(t, port)
	first := retainedPublishV2(t, coordinator, retainedFixtureInputV2(t, "chain", nil, 0))
	prior := first.Plan().OperationState()
	port.revision = 29 // noncontiguous provider revisions are legal
	second := retainedPublishV2(t, coordinator, retainedFixtureInputV2(t, "chain", &prior, 7))
	prior = second.Plan().OperationState()
	port.revision = 81
	third := retainedPublishV2(t, coordinator, retainedFixtureInputV2(t, "chain", &prior, 29))
	forward := []journalv2.AcknowledgedPublicationV2{first, second, third}
	reverse := []journalv2.AcknowledgedPublicationV2{third, second, first}
	before := append([]journalv2.AcknowledgedPublicationV2(nil), reverse...)
	want := retainedCopiedOracleV2(t, forward)
	for _, input := range [][]journalv2.AcknowledgedPublicationV2{forward, reverse} {
		got, err := buildRetainedPublicationMapV2(t.Context(), input)
		if err != nil || !reflect.DeepEqual(retainedDenseOracleViewV2(got), want) || got.maxProviderRevision() != 81 || len(got) != 19 {
			t.Fatalf("history lost: %v rows=%d", err, len(got))
		}
	}
	if !reflect.DeepEqual(reverse, before) {
		t.Fatal("caller array reordered")
	}
	old := retainedCopiedOracleV2(t, forward[:1])
	for key, entry := range old {
		final := want[key]
		switch entry.role {
		case journalv2.RecordOperationHead, journalv2.RecordAggregateHead, journalv2.RecordEligibilityGuard, journalv2.RecordCheckpointSlot:
			if final.revision != 81 || bytes.Equal(final.canonical, entry.canonical) {
				t.Fatal("mutable old value survived")
			}
		default:
			if !reflect.DeepEqual(final, entry) {
				t.Fatal("immutable history rewritten")
			}
		}
	}
	got, err := buildRetainedPublicationMapV2(t.Context(), []journalv2.AcknowledgedPublicationV2{first, third})
	if got != nil || err != errRetainedPublicationMapV2 {
		t.Fatal("uncollected predecessor accepted")
	}
}

func TestRetainedPublicationMapOpaqueRefusals(t *testing.T) {
	first := retainedACKV2(t, "refusals", 7)
	// A new coordinator legitimately mints the same sealed publication at another
	// fixture revision: this does not forge or purport real provider history.
	samePublication := retainedACKV2(t, "refusals", 9)
	sameRevision := retainedACKV2(t, "other", 7)
	port := &retainedFixturePortV2{11}
	input := retainedFixtureInputV2(t, "immutable-duplicate", nil, 0)
	input.WorkspacePathClaim = first.Plan().WorkspacePathClaim()
	duplicateClaim := retainedPublishV2(t, retainedCoordinatorV2(t, port), input)
	for name, acks := range map[string][]journalv2.AcknowledgedPublicationV2{
		"zero": {{}}, "zero_after_valid": {first, {}}, "duplicate_ack": {first, first},
		"duplicate_publication": {first, samePublication}, "duplicate_revision": {first, sameRevision},
		"immutable_identical_claim": {first, duplicateClaim},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := buildRetainedPublicationMapV2(t.Context(), acks)
			if got != nil || err != errRetainedPublicationMapV2 || err.Error() != "retained publication map refused" {
				t.Fatal("partial or disclosing refusal")
			}
		})
	}
	for _, ctx := range []context.Context{nil, func() context.Context { ctx, cancel := context.WithCancel(t.Context()); cancel(); return ctx }()} {
		got, err := buildRetainedPublicationMapV2(ctx, []journalv2.AcknowledgedPublicationV2{first})
		if got != nil || err != errRetainedPublicationMapV2 {
			t.Fatal("invalid context accepted")
		}
	}
	empty, err := buildRetainedPublicationMapV2(t.Context(), nil)
	if err != nil || len(empty) != 0 || empty.maxProviderRevision() != 0 {
		t.Fatal("empty map semantics")
	}
}

type retainedCancelContextV2 struct {
	context.Context
	calls, cancelAt int
}

func (c *retainedCancelContextV2) Err() error {
	c.calls++
	if c.calls >= c.cancelAt {
		return context.Canceled
	}
	return nil
}
func TestRetainedPublicationMapCancellationAtEveryCheckNoPartial(t *testing.T) {
	acks := []journalv2.AcknowledgedPublicationV2{retainedACKV2(t, "cancel-a", 1), retainedACKV2(t, "cancel-b", 2)}
	records := 0
	for _, ack := range acks {
		footprint, err := ack.Plan().MeasureRecordFootprintV2(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		records += int(footprint.RecordCount)
	}
	levels := 0
	for width := 1; width < records; width *= 2 {
		levels++
	}
	// Derived checkpoint bound, not a new arbitrary work cap: preflight
	// 2+3p+r, materialization p+r+1, merge <=rL+2(r+L), collapse r+4.
	maxChecks := 7 + 4*len(acks) + 5*records + records*levels + 2*levels
	for at := 1; ; at++ {
		ctx := &retainedCancelContextV2{Context: t.Context(), cancelAt: at}
		got, err := buildRetainedPublicationMapV2(ctx, acks)
		if err == nil {
			if at < 30 || len(got) != 22 {
				t.Fatal("did not traverse clone and final cancellation checks")
			}
			break
		}
		if got != nil || err != errRetainedPublicationMapV2 {
			t.Fatal("partial cancellation")
		}
		if at > maxChecks {
			t.Fatal("unbounded checks")
		}
	}
}

// Test-only adapter for historical PRIVATE structural controls. The independent
// expected-map oracle above never calls this new production geometry path.
func retainPublicationRecordV2(m retainedOracleMapV2, key string, next retainedPublicationEntryV2, kind journalv2.ConditionKind, predecessor uint64) bool {
	rows := make(retainedPublicationMapV2, len(m)+1)
	i := 0
	for k, entry := range m {
		rows[i] = retainedPublicationOccurrenceV2{k, entry, journalv2.ConditionAbsent, 0}
		i++
	}
	rows[i] = retainedPublicationOccurrenceV2{key, next, kind, predecessor}
	for _, row := range rows {
		if !validRetainedPublicationOccurrenceV2(row) {
			return false
		}
	}
	got, err := sortCollapseRetainedPublicationsV2(context.Background(), rows, make(retainedPublicationMapV2, len(rows)), nil)
	if err != nil {
		return false
	}
	for _, row := range got {
		m[row.key] = row.retainedPublicationEntryV2
	}
	return true
}

func TestRetainedPublicationMapStructuralIngestionLawsNotACKAuthority(t *testing.T) {
	// These are PRIVATE metadata mutations, explicitly NOT forged valid opaque
	// ACKs, seals or provider receipts. They isolate impossible-to-mint negatives.
	ack := retainedACKV2(t, "laws", 7)
	var mutable, immutable retainedPublicationEntryV2
	var mutableKey, immutableKey string
	for key, entry := range retainedCopiedOracleV2(t, []journalv2.AcknowledgedPublicationV2{ack}) {
		if entry.role == journalv2.RecordOperationHead {
			mutableKey, mutable = key, entry
		}
		if entry.role == journalv2.RecordWorkspacePathClaim {
			immutableKey, immutable = key, entry
		}
	}
	for _, test := range []struct {
		name   string
		change func(retainedOracleMapV2) bool
	}{
		{"absent_overwrite", func(m retainedOracleMapV2) bool {
			n := mutable
			n.revision = 9
			return retainPublicationRecordV2(m, mutableKey, n, journalv2.ConditionAbsent, 0)
		}},
		{"absent_nonzero", func(m retainedOracleMapV2) bool {
			return retainPublicationRecordV2(m, immutableKey, immutable, journalv2.ConditionAbsent, 7)
		}},
		{"missing_predecessor", func(m retainedOracleMapV2) bool {
			n := immutable
			n.role = journalv2.RecordCheckpointSlot
			n.revision = 9
			return retainPublicationRecordV2(m, immutableKey, n, journalv2.ConditionRecordRevision, 7)
		}},
		{"wrong_predecessor", func(m retainedOracleMapV2) bool {
			n := mutable
			n.revision = 9
			return retainPublicationRecordV2(m, mutableKey, n, journalv2.ConditionRecordRevision, 6)
		}},
		{"zero_predecessor", func(m retainedOracleMapV2) bool {
			n := mutable
			n.revision = 9
			return retainPublicationRecordV2(m, mutableKey, n, journalv2.ConditionRecordRevision, 0)
		}},
		{"revision_regression", func(m retainedOracleMapV2) bool {
			n := mutable
			n.revision = 6
			return retainPublicationRecordV2(m, mutableKey, n, journalv2.ConditionRecordRevision, 7)
		}},
		{"same_revision", func(m retainedOracleMapV2) bool {
			return retainPublicationRecordV2(m, mutableKey, mutable, journalv2.ConditionRecordRevision, 7)
		}},
		{"role_alias", func(m retainedOracleMapV2) bool {
			n := mutable
			n.role = journalv2.RecordAggregateHead
			n.revision = 9
			return retainPublicationRecordV2(m, mutableKey, n, journalv2.ConditionRecordRevision, 7)
		}},
		{"target_alias", func(m retainedOracleMapV2) bool {
			n := mutable
			n.target = immutable.target
			n.revision = 9
			return retainPublicationRecordV2(m, mutableKey, n, journalv2.ConditionRecordRevision, 7)
		}},
		{"unsupported_condition", func(m retainedOracleMapV2) bool {
			n := mutable
			n.revision = 9
			return retainPublicationRecordV2(m, mutableKey, n, "delete", 7)
		}},
		{"unsupported_role", func(m retainedOracleMapV2) bool {
			n := immutable
			n.role = "delete"
			return retainPublicationRecordV2(m, immutableKey, n, journalv2.ConditionAbsent, 0)
		}},
		{"encoded_key_alias", func(m retainedOracleMapV2) bool {
			return retainPublicationRecordV2(m, immutableKey+"/", immutable, journalv2.ConditionAbsent, 0)
		}},
		{"empty_value", func(m retainedOracleMapV2) bool {
			n := immutable
			n.canonical = nil
			return retainPublicationRecordV2(m, immutableKey, n, journalv2.ConditionAbsent, 0)
		}},
		{"zero_revision", func(m retainedOracleMapV2) bool {
			n := immutable
			n.revision = 0
			return retainPublicationRecordV2(m, immutableKey, n, journalv2.ConditionAbsent, 0)
		}},
		{"immutable_revision", func(m retainedOracleMapV2) bool {
			m[immutableKey] = immutable
			n := immutable
			n.revision = 9
			return retainPublicationRecordV2(m, immutableKey, n, journalv2.ConditionRecordRevision, 7)
		}},
		{"immutable_recreate", func(m retainedOracleMapV2) bool {
			m[immutableKey] = immutable
			n := immutable
			n.revision = 9
			return retainPublicationRecordV2(m, immutableKey, n, journalv2.ConditionAbsent, 0)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := retainedOracleMapV2{mutableKey: mutable}
			if test.change(m) {
				t.Fatal("structural law admitted")
			}
			if !reflect.DeepEqual(m[mutableKey], mutable) {
				t.Fatal("refusal overwrote existing value")
			}
		})
	}
	for key, entry := range retainedCopiedOracleV2(t, []journalv2.AcknowledgedPublicationV2{ack}) {
		switch entry.role {
		case journalv2.RecordOperationHead, journalv2.RecordAggregateHead, journalv2.RecordEligibilityGuard, journalv2.RecordCheckpointSlot:
			continue
		}
		m := retainedOracleMapV2{key: entry}
		next := entry
		next.revision = 109
		if retainPublicationRecordV2(m, key, next, journalv2.ConditionAbsent, 0) ||
			retainPublicationRecordV2(m, key, next, journalv2.ConditionRecordRevision, 7) || !reflect.DeepEqual(m[key], entry) {
			t.Fatal("immutable role recreated or changed")
		}
	}
	m := retainedOracleMapV2{mutableKey: mutable}
	next := mutable
	next.revision = 109
	next.canonical = []byte(`{"exact":"next"}`)
	if !retainPublicationRecordV2(m, mutableKey, next, journalv2.ConditionRecordRevision, 7) || !reflect.DeepEqual(m[mutableKey], next) {
		t.Fatal("exact noncontiguous predecessor refused")
	}
}

func TestRetainedPublicationMapStructuralAggregateExactPlusOneOverflow(t *testing.T) {
	// Scalar metadata probes, NOT fabricated ACK authority. Real-plan meter and
	// before-copy production ordering are separately exercised below.
	for _, test := range []struct {
		name  string
		exact journalv2.SealedRecordFootprintV2
		plus  func(*journalv2.SealedRecordFootprintV2)
	}{
		{"records", journalv2.SealedRecordFootprintV2{RecordCount: retainedRecordLimitV2}, func(f *journalv2.SealedRecordFootprintV2) { f.RecordCount++ }},
		{"values", journalv2.SealedRecordFootprintV2{RecordValueBytes: retainedValueBytesLimitV2}, func(f *journalv2.SealedRecordFootprintV2) { f.RecordValueBytes++ }},
		{"plans", journalv2.SealedRecordFootprintV2{CanonicalPlanBytes: retainedPlanBytesLimitV2}, func(f *journalv2.SealedRecordFootprintV2) { f.CanonicalPlanBytes++ }},
		{"keys", journalv2.SealedRecordFootprintV2{RecordKeyBytesUpperBound: retainedKeyBytesLimitV2}, func(f *journalv2.SealedRecordFootprintV2) { f.RecordKeyBytesUpperBound++ }},
		{"operations", journalv2.SealedRecordFootprintV2{RecordCount: 4096, AssertionCount: 4096}, func(f *journalv2.SealedRecordFootprintV2) { f.AssertionCount++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var b retainedPublicationBudgetV2
			if !b.charge(test.exact) {
				t.Fatal("exact cap refused")
			}
			plus := test.exact
			test.plus(&plus)
			var zero retainedPublicationBudgetV2
			if zero.charge(plus) || zero != (retainedPublicationBudgetV2{}) {
				t.Fatal("plus one or partial charge accepted")
			}
			// Exact aggregate via split charges; excess after sum also refuses.
			first := test.exact
			first.RecordCount /= 2
			first.RecordValueBytes /= 2
			first.CanonicalPlanBytes /= 2
			first.RecordKeyBytesUpperBound /= 2
			first.AssertionCount /= 2
			var split retainedPublicationBudgetV2
			if !split.charge(first) || !split.charge(first) || split != b {
				t.Fatal("aggregate exact cap mismatch")
			}
			before := split
			if split.charge(plus) || split != before {
				t.Fatal("aggregate overflow changed budget")
			}
		})
	}
	for _, f := range []journalv2.SealedRecordFootprintV2{{RecordCount: math.MaxUint64}, {RecordValueBytes: math.MaxUint64}, {CanonicalPlanBytes: math.MaxUint64}, {RecordKeyBytesUpperBound: math.MaxUint64}, {AssertionCount: math.MaxUint64}} {
		var b retainedPublicationBudgetV2
		if b.charge(f) || b != (retainedPublicationBudgetV2{}) {
			t.Fatal("uint overflow")
		}
	}
}

func retainedAddAssertionsV2(t *testing.T, input *journalv2.CommitPlanInput, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		a, err := journalv2.NewAuthorityAssertionV2(journalv2.AuthorityAssertionInputV2{Target: journalv2.ConditionTarget{Kind: journalv2.ConditionTargetEligibilityGuard, MissionID: "unchanged-authority", ShardID: journalv2.EligibilityShardID(fmt.Sprintf("read-%03d", i))}, Kind: journalv2.ConditionAbsent})
		if err != nil {
			t.Fatal(err)
		}
		input.Assertions = append(input.Assertions, a)
	}
}
func retainedAddGuardsV2(t *testing.T, input *journalv2.CommitPlanInput, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		input.EligibilityGuards = append(input.EligibilityGuards, journalv2.EligibilityGuardTransition{MissionID: journalv2.MissionID(input.OperationState.OperationID()), ShardID: journalv2.EligibilityShardID(fmt.Sprintf("extra-%03d", i)), Canonical: []byte(`{"eligible":true}`)})
	}
	input.Conditions = v2TestConditions(t, input.Heads, input.EligibilityGuards, input.Checkpoints)
}

func TestRetainedPublicationMapRealAggregateCountKeysOperationsBeforeClones(t *testing.T) {
	acks := make([]journalv2.AcknowledgedPublicationV2, retainedPublicationLimitV2)
	port := &retainedFixturePortV2{}
	coordinator := retainedCoordinatorV2(t, port)
	for i := range acks {
		input := retainedFixtureInputV2(t, fmt.Sprintf("budget-%03d", i), nil, 0)
		retainedAddGuardsV2(t, &input, 5)      // 16 records/ACK; 4096 occurrences
		retainedAddAssertionsV2(t, &input, 16) // 8192 operations exactly
		port.revision = uint64(i + 1)
		acks[i] = retainedPublishV2(t, coordinator, input)
	}
	got, err := buildRetainedPublicationMapV2(t.Context(), acks)
	if err != nil || len(got) != retainedRecordLimitV2 || got.maxProviderRevision() != 256 {
		t.Fatalf("exact real aggregate caps refused: %v", err)
	}
	// Assertions are not rows, regardless of their repeated authority targets.
	for _, assertion := range acks[0].Plan().Assertions() {
		key, err := v2KeyForAuthorityAssertion(assertion)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := retainedDenseOracleViewV2(got)[key.String()]; exists {
			t.Fatal("assertion retained")
		}
	}
	for name, extraGuards := range map[string]int{"record_plus_one": 6, "operation_plus_one": 5} {
		t.Run(name, func(t *testing.T) {
			input := retainedFixtureInputV2(t, "budget-255", nil, 0)
			retainedAddGuardsV2(t, &input, extraGuards)
			assertions := 16
			if name == "operation_plus_one" {
				assertions++
			}
			retainedAddAssertionsV2(t, &input, assertions)
			bad := append([]journalv2.AcknowledgedPublicationV2(nil), acks...)
			bad[len(bad)-1] = retainedPublishV2(t, retainedCoordinatorV2(t, &retainedFixturePortV2{256}), input)
			// All genuine meters succeed; only the fixed opaque ACK array escapes
			// (too large for stack). No Records/CanonicalValue/key payload copy.
			var result retainedPublicationMapV2
			var failure error
			allocs := testing.AllocsPerRun(2, func() { result, failure = buildRetainedPublicationMapV2(context.Background(), bad) })
			if result != nil || failure != errRetainedPublicationMapV2 || allocs != 1 {
				t.Fatalf("copies before aggregate refusal: allocations=%v", allocs)
			}
		})
	}
	tooMany := append(append([]journalv2.AcknowledgedPublicationV2(nil), acks...), acks[0])
	if allocs := testing.AllocsPerRun(2, func() {
		got, err := buildRetainedPublicationMapV2(context.Background(), tooMany)
		if got != nil || err != errRetainedPublicationMapV2 {
			t.Fatal("input count plus one")
		}
	}); allocs != 0 {
		t.Fatal("allocation before input count bound")
	}
}

func TestRetainedPublicationMapRealLargeCheckpointPlanBudgetBeforeClones(t *testing.T) {
	// Six real valid 4 MiB checkpoint plans exceed the 32 MiB canonical-plan
	// aggregate (base64 plus metadata), without exceeding the value aggregate.
	var acks []journalv2.AcknowledgedPublicationV2
	var total uint64
	for i := 0; i < 6; i++ {
		input := retainedFixtureInputV2(t, fmt.Sprintf("large-%d", i), nil, 0)
		input.Checkpoints[0].Canonical = v2CanonicalAt(MaxCheckpointValueBytes)
		ack := retainedPublishV2(t, retainedCoordinatorV2(t, &retainedFixturePortV2{uint64(i + 1)}), input)
		f, err := ack.Plan().MeasureRecordFootprintV2(t.Context())
		if err != nil || f.LargestRecordValueBytes != MaxCheckpointValueBytes {
			t.Fatal("large genuine plan meter")
		}
		total += f.CanonicalPlanBytes
		acks = append(acks, ack)
	}
	if total <= retainedPlanBytesLimitV2 {
		t.Fatal("fixture does not exceed canonical plan budget")
	}
	// Tune the sixth genuine sealed plan to an EXACT 32 MiB aggregate. Base64
	// length changes in multiples of four; a validated command-kind suffix
	// supplies the remaining 0..3 bytes, without manufacturing sealed fields.
	over := total - retainedPlanBytesLimitV2
	last := retainedFixtureInputV2(t, "large-5", nil, 0)
	last.Checkpoints[0].Canonical = v2CanonicalAt(MaxCheckpointValueBytes - int((over+3)/4)*3)
	base, err := journalv2.SealCommitPlanV2(last, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	baseFootprint, err := base.MeasureRecordFootprintV2(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	oldFootprint, err := acks[5].Plan().MeasureRecordFootprintV2(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	remaining := retainedPlanBytesLimitV2 - (total - oldFootprint.CanonicalPlanBytes) - baseFootprint.CanonicalPlanBytes
	if remaining > 3 {
		t.Fatal("canonical-plan tuning assumption drift")
	}
	setBinding := func(padding int) {
		binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: last.OperationState.OperationID(), CommandKind: "retained.proof" + string(bytes.Repeat([]byte("x"), padding)), CommandSchemaVersion: 1, CanonicalIntentDigest: v2TestDigest("large-5"), Scope: journalv2.Scope{Kind: journalv2.ScopeHost}, ActorID: "retained-actor", ProcessID: "retained-process", OriginatingRuntimeEpochID: "retained-epoch", ObservedCreatedAt: "2026-09-07T00:00:00Z"})
		if err != nil {
			t.Fatal(err)
		}
		last.OperationBinding = &binding
	}
	setBinding(int(remaining))
	exact := append([]journalv2.AcknowledgedPublicationV2(nil), acks...)
	exact[5] = retainedPublishV2(t, retainedCoordinatorV2(t, &retainedFixturePortV2{6}), last)
	exactFootprint, err := exact[5].Plan().MeasureRecordFootprintV2(t.Context())
	if err != nil || total-oldFootprint.CanonicalPlanBytes+exactFootprint.CanonicalPlanBytes != retainedPlanBytesLimitV2 {
		t.Fatal("not actual exact canonical plan aggregate")
	}
	exactMap, err := buildRetainedPublicationMapV2(t.Context(), exact)
	if err != nil || len(exactMap) != 66 {
		t.Fatalf("actual 32 MiB plan aggregate refused: %v", err)
	}
	setBinding(int(remaining) + 1)
	plus := append([]journalv2.AcknowledgedPublicationV2(nil), exact...)
	plus[5] = retainedPublishV2(t, retainedCoordinatorV2(t, &retainedFixturePortV2{6}), last)
	plusFootprint, err := plus[5].Plan().MeasureRecordFootprintV2(t.Context())
	if err != nil || plusFootprint.CanonicalPlanBytes != exactFootprint.CanonicalPlanBytes+1 {
		t.Fatal("not actual aggregate plus one")
	}
	var result retainedPublicationMapV2
	var failure error
	allocs := testing.AllocsPerRun(2, func() { result, failure = buildRetainedPublicationMapV2(context.Background(), acks) })
	if result != nil || failure != errRetainedPublicationMapV2 || allocs != 1 {
		t.Fatalf("large plan copied before refusal: %v", allocs)
	}
	allocs = testing.AllocsPerRun(2, func() { result, failure = buildRetainedPublicationMapV2(context.Background(), plus) })
	if result != nil || failure != errRetainedPublicationMapV2 || allocs != 1 {
		t.Fatalf("actual exact+1 copied before refusal: %v", allocs)
	}
	// One actual maximum checkpoint succeeds with its exact canonical bytes.
	got, err := buildRetainedPublicationMapV2(t.Context(), acks[:1])
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range acks[0].Plan().Records() {
		if record.Role() == journalv2.RecordCheckpointRecord {
			key, _ := v2KeyForSealedRecord(record)
			if !bytes.Equal(retainedDenseOracleViewV2(got)[key.String()].canonical, record.CanonicalValue()) {
				t.Fatal("large checkpoint canonical rewritten")
			}
		}
	}
}

// Each mutation is synchronous with a context callback, never concurrent with
// a caller-array read. Genuine ACK internals are sealed and immutable.
type retainedSnapshotContextV2 struct {
	context.Context
	calls      [4]int // Err, Deadline, Done, Value
	method, at int
	swap       func()
}

func (c *retainedSnapshotContextV2) callback(method int) {
	c.calls[method]++
	if method == c.method && c.calls[method] == c.at && c.swap != nil {
		c.swap()
	}
}
func (c *retainedSnapshotContextV2) Err() error { c.callback(0); return c.Context.Err() }
func (c *retainedSnapshotContextV2) Deadline() (time.Time, bool) {
	c.callback(1)
	return c.Context.Deadline()
}
func (c *retainedSnapshotContextV2) Done() <-chan struct{} { c.callback(2); return c.Context.Done() }
func (c *retainedSnapshotContextV2) Value(key any) any {
	c.callback(3)
	return c.Context.Value(key)
}

func retainedSnapshotACKPairV2(t *testing.T) (journalv2.AcknowledgedPublicationV2, journalv2.AcknowledgedPublicationV2) {
	t.Helper()
	small := retainedACKV2(t, "snapshot-small", 7)
	input := retainedFixtureInputV2(t, "snapshot-large", nil, 0)
	input.Checkpoints[0].Canonical = v2CanonicalAt(MaxCheckpointValueBytes)
	large := retainedPublishV2(t, retainedCoordinatorV2(t, &retainedFixturePortV2{99}), input)
	for _, ack := range []journalv2.AcknowledgedPublicationV2{small, large} {
		if allocs := testing.AllocsPerRun(1, func() {
			if _, err := ack.Plan().MeasureRecordFootprintV2(context.Background()); err != nil {
				t.Fatal(err)
			}
		}); allocs != 0 {
			t.Fatal("scalar meter allocated", allocs)
		}
	}
	return small, large
}

// Shared oracle for both entrypoints: every reachable synchronous Err callback
// (including before first preflight, during it, after it and before copies), plus
// every other context method reached, must retain the original exact plan.
func retainedSnapshotCallbackChecksV2(t *testing.T, run func(context.Context, []journalv2.AcknowledgedPublicationV2) retainedPublicationMapV2) {
	t.Helper()
	small, large := retainedSnapshotACKPairV2(t)
	want := retainedCopiedOracleV2(t, []journalv2.AcknowledgedPublicationV2{small})
	cancellable, cancel := context.WithCancel(context.Background())
	defer cancel()
	// A cancellable parent also exercises Deadline/Done/Value during context
	// derivation/removal; its genuine cancelCtx identity avoids a watcher race.
	for parentIndex, parent := range []context.Context{context.Background(), cancellable} {
		baseline := &retainedSnapshotContextV2{Context: parent, method: -1}
		if got := run(baseline, []journalv2.AcknowledgedPublicationV2{small}); !reflect.DeepEqual(retainedDenseOracleViewV2(got), want) {
			t.Fatal("baseline plan drift")
		}
		for method, calls := range baseline.calls {
			for at := 1; at <= calls; at++ {
				t.Run(fmt.Sprintf("parent%d_method%d_callback%d", parentIndex, method, at), func(t *testing.T) {
					input := []journalv2.AcknowledgedPublicationV2{small}
					ctx := &retainedSnapshotContextV2{Context: parent, method: method, at: at, swap: func() { input[0] = large }}
					got := run(ctx, input)
					if input[0].PublicationID() != large.PublicationID() || !reflect.DeepEqual(retainedDenseOracleViewV2(got), want) || got.maxProviderRevision() != small.ProviderRevision() {
						t.Fatal("callback changed frozen original bytes/revision/publication/roles/targets")
					}
				})
			}
		}
		t.Logf("parent%d reachable callbacks Err/Deadline/Done/Value=%v; complete original map checked at each", parentIndex, baseline.calls)
	}
	port := &retainedFixturePortV2{7}
	coordinator := retainedCoordinatorV2(t, port)
	first := retainedPublishV2(t, coordinator, retainedFixtureInputV2(t, "snapshot-chain", nil, 0))
	prior := first.Plan().OperationState()
	port.revision = 29
	second := retainedPublishV2(t, coordinator, retainedFixtureInputV2(t, "snapshot-chain", &prior, 7))
	input := []journalv2.AcknowledgedPublicationV2{second, first}
	ctx := &retainedSnapshotContextV2{Context: context.Background(), method: 0, at: 1, swap: func() { slices.Reverse(input) }}
	got := run(ctx, input)
	if !reflect.DeepEqual(retainedDenseOracleViewV2(got), retainedCopiedOracleV2(t, []journalv2.AcknowledgedPublicationV2{first, second})) || input[0].PublicationID() != first.PublicationID() {
		t.Fatal("caller reordering changed frozen predecessor chain")
	}
}

func TestRetainedPublicationMapFrozenACKsAcrossContextCallbacks(t *testing.T) {
	retainedSnapshotCallbackChecksV2(t, func(ctx context.Context, input []journalv2.AcknowledgedPublicationV2) retainedPublicationMapV2 {
		got, err := buildRetainedPublicationMapV2(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		return got
	})
}

func TestRetainedPublicationGeometryExactArraysWorkAndPayloadOwnership(t *testing.T) {
	ack := retainedACKV2(t, "geometry", 7)
	var entry retainedPublicationEntryV2
	for _, record := range ack.Plan().Records() {
		if record.Role() == journalv2.RecordOperationHead {
			entry = retainedPublicationEntryV2{record.CanonicalValue(), 7, record.Role(), record.Target()}
		}
	}
	for _, count := range []int{0, 1, 2, 3, 5, 17, 4095, 4096} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("r%d_reverse%v", count, reverse), func(t *testing.T) {
				rows, scratch := make(retainedPublicationMapV2, count), make(retainedPublicationMapV2, count)
				// Private geometry metadata, NOT minted ACKs or public key authority.
				// Existing sealed codec creates bounded long-common-prefix keys.
				for i := range rows {
					index := i
					if reverse {
						index = count - 1 - i
					}
					key, err := v2OperationHeadKey(journalv2.OperationID(strings.Repeat("prefix", 20) + fmt.Sprintf("-%04d", index)))
					if err != nil {
						t.Fatal(err)
					}
					rows[i] = retainedPublicationOccurrenceV2{key.String(), entry, journalv2.ConditionAbsent, 0}
				}
				var firstRows, firstScratch *retainedPublicationOccurrenceV2
				if count > 0 {
					firstRows, firstScratch = &rows[0], &scratch[0]
				}
				var work retainedPublicationWorkV2
				got, err := sortCollapseRetainedPublicationsV2(t.Context(), rows, scratch, &work)
				if err != nil || len(got) != count || cap(got) != count {
					t.Fatal("exact array geometry", err)
				}
				levels := uint64(0)
				for width := 1; width < count; width *= 2 {
					levels++
				}
				r, m := uint64(count), uint64(max(0, count-1))
				if work.passes != levels || work.mergeComparisons > r*levels || work.mergeKeyBytes > 2*MaxKeyBytes*r*levels || work.mergeAssignments != r*levels || work.collapseComparisons != m || work.collapseKeyBytes > 2*MaxKeyBytes*m || work.collapseWrites != 2*r {
					t.Fatalf("work formula drift: %+v", work)
				}
				if count > 0 {
					if &got[0] != firstRows && &got[0] != firstScratch {
						t.Fatal("third backing")
					}
					other := scratch
					if &got[0] == firstScratch {
						other = rows
					}
					for _, row := range other {
						if !reflect.ValueOf(row).IsZero() {
							t.Fatal("other array retained references")
						}
					}
				}
				for i, row := range got {
					if &row.canonical[0] != &entry.canonical[0] {
						t.Fatal("sort recopied payload")
					}
					if i > 0 && strings.Compare(got[i-1].key, row.key) >= 0 {
						t.Fatal("not sorted")
					}
				}
				t.Logf("r=%d S=%d exact2rS=%d merge=%+v", count, unsafe.Sizeof(retainedPublicationOccurrenceV2{}), 2*uintptr(count)*unsafe.Sizeof(retainedPublicationOccurrenceV2{}), work)
			})
		}
	}
	for _, bad := range []int{4097} {
		got, err := sortCollapseRetainedPublicationsV2(t.Context(), make(retainedPublicationMapV2, bad), make(retainedPublicationMapV2, bad), nil)
		if err != errRetainedPublicationMapV2 || got != nil {
			t.Fatal("r+1 geometry accepted")
		}
	}
	rows := make(retainedPublicationMapV2, 1, 2)
	if got, err := sortCollapseRetainedPublicationsV2(t.Context(), rows, make(retainedPublicationMapV2, 1), nil); err == nil || got != nil {
		t.Fatal("spare growth capacity admitted")
	}
}

func TestRetainedPublicationGeometryCollapseTailOtherAndEveryCancellation(t *testing.T) {
	ack := retainedACKV2(t, "geometry-chain", 7)
	var base retainedPublicationOccurrenceV2
	for key, entry := range retainedCopiedOracleV2(t, []journalv2.AcknowledgedPublicationV2{ack}) {
		if entry.role == journalv2.RecordOperationHead {
			base = retainedPublicationOccurrenceV2{key, entry, journalv2.ConditionAbsent, 0}
		}
	}
	original := make(retainedPublicationMapV2, 5)
	for i := range original {
		next := base
		next.revision = uint64(7 + i*13)
		next.canonical = []byte(fmt.Sprintf(`{"version":%d}`, i))
		if i > 0 {
			next.condition, next.predecessor = journalv2.ConditionRecordRevision, uint64(7+(i-1)*13)
		}
		original[len(original)-1-i] = next
	}
	baseline := &retainedCancelContextV2{Context: t.Context(), cancelAt: math.MaxInt}
	rows, scratch := make(retainedPublicationMapV2, 5), make(retainedPublicationMapV2, 5)
	copy(rows, original)
	var work retainedPublicationWorkV2
	got, err := sortCollapseRetainedPublicationsV2(baseline, rows, scratch, &work)
	if err != nil || len(got) != 1 || got[0].revision != 59 || &got[0] != &scratch[0] || &got[0].canonical[0] != &original[0].canonical[0] {
		t.Fatal("odd pass collapse recopy/ownership", err)
	}
	for _, row := range got[len(got):cap(got)] {
		if !reflect.ValueOf(row).IsZero() {
			t.Fatal("discarded tail retains payload")
		}
	}
	for _, row := range rows {
		if !reflect.ValueOf(row).IsZero() {
			t.Fatal("whole other array not cleared")
		}
	}
	if work.collapseComparisons != 4 || work.collapseWrites != 10 {
		t.Fatal("collapse rescanned or extra writes")
	}
	// Sorting/collapse uses ONLY the supplied fixed backings. Payload pointers
	// above and zero allocations here separately guard against hidden recopy.
	if allocations := testing.AllocsPerRun(3, func() {
		copy(rows, original)
		clear(scratch)
		got, err = sortCollapseRetainedPublicationsV2(context.Background(), rows, scratch, nil)
	}); allocations != 0 || err != nil || len(got) != 1 {
		t.Fatal("sort/collapse allocated", allocations, err)
	}
	for at := 1; at <= baseline.calls; at++ {
		copy(rows, original)
		clear(scratch)
		ctx := &retainedCancelContextV2{Context: t.Context(), cancelAt: at}
		if partial, err := sortCollapseRetainedPublicationsV2(ctx, rows, scratch, nil); partial != nil || err != errRetainedPublicationMapV2 {
			t.Fatalf("partial cancellation at %d", at)
		}
	}
	// Sorting cannot repair an omitted/decreased predecessor or recreate a row.
	for _, fault := range []string{"tie", "omitted", "wrong_component", "zero", "decreased", "absent", "role", "target"} {
		t.Run(fault, func(t *testing.T) {
			copy(rows, original)
			clear(scratch)
			switch fault {
			case "tie":
				rows[0].revision = rows[1].revision
			case "omitted":
				rows[0].predecessor = 46 - 13
			case "wrong_component":
				rows[0].predecessor = 900
			case "zero":
				rows[0].predecessor = 0
			case "decreased":
				rows[0].predecessor = 1
			case "absent":
				rows[0].condition = journalv2.ConditionAbsent
				rows[0].predecessor = 0
			case "role":
				rows[0].role = journalv2.RecordAggregateHead
			case "target":
				rows[0].target = journalv2.SealedRecordTargetV2{}
			}
			if partial, err := sortCollapseRetainedPublicationsV2(t.Context(), rows, scratch, nil); err == nil || partial != nil {
				t.Fatal("invalid chain published")
			}
		})
	}
	t.Logf("every sort/merge/block/collapse/cleanup/final check cancelled: %d", baseline.calls)
}

func TestRetainedPublicationGeometryMaterializationFillCountAndNoGrowthSource(t *testing.T) {
	ack := retainedACKV2(t, "fill", 7)
	acks := []journalv2.AcknowledgedPublicationV2{ack}
	order, budget, err := preflightRetainedPublicationsV2(t.Context(), acks)
	if err != nil {
		t.Fatal(err)
	}
	for _, delta := range []int{-1, 1} {
		bad := budget
		bad.records = uint64(int(budget.records) + delta)
		if got, err := copyRetainedPublicationMapV2(t.Context(), acks, &order, bad); got != nil || err == nil {
			t.Fatal("inexact fill accepted")
		}
	}
	file, err := parser.ParseFile(token.NewFileSet(), "retained_publication_map.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	arrays, forbidden := 0, false
	ast.Inspect(file, func(n ast.Node) bool {
		if _, ok := n.(*ast.MapType); ok {
			forbidden = true
		}
		if call, ok := n.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok {
				if id.Name == "append" {
					forbidden = true
				}
				if id.Name == "make" {
					arrays++
				}
			}
		}
		return true
	})
	if forbidden || arrays != 2 {
		t.Fatal("observer source map/append/third backing drift", arrays)
	}
}

func TestRetainedACKSnapshotBoundsCancellationAndFixedAllocation(t *testing.T) {
	small, large := retainedSnapshotACKPairV2(t)
	a, capture, accepted := retainedObservationFixture(t, nil, nil, nil)
	for _, prepare := range []bool{false, true} {
		run := func(ctx context.Context, input []journalv2.AcknowledgedPublicationV2) {
			if prepare {
				s, err := a.PrepareRetainedObservationV2(ctx, capture, input)
				if err != errRetainedObservation || s.state != nil {
					t.Fatal("invalid entry accepted")
				}
			} else {
				m, err := buildRetainedPublicationMapV2(ctx, input)
				if err != errRetainedPublicationMapV2 || m != nil {
					t.Fatal("invalid entry accepted")
				}
			}
		}
		ctx := &retainedSnapshotContextV2{Context: context.Background(), method: -1}
		tooMany := make([]journalv2.AcknowledgedPublicationV2, 257)
		if allocs := testing.AllocsPerRun(1, func() { run(ctx, tooMany) }); allocs != 0 || ctx.calls != ([4]int{}) {
			t.Fatal("257 bound invoked callback/allocated", allocs, ctx.calls)
		}
		if allocs := testing.AllocsPerRun(1, func() { run(nil, []journalv2.AcknowledgedPublicationV2{small}) }); allocs != 0 {
			t.Fatal("nil context allocated", allocs)
		}
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		// Go1.26.7: the fixed array exceeds the compiler's stack-object limit.
		// Measure exactly one heap object and its page-rounded bytes, independent
		// of input length/payload. This is NOT a zero-allocation preparation claim.
		const arrayBytes = unsafe.Sizeof([retainedPublicationLimitV2]journalv2.AcknowledgedPublicationV2{})
		const heapBytes = (arrayBytes + 8191) &^ 8191 // large-object allocator pages
		for _, input := range [][]journalv2.AcknowledgedPublicationV2{nil, {small}, {large}, make([]journalv2.AcknowledgedPublicationV2, 256)} {
			if allocs := testing.AllocsPerRun(1, func() { run(cancelled, input) }); allocs != 1 {
				t.Fatal("not one fixed snapshot", allocs)
			}
			// TotalAlloc is process-wide (unlike the isolated allocation count
			// above). Retain every sample and require an EXACT fixed-byte
			// sample, never raise a tolerance for variable cloning. This is the
			// prior independent R1 review's allocation-attribution method.
			minimum := ^uint64(0)
			var samples [5]uint64
			for i := range samples {
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				run(cancelled, input)
				runtime.ReadMemStats(&after)
				samples[i] = after.TotalAlloc - before.TotalAlloc
				minimum = min(minimum, samples[i])
			}
			t.Logf("prepare=%v input=%d isolatedAllocs=1 process-wide bytes=%v", prepare, len(input), samples)
			if minimum != uint64(heapBytes) {
				t.Fatalf("no exact snapshot byte sample: %v want=%d", samples, heapBytes)
			}
		}
		t.Logf("prepare=%v opaque ACK=%d fixed[256]=%d actual heap=%d allocations=1; no payload copies", prepare, unsafe.Sizeof(small), arrayBytes, heapBytes)
	}
	if accepted.Load() != 0 {
		t.Fatal("snapshot bounds made RPC")
	}
}
