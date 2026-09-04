package restore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	journal "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/checkpoint"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/playback"
)

type emptyReader struct {
	mu                        sync.Mutex
	captures, verifies, scans int
	moveCaptures              int
	moveVerify                bool
}

func (r *emptyReader) CaptureRevisionV2(context.Context) (journal.StoreRevisionV2, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.captures++
	if r.captures <= r.moveCaptures {
		return journal.StoreRevisionV2{}, &journal.Error{Kind: journal.ErrorRevisionMoved, Field: "capture"}
	}
	return journal.NewStoreRevisionV2("database-1", 7, 7)
}
func (r *emptyReader) VerifyRevisionV2(context.Context, journal.StoreRevisionV2) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.verifies++
	if r.moveVerify {
		return &journal.Error{Kind: journal.ErrorRevisionMoved, Field: "final"}
	}
	return nil
}
func (r *emptyReader) ReadV2(context.Context, journal.ReadRequestV2) (journal.RecoveryRecordV2, error) {
	panic("empty restore must not exact-read")
}
func (r *emptyReader) ScanV2(_ context.Context, request journal.ScanRequestV2) (journal.RecoveryPageV2, error) {
	r.mu.Lock()
	r.scans++
	r.mu.Unlock()
	return journal.NewRecoveryPageV2(request.AttemptRevision().CurrentRevision(), "2026-09-03T12:00:00Z", nil, nil, false)
}
func restoreDigest(label string) journal.Digest {
	value := sha256.Sum256([]byte(label))
	return journal.Digest(value)
}
func restoreRegistries(t *testing.T) (journal.EventRegistryV2, journal.ReducerSetV2) {
	t.Helper()
	registry := journal.DefaultEventRegistryV2()
	registrations := make([]journal.ReducerRegistration, 0, len(journal.ExpectedEventTuples()))
	for _, tuple := range journal.ExpectedEventTuples() {
		reduce := journal.Reducer(func(_ []byte, event journal.EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil })
		if tuple.Family == "operation" {
			reduce = journal.ReduceOperationStateV2
		}
		registrations = append(registrations, journal.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: restoreDigest(tuple.Family), Reduce: reduce})
	}
	reducers, err := journal.NewReducerSetV2(registry, registrations)
	if err != nil {
		t.Fatal(err)
	}
	return registry, reducers
}
func restoreInput(t *testing.T, reader ReaderV2) InputV2 {
	registry, reducers := restoreRegistries(t)
	return InputV2{Reader: reader, EventRegistry: registry, Reducers: reducers, HostAuthorityID: "host-1", RecoveryAttemptID: "attempt-1"}
}

func TestRestoreWholeAttemptRestartAndSealedEpochRequest(t *testing.T) {
	reader := &emptyReader{moveCaptures: 2}
	result, err := RestoreV2(t.Context(), restoreInput(t, reader))
	if err != nil {
		t.Fatal(err)
	}
	if reader.captures != 3 || reader.scans != 3 || reader.verifies != 1 {
		t.Fatalf("captures=%d scans=%d verifies=%d", reader.captures, reader.scans, reader.verifies)
	}
	request, err := result.FreshEpochAdmissionRequestV2()
	if err != nil || request.DatabaseID() != "database-1" || request.ThroughRecordRevision() != 7 || request.RecoveryAttemptID() != "attempt-1" {
		t.Fatalf("request=%+v err=%v", request, err)
	}
	if _, err := (ResultV2{}).FreshEpochAdmissionRequestV2(); err == nil {
		t.Fatal("zero restore admitted epoch")
	}
}
func TestRestoreFinalRevisionMovementExhaustsBoundedAttempts(t *testing.T) {
	reader := &emptyReader{moveVerify: true}
	input := restoreInput(t, reader)
	input.Budgets = DefaultBudgetsV2()
	input.Budgets.MaximumRestarts = 2
	if _, err := RestoreV2(t.Context(), input); err == nil {
		t.Fatal("moving final revision accepted")
	}
	if reader.captures != 2 || reader.verifies != 2 {
		t.Fatalf("attempts captures=%d verifies=%d", reader.captures, reader.verifies)
	}
}

type journalReader struct {
	emptyReader
	revision   uint64
	records    map[journal.RecordAddressV2]journal.RecoveryRecordV2
	aggregates []journal.RecoveryRecordV2
	operations []journal.RecoveryRecordV2
	slots      []journal.RecoveryRecordV2
}

func (r *journalReader) CaptureRevisionV2(context.Context) (journal.StoreRevisionV2, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.captures++
	revision := r.revision
	if revision == 0 {
		revision = 7
	}
	return journal.NewStoreRevisionV2("database-1", revision, revision)
}

func (r *journalReader) VerifyRevisionV2(_ context.Context, attempt journal.StoreRevisionV2) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.verifies++
	revision := r.revision
	if revision == 0 {
		revision = 7
	}
	if r.moveVerify || attempt.DatabaseID() != "database-1" || attempt.CurrentRevision() != revision {
		return &journal.Error{Kind: journal.ErrorRevisionMoved, Field: "final"}
	}
	return nil
}

func (r *journalReader) ReadV2(_ context.Context, request journal.ReadRequestV2) (journal.RecoveryRecordV2, error) {
	record, ok := r.records[request.Address()]
	if !ok {
		return journal.RecoveryRecordV2{}, &journal.Error{Kind: journal.ErrorNotFound, Field: "read"}
	}
	return record, nil
}
func (r *journalReader) ScanV2(_ context.Context, request journal.ScanRequestV2) (journal.RecoveryPageV2, error) {
	var records []journal.RecoveryRecordV2
	switch request.Target().Kind() {
	case journal.ScanAllAggregateHeads:
		records = r.aggregates
	case journal.ScanAllOperationRecords:
		records = r.operations
	case journal.ScanCheckpointSlots:
		records = r.slots
	}
	offset := 0
	if len(request.Cursor()) != 0 {
		parsed, err := strconv.Atoi(string(request.Cursor()))
		if err != nil || parsed < 0 || parsed > len(records) {
			return journal.RecoveryPageV2{}, &journal.Error{Kind: journal.ErrorStaleCursor, Field: "cursor"}
		}
		offset = parsed
	}
	end := min(offset+request.Limit(), len(records))
	var cursor []byte
	if end < len(records) {
		cursor = []byte(strconv.Itoa(end))
	}
	return journal.NewRecoveryPageV2(request.AttemptRevision().CurrentRevision(), "2026-09-03T12:00:00Z", records[offset:end], cursor, len(cursor) != 0)
}

func witnessBytes(t *testing.T, publicationID journal.PublicationID, operationID journal.OperationID, version journal.LogicalVersion) ([]byte, journal.Digest, journal.Digest) {
	return witnessBytesWithCount(t, publicationID, operationID, version, 5)
}

func witnessBytesWithCount(t *testing.T, publicationID journal.PublicationID, operationID journal.OperationID, version journal.LogicalVersion, recordCount uint32) ([]byte, journal.Digest, journal.Digest) {
	t.Helper()
	plan, records := restoreDigest("plan"), restoreDigest("records")
	preimage, _ := json.Marshal(struct {
		SchemaVersion   uint32 `json:"schema_version"`
		PublicationID   string `json:"publication_id"`
		OperationID     string `json:"operation_id"`
		LogicalVersion  uint64 `json:"operation_logical_version"`
		PlanSHA256      string `json:"plan_sha256"`
		RecordSetSHA256 string `json:"authority_record_set_sha256"`
		RecordCount     uint32 `json:"authority_record_count"`
	}{1, string(publicationID), string(operationID), uint64(version), plan.String(), records.String(), recordCount})
	h := sha256.New()
	_, _ = h.Write([]byte("spec-kitty.publication-witness.v2"))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(preimage)
	var witnessDigest journal.Digest
	copy(witnessDigest[:], h.Sum(nil))
	wire, _ := json.Marshal(map[string]any{"schema_version": uint32(1), "publication_id": string(publicationID), "operation_id": string(operationID), "operation_logical_version": uint64(version), "plan_sha256": plan.String(), "authority_record_set_sha256": records.String(), "authority_record_count": recordCount, "witness_sha256": witnessDigest.String()})
	encoded, err := journal.CanonicalObject(wire)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.DecodePublicationWitnessV2(encoded); err != nil {
		t.Fatal(err)
	}
	return encoded, plan, records
}
func recoveryRecord(t *testing.T, address journal.RecordAddressV2, value []byte, revision uint64) journal.RecoveryRecordV2 {
	t.Helper()
	record, err := journal.NewRecoveryRecordV2(address, value, revision, "")
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestRestoreSuffixMembershipOrderingTerminalAndRollingPrefix(t *testing.T) {
	registry, reducers := restoreRegistries(t)
	operationID := journal.OperationID("operation-1")
	publicationID := journal.PublicationID(restoreDigest("publication").String())
	missionCoordinate, _ := journal.NewAggregateCoordinate("mission", "mission-1", journal.Scope{Kind: journal.ScopeMission, ProjectID: "project-1", MissionID: "mission-1"})
	operationCoordinate := operationCoordinate(operationID)
	missionPayload := []byte(`{"status":"ready"}`)
	missionEventID, operationEventID := journal.EventID("event-mission"), journal.EventID("event-operation")
	stateWithoutPublication, err := journal.NewOperationStateV2(journal.OperationStateInput{OperationID: operationID, LogicalVersion: 1, Status: journal.StatusAccepted, EventIDs: []journal.EventID{missionEventID, operationEventID}})
	if err != nil {
		t.Fatal(err)
	}
	operationPayload, err := journal.EncodeOperationStateEventPayloadV2(stateWithoutPublication)
	if err != nil {
		t.Fatal(err)
	}
	state, err := journal.NewOperationStateV2(journal.OperationStateInput{OperationID: operationID, LogicalVersion: 1, Status: journal.StatusAccepted, EventIDs: []journal.EventID{missionEventID, operationEventID}, LastPublicationID: publicationID})
	if err != nil {
		t.Fatal(err)
	}
	missionEvent, err := journal.NewEventEnvelopeV2(journal.EventEnvelopeInput{EventID: missionEventID, EventType: "mission.ready", Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: missionCoordinate, AggregateLogicalVersion: 1, OperationID: operationID, OperationLogicalVersion: 1, PublicationID: publicationID, CommittedRuntimeEpochID: "epoch-1", EventIndex: 0, EventCount: 2, Payload: missionPayload, Observations: []byte(`{}`)}, registry)
	if err != nil {
		t.Fatal(err)
	}
	operationEvent, err := journal.NewEventEnvelopeV2(journal.EventEnvelopeInput{EventID: operationEventID, EventType: "operation.accepted", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: operationCoordinate, AggregateLogicalVersion: 1, OperationID: operationID, OperationLogicalVersion: 1, PublicationID: publicationID, CommittedRuntimeEpochID: "epoch-1", EventIndex: 1, EventCount: 2, Payload: operationPayload, Observations: []byte(`{}`)}, registry)
	if err != nil {
		t.Fatal(err)
	}
	missionDigest, _ := journal.ReducerStateDigestV2(missionPayload)
	missionHead, err := journal.NewAggregateHeadV2(journal.AggregateHeadInput{Coordinate: missionCoordinate, LogicalVersion: 1, LastEventID: missionEventID, LastPublicationID: publicationID, CommittedRuntimeEpochID: "epoch-1", CanonicalStateDigest: missionDigest, AuthoritySummary: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	missionHeadBytes, err := journal.EncodeAggregateHeadV2(missionHead)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.DecodeAggregateHeadV2(missionHeadBytes); err != nil {
		t.Fatalf("encoded head invalid: %v bytes=%s", err, missionHeadBytes)
	}
	intent, _ := journal.CanonicalIntentDigest(journal.StableIntentInput{CommandKind: "mission.ready", CommandSchemaVersion: 1, Scope: missionCoordinate.Scope, Command: []byte(`{"ready":true}`)})
	binding, err := journal.NewOperationBindingV2(journal.OperationBindingInput{OperationID: operationID, CommandKind: "mission.ready", CommandSchemaVersion: 1, CanonicalIntentDigest: intent, Scope: missionCoordinate.Scope, OriginatingRuntimeEpochID: "epoch-1", ObservedCreatedAt: "2026-09-03T12:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	bindingBytes, _ := journal.EncodeOperationBindingV2(binding)
	stateBytes, _ := journal.EncodeOperationStateV2(state)
	missionEventBytes, _ := journal.EncodeEventEnvelopeV2(missionEvent)
	operationEventBytes, _ := journal.EncodeEventEnvelopeV2(operationEvent)
	witness, planDigest, recordSetDigest := witnessBytes(t, publicationID, operationID, 1)
	missionHeadAddress, _ := journal.NewAggregateHeadAddressV2(missionCoordinate)
	bindingAddress, _ := journal.NewOperationBindingAddressV2(operationID)
	stateAddress, _ := journal.NewOperationHeadAddressV2(operationID)
	missionEventAddress, _ := journal.NewJournalEventAddressV2(missionCoordinate, 1)
	operationEventAddress, _ := journal.NewJournalEventAddressV2(operationCoordinate, 1)
	witnessAddress, _ := publicationID.WitnessAddressV2()
	reader := &journalReader{emptyReader: emptyReader{}, records: map[journal.RecordAddressV2]journal.RecoveryRecordV2{}, aggregates: []journal.RecoveryRecordV2{recoveryRecord(t, missionHeadAddress, missionHeadBytes, 7)}, operations: []journal.RecoveryRecordV2{recoveryRecord(t, bindingAddress, bindingBytes, 7), recoveryRecord(t, stateAddress, stateBytes, 7)}}
	reader.records[missionEventAddress] = recoveryRecord(t, missionEventAddress, missionEventBytes, 7)
	reader.records[operationEventAddress] = recoveryRecord(t, operationEventAddress, operationEventBytes, 7)
	reader.records[witnessAddress] = recoveryRecord(t, witnessAddress, witness, 7)
	result, err := RestoreV2(t.Context(), InputV2{Reader: reader, EventRegistry: registry, Reducers: reducers, HostAuthorityID: "host-1", RecoveryAttemptID: "attempt-suffix"})
	if err != nil {
		t.Fatal(err)
	}
	want, err := journal.AdvanceProjectionPrefixV2(journal.ProjectionPrefixGenesisV2(), 7, publicationID, planDigest, recordSetDigest)
	if err != nil {
		t.Fatal(err)
	}
	if result.RollingPrefixDigest() != want || result.Playback().EventCount() != 2 {
		t.Fatalf("prefix=%s want=%s events=%d", result.RollingPrefixDigest(), want, result.Playback().EventCount())
	}
}

func TestQualificationFixtureCheckpointPlus10000Operations30Runs(t *testing.T) {
	if raceBuild() {
		t.Skip("the 30x10k wall-clock qualification is a performance campaign; the 100-run worker scenario remains enabled under -race")
	}
	input, uninterrupted, wantPrefix := qualificationRestoreFixture(t, 10_000, 300)
	durations := make([]time.Duration, 30)
	for run := range durations {
		input.Budgets = DefaultBudgetsV2()
		if run%2 == 0 {
			input.Budgets.MaximumWorkers = 1
		}
		started := time.Now()
		result, err := RestoreV2(t.Context(), input)
		durations[run] = time.Since(started)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if result.Playback().CanonicalProjectionDigest() != uninterrupted.CanonicalProjectionDigest() ||
			result.RollingPrefixDigest() != wantPrefix || result.Playback().EventCount() != 601 {
			t.Fatalf("run %d diverged from uninterrupted projection/prefix: events=%d", run, result.Playback().EventCount())
		}
		if _, err := result.FreshEpochAdmissionRequestV2(); err != nil {
			t.Fatalf("run %d did not produce sealed epoch request: %v", run, err)
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[28]
	if p95 > 5*time.Second {
		t.Fatalf("gross CI recovery bound exceeded: p95=%s", p95)
	}
	t.Logf("qualification fixture: checkpoint_operations=10000 suffix_events=601 reducer_inputs=601 runs=30 p95=%s (reference 2s adoption threshold measured separately)", p95)
}

func raceBuild() bool {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, setting := range info.Settings {
		if setting.Key == "-race" && setting.Value == "true" {
			return true
		}
	}
	return false
}

func TestRestoreParallelWorkerDeterminism100Runs(t *testing.T) {
	input, uninterrupted, wantPrefix := qualificationRestoreFixture(t, 1, 1)
	for run := 0; run < 100; run++ {
		input.Budgets = DefaultBudgetsV2()
		input.Budgets.MaximumWorkers = 1 + 7*(run%2)
		result, err := RestoreV2(t.Context(), input)
		if err != nil {
			t.Fatalf("run %d workers=%d: %v", run, input.Budgets.MaximumWorkers, err)
		}
		if result.Playback().CanonicalProjectionDigest() != uninterrupted.CanonicalProjectionDigest() || result.RollingPrefixDigest() != wantPrefix {
			t.Fatalf("run %d workers=%d produced nondeterministic authority", run, input.Budgets.MaximumWorkers)
		}
	}
}

func TestRestoreBudgetsAndWallClockExpirationFailClosed(t *testing.T) {
	base, _, _ := qualificationRestoreFixture(t, 1, 1)
	cases := []struct {
		name   string
		change func(*BudgetsV2)
	}{
		{"suffix_events", func(b *BudgetsV2) { b.MaximumSuffixEvents = 1 }},
		{"record_census", func(b *BudgetsV2) { b.MaximumRecords = 1 }},
		{"key_bytes", func(b *BudgetsV2) { b.MaximumKeyBytes = 1 }},
		{"workers", func(b *BudgetsV2) { b.MaximumWorkers = 65 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			input.Budgets = DefaultBudgetsV2()
			tc.change(&input.Budgets)
			_, err := RestoreV2(t.Context(), input)
			requireRestoreKind(t, err, ErrorBudgetExceeded)
		})
	}

	input := base
	input.Reader = &delayedScanReader{ReaderV2: base.Reader}
	input.Budgets = DefaultBudgetsV2()
	input.Budgets.MaximumRestarts = 2
	input.Budgets.MaximumDuration = time.Millisecond
	result, err := RestoreV2(t.Context(), input)
	requireRestoreKind(t, err, ErrorUnstableStore)
	if _, admissionErr := result.FreshEpochAdmissionRequestV2(); admissionErr == nil {
		t.Fatal("deadline-degraded restore admitted a fresh epoch")
	}
}

func TestRestoreRevisionCensusAndExactReadFaultsFailClosed(t *testing.T) {
	base, _, _ := qualificationRestoreFixture(t, 1, 1)
	t.Run("first_page_revision_mismatch_restarts_whole_attempt", func(t *testing.T) {
		input := base
		input.Reader = &wrongObservedRevisionReader{ReaderV2: base.Reader}
		input.Budgets = DefaultBudgetsV2()
		input.Budgets.MaximumRestarts = 2
		_, err := RestoreV2(t.Context(), input)
		requireRestoreKind(t, err, ErrorUnstableStore)
	})
	t.Run("missing_suffix_event", func(t *testing.T) {
		input, _, _ := qualificationRestoreFixture(t, 1, 1)
		reader := input.Reader.(*journalReader)
		for address, record := range reader.records {
			if address.Kind() == journal.AddressJournalEvent && record.RecordRevision() == reader.revision {
				delete(reader.records, address)
				break
			}
		}
		_, err := RestoreV2(t.Context(), input)
		requireRestoreField(t, err, ErrorJournalCorrupt, "missing_event")
	})
	t.Run("missing_operation_only_witness", func(t *testing.T) {
		input, _, _ := qualificationRestoreFixture(t, 1, 1)
		reader := input.Reader.(*journalReader)
		for address, record := range reader.records {
			if address.Kind() == journal.AddressPublicationWitness && record.RecordRevision() == 2 {
				delete(reader.records, address)
				break
			}
		}
		_, err := RestoreV2(t.Context(), input)
		requireRestoreField(t, err, ErrorJournalCorrupt, "missing_publication_witness")
	})
	t.Run("witness_revision_differs_from_group", func(t *testing.T) {
		input, _, _ := qualificationRestoreFixture(t, 1, 1)
		reader := input.Reader.(*journalReader)
		for address, record := range reader.records {
			if address.Kind() == journal.AddressPublicationWitness && record.RecordRevision() == reader.revision {
				reader.records[address] = recoveryRecord(t, address, record.CanonicalValue(), record.RecordRevision()-1)
				break
			}
		}
		_, err := RestoreV2(t.Context(), input)
		requireRestoreField(t, err, ErrorJournalCorrupt, "publication_witness_record")
	})
}

type delayedScanReader struct{ ReaderV2 }

func (r *delayedScanReader) ScanV2(ctx context.Context, _ journal.ScanRequestV2) (journal.RecoveryPageV2, error) {
	<-ctx.Done()
	return journal.RecoveryPageV2{}, ctx.Err()
}

type wrongObservedRevisionReader struct{ ReaderV2 }

func (r *wrongObservedRevisionReader) ScanV2(ctx context.Context, request journal.ScanRequestV2) (journal.RecoveryPageV2, error) {
	page, err := r.ReaderV2.ScanV2(ctx, request)
	if err != nil {
		return journal.RecoveryPageV2{}, err
	}
	return journal.NewRecoveryPageV2(page.ObservedRevision()+1, page.AsOf(), page.Records(), page.Cursor(), page.Truncated())
}

func requireRestoreKind(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	var restoreErr *Error
	if !errors.As(err, &restoreErr) || restoreErr.Kind != kind {
		t.Fatalf("error=%v, want restore kind %s", err, kind)
	}
}

func requireRestoreField(t *testing.T, err error, kind ErrorKind, field string) {
	t.Helper()
	var restoreErr *Error
	if !errors.As(err, &restoreErr) || restoreErr.Kind != kind || restoreErr.Field != field {
		t.Fatalf("error=%v, want %s/%s", err, kind, field)
	}
}

func qualificationRestoreFixture(t *testing.T, checkpointOperations, postCheckpointScopes int) (InputV2, playback.ResultV2, journal.Digest) {
	t.Helper()
	registry, reducers := restoreRegistries(t)
	eventDescriptor, _ := registry.Descriptor()
	reducerDescriptor, _ := reducers.Descriptor()
	reader := &journalReader{records: make(map[journal.RecordAddressV2]journal.RecoveryRecordV2)}
	operationRecords := make(map[journal.RecordAddressV2]journal.RecoveryRecordV2, 2*(checkpointOperations+postCheckpointScopes+1))
	aggregateRecords := make(map[journal.RecordAddressV2]journal.RecoveryRecordV2, postCheckpointScopes)
	checkpointStates := make([]journal.OperationStateV2, checkpointOperations)
	basePlayback := make([]playback.StateV2, checkpointOperations)
	baseEventIDs := make([]journal.EventID, checkpointOperations)

	for i := 0; i < checkpointOperations; i++ {
		operationID := journal.OperationID(fmt.Sprintf("qualification-operation-%05d", i))
		publicationID := qualificationPublicationID(fmt.Sprintf("base-%05d", i))
		eventID := journal.EventID(fmt.Sprintf("qualification-event-%05d", i))
		state, event := qualificationOperationEvent(t, registry, operationID, 1, journal.StatusAccepted, []journal.EventID{eventID}, publicationID, eventID, 0, 1)
		revision := uint64(i + 1)
		addQualificationOperation(t, reader, operationRecords, operationID, state, event, revision, true)
		checkpointStates[i], baseEventIDs[i] = state, eventID
		stateBytes, _ := journal.EncodeOperationStateV2(state)
		stateDigest, _ := journal.ReducerStateDigestV2(stateBytes)
		basePlayback[i], _ = playback.NewStateV2(operationCoordinate(operationID), 1, eventID, stateBytes, stateDigest, false)
	}

	checkpointSummary, err := journal.BuildOperationSummaryV2(checkpointStates)
	if err != nil {
		t.Fatal(err)
	}
	emptyPlayback, err := playback.RunV2(playback.InputV2{EventRegistry: registry, Reducers: reducers,
		ExpectedEventRegistryDigest: eventDescriptor.Digest(), ExpectedReducerRegistryDigest: reducerDescriptor.Digest()})
	if err != nil {
		t.Fatal(err)
	}
	checkpointRevision := uint64(checkpointOperations)
	coveredPublication := checkpointStates[len(checkpointStates)-1].LastPublicationID()
	checkpointPrefix := restoreDigest("qualification-checkpoint-prefix")
	checkpointID := journal.CheckpointID("qualification-checkpoint")
	checkpointBytes := qualificationCheckpointBytes(t, checkpointID, checkpointRevision, eventDescriptor.Digest(), reducerDescriptor.Digest(), checkpointPrefix, checkpointSummary, emptyPlayback.CanonicalProjectionDigest(), coveredPublication)
	slotID := journal.CheckpointSlotID("qualification-slot")
	slotBytes := qualificationSlotBytes(t, slotID, checkpointID, qualificationCheckpointDigest(t, checkpointBytes), checkpointRevision)
	commitRevision := checkpointRevision + 1
	checkpointAddress, _ := journal.NewCheckpointRecordAddressV2(checkpointID)
	slotAddress, _ := journal.NewCheckpointSlotAddressV2(slotID)
	checkpointRecord := recoveryRecord(t, checkpointAddress, checkpointBytes, commitRevision)
	slotRecord := recoveryRecord(t, slotAddress, slotBytes, commitRevision)
	reader.records[checkpointAddress] = checkpointRecord
	reader.slots = []journal.RecoveryRecordV2{slotRecord}
	coveredAddress, _ := coveredPublication.WitnessAddressV2()
	coveredBytes, _, _ := witnessBytesWithCount(t, coveredPublication, checkpointStates[len(checkpointStates)-1].OperationID(), 1, 3)
	reader.records[coveredAddress] = recoveryRecord(t, coveredAddress, coveredBytes, checkpointRevision)

	suffix := make([]playback.MaterializedEventV2, 0, 1+2*postCheckpointScopes)
	wantPrefix := checkpointPrefix
	writerID := journal.OperationID("qualification-checkpoint-writer")
	writerPublication := qualificationPublicationID("checkpoint-writer")
	writerEventID := journal.EventID("qualification-checkpoint-writer-event")
	writerState, writerEvent := qualificationOperationEvent(t, registry, writerID, 1, journal.StatusAccepted, []journal.EventID{writerEventID}, writerPublication, writerEventID, 0, 1)
	addQualificationOperation(t, reader, operationRecords, writerID, writerState, writerEvent, commitRevision, true)
	addQualificationWitness(t, reader, writerPublication, writerID, 1, commitRevision, 5, &wantPrefix)
	writerMaterial, _ := playback.NewMaterializedEventV2(commitRevision, writerEvent, false)
	suffix = append(suffix, writerMaterial)

	for i := 0; i < postCheckpointScopes; i++ {
		revision := commitRevision + uint64(i) + 1
		missionID := journal.MissionID(fmt.Sprintf("qualification-mission-%03d", i))
		missionCoordinate, _ := journal.NewAggregateCoordinate("mission", journal.AggregateID(missionID), journal.Scope{Kind: journal.ScopeMission, ProjectID: "qualification-project", MissionID: missionID})
		missionEventID := journal.EventID(fmt.Sprintf("qualification-mission-event-%03d", i))
		operationEventID := journal.EventID(fmt.Sprintf("qualification-operation-event-%03d", i))
		publicationID := qualificationPublicationID(fmt.Sprintf("suffix-%03d", i))
		operationID := journal.OperationID(fmt.Sprintf("qualification-suffix-operation-%03d", i))
		version, status, createBinding := journal.LogicalVersion(1), journal.StatusAccepted, true
		eventIDs := []journal.EventID{missionEventID, operationEventID}
		if i == 0 {
			operationID = checkpointStates[0].OperationID()
			version, status, createBinding = 2, journal.StatusInProgress, false
			eventIDs = append([]journal.EventID{baseEventIDs[0]}, eventIDs...)
		}
		operationState, operationEvent := qualificationOperationEvent(t, registry, operationID, version, status, eventIDs, publicationID, operationEventID, 1, 2)
		addQualificationOperation(t, reader, operationRecords, operationID, operationState, operationEvent, revision, createBinding)
		missionPayload := []byte(fmt.Sprintf(`{"sequence":%d}`, i))
		missionEvent, err := journal.NewEventEnvelopeV2(journal.EventEnvelopeInput{EventID: missionEventID, EventType: "mission.changed", Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1,
			Coordinate: missionCoordinate, AggregateLogicalVersion: 1, OperationID: operationID, OperationLogicalVersion: version, PublicationID: publicationID,
			CommittedRuntimeEpochID: "qualification-epoch", EventIndex: 0, EventCount: 2, Payload: missionPayload, Observations: []byte(`{}`)}, registry)
		if err != nil {
			t.Fatal(err)
		}
		missionEventBytes, _ := journal.EncodeEventEnvelopeV2(missionEvent)
		missionEventAddress, _ := journal.NewJournalEventAddressV2(missionCoordinate, 1)
		reader.records[missionEventAddress] = recoveryRecord(t, missionEventAddress, missionEventBytes, revision)
		missionDigest, _ := journal.ReducerStateDigestV2(missionPayload)
		missionHead, _ := journal.NewAggregateHeadV2(journal.AggregateHeadInput{Coordinate: missionCoordinate, LogicalVersion: 1, LastEventID: missionEventID,
			LastPublicationID: publicationID, CommittedRuntimeEpochID: "qualification-epoch", CanonicalStateDigest: missionDigest, AuthoritySummary: missionPayload})
		missionHeadBytes, _ := journal.EncodeAggregateHeadV2(missionHead)
		missionHeadAddress, _ := journal.NewAggregateHeadAddressV2(missionCoordinate)
		aggregateRecords[missionHeadAddress] = recoveryRecord(t, missionHeadAddress, missionHeadBytes, revision)
		addQualificationWitness(t, reader, publicationID, operationID, version, revision, map[bool]uint32{true: 5, false: 4}[createBinding], &wantPrefix)
		missionMaterial, _ := playback.NewMaterializedEventV2(revision, missionEvent, false)
		operationMaterial, _ := playback.NewMaterializedEventV2(revision, operationEvent, false)
		suffix = append(suffix, missionMaterial, operationMaterial)
	}

	reader.operations = qualificationRecordValues(operationRecords)
	reader.aggregates = qualificationRecordValues(aggregateRecords)
	reader.revision = commitRevision + uint64(postCheckpointScopes)
	uninterrupted, err := playback.RunV2(playback.InputV2{Base: basePlayback, Events: suffix, EventRegistry: registry, Reducers: reducers,
		ExpectedEventRegistryDigest: eventDescriptor.Digest(), ExpectedReducerRegistryDigest: reducerDescriptor.Digest()})
	if err != nil {
		t.Fatal(err)
	}
	input := InputV2{Reader: reader, EventRegistry: registry, Reducers: reducers, HostAuthorityID: "qualification-host", PriorRuntimeEpochID: "qualification-prior-epoch", RecoveryAttemptID: "qualification-attempt"}
	return input, uninterrupted, wantPrefix
}

func qualificationOperationEvent(t *testing.T, registry journal.EventRegistryV2, operationID journal.OperationID, version journal.LogicalVersion, status journal.OperationStatus, eventIDs []journal.EventID, publicationID journal.PublicationID, eventID journal.EventID, eventIndex, eventCount uint32) (journal.OperationStateV2, journal.EventEnvelopeV2) {
	t.Helper()
	withoutPublication, err := journal.NewOperationStateV2(journal.OperationStateInput{OperationID: operationID, LogicalVersion: version, Status: status, EventIDs: eventIDs})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := journal.EncodeOperationStateEventPayloadV2(withoutPublication)
	if err != nil {
		t.Fatal(err)
	}
	state, err := journal.ReconstructOperationStateV2(payload, publicationID)
	if err != nil {
		t.Fatal(err)
	}
	event, err := journal.NewEventEnvelopeV2(journal.EventEnvelopeInput{EventID: eventID, EventType: map[bool]string{true: "operation.in_progress", false: "operation.accepted"}[status == journal.StatusInProgress], Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2,
		Coordinate: operationCoordinate(operationID), AggregateLogicalVersion: version, OperationID: operationID, OperationLogicalVersion: version, PublicationID: publicationID,
		CommittedRuntimeEpochID: "qualification-epoch", EventIndex: uint16(eventIndex), EventCount: uint16(eventCount), Payload: payload, Observations: []byte(`{}`)}, registry)
	if err != nil {
		t.Fatal(err)
	}
	return state, event
}

func addQualificationOperation(t *testing.T, reader *journalReader, operationRecords map[journal.RecordAddressV2]journal.RecoveryRecordV2, operationID journal.OperationID, state journal.OperationStateV2, event journal.EventEnvelopeV2, revision uint64, createBinding bool) {
	t.Helper()
	if createBinding {
		binding, err := journal.NewOperationBindingV2(journal.OperationBindingInput{OperationID: operationID, CommandKind: "qualification.run", CommandSchemaVersion: 1,
			CanonicalIntentDigest: restoreDigest("intent-" + string(operationID)), Scope: journal.Scope{Kind: journal.ScopeHost}, OriginatingRuntimeEpochID: "qualification-epoch", ObservedCreatedAt: "2026-09-03T12:00:00Z"})
		if err != nil {
			t.Fatal(err)
		}
		bindingBytes, _ := journal.EncodeOperationBindingV2(binding)
		bindingAddress, _ := journal.NewOperationBindingAddressV2(operationID)
		operationRecords[bindingAddress] = recoveryRecord(t, bindingAddress, bindingBytes, revision)
	}
	stateBytes, _ := journal.EncodeOperationStateV2(state)
	stateAddress, _ := journal.NewOperationHeadAddressV2(operationID)
	operationRecords[stateAddress] = recoveryRecord(t, stateAddress, stateBytes, revision)
	eventBytes, _ := journal.EncodeEventEnvelopeV2(event)
	eventAddress, _ := journal.NewJournalEventAddressV2(event.Coordinate(), event.LogicalVersion())
	reader.records[eventAddress] = recoveryRecord(t, eventAddress, eventBytes, revision)
}

func addQualificationWitness(t *testing.T, reader *journalReader, publicationID journal.PublicationID, operationID journal.OperationID, version journal.LogicalVersion, revision uint64, recordCount uint32, prefix *journal.Digest) {
	t.Helper()
	witness, plan, records := witnessBytesWithCount(t, publicationID, operationID, version, recordCount)
	address, _ := publicationID.WitnessAddressV2()
	reader.records[address] = recoveryRecord(t, address, witness, revision)
	next, err := journal.AdvanceProjectionPrefixV2(*prefix, revision, publicationID, plan, records)
	if err != nil {
		t.Fatal(err)
	}
	*prefix = next
}

func qualificationPublicationID(label string) journal.PublicationID {
	return journal.PublicationID(restoreDigest("qualification-publication-" + label).String())
}

func qualificationRecordValues(records map[journal.RecordAddressV2]journal.RecoveryRecordV2) []journal.RecoveryRecordV2 {
	values := make([]journal.RecoveryRecordV2, 0, len(records))
	for _, record := range records {
		values = append(values, record)
	}
	sort.Slice(values, func(i, j int) bool {
		left, right := values[i].Address(), values[j].Address()
		if left.OperationID() != right.OperationID() {
			return left.OperationID() < right.OperationID()
		}
		if left.OperationID() == "" && left.Kind() == right.Kind() {
			leftCoordinate, leftErr := journal.CanonicalAggregateCoordinate(left.Coordinate())
			rightCoordinate, rightErr := journal.CanonicalAggregateCoordinate(right.Coordinate())
			if leftErr == nil && rightErr == nil {
				return bytes.Compare(leftCoordinate, rightCoordinate) < 0
			}
		}
		return left.Kind() < right.Kind()
	})
	return values
}

func qualificationCheckpointBytes(t *testing.T, checkpointID journal.CheckpointID, revision uint64, eventRegistry, reducerRegistry, prefix journal.Digest, summary journal.OperationSummaryV2, projection journal.Digest, covered journal.PublicationID) []byte {
	t.Helper()
	summaryBytes, err := journal.EncodeOperationSummaryV2(summary)
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]any{"schema_version": uint32(2), "checkpoint_id": string(checkpointID), "through_record_revision": revision,
		"event_registry_sha256": eventRegistry.String(), "reducer_registry_sha256": reducerRegistry.String(), "canonical_codec_version": journal.CanonicalCodecVersionV2,
		"rolling_prefix_sha256": prefix.String(), "aggregates": []any{}, "operation_summary": json.RawMessage(summaryBytes),
		"canonical_projection_sha256": projection.String(), "covered_through_publication_id": string(covered)}
	unsigned, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	value["checkpoint_sha256"] = restoreDomainDigest("spec-kitty.checkpoint.v2", unsigned).String()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkpoint.DecodeV2(encoded); err != nil {
		t.Fatalf("qualification checkpoint is invalid: %v", err)
	}
	return encoded
}

func qualificationCheckpointDigest(t *testing.T, encoded []byte) journal.Digest {
	t.Helper()
	decoded, err := checkpoint.DecodeV2(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return decoded.CheckpointDigest()
}

func qualificationSlotBytes(t *testing.T, slotID journal.CheckpointSlotID, checkpointID journal.CheckpointID, digest journal.Digest, revision uint64) []byte {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"schema_version": uint32(2), "slot_id": string(slotID), "checkpoint_id": string(checkpointID), "checkpoint_sha256": digest.String(), "through_record_revision": revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkpoint.DecodeSlotV2(encoded); err != nil {
		t.Fatalf("qualification slot is invalid: %v", err)
	}
	return encoded
}

func restoreDomainDigest(domain string, payload []byte) journal.Digest {
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	var result journal.Digest
	copy(result[:], hash.Sum(nil))
	return result
}
