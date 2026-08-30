package provingground

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/application"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/checkpoint"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/playback"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	trackerfake "github.com/spec-kitty/go-kitty3/internal/tracker/fake"
)

type wp08Store struct {
	mu         sync.Mutex
	revision   uint64
	records    map[string]storegapdb.Record
	applyCalls int
}

func newWP08Store() *wp08Store {
	return &wp08Store{revision: 1, records: make(map[string]storegapdb.Record)}
}
func (s *wp08Store) Apply(_ context.Context, batch application.ValidatedBatchV1) (application.ResultV1, error) {
	product, err := batch.ProductBatch()
	if err != nil {
		return application.ResultV1{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyCalls++
	if old, ok := s.records[product.Operation.Key]; ok {
		prior, _ := controljournal.DecodeOperationRecordV1(old.Value)
		next, _ := controljournal.DecodeOperationRecordV1(product.Operation.Value)
		if !bytes.Equal(old.Value, product.Operation.Value) {
			return application.ResultV1{Kind: application.OutcomeOperationIdentityCollision, OperationID: next.OperationID}, nil
		}
		return application.ResultV1{Kind: application.OutcomeAlreadyCommitted, OperationID: prior.OperationID, CommittedRevision: old.Revision, ResultType: prior.ResultType, CanonicalResult: prior.CanonicalResult()}, nil
	}
	for _, m := range append(append(append([]storegapdb.Mutation{product.Operation}, product.Events...), product.AggregateHeads...), product.EffectIntents...) {
		if m.Condition.Kind == storegapdb.ConditionAbsent {
			if _, ok := s.records[m.Key]; ok {
				return application.ResultV1{Kind: application.OutcomeHeadRevisionConflict}, nil
			}
		}
		if m.Condition.Kind == storegapdb.ConditionRevision {
			if old, ok := s.records[m.Key]; !ok || old.Revision != m.Condition.ExpectedRevision {
				return application.ResultV1{Kind: application.OutcomeHeadRevisionConflict}, nil
			}
		}
	}
	s.revision++
	for _, m := range append(append(append([]storegapdb.Mutation{product.Operation}, product.Events...), product.AggregateHeads...), product.EffectIntents...) {
		s.records[m.Key] = storegapdb.Record{Key: m.Key, Value: append([]byte(nil), m.Value...), Revision: s.revision}
	}
	operation, _ := controljournal.DecodeOperationRecordV1(product.Operation.Value)
	return application.ResultV1{Kind: application.OutcomeCommitted, OperationID: operation.OperationID, CommittedRevision: s.revision, ResultType: operation.ResultType, CanonicalResult: operation.CanonicalResult()}, nil
}
func (s *wp08Store) Get(_ context.Context, key string) (storegapdb.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[key]
	if !ok {
		return storegapdb.Record{}, &storegapdb.Error{Kind: storegapdb.OutcomeNotFound}
	}
	r.Value = append([]byte(nil), r.Value...)
	return r, nil
}
func (s *wp08Store) Scan(_ context.Context, r storegapdb.ScanRequest) (storegapdb.ScanPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Cursor != "" {
		return storegapdb.ScanPage{}, &storegapdb.Error{Kind: storegapdb.OutcomeInvalid}
	}
	page := storegapdb.ScanPage{ObservedRevision: s.revision, AsOf: time.Unix(1, 0).UTC()}
	for key, record := range s.records {
		if len(key) >= len(r.Prefix) && key[:len(r.Prefix)] == r.Prefix {
			copyRecord := record
			copyRecord.Value = append([]byte(nil), record.Value...)
			page.Records = append(page.Records, copyRecord)
		}
	}
	sort.Slice(page.Records, func(i, j int) bool { return page.Records[i].Key < page.Records[j].Key })
	return page, nil
}

func wp08Runtime(t *testing.T, store batchApplier, path, branch, base string, process controljournal.ProcessID) (*Runtime, controljournal.AuthorityCapsuleV1) {
	t.Helper()
	config := testConfig(t)
	core, err := newService(config, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	actor := config.Actors.ActorB
	work := controljournal.WorkID("work-b")
	lease := controljournal.LeaseID("lease-b")
	workspace := controljournal.WorkspaceID("workspace-b")
	if process == config.Actors.ProcessC {
		actor = config.Actors.ActorA
		work = "work-a"
		lease = "lease-c"
		workspace = "workspace-c"
	}
	authority := Authority{ProjectID: config.ProjectID, MissionID: config.MissionID, WorkRevisionID: config.WorkRevisionID, WorkID: work, ActorID: actor, ProcessID: process, RunID: config.RunID, LeaseID: lease, RuntimeEpochID: config.RuntimeEpochID, Generation: 1, FenceToken: 17, AuthorityHeadRecordRevision: 2, Deadline: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active", OperationID: "claim-test"}
	state := core.works[work]
	state.current = &authority
	state.logical = 1
	state.headRevision = 2
	state.lastFence = 17
	state.lastGen = 1
	binding := WorkspaceBinding{WorkspaceID: workspace, RepositoryID: "repository-m1", LaneID: "lane-b", CanonicalPath: path, OwnedBranch: branch, PinnedBase: base, ActorID: actor, ProcessID: process, RunID: config.RunID, LeaseID: lease, Generation: 1, FenceToken: 17, AuthorityHeadRecordRevision: 2, WorkID: work, WorkRevisionID: config.WorkRevisionID, RuntimeEpochID: config.RuntimeEpochID, LogicalVersion: 1, RecordRevision: 2, Status: "active-bound", OperationID: "bind-test", RequestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), CommandDigest: digestJSON("binding")}
	core.workspaces[workspace] = binding
	runtime := &Runtime{core: core}
	t.Cleanup(func() { assertAuthorityInvariants(t, runtime.core) })
	capsule, err := authorityCapsule(authority, binding)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, capsule
}

func wp08Provider(t *testing.T) (*trackerfake.Store, *trackerfake.Provider, tracker.ExternalWorkRef) {
	t.Helper()
	path := t.TempDir() + "/tracker.json"
	store, err := trackerfake.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Seed(t.Context(), trackerfake.WorkSeed{Key: "ticket-a", Title: "A", Assignee: "human"}); err != nil {
		t.Fatal(err)
	}
	provider := trackerfake.NewProvider(store)
	return store, provider, tracker.ExternalWorkRef{ProjectID: "project-m1", WorkID: "work-b", Provider: "durable_fake", ExternalID: "ticket-a", MetadataSchema: 1, CreatedByOperation: "operation-create", LastObservation: 1}
}

func wp08Intent(capsule controljournal.AuthorityCapsuleV1, ref tracker.ExternalWorkRef) AssignmentEffectIntent {
	return AssignmentEffectIntent{OperationID: "operation-assign", IntentID: "intent-assign", EffectID: "effect-assign", ExternalRef: ref, ExpectedRevision: 1, ExpectedAssignee: "human", DesiredAssignee: "actor-b", Marker: "operation-assign", Diagnostic: "bounded", OccurredAt: time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC), Authority: capsule}
}

type effectCutHook struct {
	cut   string
	err   error
	calls int
}

func (h *effectCutHook) Reach(_ context.Context, cut string) error {
	if cut == h.cut {
		h.calls++
		return h.err
	}
	return nil
}

func TestEffectGJ05LeavesDiscoverableIntentAndRecoveryObservesWithoutRepeat(t *testing.T) {
	journal := newWP08Store()
	runtime, capsule := wp08Runtime(t, journal, "/tmp", "lane-b", string(make([]byte, 40)), "process-b")
	providerStore, provider, ref := wp08Provider(t)
	coordinator, err := newEffectCoordinator(t.Context(), runtime, journal, journal, provider, 3)
	if err != nil {
		t.Fatal(err)
	}
	crash := errors.New("GJ-05 crash")
	hook := &effectCutHook{cut: "GJ-05", err: crash}
	coordinator.WithFaults(hook)
	if _, err := coordinator.PublishAssignment(t.Context(), wp08Intent(capsule, ref)); !errors.Is(err, crash) {
		t.Fatalf("want cut: %v", err)
	}
	history, _ := providerStore.History(t.Context())
	if len(history) != 1 {
		t.Fatalf("provider touched before observation: %#v", history)
	}
	restored, err := newEffectCoordinator(t.Context(), runtime, journal, journal, provider, 3)
	if err != nil {
		t.Fatal(err)
	}
	results, err := restored.RecoverUnresolved(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Outcome != EffectAbsent {
		t.Fatalf("recovery=%#v", results)
	}
	history, _ = providerStore.History(t.Context())
	if len(history) != 1 {
		t.Fatalf("recovery blindly repeated effect: %#v", history)
	}
}

func TestEffectLostResponseRecoversAppliedFactAndSameOperationDoesNotRepeat(t *testing.T) {
	journal := newWP08Store()
	runtime, capsule := wp08Runtime(t, journal, "/tmp", "lane-b", string(make([]byte, 40)), "process-b")
	providerStore, provider, ref := wp08Provider(t)
	if err := providerStore.Inject(t.Context(), trackerfake.FailureTimeoutAfterApply); err != nil {
		t.Fatal(err)
	}
	coordinator, _ := newEffectCoordinator(t.Context(), runtime, journal, journal, provider, 3)
	intent := wp08Intent(capsule, ref)
	if _, err := coordinator.PublishAssignment(t.Context(), intent); !tracker.IsKind(err, tracker.ErrorAmbiguous) {
		t.Fatalf("want ambiguous: %v", err)
	}
	results, err := coordinator.RecoverUnresolved(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Outcome != EffectApplied {
		t.Fatalf("got %#v", results[0])
	}
	before, _ := providerStore.History(t.Context())
	replayed, err := coordinator.PublishAssignment(t.Context(), intent)
	if err != nil || replayed.Outcome != EffectApplied {
		t.Fatalf("replay=%#v %v", replayed, err)
	}
	after, _ := providerStore.History(t.Context())
	if len(after) != len(before) {
		t.Fatal("same operation repeated provider mutation")
	}
	mutant := intent
	mutant.DesiredAssignee = "changed"
	if _, err := coordinator.PublishAssignment(t.Context(), mutant); err == nil {
		t.Fatal("changed-input replay survived")
	}
}

func TestEffectExactReplayRejectsMissingOrTamperedDurableIntent(t *testing.T) {
	for _, mutation := range []string{"missing", "tampered"} {
		t.Run(mutation, func(t *testing.T) {
			journal := newWP08Store()
			runtime, capsule := wp08Runtime(t, journal, "/tmp", "lane-b", strings.Repeat("0", 40), "process-b")
			_, provider, ref := wp08Provider(t)
			coordinator, err := newEffectCoordinator(t.Context(), runtime, journal, journal, provider, 3)
			if err != nil {
				t.Fatal(err)
			}
			cut := errors.New("stop after intent")
			coordinator.WithFaults(&effectCutHook{cut: "GJ-05", err: cut})
			intent := wp08Intent(capsule, ref)
			if _, err := coordinator.PublishAssignment(t.Context(), intent); !errors.Is(err, cut) {
				t.Fatalf("establish intent: %v", err)
			}
			key := coordinator.journal.prefix() + "effects/effect-assign/intent"
			journal.mu.Lock()
			record := journal.records[key]
			if mutation == "missing" {
				delete(journal.records, key)
			} else {
				record.Value = append([]byte(nil), record.Value...)
				record.Value[len(record.Value)-1] ^= 1
				journal.records[key] = record
			}
			journal.mu.Unlock()

			if _, err := coordinator.PublishAssignment(t.Context(), intent); err == nil {
				t.Fatalf("%s effect intent survived exact replay", mutation)
			}
		})
	}
}

func TestCompensationPreservesHumanReassignmentAndValidAttributionCompensatesOnce(t *testing.T) {
	for _, human := range []bool{false, true} {
		t.Run(map[bool]string{false: "attributable", true: "human"}[human], func(t *testing.T) {
			journal := newWP08Store()
			runtime, capsule := wp08Runtime(t, journal, "/tmp", "lane-b", string(make([]byte, 40)), "process-b")
			providerStore, provider, ref := wp08Provider(t)
			coordinator, _ := newEffectCoordinator(t.Context(), runtime, journal, journal, provider, 3)
			applied, err := coordinator.PublishAssignment(t.Context(), wp08Intent(capsule, ref))
			if err != nil || applied.Outcome != EffectApplied {
				t.Fatalf("apply=%#v %v", applied, err)
			}
			if human {
				if err := providerStore.HumanAssign(t.Context(), "ticket-a", "person"); err != nil {
					t.Fatal(err)
				}
			}
			result, err := coordinator.Compensate(t.Context(), "effect-assign", "operation-compensate", time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			observed, _ := provider.ObserveAssignment(t.Context(), ref)
			if human {
				if result.Outcome != EffectAttributionLost || observed.Assignee != "person" {
					t.Fatalf("human edit lost: %#v %#v", result, observed)
				}
			} else {
				if result.Outcome != EffectCompensated || observed.Assignee != "human" {
					t.Fatalf("compensation=%#v %#v", result, observed)
				}
				history, _ := providerStore.History(t.Context())
				before := len(history)
				again, err := coordinator.Compensate(t.Context(), "effect-assign", "operation-compensate", time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC))
				if err == nil || again.Outcome == EffectCompensated {
					t.Fatal("terminal compensation repeated")
				}
				history, _ = providerStore.History(t.Context())
				if len(history) != before {
					t.Fatal("compensation repeated provider mutation")
				}
			}
		})
	}
}

type dishonestProjection struct {
	tracker.AssignmentProjection
	observation tracker.AssignmentObservation
	err         error
}

func (d dishonestProjection) ObserveAssignment(context.Context, tracker.ExternalWorkRef) (tracker.AssignmentObservation, error) {
	return d.observation, d.err
}
func TestEffectDishonestAndUnavailableProviderCannotFabricateAttribution(t *testing.T) {
	journal := newWP08Store()
	runtime, capsule := wp08Runtime(t, journal, "/tmp", "lane-b", string(make([]byte, 40)), "process-b")
	_, provider, ref := wp08Provider(t)
	coordinator, _ := newEffectCoordinator(t.Context(), runtime, journal, journal, provider, 3)
	hook := &effectCutHook{cut: "GJ-05", err: errors.New("cut")}
	coordinator.WithFaults(hook)
	_, _ = coordinator.PublishAssignment(t.Context(), wp08Intent(capsule, ref))
	wrong := tracker.AssignmentObservation{ExternalRef: ref, Assignee: "actor-b", OperationMarker: "operation-assign", Observation: tracker.ProviderObservation{ExternalID: "wrong", Revision: 2, ObservedAt: time.Now()}}
	restored, _ := newEffectCoordinator(t.Context(), runtime, journal, journal, dishonestProjection{AssignmentProjection: provider, observation: wrong}, 3)
	results, err := restored.RecoverUnresolved(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Outcome != EffectAmbiguous {
		t.Fatalf("dishonest provider fabricated attribution: %#v", results[0])
	}
}

type slowProjection struct {
	tracker.AssignmentProjection
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *slowProjection) ObserveAssignment(ctx context.Context, ref tracker.ExternalWorkRef) (tracker.AssignmentObservation, error) {
	p.once.Do(func() { close(p.started) })
	select {
	case <-ctx.Done():
		return tracker.AssignmentObservation{}, ctx.Err()
	case <-p.release:
		return p.AssignmentProjection.ObserveAssignment(ctx, ref)
	}
}

func TestEffectSlowAndUnavailableProviderRecoveryIsDeterministicAndBounded(t *testing.T) {
	t.Run("slow", func(t *testing.T) {
		journal := newWP08Store()
		runtime, capsule := wp08Runtime(t, journal, "/tmp", "lane-b", strings.Repeat("0", 40), "process-b")
		store, provider, ref := wp08Provider(t)
		coordinator, _ := newEffectCoordinator(t.Context(), runtime, journal, journal, provider, 3)
		coordinator.WithFaults(&effectCutHook{cut: "GJ-05", err: errors.New("cut")})
		_, _ = coordinator.PublishAssignment(t.Context(), wp08Intent(capsule, ref))
		slow := &slowProjection{AssignmentProjection: provider, started: make(chan struct{}), release: make(chan struct{})}
		restored, _ := newEffectCoordinator(t.Context(), runtime, journal, journal, slow, 3)
		result := make(chan []EffectResolution, 1)
		errs := make(chan error, 1)
		go func() {
			values, err := restored.RecoverUnresolved(t.Context())
			result <- values
			errs <- err
		}()
		<-slow.started
		history, _ := store.History(t.Context())
		if len(history) != 1 {
			t.Fatalf("slow observation repeated mutation: %#v", history)
		}
		close(slow.release)
		values := <-result
		if err := <-errs; err != nil || len(values) != 1 || values[0].Outcome != EffectAbsent {
			t.Fatalf("slow recovery=%#v err=%v", values, err)
		}
	})

	t.Run("unavailable bounded", func(t *testing.T) {
		journal := newWP08Store()
		runtime, capsule := wp08Runtime(t, journal, "/tmp", "lane-b", strings.Repeat("0", 40), "process-b")
		_, provider, ref := wp08Provider(t)
		coordinator, _ := newEffectCoordinator(t.Context(), runtime, journal, journal, provider, 2)
		coordinator.WithFaults(&effectCutHook{cut: "GJ-05", err: errors.New("cut")})
		_, _ = coordinator.PublishAssignment(t.Context(), wp08Intent(capsule, ref))
		unavailable := dishonestProjection{AssignmentProjection: provider, err: &tracker.Error{Kind: tracker.ErrorUnavailable, Message: "provider unavailable"}}
		restored, _ := newEffectCoordinator(t.Context(), runtime, journal, journal, unavailable, 2)
		for attempt := 1; attempt <= 2; attempt++ {
			values, err := restored.RecoverUnresolved(t.Context())
			if err != nil || len(values) != 1 || values[0].Outcome != EffectUnavailable || values[0].Attempts != attempt {
				t.Fatalf("attempt %d recovery=%#v err=%v", attempt, values, err)
			}
		}
		values, err := restored.RecoverUnresolved(t.Context())
		if err != nil || len(values) != 1 || values[0].Outcome != EffectReconciliationLimit || values[0].Attempts != 2 {
			t.Fatalf("bounded recovery=%#v err=%v", values, err)
		}
		restarted, err := newEffectCoordinator(t.Context(), runtime, journal, journal, unavailable, 2)
		if err != nil {
			t.Fatal(err)
		}
		state, ok := restarted.State("effect-assign")
		if !ok || state.Resolution == nil || state.Resolution.Outcome != EffectReconciliationLimit {
			t.Fatalf("durable reconciliation limit=%#v", state)
		}
		if values, err := restarted.RecoverUnresolved(t.Context()); err != nil || len(values) != 0 {
			t.Fatalf("terminal limit re-entered recovery=%#v err=%v", values, err)
		}
	})
}

func TestCompensationStaleRevisionCannotFabricateAttribution(t *testing.T) {
	journal := newWP08Store()
	runtime, capsule := wp08Runtime(t, journal, "/tmp", "lane-b", strings.Repeat("0", 40), "process-b")
	_, provider, ref := wp08Provider(t)
	coordinator, _ := newEffectCoordinator(t.Context(), runtime, journal, journal, provider, 3)
	if result, err := coordinator.PublishAssignment(t.Context(), wp08Intent(capsule, ref)); err != nil || result.Outcome != EffectApplied {
		t.Fatalf("apply=%#v err=%v", result, err)
	}
	observed, err := provider.ObserveAssignment(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	observed.Observation.Revision--
	coordinator.provider = dishonestProjection{AssignmentProjection: provider, observation: observed}
	result, err := coordinator.Compensate(t.Context(), "effect-assign", "operation-stale-compensation", time.Date(2026, 8, 30, 12, 2, 0, 0, time.UTC))
	if err != nil || result.Outcome != EffectAttributionLost {
		t.Fatalf("stale compensation=%#v err=%v", result, err)
	}
	actual, _ := provider.ObserveAssignment(t.Context(), ref)
	if actual.Assignee != "actor-b" {
		t.Fatal("stale revision mutated provider")
	}
}

type unsupportedCompensation struct{ tracker.AssignmentProjection }

func (unsupportedCompensation) CompensateAssignment(context.Context, tracker.CompensateAssignmentRequest) (tracker.AssignmentMutation, error) {
	return tracker.AssignmentMutation{}, &tracker.Error{Kind: tracker.ErrorSafeCompensationUnsupported, Message: "guard unavailable"}
}

func TestCompensationMissingAtomicGuardIsSafeUnsupported(t *testing.T) {
	journal := newWP08Store()
	runtime, capsule := wp08Runtime(t, journal, "/tmp", "lane-b", strings.Repeat("0", 40), "process-b")
	_, provider, ref := wp08Provider(t)
	coordinator, _ := newEffectCoordinator(t.Context(), runtime, journal, journal, provider, 3)
	if result, err := coordinator.PublishAssignment(t.Context(), wp08Intent(capsule, ref)); err != nil || result.Outcome != EffectApplied {
		t.Fatalf("apply=%#v %v", result, err)
	}
	coordinator.provider = unsupportedCompensation{provider}
	result, err := coordinator.Compensate(t.Context(), "effect-assign", "operation-no-guard", time.Date(2026, 8, 30, 12, 2, 0, 0, time.UTC))
	if !tracker.IsKind(err, tracker.ErrorSafeCompensationUnsupported) || result.Outcome != EffectCompensationUnsafe {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	observed, _ := provider.ObserveAssignment(t.Context(), ref)
	if observed.Assignee != "actor-b" {
		t.Fatal("unsupported compensation mutated provider")
	}
}

func TestWorkflowJournalExactReplayAfterLaterTransitionAndRestoreTamperGuards(t *testing.T) {
	journal := newWP08Store()
	config := testConfig(t)
	publisher := newWorkflowJournal(config, journal, journal)
	first := journalPublication{OperationID: "operation-first", CommandType: "effect.test", ResultType: "test-v1", AggregateKind: "external-effect", AggregateID: "effect-replay", EventType: "effect.observed", Payload: map[string]any{"step": 1}, Result: map[string]any{"step": 1}}
	if _, err := publisher.publish(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.OperationID = "operation-second"
	second.Payload = map[string]any{"step": 2}
	second.Result = map[string]any{"step": 2}
	if _, err := publisher.publish(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	calls := journal.applyCalls
	if result, err := publisher.publish(t.Context(), first); err != nil || result.Kind != application.OutcomeAlreadyCommitted {
		t.Fatalf("exact replay=%#v %v", result, err)
	}
	if journal.applyCalls != calls {
		t.Fatal("exact replay consulted mutation path")
	}
	changed := first
	changed.Result = map[string]any{"step": 99}
	if _, err := publisher.publish(t.Context(), changed); err == nil {
		t.Fatal("same operation changed input survived")
	}
	if events, err := publisher.loadAggregateEvents(t.Context(), "external-effect"); err != nil || len(events) != 2 || events[0].AggregateLogicalVersion != 1 || events[1].AggregateLogicalVersion != 2 {
		t.Fatalf("bounded restore=%d %v", len(events), err)
	}
	journal.mu.Lock()
	headKey := publisher.prefix() + "heads/external-effect/effect-replay"
	headRecord := journal.records[headKey]
	head, _ := controljournal.DecodeAggregateHeadV1(headRecord.Value)
	head.StateDigest = sha256.Sum256([]byte("tampered"))
	headRecord.Value, _ = controljournal.EncodeAggregateHeadV1(head)
	journal.records[headKey] = headRecord
	journal.mu.Unlock()
	if _, err := publisher.loadAggregateEvents(t.Context(), "external-effect"); err == nil {
		t.Fatal("tampered terminal head survived restore")
	}
}

func TestM1RegistriesCoverEveryWP08ProductionPublication(t *testing.T) {
	coordinates := []struct {
		kind      controljournal.AggregateKind
		eventType controljournal.EventType
	}{
		{"external-effect", "effect.intent_recorded"},
		{"external-effect", "effect.observed"},
		{"external-effect", "effect.reconciliation_limited"},
		{"external-effect", "effect.compensation_recorded"},
		{"candidate", "candidate.created"},
		{"candidate", "candidate.refused"},
		{"review", "review.recorded"},
		{"integration", "integration.plan"},
		{"integration", "integration.plan_completed"},
		{"integration", "integration.intent"},
		{"integration", "integration.activated"},
		{"integration", "integration.workspace"},
		{"integration", "integration.revalidated"},
		{"integration", "integration.result"},
		{"integration", "integration.repair"},
		{"integration", "integration.completed"},
		{"integration", "integration.cleanup_eligible"},
		{"integration", "integration.cleanup_completed"},
	}
	eventRegistry, err := M1EventRegistryV1()
	if err != nil {
		t.Fatal(err)
	}
	reducerRegistry, err := M1ReducerRegistryV1()
	if err != nil {
		t.Fatal(err)
	}
	stored := make([]controljournal.StoredEventV1, 0, len(coordinates))
	for index, coordinate := range coordinates {
		count, _ := controljournal.NewEventCountV1(1)
		ordinal, _ := controljournal.NewEventIndexV1(0)
		event, eventErr := controljournal.NewEventEnvelopeV1(controljournal.EventEnvelopeInputV1{
			EnvelopeVersion: 1, EventID: controljournal.EventID("event-registry-" + fmtInt(index+10)), EventType: coordinate.eventType,
			EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", ProjectID: "project-m1", AggregateKind: coordinate.kind,
			AggregateID: controljournal.AggregateID("aggregate-registry-" + fmtInt(index+10)), AggregateLogicalVersion: 1,
			OperationID: controljournal.OperationID("operation-registry-" + fmtInt(index+10)), RuntimeEpochID: "epoch-m1", EventIndex: ordinal, EventCount: count,
			WorkRevisionID: "revision-one", PolicyRevision: "m1", DoctrineRevision: "m1", ProfileRevision: "implementer-ivan", Payload: []byte(`{"covered":true}`),
		})
		if eventErr != nil {
			t.Fatal(eventErr)
		}
		stored = append(stored, controljournal.StoredEventV1{RecordRevision: uint64(index + 1), Envelope: event})
	}
	journal, err := controljournal.ValidateJournalV1(eventRegistry, stored)
	if err != nil {
		t.Fatal(err)
	}
	result, err := playback.RunV1(playback.InputV1{Journal: journal, Reducers: reducerRegistry})
	if err != nil || result.EventCount != uint64(len(coordinates)) {
		t.Fatalf("registry playback count=%d err=%v", result.EventCount, err)
	}
}

func TestRealGapDBEffectIntentGJ05AndObservationRecovery(t *testing.T) {
	binary := os.Getenv("SPEC_KITTY_M1_GAPDBD")
	if binary == "" {
		t.Skip("set SPEC_KITTY_M1_GAPDBD to the exact admitted gapdbd")
	}
	binary = privateAdmittedBinary(t, binary)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := sidecargapdb.Start(t.Context(), sidecargapdb.LifecycleConfig{
		ExecutablePath: binary, AdmissionRecordPath: filepath.Join(repoRoot, "docs", "programs", "evidence", "m1-admission.json"),
		RuntimeRoot: privateProvingGroundRoot(t), DataDirectory: filepath.Join(privateProvingGroundRoot(t), "database"), SupervisorRunID: "run-m1",
		ProductPolicySHA256: strings.Repeat("a", 64), StartTimeout: 5 * time.Second, StopTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lifecycle.Stop(context.Background()); err != nil {
			t.Errorf("stop GapDB: %v", err)
		}
	}()
	adapter, connection, err := storegapdb.Dial(lifecycle.Provenance().ConfiguredSocket, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	events, err := M1EventRegistryV1()
	if err != nil {
		t.Fatal(err)
	}
	reducers, err := M1ReducerRegistryV1()
	if err != nil {
		t.Fatal(err)
	}
	eventDigest, _ := events.Digest()
	reducerDigest, _ := reducers.Digest()
	binding, err := lifecycle.ProductRestoreBindingV1()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := application.NewRestoreReaderV1(adapter, "project-m1", "mission-m1")
	if err != nil {
		t.Fatal(err)
	}
	pristine, err := checkpoint.RestoreV1(t.Context(), checkpoint.RestoreInputV1{
		Reader: reader, Source: checkpoint.SourceIdentityV1{ProjectID: "project-m1", MissionID: "mission-m1", RunID: "run-m1", ReducerSetDigest: reducerDigest, EventSchemaSetDigest: eventDigest},
		Binding: binding, EventRegistry: events, Reducers: reducers,
	})
	if err != nil || !pristine.Pristine {
		t.Fatalf("pristine restore=%+v err=%v", pristine, err)
	}
	admission, err := pristine.AdmissionV1()
	if err != nil {
		t.Fatal(err)
	}
	productProof, err := lifecycle.MarkProductRecovered(admission)
	if err != nil {
		t.Fatal(err)
	}
	startupProof, err := lifecycle.BindOwnerStartupEpochV1("epoch-one")
	if err != nil {
		t.Fatal(err)
	}
	epoch, result, err := application.AdmitRuntimeEpochV1(t.Context(), adapter, application.EpochAdmissionInputV1{
		ProjectID: "project-m1", MissionID: "mission-m1", OperationID: "admit-wp08-effects", EventID: "event-admit-wp08-effects", StartupProof: startupProof, ProductRestoreProof: productProof,
	})
	if err != nil || result.Kind != application.OutcomeCommitted {
		t.Fatalf("epoch admission=%+v err=%v", result, err)
	}
	app, err := application.NewService(adapter, epoch)
	if err != nil {
		t.Fatal(err)
	}
	runtime, capsule := wp08Runtime(t, newWP08Store(), "/tmp", "lane-b", strings.Repeat("0", 40), "process-b")
	providerStore, provider, ref := wp08Provider(t)
	coordinator, err := NewEffectCoordinator(t.Context(), runtime, app, adapter, provider, 3)
	if err != nil {
		t.Fatal(err)
	}
	cut := errors.New("GJ-05")
	coordinator.WithFaults(&effectCutHook{cut: "GJ-05", err: cut})
	if _, err := coordinator.PublishAssignment(t.Context(), wp08Intent(capsule, ref)); !errors.Is(err, cut) {
		t.Fatalf("GJ-05=%v", err)
	}
	restored, err := NewEffectCoordinator(t.Context(), runtime, app, adapter, provider, 3)
	if err != nil {
		t.Fatal(err)
	}
	resolutions, err := restored.RecoverUnresolved(t.Context())
	if err != nil || len(resolutions) != 1 || resolutions[0].Outcome != EffectAbsent {
		t.Fatalf("recovery=%#v err=%v", resolutions, err)
	}
	history, _ := providerStore.History(t.Context())
	if len(history) != 1 {
		t.Fatalf("recovery repeated provider mutation: %#v", history)
	}
}
