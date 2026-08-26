package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"

	_ "modernc.org/sqlite"
)

func TestCycle1ExternalEffectPriorEvidenceIsImmutable(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	effect := execution.ExternalEffect{
		SchemaVersion: execution.ResultSchemaVersion, OperationID: ids.OperationID("operation-immutable"),
		CorrelationID: "correlation-immutable", ActorID: ids.ActorID("actor-immutable"), ProcessID: ids.ProcessID("process-immutable"),
		Provider: "fake", WorkKey: "TK-1", Marker: "operation-immutable", PriorRevision: 1, PriorValue: "original",
		AppliedValue: "agent-a", ResponseStatus: execution.EffectPending, CompensationStatus: execution.CompensationPending,
		ObservedAt: time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC),
	}
	if err := store.SaveExternalEffect(ctx, effect); err != nil {
		t.Fatal(err)
	}
	effect.PriorRevision = tracker.Revision(2)
	effect.PriorValue = "agent-a"
	if err := store.SaveExternalEffect(ctx, effect); !execution.IsKind(err, execution.KindIdempotencyConflict) {
		t.Fatalf("immutable evidence overwrite error = %v", err)
	}
	stored, err := store.ExternalEffect(ctx, effect.OperationID)
	if err != nil || stored.PriorRevision != 1 || stored.PriorValue != "original" {
		t.Fatalf("immutable evidence = %+v, %v", stored, err)
	}
}

func TestProjectionReconciliationObservationSurvivesCloseAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	effect := projectionPersistenceEffect(execution.CompensationUnsupported, 13, "human-owner", "human-marker")
	if err := store.SaveExternalEffect(ctx, effect); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, err := store.ExternalEffect(ctx, effect.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CompensationStatus != execution.CompensationUnsupported || loaded.CompensationRevision != 0 || loaded.ObservationRevision != 13 || loaded.ObservationValue != "human-owner" || loaded.ObservationMarker != "human-marker" {
		t.Fatalf("reopened reconciliation evidence is not one coherent tuple: %+v", loaded)
	}
	var status execution.CompensationStatus
	var revision int64
	var value, marker string
	if err := store.db.QueryRowContext(ctx, `SELECT compensation_status,provider_revision,provider_value,provider_marker FROM external_effect_observations WHERE operation_id=? ORDER BY sequence DESC LIMIT 1`, effect.OperationID).Scan(&status, &revision, &value, &marker); err != nil {
		t.Fatal(err)
	}
	if status != execution.CompensationUnsupported || revision != 13 || value != "human-owner" || marker != "human-marker" {
		t.Fatalf("unsupported observation ledger = %s/%d/%q/%q", status, revision, value, marker)
	}
}

func TestExternalEffectContentDerivedRevisionsRoundTripAboveSignedRange(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	high := tracker.Revision(uint64(1)<<63 + 0x1234)
	effect := projectionPersistenceEffect(execution.CompensationAttributionLost, high, "human-owner", "")
	effect.PriorRevision = high + 1
	effect.AppliedRevision = high + 2
	effect.ConflictRevision = high + 3
	if err := store.SaveExternalEffect(ctx, effect); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, err := store.ExternalEffect(ctx, effect.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PriorRevision != high+1 || loaded.AppliedRevision != high+2 || loaded.ConflictRevision != high+3 || loaded.ObservationRevision != high {
		t.Fatalf("content revision bits changed across SQLite: %+v", loaded)
	}
}

func TestProjectionTerminalWithoutFreshObservationPersistsUnknownAcrossReopen(t *testing.T) {
	for _, status := range []execution.CompensationStatus{execution.CompensationUnsupported, execution.CompensationAttributionLost, execution.CompensationReconciliation} {
		t.Run(string(status), func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "control.db")
			store, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			effect := projectionPersistenceEffect(status, 0, "", "")
			if err := store.SaveExternalEffect(ctx, effect); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			loaded, err := store.ExternalEffect(ctx, effect.OperationID)
			if err != nil || loaded.ObservationRevision != 0 || loaded.ObservationValue != "" || loaded.ObservationMarker != "" {
				t.Fatalf("reopen fabricated historical tuple: %+v err=%v", loaded, err)
			}
			var revision int64
			var value, marker string
			if err := store.db.QueryRowContext(ctx, `SELECT provider_revision,provider_value,provider_marker FROM external_effect_observations WHERE operation_id=? ORDER BY sequence DESC LIMIT 1`, effect.OperationID).Scan(&revision, &value, &marker); err != nil {
				t.Fatal(err)
			}
			if revision != 0 || value != "" || marker != "" {
				t.Fatalf("ledger fabricated historical tuple: %d/%q/%q", revision, value, marker)
			}
		})
	}
}

func TestProjectionTerminalEvidenceCannotBeDowngradedOrSpliced(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	effect := projectionPersistenceEffect(execution.CompensationAttributionLost, 14, "human-owner", "")
	if err := store.SaveExternalEffect(ctx, effect); err != nil {
		t.Fatal(err)
	}
	weaker := effect
	weaker.CompensationStatus = execution.CompensationUnavailable
	weaker.ObservationRevision, weaker.ObservationValue, weaker.ObservationMarker = 0, "", ""
	weaker.ObservedAt = weaker.ObservedAt.Add(time.Second)
	if err := store.SaveExternalEffect(ctx, weaker); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.ExternalEffect(ctx, effect.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CompensationStatus != execution.CompensationAttributionLost || loaded.ObservationRevision != 14 || loaded.ObservationValue != "human-owner" || loaded.ObservationMarker != "" {
		t.Fatalf("terminal observation was downgraded or spliced: %+v", loaded)
	}
	var unknownRevision int64
	var unknownValue, unknownMarker string
	if err := store.db.QueryRowContext(ctx, `SELECT provider_revision,provider_value,provider_marker FROM external_effect_observations WHERE operation_id=? ORDER BY sequence DESC LIMIT 1`, effect.OperationID).Scan(&unknownRevision, &unknownValue, &unknownMarker); err != nil {
		t.Fatal(err)
	}
	if unknownRevision != 0 || unknownValue != "" || unknownMarker != "" {
		t.Fatalf("unavailable save fabricated provider state: %d/%q/%q", unknownRevision, unknownValue, unknownMarker)
	}
}

func TestProjectionConfirmedObservationSurvivesStalePendingReplay(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	confirmed := projectionPersistenceEffect(execution.CompensationPending, 12, "actor-a", "operation-projection-store")
	if err := store.SaveExternalEffect(ctx, confirmed); err != nil {
		t.Fatal(err)
	}
	stale := confirmed
	stale.ResponseStatus = execution.EffectPending
	stale.AppliedRevision = 0
	stale.ObservationRevision, stale.ObservationValue, stale.ObservationMarker = 11, "original", ""
	stale.ObservedAt = stale.ObservedAt.Add(time.Second)
	if err := store.SaveExternalEffect(ctx, stale); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.ExternalEffect(ctx, confirmed.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ResponseStatus != execution.EffectConfirmed || loaded.AppliedRevision != 12 || loaded.ObservationRevision != 12 || loaded.ObservationValue != "actor-a" || loaded.ObservationMarker != "operation-projection-store" {
		t.Fatalf("stale replay erased confirmed coherent observation: %+v", loaded)
	}
}

func TestProjectionConcurrentSemanticReplayConvergesOrConflicts(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base := projectionPersistenceEffect(execution.CompensationPending, 12, "actor-a", "operation-projection-store")
	const workers = 16
	results := make(chan error, workers)
	for index := 0; index < workers; index++ {
		go func() { results <- store.SaveExternalEffect(ctx, base) }()
	}
	for index := 0; index < workers; index++ {
		if err := <-results; err != nil && !execution.IsKind(err, execution.KindControlStoreBusy) {
			t.Fatalf("same-semantic concurrent save = %v", err)
		}
	}
	changed := base
	changed.AppliedValue = "different-actor"
	if err := store.SaveExternalEffect(ctx, changed); !execution.IsKind(err, execution.KindIdempotencyConflict) {
		t.Fatalf("changed semantic replay = %v", err)
	}
}

func TestProjectionBusyFailureUsesStableSameOperationDiagnostic(t *testing.T) {
	at := time.Date(2026, 8, 26, 13, 30, 0, 0, time.UTC)
	err := externalEffectStoreError("operation-busy-effect", "correlation-busy-effect", "actor-busy-effect", "process-busy-effect", at, "save external effect", errors.New("database is locked"))
	var diagnostic *execution.DiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.Kind != execution.KindControlStoreBusy || diagnostic.RetrySafety != execution.RetrySafeSameOperation || diagnostic.OperationID != "operation-busy-effect" || diagnostic.CorrelationID != "correlation-busy-effect" || diagnostic.AggregateIDs["actor_id"] != "actor-busy-effect" || diagnostic.AggregateIDs["process_id"] != "process-busy-effect" || diagnostic.OccurredAt != at {
		t.Fatalf("busy external-effect diagnostic = %+v", diagnostic)
	}
}

func projectionPersistenceEffect(status execution.CompensationStatus, revision tracker.Revision, value, marker string) execution.ExternalEffect {
	return execution.ExternalEffect{
		SchemaVersion: execution.ResultSchemaVersion, OperationID: "operation-projection-store", CorrelationID: "correlation-projection-store",
		ActorID: "actor-projection-store", ProcessID: "process-projection-store", Provider: "portable", WorkKey: "external-projection",
		Marker: "operation-projection-store", PriorRevision: 11, PriorValue: "original", AppliedRevision: 12, AppliedValue: "actor-a",
		ResponseStatus: execution.EffectConfirmed, CompensationStatus: status,
		ObservationRevision: revision, ObservationValue: value, ObservationMarker: marker,
		ObservedAt: time.Date(2026, 8, 26, 13, 0, 0, 0, time.UTC),
	}
}

func TestCycle3CompensationObservationRecordsOnlyProvenProviderReality(t *testing.T) {
	for _, test := range []struct {
		name           string
		status         execution.CompensationStatus
		repeat         bool
		conflict       tracker.Revision
		value          string
		marker         string
		observedRev    tracker.Revision
		observedValue  string
		observedMarker string
		wantRev        int64
		wantValue      string
		wantMarker     string
	}{
		{name: "successful", status: execution.CompensationDone, wantRev: 3, wantValue: "original"},
		{name: "repeated successful", status: execution.CompensationDone, repeat: true, wantRev: 3, wantValue: "original"},
		{name: "actual provider result", status: execution.CompensationDone, observedRev: 9, observedValue: "provider-actual", observedMarker: "provider-marker", wantRev: 9, wantValue: "provider-actual", wantMarker: "provider-marker"},
		{name: "attribution lost", status: execution.CompensationAttributionLost, conflict: 3, value: "human", marker: "", observedRev: 3, observedValue: "human", wantRev: 3, wantValue: "human"},
		{name: "unavailable", status: execution.CompensationUnavailable},
		{name: "timeout after ambiguous", status: execution.CompensationAmbiguous},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			effect := execution.ExternalEffect{
				SchemaVersion: execution.ResultSchemaVersion, OperationID: ids.OperationID("operation-observation"),
				CorrelationID: "correlation-observation", ActorID: ids.ActorID("actor-observation"), ProcessID: ids.ProcessID("process-observation"),
				Provider: "fake", WorkKey: "TK-1", Marker: "operation-observation", PriorRevision: 1, PriorValue: "original",
				AppliedRevision: 2, AppliedValue: "agent-a", ResponseStatus: execution.EffectConfirmed,
				CompensationStatus: test.status, CompensationRevision: 3, ConflictRevision: test.conflict,
				ConflictValue: test.value, ConflictMarker: test.marker, ObservedAt: time.Date(2026, 8, 24, 14, 0, 0, 0, time.UTC),
				ObservationRevision: test.observedRev, ObservationValue: test.observedValue, ObservationMarker: test.observedMarker,
			}
			if test.status != execution.CompensationDone {
				effect.CompensationRevision = 0
			}
			if err := store.SaveExternalEffect(ctx, effect); err != nil {
				t.Fatal(err)
			}
			if test.repeat {
				if err := store.SaveExternalEffect(ctx, effect); err != nil {
					t.Fatal(err)
				}
			}
			var revision int64
			var value, marker string
			if err := store.db.QueryRowContext(ctx, `SELECT provider_revision,provider_value,provider_marker FROM external_effect_observations WHERE operation_id=? ORDER BY sequence DESC LIMIT 1`, effect.OperationID).Scan(&revision, &value, &marker); err != nil {
				t.Fatal(err)
			}
			if revision != test.wantRev || value != test.wantValue || marker != test.wantMarker {
				t.Fatalf("stale compensation observation: revision=%d value=%q marker=%q; want %d %q %q", revision, value, marker, test.wantRev, test.wantValue, test.wantMarker)
			}
		})
	}
}

func TestCycle3SagaRepeatedCompensationKeepsLastProvenObservation(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	providerStore, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := providerStore.Seed(ctx, fake.WorkSeed{Key: "TK-1", Title: "observation", Assignee: "original"}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 14, 30, 0, 0, time.UTC)
	assignment := execution.TrackerAssignRequest{
		SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "correlation-saga-observation", OperationID: ids.OperationID("operation-saga-observation"),
		ActorID: ids.ActorID("actor-saga-observation"), ProcessID: ids.ProcessID("process-saga-observation"), Now: now,
		Provider: "fake", WorkKey: "TK-1", ExpectedRevision: 1, Assignee: "agent-a",
	}
	compensation := execution.TrackerCompensateRequest{
		SchemaVersion: assignment.SchemaVersion, CorrelationID: assignment.CorrelationID, OperationID: assignment.OperationID,
		ActorID: assignment.ActorID, ProcessID: assignment.ProcessID, Now: now,
		Provider: assignment.Provider, WorkKey: assignment.WorkKey, ExpectedRevision: 2,
	}
	saga := execution.NewTrackerSaga(store, fake.NewProvider(providerStore), execution.SystemClock{})
	if _, err := saga.Assign(ctx, assignment); err != nil {
		t.Fatal(err)
	}
	if _, err := saga.Compensate(ctx, compensation); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM external_effect_observations WHERE operation_id=?`, assignment.OperationID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	replayed, err := saga.Compensate(ctx, compensation)
	if err != nil || !replayed.Noop || !replayed.Replayed {
		t.Fatalf("compensation replay = %+v, %v", replayed, err)
	}
	var after int
	var revision int64
	var value, marker string
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM external_effect_observations WHERE operation_id=?`, assignment.OperationID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT provider_revision,provider_value,provider_marker FROM external_effect_observations WHERE operation_id=? ORDER BY sequence DESC LIMIT 1`, assignment.OperationID).Scan(&revision, &value, &marker); err != nil {
		t.Fatal(err)
	}
	if before != after || revision != 3 || value != "original" || marker != "" {
		t.Fatalf("repeated compensation changed observation ledger: before=%d after=%d revision=%d value=%q marker=%q", before, after, revision, value, marker)
	}
}

func TestCycle5AssignmentUnknownObservationsRemainUnknown(t *testing.T) {
	for _, test := range []struct {
		name          string
		failure       fake.FailureMode
		response      execution.EffectResponseStatus
		currentRev    tracker.Revision
		currentValue  string
		currentMarker string
	}{
		{name: "timeout before apply", failure: fake.FailureTimeoutBeforeApply, response: execution.EffectAmbiguous, currentRev: 1, currentValue: "original"},
		{name: "timeout after apply", failure: fake.FailureTimeoutAfterApply, response: execution.EffectAmbiguous, currentRev: 2, currentValue: "agent-a", currentMarker: "operation-cycle5-unknown"},
		{name: "unavailable", failure: fake.FailureUnavailable, response: execution.EffectUnavailable, currentRev: 1, currentValue: "original"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			controlPath := filepath.Join(t.TempDir(), "control.db")
			control, err := Open(ctx, controlPath)
			if err != nil {
				t.Fatal(err)
			}
			trackerPath := filepath.Join(t.TempDir(), "tracker.json")
			providerStore, err := fake.Open(trackerPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := providerStore.Seed(ctx, fake.WorkSeed{Key: "TK-1", Title: "unknown assignment", Assignee: "original"}); err != nil {
				t.Fatal(err)
			}
			if err := providerStore.Inject(ctx, test.failure); err != nil {
				t.Fatal(err)
			}
			request := cycle5AssignRequest()
			saga := execution.NewTrackerSaga(control, fake.NewProvider(providerStore), execution.SystemClock{})
			if _, err := saga.Assign(ctx, request); err == nil {
				t.Fatal("assignment refusal unexpectedly succeeded")
			}
			if err := control.Close(); err != nil {
				t.Fatal(err)
			}
			control, err = Open(ctx, controlPath)
			if err != nil {
				t.Fatal(err)
			}
			defer control.Close()

			rows, err := control.db.QueryContext(ctx, `SELECT response_status,provider_revision,provider_value,provider_marker FROM external_effect_observations WHERE operation_id=? ORDER BY sequence`, request.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			type observation struct {
				status                  execution.EffectResponseStatus
				revision                int64
				value, assignmentMarker string
			}
			var observations []observation
			for rows.Next() {
				var got observation
				if err := rows.Scan(&got.status, &got.revision, &got.value, &got.assignmentMarker); err != nil {
					t.Fatal(err)
				}
				observations = append(observations, got)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if len(observations) != 2 {
				t.Fatalf("observation count = %d, want pending and unknown: %+v", len(observations), observations)
			}
			if got := observations[0]; got.status != execution.EffectPending || got.revision != 1 || got.value != "original" || got.assignmentMarker != "" {
				t.Fatalf("durable pre-call observation = %+v", got)
			}
			if got := observations[1]; got.status != test.response || got.revision != 0 || got.value != "" || got.assignmentMarker != "" {
				t.Fatalf("unknown provider outcome invented an observation: %+v", got)
			}
			work, err := fake.NewProvider(providerStore).Read(ctx, "TK-1")
			if err != nil || work.Revision != test.currentRev || work.Assignee != test.currentValue || work.AssignmentMarker != test.currentMarker {
				t.Fatalf("provider reality = %+v, %v", work, err)
			}
		})
	}
}

func TestCycle5LostCompensationSaveReplayPersistsOneProviderState(t *testing.T) {
	ctx := context.Background()
	controlPath := filepath.Join(t.TempDir(), "control.db")
	control, err := Open(ctx, controlPath)
	if err != nil {
		t.Fatal(err)
	}
	trackerPath := filepath.Join(t.TempDir(), "tracker.json")
	providerStore, err := fake.Open(trackerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := providerStore.Seed(ctx, fake.WorkSeed{Key: "TK-1", Title: "replay truth", Assignee: "original"}); err != nil {
		t.Fatal(err)
	}
	request := cycle5AssignRequest()
	provider := fake.NewProvider(providerStore)
	if _, err := execution.NewTrackerSaga(control, provider, execution.SystemClock{}).Assign(ctx, request); err != nil {
		t.Fatal(err)
	}
	compensation := execution.TrackerCompensateRequest{
		SchemaVersion: request.SchemaVersion, CorrelationID: request.CorrelationID, OperationID: request.OperationID,
		ActorID: request.ActorID, ProcessID: request.ProcessID, Now: request.Now.Add(time.Minute),
		Provider: request.Provider, WorkKey: request.WorkKey, ExpectedRevision: 2,
	}
	sentinel := errors.New("lost compensation control save")
	failing := &cycle5FailNextEffectSave{ExternalEffectRepository: control, err: sentinel}
	if _, err := execution.NewTrackerSaga(failing, provider, execution.SystemClock{}).Compensate(ctx, compensation); !errors.Is(err, sentinel) {
		t.Fatalf("lost-save compensation error = %v", err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}

	providerStore, err = fake.Open(trackerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := providerStore.HumanAssign(ctx, "TK-1", "human-owner"); err != nil {
		t.Fatal(err)
	}
	control, err = Open(ctx, controlPath)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	provider = fake.NewProvider(providerStore)
	replayed, err := execution.NewTrackerSaga(control, provider, execution.SystemClock{}).Compensate(ctx, compensation)
	if err != nil || !replayed.Replayed || !replayed.Noop {
		t.Fatalf("compensation replay = %+v, %v", replayed, err)
	}
	current, err := provider.Read(ctx, "TK-1")
	if err != nil || current.Revision != 4 || current.Assignee != "human-owner" || current.AssignmentMarker != "" {
		t.Fatalf("human assignment was overwritten: %+v, %v", current, err)
	}
	var revision int64
	var value, marker string
	if err := control.db.QueryRowContext(ctx, `SELECT provider_revision,provider_value,provider_marker FROM external_effect_observations WHERE operation_id=? ORDER BY sequence DESC LIMIT 1`, request.OperationID).Scan(&revision, &value, &marker); err != nil {
		t.Fatal(err)
	}
	history, err := providerStore.History(ctx)
	if err != nil {
		t.Fatal(err)
	}
	knownStates := make(map[string]bool, len(history))
	for _, entry := range history {
		stateMarker := entry.Marker
		if entry.Kind == "seeded" || entry.Kind == "compensation" || entry.Kind == "human_assignment" {
			stateMarker = ""
		}
		knownStates[fmt.Sprintf("%d\x00%s\x00%s", entry.Revision, entry.Value, stateMarker)] = true
	}
	key := fmt.Sprintf("%d\x00%s\x00%s", revision, value, marker)
	if !knownStates[key] {
		t.Fatalf("observation tuple never existed at provider: revision=%d value=%q marker=%q", revision, value, marker)
	}
	if revision != 3 || value != "original" || marker != "" {
		t.Fatalf("replay did not preserve the coherent compensation mutation: revision=%d value=%q marker=%q", revision, value, marker)
	}
	stored, err := control.ExternalEffect(ctx, request.OperationID)
	if err != nil || stored.CompensationStatus != execution.CompensationDone || stored.CompensationRevision != 3 {
		t.Fatalf("durable compensation state = %+v, %v", stored, err)
	}
}

func TestReviewCycle7CorruptReplayHistoryCannotRecordCompensationDone(t *testing.T) {
	for _, corruption := range []string{"missing", "altered", "duplicate_conflict"} {
		t.Run(corruption, func(t *testing.T) {
			ctx := context.Background()
			controlPath := filepath.Join(t.TempDir(), "control.db")
			control, err := Open(ctx, controlPath)
			if err != nil {
				t.Fatal(err)
			}
			trackerPath := filepath.Join(t.TempDir(), "tracker.json")
			providerStore, err := fake.Open(trackerPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := providerStore.Seed(ctx, fake.WorkSeed{Key: "TK-1", Title: "corrupt replay", Assignee: "original"}); err != nil {
				t.Fatal(err)
			}
			request := cycle7AssignRequest()
			provider := fake.NewProvider(providerStore)
			if _, err := execution.NewTrackerSaga(control, provider, execution.SystemClock{}).Assign(ctx, request); err != nil {
				t.Fatal(err)
			}
			compensation := execution.TrackerCompensateRequest{
				SchemaVersion: request.SchemaVersion, CorrelationID: request.CorrelationID, OperationID: request.OperationID,
				ActorID: request.ActorID, ProcessID: request.ProcessID, Now: request.Now.Add(time.Minute),
				Provider: request.Provider, WorkKey: request.WorkKey, ExpectedRevision: 2,
			}
			sentinel := errors.New("lost compensation control save")
			failing := &cycle5FailNextEffectSave{ExternalEffectRepository: control, err: sentinel}
			if _, err := execution.NewTrackerSaga(failing, provider, execution.SystemClock{}).Compensate(ctx, compensation); !errors.Is(err, sentinel) {
				t.Fatalf("lost-save compensation error = %v", err)
			}
			if err := providerStore.HumanAssign(ctx, "TK-1", "human-owner"); err != nil {
				t.Fatal(err)
			}
			if err := control.Close(); err != nil {
				t.Fatal(err)
			}
			cycle7CorruptTrackerHistory(t, trackerPath, corruption)

			providerStore, err = fake.Open(trackerPath)
			if err != nil {
				t.Fatal(err)
			}
			control, err = Open(ctx, controlPath)
			if err != nil {
				t.Fatal(err)
			}
			defer control.Close()
			provider = fake.NewProvider(providerStore)
			_, err = execution.NewTrackerSaga(control, provider, execution.SystemClock{}).Compensate(ctx, compensation)
			if !execution.IsKind(err, execution.KindExternalAttributionLost) {
				t.Fatalf("corrupt replay error = %T %v", err, err)
			}
			work, err := provider.Read(ctx, "TK-1")
			if err != nil || work.Revision != 4 || work.Assignee != "human-owner" || work.AssignmentMarker != "" {
				t.Fatalf("corrupt replay changed human state: %+v, %v", work, err)
			}
			var done int
			if err := control.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM external_effect_observations WHERE operation_id=? AND compensation_status=?`, request.OperationID, execution.CompensationDone).Scan(&done); err != nil {
				t.Fatal(err)
			}
			if done != 0 {
				t.Fatalf("corrupt replay recorded %d fabricated compensation-done observations", done)
			}
			var status execution.CompensationStatus
			var revision int64
			var value, marker string
			if err := control.db.QueryRowContext(ctx, `SELECT compensation_status,provider_revision,provider_value,provider_marker FROM external_effect_observations WHERE operation_id=? ORDER BY sequence DESC LIMIT 1`, request.OperationID).Scan(&status, &revision, &value, &marker); err != nil {
				t.Fatal(err)
			}
			if status != execution.CompensationAttributionLost || revision != 4 || value != "human-owner" || marker != "" {
				t.Fatalf("decision observation = %s/%d/%q/%q", status, revision, value, marker)
			}
		})
	}
}

func cycle7AssignRequest() execution.TrackerAssignRequest {
	request := cycle5AssignRequest()
	request.CorrelationID = "correlation-cycle7"
	request.OperationID = ids.OperationID("operation-cycle7")
	return request
}

func cycle7CorruptTrackerHistory(t *testing.T, path, corruption string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	var history []fake.HistoryEntry
	if err := json.Unmarshal(document["history"], &history); err != nil {
		t.Fatal(err)
	}
	compensationIndex := -1
	for index := range history {
		if history[index].Kind == "compensation" {
			compensationIndex = index
			break
		}
	}
	if compensationIndex < 0 {
		t.Fatal("fixture has no compensation history")
	}
	switch corruption {
	case "missing":
		history = append(history[:compensationIndex], history[compensationIndex+1:]...)
	case "altered":
		history[compensationIndex].Value = "forged-owner"
	case "duplicate_conflict":
		conflict := history[compensationIndex]
		conflict.Value = "forged-owner"
		history = append(history, conflict)
	default:
		t.Fatalf("unknown corruption %q", corruption)
	}
	document["history"], err = json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func cycle5AssignRequest() execution.TrackerAssignRequest {
	return execution.TrackerAssignRequest{
		SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "correlation-cycle5", OperationID: ids.OperationID("operation-cycle5-unknown"),
		ActorID: ids.ActorID("actor-cycle5"), ProcessID: ids.ProcessID("process-cycle5"), Now: time.Date(2026, 8, 24, 16, 0, 0, 0, time.UTC),
		Provider: "fake", WorkKey: "TK-1", ExpectedRevision: 1, Assignee: "agent-a",
	}
}

type cycle5FailNextEffectSave struct {
	execution.ExternalEffectRepository
	err error
}

func (r *cycle5FailNextEffectSave) SaveExternalEffect(context.Context, execution.ExternalEffect) error {
	return r.err
}

func TestCycle1ExternalEffectMigrationFromExactPriorVersion(t *testing.T) {
	for _, used := range []bool{false, true} {
		name := "empty"
		if used {
			name = "used"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "control.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			for index := 0; index < 3; index++ {
				if _, err := db.ExecContext(ctx, migrations[index]); err != nil {
					db.Close()
					t.Fatalf("apply prior migration %d: %v", index+1, err)
				}
			}
			if used {
				if _, err := db.ExecContext(ctx, `INSERT INTO projects(project_id,created_at) VALUES('project-used','2026-08-24T00:00:00Z')`); err != nil {
					db.Close()
					t.Fatal(err)
				}
			}
			if _, err := db.ExecContext(ctx, `PRAGMA user_version=3`); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			store, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			assertExternalEffectSchema(t, ctx, store.db)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = Open(ctx, path)
			if err != nil {
				t.Fatalf("idempotent reopen: %v", err)
			}
			defer store.Close()
			assertExternalEffectSchema(t, ctx, store.db)
			if used {
				var projects int
				if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE project_id='project-used'`).Scan(&projects); err != nil || projects != 1 {
					t.Fatalf("prior state was not preserved: projects=%d err=%v", projects, err)
				}
			}
		})
	}
}

func assertExternalEffectSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("schema version=%d err=%v want=%d", version, err, currentSchemaVersion)
	}
	for _, table := range []string{"external_effects", "external_effect_observations"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d err=%v", table, count, err)
		}
	}
}
