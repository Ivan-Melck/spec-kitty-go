package provingground

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/application"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/checkpoint"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

type journalFixture struct {
	mu       sync.Mutex
	revision uint64
	results  map[string]uint64
	records  map[string]storegapdb.Record
	events   []controljournal.StoredEventV1
	calls    int
}

func newJournalFixture() *journalFixture {
	return &journalFixture{results: make(map[string]uint64), records: make(map[string]storegapdb.Record)}
}

func (f *journalFixture) Apply(_ context.Context, batch application.ValidatedBatchV1) (application.ResultV1, error) {
	product, err := batch.ProductBatch()
	if err != nil {
		return application.ResultV1{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if revision := f.results[product.OperationID]; revision != 0 {
		return application.ResultV1{Kind: application.OutcomeAlreadyCommitted, CommittedRevision: revision}, nil
	}
	f.revision++
	revision := f.revision
	for _, mutation := range product.Events {
		event, err := controljournal.DecodeEventEnvelopeV1(mutation.Value)
		if err != nil {
			return application.ResultV1{}, err
		}
		f.events = append(f.events, controljournal.StoredEventV1{RecordRevision: revision, Envelope: event})
		f.records[mutation.Key] = storegapdb.Record{Key: mutation.Key, Value: append([]byte(nil), mutation.Value...), Revision: revision}
	}
	for _, mutation := range product.AggregateHeads {
		f.records[mutation.Key] = storegapdb.Record{Key: mutation.Key, Value: append([]byte(nil), mutation.Value...), Revision: revision}
	}
	f.results[product.OperationID] = revision
	return application.ResultV1{Kind: application.OutcomeCommitted, CommittedRevision: revision}, nil
}

func (f *journalFixture) CheckpointHeadsV1(context.Context) ([]checkpoint.CheckpointHeadV1, error) {
	return nil, nil
}
func (f *journalFixture) CheckpointBlobV1(context.Context, checkpoint.CheckpointID) ([]byte, error) {
	return nil, fmt.Errorf("checkpoint absent")
}
func (f *journalFixture) JournalPageV1(_ context.Context, _ checkpoint.WatermarkV1, cursor string) (checkpoint.JournalScanPageV1, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cursor != "" {
		return checkpoint.JournalScanPageV1{}, fmt.Errorf("unexpected cursor")
	}
	records := append([]controljournal.StoredEventV1(nil), f.events...)
	sort.Slice(records, func(i, j int) bool {
		if records[i].RecordRevision != records[j].RecordRevision {
			return records[i].RecordRevision < records[j].RecordRevision
		}
		return records[i].Envelope.EventIndex < records[j].Envelope.EventIndex
	})
	page := checkpoint.JournalScanPageV1{ObservedRevision: f.revision, RecordCount: uint16(len(records))}
	for index, record := range records {
		page.Records[index] = record
		encoded, _ := controljournal.EncodeEventEnvelopeV1(record.Envelope)
		page.RawBytes += uint64(len(encoded))
	}
	return page, nil
}
func (f *journalFixture) Get(_ context.Context, key string) (storegapdb.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if record, ok := f.records[key]; ok {
		return record, nil
	}
	return storegapdb.Record{}, &storegapdb.Error{Kind: storegapdb.OutcomeNotFound}
}
func (f *journalFixture) Scan(_ context.Context, request storegapdb.ScanRequest) (storegapdb.ScanPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if request.Cursor != "" {
		return storegapdb.ScanPage{}, &storegapdb.Error{Kind: storegapdb.OutcomeInvalid}
	}
	page := storegapdb.ScanPage{ObservedRevision: f.revision}
	for key, record := range f.records {
		if len(key) >= len(request.Prefix) && key[:len(request.Prefix)] == request.Prefix {
			page.Records = append(page.Records, record)
		}
	}
	sort.Slice(page.Records, func(i, j int) bool { return page.Records[i].Key < page.Records[j].Key })
	return page, nil
}

type ambiguousCommitApplier struct {
	mu        sync.Mutex
	revision  uint64
	operation []byte
	calls     int
	heads     int
	events    int
}

func (a *ambiguousCommitApplier) Apply(_ context.Context, batch application.ValidatedBatchV1) (application.ResultV1, error) {
	product, err := batch.ProductBatch()
	if err != nil {
		return application.ResultV1{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if a.operation == nil {
		a.operation = append([]byte(nil), product.Operation.Value...)
		a.revision, a.heads, a.events = 77, len(product.AggregateHeads), len(product.Events)
		return application.ResultV1{Kind: application.OutcomeAmbiguousResponse}, nil
	}
	if !bytes.Equal(a.operation, product.Operation.Value) || a.heads != len(product.AggregateHeads) || a.events != len(product.Events) {
		return application.ResultV1{Kind: application.OutcomeIndeterminate}, nil
	}
	return application.ResultV1{Kind: application.OutcomeAlreadyCommitted, CommittedRevision: a.revision}, nil
}

type recordingApplier struct {
	revision atomic.Uint64
	calls    atomic.Int64
	mu       sync.Mutex
	commands []string
	arrived  chan struct{}
	release  <-chan struct{}
}

func (a *recordingApplier) Apply(ctx context.Context, batch application.ValidatedBatchV1) (application.ResultV1, error) {
	a.calls.Add(1)
	if product, err := batch.ProductBatch(); err == nil {
		if operation, err := controljournal.DecodeOperationRecordV1(product.Operation.Value); err == nil {
			a.mu.Lock()
			a.commands = append(a.commands, operation.CommandType)
			a.mu.Unlock()
		}
	}
	if a.arrived != nil {
		a.arrived <- struct{}{}
	}
	if a.release != nil {
		select {
		case <-a.release:
		case <-ctx.Done():
			return application.ResultV1{}, ctx.Err()
		}
	}
	return application.ResultV1{Kind: application.OutcomeCommitted, CommittedRevision: a.revision.Add(1)}, nil
}

func (a *recordingApplier) commandTypes() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.commands...)
}

func testConfig(t *testing.T) Config {
	t.Helper()
	actors, err := controljournal.NewTrialImplementationActorsV1("actor-a", "actor-b", "process-a", "process-b", "process-c")
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		ProjectID: "project-m1", MissionID: "mission-m1", WorkRevisionID: "revision-one", RunID: "run-m1",
		RuntimeEpochID: "epoch-one", Actors: actors, WorkIDs: []controljournal.WorkID{"work-a", "work-b"}, LeaseTTL: time.Minute,
	}
}

func claimRequest(work, actor, process, suffix string, now time.Time) ClaimRequest {
	return ClaimRequest{OperationID: controljournal.OperationID("claim-" + suffix), WorkRevisionID: "revision-one", RuntimeEpochID: "epoch-one", WorkID: controljournal.WorkID(work), ActorID: controljournal.ActorID(actor), ProcessID: controljournal.ProcessID(process), LeaseID: controljournal.LeaseID("lease-" + suffix), Now: now}
}

func mustService(t *testing.T, applier batchApplier, git GitWorkspace) *service {
	t.Helper()
	service, err := newService(testConfig(t), applier, git)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestAuthorityConcurrentDistinctClaimsOverlapAndBothSucceed(t *testing.T) {
	release := make(chan struct{})
	applier := &recordingApplier{arrived: make(chan struct{}, 2), release: release}
	service := mustService(t, applier, nil)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	requests := []ClaimRequest{
		claimRequest("work-a", "actor-a", "process-a", "a", now),
		claimRequest("work-b", "actor-b", "process-b", "b", now),
	}
	results := make(chan error, 2)
	for _, request := range requests {
		request := request
		go func() {
			_, err := service.Claim(t.Context(), request)
			results <- err
		}()
	}
	for range requests {
		select {
		case <-applier.arrived:
		case <-time.After(time.Second):
			t.Fatal("different work aggregates were globally serialized")
		}
	}
	close(release)
	for range requests {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	assertAuthorityInvariants(t, service)
}

func TestAuthorityHighContentionSameItemHasOneWinner(t *testing.T) {
	service := mustService(t, &recordingApplier{}, nil)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	const contenders = 64
	start := make(chan struct{})
	var successes atomic.Int64
	var failures atomic.Int64
	var wait sync.WaitGroup
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, err := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", fmt.Sprintf("same-%d", index), now))
			if err == nil {
				successes.Add(1)
			} else if IsOutcome(err, OutcomeAuthorityConflict) {
				failures.Add(1)
			} else {
				t.Errorf("untyped terminal outcome: %v", err)
			}
		}(index)
	}
	close(start)
	wait.Wait()
	if successes.Load() != 1 || failures.Load() != contenders-1 {
		t.Fatalf("successes=%d failures=%d", successes.Load(), failures.Load())
	}
	assertAuthorityInvariants(t, service)
}

func TestAuthorityTypedCoordinateRefusals(t *testing.T) {
	service := mustService(t, &recordingApplier{}, nil)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	authority, err := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "typed", now))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		kind   OutcomeKind
		mutate func(*Authority)
	}{
		{"revision", OutcomeStaleWorkRevision, func(a *Authority) { a.WorkRevisionID = "revision-old" }},
		{"lease", OutcomeStaleLease, func(a *Authority) { a.LeaseID = "lease-old" }},
		{"fence", OutcomeStaleFence, func(a *Authority) { a.FenceToken++ }},
		{"epoch", OutcomeStaleEpoch, func(a *Authority) { a.RuntimeEpochID = "epoch-old" }},
		{"process", OutcomeActorProcessMismatch, func(a *Authority) { a.ProcessID = "process-b" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutant := authority
			test.mutate(&mutant)
			if err := service.Authorize(mutant, "handoff", now.Add(time.Second)); !IsOutcome(err, test.kind) {
				t.Fatalf("got %v want %s", err, test.kind)
			}
		})
	}
	assertAuthorityInvariants(t, service)
}

func TestAuthorityClaimRefusesStaleRevisionAndEpochBeforePublication(t *testing.T) {
	applier := &recordingApplier{}
	service := mustService(t, applier, nil)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	staleRevision := claimRequest("work-a", "actor-a", "process-a", "stale-revision", now)
	staleRevision.WorkRevisionID = "revision-old"
	if _, err := service.Claim(t.Context(), staleRevision); !IsOutcome(err, OutcomeStaleWorkRevision) {
		t.Fatalf("stale revision=%v", err)
	}
	staleEpoch := claimRequest("work-a", "actor-a", "process-a", "stale-epoch", now)
	staleEpoch.RuntimeEpochID = "epoch-old"
	if _, err := service.Claim(t.Context(), staleEpoch); !IsOutcome(err, OutcomeStaleEpoch) {
		t.Fatalf("stale epoch=%v", err)
	}
	if applier.calls.Load() != 0 {
		t.Fatalf("refusal published %d batches", applier.calls.Load())
	}
}

func TestAuthorityExpireValidatesFenceAndProcessCoordinates(t *testing.T) {
	service := mustService(t, &recordingApplier{}, nil)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	authority, err := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "expire-coordinate", now))
	if err != nil {
		t.Fatal(err)
	}
	staleFence := authority
	staleFence.FenceToken++
	if err := service.Expire(t.Context(), "expire-stale-fence", staleFence, authority.Deadline); !IsOutcome(err, OutcomeStaleFence) {
		t.Fatalf("stale fence expired authority: %v", err)
	}
	wrongProcess := authority
	wrongProcess.ProcessID = "process-b"
	if err := service.Expire(t.Context(), "expire-wrong-process", wrongProcess, authority.Deadline); !IsOutcome(err, OutcomeActorProcessMismatch) {
		t.Fatalf("wrong process expired authority: %v", err)
	}
	if _, ok := service.Current("work-a"); !ok {
		t.Fatal("refusal removed current authority")
	}
}

func TestAuthorityExpireIsOneAtomicRetryableInvalidation(t *testing.T) {
	git := newFakeGit(t)
	base := &recordingApplier{}
	service := mustService(t, base, git)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	authority, err := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "atomic-expire", now))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := service.EnsureWorkspace(t.Context(), workspaceRequest(authority, "atomic-expire", now.Add(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	cut := &ambiguousCommitApplier{}
	service.applier = cut
	if err := service.Expire(t.Context(), "expire-atomic", authority, authority.Deadline); !IsOutcome(err, OutcomeAmbiguous) {
		t.Fatalf("first expiry=%v", err)
	}
	if current, ok := service.Current("work-a"); !ok || current.LeaseID != authority.LeaseID {
		t.Fatalf("ambiguous expiry leaked authority projection: %+v %v", current, ok)
	}
	if got, _ := service.Binding(binding.WorkspaceID); got.Status != "active-bound" {
		t.Fatalf("ambiguous expiry leaked workspace projection: %+v", got)
	}
	if err := service.Expire(t.Context(), "expire-atomic", authority, authority.Deadline); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.Current("work-a"); ok {
		t.Fatal("reconciled expiry retained authority")
	}
	if got, _ := service.Binding(binding.WorkspaceID); got.Status != "quarantined" || got.RecordRevision != 77 {
		t.Fatalf("workspace not atomically quarantined: %+v", got)
	}
	if cut.heads != 2 || cut.events != 2 || cut.calls != 2 {
		t.Fatalf("expiry batch heads=%d events=%d calls=%d", cut.heads, cut.events, cut.calls)
	}
	if err := service.Expire(t.Context(), "expire-atomic", authority, authority.Deadline); err != nil || cut.calls != 2 {
		t.Fatalf("terminal retry was not idempotent: err=%v calls=%d", err, cut.calls)
	}
}

func TestExpireCutsNeverPublishSplitProcessState(t *testing.T) {
	journal := newJournalFixture()
	git := newFakeGit(t)
	service := mustService(t, journal, git)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	authority, _ := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "expire-cuts", now))
	binding, _ := service.EnsureWorkspace(t.Context(), workspaceRequest(authority, "expire-cuts", now.Add(time.Second)))
	before := journal.calls
	service.WithFaults(&cutHook{id: "PG-EXPIRE-BEFORE-APPLY", remaining: 1})
	if err := service.Expire(t.Context(), "expire-cuts", authority, authority.Deadline); !IsOutcome(err, OutcomeAmbiguous) || journal.calls != before {
		t.Fatalf("before-apply cut published: err=%v calls=%d want=%d", err, journal.calls, before)
	}
	service.WithFaults(&cutHook{id: "PG-EXPIRE-BEFORE-PROJECTION", remaining: 1})
	if err := service.Expire(t.Context(), "expire-cuts", authority, authority.Deadline); !IsOutcome(err, OutcomeAmbiguous) {
		t.Fatalf("before-projection cut=%v", err)
	}
	if _, ok := service.Current("work-a"); !ok {
		t.Fatal("committed cut leaked only the authority half into memory")
	}
	if got, _ := service.Binding(binding.WorkspaceID); got.Status != "active-bound" {
		t.Fatalf("committed cut leaked only the workspace half into memory: %+v", got)
	}
	service.WithFaults(nil)
	if err := service.Expire(t.Context(), "expire-cuts", authority, authority.Deadline); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.Current("work-a"); ok {
		t.Fatal("reconciled cut retained authority")
	}
	if got, _ := service.Binding(binding.WorkspaceID); got.Status != "quarantined" {
		t.Fatalf("reconciled cut lost quarantine: %+v", got)
	}
	assertAuthorityInvariants(t, service)
}

func TestOwnerRestartIsOneCanonicalBatchAcrossTwoWorkspaces(t *testing.T) {
	git := newFakeGit(t)
	service := mustService(t, &recordingApplier{}, git)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	a, _ := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "restart-a", now))
	b, _ := service.Claim(t.Context(), claimRequest("work-b", "actor-b", "process-b", "restart-b", now))
	w1, _ := service.EnsureWorkspace(t.Context(), workspaceRequest(a, "restart-w1", now.Add(time.Second)))
	w2, _ := service.EnsureWorkspace(t.Context(), workspaceRequest(b, "restart-w2", now.Add(time.Second)))
	cut := &ambiguousCommitApplier{}
	if err := service.ReplaceRuntime(t.Context(), "restart-atomic", "epoch-two", cut, now.Add(2*time.Second)); !IsOutcome(err, OutcomeAmbiguous) {
		t.Fatalf("first restart=%v", err)
	}
	if _, ok := service.Current("work-a"); !ok {
		t.Fatal("ambiguous restart partially invalidated A")
	}
	if got, _ := service.Binding(w1.WorkspaceID); got.Status != "active-bound" {
		t.Fatalf("ambiguous restart partially quarantined W1: %+v", got)
	}
	if err := service.ReplaceRuntime(t.Context(), "restart-atomic", "epoch-two", cut, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if cut.heads != 5 || cut.events != 5 || cut.calls != 2 {
		t.Fatalf("restart batch heads=%d events=%d calls=%d", cut.heads, cut.events, cut.calls)
	}
	for _, id := range []controljournal.WorkspaceID{w1.WorkspaceID, w2.WorkspaceID} {
		if got, _ := service.Binding(id); got.Status != "quarantined" || got.RecordRevision != 77 {
			t.Fatalf("workspace %s not quarantined: %+v", id, got)
		}
	}
	if err := service.ReplaceRuntime(t.Context(), "restart-atomic", "epoch-two", cut, now.Add(2*time.Second)); err != nil || cut.calls != 2 {
		t.Fatalf("terminal restart retry was not idempotent: err=%v calls=%d", err, cut.calls)
	}
}

func TestReplaceRuntimeExactRetryAfterSealedProcessRestart(t *testing.T) {
	journal := newJournalFixture()
	git := newFakeGit(t)
	service := mustService(t, journal, git)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	a, _ := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "sealed-restart-a", now))
	b, _ := service.Claim(t.Context(), claimRequest("work-b", "actor-b", "process-b", "sealed-restart-b", now))
	_, _ = service.EnsureWorkspace(t.Context(), workspaceRequest(a, "sealed-restart-w1", now.Add(time.Second)))
	_, _ = service.EnsureWorkspace(t.Context(), workspaceRequest(b, "sealed-restart-w2", now.Add(time.Second)))
	if err := service.ReplaceRuntime(t.Context(), "restart-sealed-retry", "epoch-two", journal, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	events, _ := EventRegistryV1()
	reducers, _ := ReducerRegistryV1()
	eventDigest, _ := events.Digest()
	reducerDigest, _ := reducers.Digest()
	restored, err := checkpoint.RestoreV1(t.Context(), checkpoint.RestoreInputV1{
		Reader: journal, Source: checkpoint.SourceIdentityV1{ProjectID: "project-m1", MissionID: "mission-m1", RunID: "run-m1", ReducerSetDigest: reducerDigest, EventSchemaSetDigest: eventDigest},
		Binding: checkpoint.RestoreBindingV1{DatabaseID: "database-fixture", HostInstanceID: "host-fixture"}, EventRegistry: events, Reducers: reducers,
	})
	if err != nil {
		t.Fatal(err)
	}
	config := testConfig(t)
	config.RuntimeEpochID = "epoch-two"
	fresh, err := restoreValidated(t.Context(), config, journal, git, journal, restored)
	if err != nil {
		t.Fatal(err)
	}
	calls := journal.calls
	if err := fresh.ReplaceRuntime(t.Context(), "restart-sealed-retry", "epoch-two", journal, now.Add(2*time.Second)); err != nil {
		t.Fatalf("exact owner-restart retry failed after sealed reconstruction: %v", err)
	}
	if journal.calls != calls {
		t.Fatalf("restored retry republished a committed operation: calls=%d want=%d", journal.calls, calls)
	}
	for _, id := range []controljournal.WorkspaceID{"workspace-sealed-restart-w1", "workspace-sealed-restart-w2"} {
		if binding, _ := fresh.Binding(id); binding.Status != "quarantined" {
			t.Fatalf("restored restart lost %s quarantine: %+v", id, binding)
		}
	}
}

func TestRestoreRejectsAuthorityCensusAndPostSealRevisionMutants(t *testing.T) {
	journal := newJournalFixture()
	service := mustService(t, journal, nil)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	_, err := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "census-a", now))
	if err != nil {
		t.Fatal(err)
	}
	events, _ := EventRegistryV1()
	reducers, _ := ReducerRegistryV1()
	eventDigest, _ := events.Digest()
	reducerDigest, _ := reducers.Digest()
	restored, err := checkpoint.RestoreV1(t.Context(), checkpoint.RestoreInputV1{Reader: journal, Source: checkpoint.SourceIdentityV1{ProjectID: "project-m1", MissionID: "mission-m1", RunID: "run-m1", ReducerSetDigest: reducerDigest, EventSchemaSetDigest: eventDigest}, Binding: checkpoint.RestoreBindingV1{DatabaseID: "database-fixture", HostInstanceID: "host-fixture"}, EventRegistry: events, Reducers: reducers})
	if err != nil {
		t.Fatal(err)
	}
	prefix := "spk/v1/project-m1/mission-m1/heads/authority/"
	t.Run("projection-head census mismatch", func(t *testing.T) {
		journal.mu.Lock()
		original := journal.records[prefix+"work-a"]
		mutant := original
		mutant.Key = prefix + "work-b"
		journal.records[mutant.Key] = mutant
		journal.mu.Unlock()
		_, err := restoreValidated(t.Context(), testConfig(t), journal, nil, journal, restored)
		if !IsOutcome(err, OutcomeInvalidCommand) {
			t.Fatalf("unprojected durable authority head restored: %v", err)
		}
		journal.mu.Lock()
		delete(journal.records, mutant.Key)
		journal.mu.Unlock()
	})
	t.Run("record revision beyond seal", func(t *testing.T) {
		journal.mu.Lock()
		original := journal.records[prefix+"work-a"]
		original.Revision = journal.revision + 1
		journal.records[original.Key] = original
		journal.mu.Unlock()
		_, err := restoreValidated(t.Context(), testConfig(t), journal, nil, journal, restored)
		if !IsOutcome(err, OutcomeIndeterminate) {
			t.Fatalf("post-seal head revision restored: %v", err)
		}
	})
}

func TestAuthorityHeartbeatRefreshesCapsuleAndStalesPriorHead(t *testing.T) {
	service := mustService(t, &recordingApplier{}, nil)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	first, err := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "heartbeat", now))
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := service.Heartbeat(t.Context(), "heartbeat-one", first, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AuthorityHeadRecordRevision == first.AuthorityHeadRecordRevision || !refreshed.Deadline.After(first.Deadline) {
		t.Fatalf("heartbeat did not refresh: first=%+v refreshed=%+v", first, refreshed)
	}
	if err := service.Authorize(first, "handoff", now.Add(2*time.Second)); !IsOutcome(err, OutcomeStaleLease) {
		t.Fatalf("prior capsule survived heartbeat: %v", err)
	}
}

func TestAuthorityConcurrentClaimFocusedCampaign(t *testing.T) {
	err := RunFocusedCampaign(t.Context(), 100, func(ctx context.Context, index int) error {
		service := mustService(t, &recordingApplier{}, nil)
		now := time.Date(2026, 8, 30, 12, 0, index, 0, time.UTC)
		var wait sync.WaitGroup
		errors := make(chan error, 2)
		for _, request := range []ClaimRequest{
			claimRequest("work-a", "actor-a", "process-a", fmt.Sprintf("campaign-a-%d", index), now),
			claimRequest("work-b", "actor-b", "process-b", fmt.Sprintf("campaign-b-%d", index), now),
		} {
			request := request
			wait.Add(1)
			go func() { defer wait.Done(); _, err := service.Claim(ctx, request); errors <- err }()
		}
		wait.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				return err
			}
		}
		assertAuthorityInvariants(t, service)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertAuthorityInvariants(t *testing.T, service *service) {
	t.Helper()
	for workID, state := range service.works {
		state.mu.Lock()
		if state.current != nil {
			if state.current.WorkID != workID || state.current.Status != "active" || state.current.Generation == 0 || state.current.FenceToken == 0 || state.current.AuthorityHeadRecordRevision == 0 {
				state.mu.Unlock()
				t.Fatalf("invalid current authority for %s: %+v", workID, state.current)
			}
		}
		state.mu.Unlock()
	}
}
