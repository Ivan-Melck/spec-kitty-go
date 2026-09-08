package productionruntime

import (
	"context"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	journalrestore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
)

type acceleratedRecoveryFixtureV2 struct {
	revision      journalv2.StoreRevisionV2
	records       map[journalv2.RecordAddressV2]journalv2.RecoveryRecordV2
	aggregates    []journalv2.RecoveryRecordV2
	operations    []journalv2.RecoveryRecordV2
	all           []journalv2.RecoveryRecordV2
	snapshotCalls int
}

func (r *acceleratedRecoveryFixtureV2) CaptureRevisionV2(context.Context) (journalv2.StoreRevisionV2, error) {
	return r.revision, nil
}

func (r *acceleratedRecoveryFixtureV2) VerifyRevisionV2(_ context.Context, expected journalv2.StoreRevisionV2) error {
	if expected != r.revision {
		return &journalv2.Error{Kind: journalv2.ErrorRevisionMoved, Field: "compatibility_revision"}
	}
	return nil
}

func (r *acceleratedRecoveryFixtureV2) ReadV2(_ context.Context, request journalv2.ReadRequestV2) (journalv2.RecoveryRecordV2, error) {
	record, ok := r.records[request.Address()]
	if !ok {
		return journalv2.RecoveryRecordV2{}, &journalv2.Error{Kind: journalv2.ErrorNotFound, Field: "compatibility_read"}
	}
	return record, nil
}

func (r *acceleratedRecoveryFixtureV2) ScanV2(_ context.Context, request journalv2.ScanRequestV2) (journalv2.RecoveryPageV2, error) {
	var records []journalv2.RecoveryRecordV2
	switch request.Target().Kind() {
	case journalv2.ScanAllAggregateHeads:
		records = r.aggregates
	case journalv2.ScanAllOperationRecords:
		records = r.operations
	}
	return journalv2.NewRecoveryPageV2(r.revision.CurrentRevision(), "2026-09-07T00:00:00Z", records, nil, false)
}

func (r *acceleratedRecoveryFixtureV2) ReadRecoverySnapshotV2(_ context.Context, request journalv2.RecoverySnapshotRequestV2) (journalv2.RecoverySnapshotV2, error) {
	r.snapshotCalls++
	return journalv2.NewRecoverySnapshotV2(request.AttemptRevision().CurrentRevision(), "2026-09-07T00:00:00Z", r.all)
}

func (r *acceleratedRecoveryFixtureV2) ReadManyV2(context.Context, journalv2.ReadManyRequestV2) (journalv2.RecoveryReadManyResultV2, error) {
	panic("empty recovery must not exact-read")
}

func TestRecoveryCompatibilitySelectionIsExplicitAndSemanticallyEquivalent(t *testing.T) {
	registry := journalv2.DefaultEventRegistryV2()
	reducers := fixtureReducers(t, registry)
	source := newAcceleratedRecoveryFixtureV2(t, registry)
	legacy := recoveryReaderForModeV2(source, false)
	candidate := recoveryReaderForModeV2(source, true)

	if _, ok := legacy.(journalrestore.RecoverySnapshotReaderV2); ok {
		t.Fatal("adopted recovery exposed candidate snapshot command")
	}
	if _, ok := legacy.(journalrestore.ReadManyReaderV2); ok {
		t.Fatal("adopted recovery exposed candidate read-many command")
	}
	if candidate != source {
		t.Fatal("candidate recovery replaced the admitted accelerated reader")
	}
	if _, ok := candidate.(journalrestore.RecoverySnapshotReaderV2); !ok {
		t.Fatal("candidate recovery lost snapshot capability")
	}
	if _, ok := candidate.(journalrestore.ReadManyReaderV2); !ok {
		t.Fatal("candidate recovery lost read-many capability")
	}

	restore := func(reader journalrestore.ReaderV2, attempt string) journalrestore.ResultV2 {
		t.Helper()
		result, err := journalrestore.RestoreV2(t.Context(), journalrestore.InputV2{
			Reader: reader, EventRegistry: registry, Reducers: reducers,
			HostAuthorityID: "host-compatibility", RecoveryAttemptID: attempt,
			Budgets: journalrestore.DefaultBudgetsV2(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	legacyResult := restore(legacy, "restore-legacy-compatibility")
	candidateResult := restore(candidate, "restore-candidate-compatibility")
	if source.snapshotCalls != 1 {
		t.Fatalf("candidate snapshot calls=%d want=1", source.snapshotCalls)
	}
	if legacyResult.StoreRevision() != candidateResult.StoreRevision() ||
		legacyResult.RollingPrefixDigest() != candidateResult.RollingPrefixDigest() ||
		legacyResult.Playback().CanonicalProjectionDigest() != candidateResult.Playback().CanonicalProjectionDigest() ||
		legacyResult.Playback().EventCount() != 2 || candidateResult.Playback().EventCount() != 2 ||
		len(legacyResult.Playback().States()) != 2 || len(candidateResult.Playback().States()) != 2 {
		t.Fatalf("legacy/candidate recovery diverged: legacy=%+v candidate=%+v", legacyResult.StoreRevision(), candidateResult.StoreRevision())
	}
}

func newAcceleratedRecoveryFixtureV2(t *testing.T, registry journalv2.EventRegistryV2) *acceleratedRecoveryFixtureV2 {
	t.Helper()
	operationID := journalv2.OperationID("operation-compatibility")
	missionCoordinate, err := journalv2.NewAggregateCoordinate("mission", "mission-compatibility", journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-compatibility", MissionID: "mission-compatibility"})
	if err != nil {
		t.Fatal(err)
	}
	operationCoordinate, err := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(operationID), journalv2.Scope{Kind: journalv2.ScopeHost})
	if err != nil {
		t.Fatal(err)
	}
	missionEventID := journalv2.EventID("event-mission-compatibility")
	operationEventID := journalv2.EventID("event-operation-compatibility")
	missionPayload := []byte(`{"status":"ready"}`)
	operationState, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{
		OperationID: operationID, LogicalVersion: 1, Status: journalv2.StatusAccepted,
		EventIDs: []journalv2.EventID{missionEventID, operationEventID},
	})
	if err != nil {
		t.Fatal(err)
	}
	operationPayload, err := journalv2.EncodeOperationStateEventPayloadV2(operationState)
	if err != nil {
		t.Fatal(err)
	}
	scope := missionCoordinate.Scope
	intent, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "mission.ready", CommandSchemaVersion: 1, Scope: scope, Command: []byte(`{"ready":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{
		OperationID: operationID, CommandKind: "mission.ready", CommandSchemaVersion: 1,
		CanonicalIntentDigest: intent, Scope: scope, OriginatingRuntimeEpochID: "epoch-compatibility",
		ObservedCreatedAt: "2026-09-07T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	missionDigest, err := journalv2.ReducerStateDigestV2(missionPayload)
	if err != nil {
		t.Fatal(err)
	}
	missionHead := journalv2.HeadTransitionV2{
		Coordinate: missionCoordinate, NextLogicalVersion: 1, EventID: missionEventID,
		CommittedRuntimeEpochID: "epoch-compatibility", CanonicalStateDigest: missionDigest,
		AuthoritySummary: []byte(`{}`),
	}
	operationDigest, err := journalv2.ReducerStateDigestV2(operationPayload)
	if err != nil {
		t.Fatal(err)
	}
	operationHead := journalv2.HeadTransitionV2{
		Coordinate: operationCoordinate, NextLogicalVersion: 1, EventID: operationEventID,
		CommittedRuntimeEpochID: "epoch-compatibility", CanonicalStateDigest: operationDigest,
		AuthoritySummary: operationPayload,
	}
	missionCondition, err := journalv2.ConditionForHeadTransitionV2(missionHead)
	if err != nil {
		t.Fatal(err)
	}
	operationCondition, err := journalv2.ConditionForHeadTransitionV2(operationHead)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := journalv2.SealCommitPlanV2(journalv2.CommitPlanInput{
		OperationBinding: &binding, OperationState: operationState,
		Events: []journalv2.EventDraftV2{
			{EventID: missionEventID, EventType: "mission.ready", Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: missionCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-compatibility", Payload: missionPayload, Observations: []byte(`{}`)},
			{EventID: operationEventID, EventType: "operation.accepted", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: operationCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-compatibility", Payload: operationPayload, Observations: []byte(`{}`)},
		},
		Heads:      []journalv2.HeadTransitionV2{missionHead, operationHead},
		Conditions: []journalv2.ProviderCondition{missionCondition, operationCondition},
	}, registry)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := journalv2.NewStoreRevisionV2("database-compatibility", 7, 7)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &acceleratedRecoveryFixtureV2{revision: revision, records: make(map[journalv2.RecordAddressV2]journalv2.RecoveryRecordV2)}
	for _, sealed := range plan.Records() {
		address := recoveryAddressForSealedRecordV2(t, sealed)
		record, recordErr := journalv2.NewRecoveryRecordV2(address, sealed.CanonicalValue(), 7, "")
		if recordErr != nil {
			t.Fatal(recordErr)
		}
		fixture.records[address] = record
		fixture.all = append(fixture.all, record)
		switch sealed.Role() {
		case journalv2.RecordAggregateHead:
			fixture.aggregates = append(fixture.aggregates, record)
		case journalv2.RecordOperationBinding, journalv2.RecordOperationHead:
			fixture.operations = append(fixture.operations, record)
		}
	}
	return fixture
}

func recoveryAddressForSealedRecordV2(t *testing.T, record journalv2.SealedRecordV2) journalv2.RecordAddressV2 {
	t.Helper()
	target := record.Target()
	var address journalv2.RecordAddressV2
	var err error
	switch record.Role() {
	case journalv2.RecordOperationBinding:
		address, err = journalv2.NewOperationBindingAddressV2(target.OperationID())
	case journalv2.RecordOperationHead:
		address, err = journalv2.NewOperationHeadAddressV2(target.OperationID())
	case journalv2.RecordJournalEvent:
		address, err = journalv2.NewJournalEventAddressV2(target.Coordinate(), target.LogicalVersion())
	case journalv2.RecordAggregateHead:
		address, err = journalv2.NewAggregateHeadAddressV2(target.Coordinate())
	case journalv2.RecordPublicationWitness:
		address, err = target.PublicationID().WitnessAddressV2()
	default:
		t.Fatalf("unexpected compatibility record role %q", record.Role())
	}
	if err != nil {
		t.Fatal(err)
	}
	return address
}

var _ journalrestore.ReaderV2 = (*acceleratedRecoveryFixtureV2)(nil)
var _ journalrestore.RecoverySnapshotReaderV2 = (*acceleratedRecoveryFixtureV2)(nil)
var _ journalrestore.ReadManyReaderV2 = (*acceleratedRecoveryFixtureV2)(nil)
