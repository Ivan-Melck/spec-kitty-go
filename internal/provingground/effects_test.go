package provingground

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/application"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/checkpoint"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/playback"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	baseintegration "github.com/spec-kitty/go-kitty3/internal/integration"
	basereview "github.com/spec-kitty/go-kitty3/internal/review"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	trackerfake "github.com/spec-kitty/go-kitty3/internal/tracker/fake"
)

// wp08InvariantObservation is the focused T046 terminal-path observer. It is
// intentionally smaller than WP09's aggregate evidence runner, but inability
// to inspect any configured surface is always an error, never a vacuous pass.
type wp08InvariantObservation struct {
	runtime      *Runtime
	effect       *EffectCoordinator
	review       *ReviewCoordinator
	repository   *gitrepo.Repository
	requireWP08  bool
	liveWorkflow bool // mutant-control seam; cleanup observes the sealed durable projection
}

func registerWP08InvariantCleanup(t *testing.T, observation wp08InvariantObservation) {
	t.Helper()
	t.Cleanup(func() {
		if err := observation.inspect(context.Background()); err != nil {
			t.Errorf("WP08 universal invariant cleanup: %v", err)
		}
	})
}

func (o wp08InvariantObservation) inspect(ctx context.Context) error {
	if o.runtime == nil || o.runtime.core == nil {
		return errors.New("runtime authority is unavailable for WP08 invariant observation")
	}
	for workID, state := range o.runtime.core.works {
		if state == nil {
			return fmt.Errorf("authority state %s is unavailable", workID)
		}
		state.mu.Lock()
		current := state.current
		if current != nil && (current.WorkID != workID || current.Status != "active" || current.Generation == 0 || current.FenceToken == 0 || current.AuthorityHeadRecordRevision == 0) {
			state.mu.Unlock()
			return fmt.Errorf("authority state %s is incoherent", workID)
		}
		state.mu.Unlock()
	}
	inspected := false
	if o.effect != nil {
		inspected = true
		if err := o.inspectEffects(ctx); err != nil {
			return err
		}
	}
	if o.review != nil {
		inspected = true
		if err := o.inspectWorkflow(ctx); err != nil {
			return err
		}
	}
	if o.requireWP08 && !inspected {
		return errors.New("no WP08 effect or workflow surface was inspectable")
	}
	return nil
}

func (o wp08InvariantObservation) inspectEffects(ctx context.Context) error {
	if o.effect.journal == nil || o.effect.provider == nil {
		return errors.New("effect journal or provider is unavailable")
	}
	// restore verifies the full intent bytes and every operation/event/effect
	// membership before the semantic resolution checks below.
	if err := o.effect.restore(ctx); err != nil {
		return fmt.Errorf("effect restore invariant: %w", err)
	}
	events, err := o.effect.journal.loadAggregateEvents(ctx, "external-effect")
	if err != nil {
		return fmt.Errorf("effect journal invariant: %w", err)
	}
	latest := make(map[controljournal.EffectID]effectState)
	intentSeen := make(map[controljournal.EffectID]bool)
	for _, event := range events {
		var state effectState
		if json.Unmarshal(event.Payload(), &state) != nil || !completeEffectState(state) {
			return errors.New("effect event has an incomplete attributable binding")
		}
		if event.EventType == "effect.intent_recorded" {
			intentSeen[state.EffectID] = true
		}
		latest[state.EffectID] = state
	}
	for effectID, state := range latest {
		if err := validateWP08EffectInvariant(effectID, state, intentSeen[effectID]); err != nil {
			return err
		}
	}
	return nil
}

func validateWP08EffectInvariant(effectID controljournal.EffectID, state effectState, intentSeen bool) error {
	if !intentSeen || !completeEffectState(state) {
		return fmt.Errorf("effect %s has no complete attributable intent", effectID)
	}
	if state.Resolution == nil {
		return nil
	}
	resolution := state.Resolution
	if resolution.EffectID != effectID || resolution.IntentOperation != state.OperationID || resolution.Attempts < 1 || resolution.ObservedAt.IsZero() {
		return fmt.Errorf("effect %s resolution identity is incoherent", effectID)
	}
	attributable := resolution.ProviderRevision != state.ExpectedRevision && resolution.ProviderValue == state.DesiredAssignee && resolution.ProviderMarker == state.Marker
	switch resolution.Outcome {
	case EffectApplied:
		if state.DispatchID == "" || !attributable {
			return fmt.Errorf("effect %s falsely claims application", effectID)
		}
	case EffectAbsent:
		if state.DispatchID != "" || resolution.ProviderRevision != state.ExpectedRevision || resolution.ProviderValue != state.ExpectedAssignee || resolution.ProviderMarker != "" {
			return fmt.Errorf("effect %s falsely claims absence", effectID)
		}
	case EffectAttributionLost:
		if attributable {
			return fmt.Errorf("effect %s discarded still-attributable state", effectID)
		}
	case EffectCompensated:
		if resolution.ProviderRevision == 0 || resolution.ProviderValue != state.ExpectedAssignee || resolution.ProviderMarker != "" {
			return fmt.Errorf("effect %s compensation fact is incoherent", effectID)
		}
	case EffectAmbiguous, EffectUnavailable, EffectCompensationUnsafe, EffectReconciliationLimit:
	default:
		return fmt.Errorf("effect %s has unknown resolution %s", effectID, resolution.Outcome)
	}
	return nil
}

func (o wp08InvariantObservation) inspectWorkflow(ctx context.Context) error {
	if o.review.store == nil || o.review.journal == nil || o.review.repository == nil {
		return errors.New("workflow store, journal, or repository is unavailable")
	}
	for _, kind := range []controljournal.AggregateKind{"candidate", "review", "integration"} {
		if _, err := o.review.journal.loadAggregateEvents(ctx, kind); err != nil {
			return fmt.Errorf("%s journal invariant: %w", kind, err)
		}
	}
	live := o.review.store
	live.mu.Lock()
	criteria := make(map[ids.WorkID][]string, len(live.criteria))
	for workID, values := range live.criteria {
		criteria[workID] = append([]string(nil), values...)
	}
	eligibility := live.eligibility
	reservations := make(map[ids.HandoffID]candidateReservation, len(live.reservations))
	for handoffID, reservation := range live.reservations {
		reservations[handoffID] = reservation
	}
	liveCandidates := make(map[ids.HandoffID]candidateJournalRecord, len(live.candidates))
	for handoffID, candidate := range live.candidates {
		liveCandidates[handoffID] = candidate
	}
	live.mu.Unlock()
	restored, err := newReviewCoordinator(ctx, o.runtime, o.review.journal.app, o.review.journal.port, o.review.repository, eligibility, criteria)
	if err != nil {
		return fmt.Errorf("workflow durable projection is not inspectable: %w", err)
	}
	store := restored.store
	if o.liveWorkflow {
		store = live
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for handoffID, reservation := range reservations {
		if reservation.RequestDigest == "" || reservation.Authority.RuntimeEpochID == "" || reservation.Binding.Status != "active-bound" || reservation.Binding.WorkID == "" || reservation.Binding.RepositoryID == "" || reservation.Binding.LaneID == "" {
			return fmt.Errorf("candidate reservation %s is incomplete", handoffID)
		}
		if candidate, ok := liveCandidates[handoffID]; ok && (candidate.RequestDigest != reservation.RequestDigest || candidate.Authority != reservation.Authority || candidate.Record.WorkID != ids.WorkID(reservation.Binding.WorkID) || candidate.Record.RepositoryID != reservation.Binding.RepositoryID || candidate.Record.LaneID != reservation.Binding.LaneID) {
			return fmt.Errorf("candidate %s differs from its immutable reservation", handoffID)
		}
	}
	for handoffID, candidate := range store.candidates {
		contributors, err := handoff.CanonicalContributors(candidate.Record.Contributors)
		if err != nil || !reflect.DeepEqual(contributors, candidate.Record.Contributors) || candidate.Record.HandoffID != handoffID || candidate.Record.Candidate == "" || candidate.Record.Verification.Candidate != candidate.Record.Candidate || candidate.Record.Verification.ObservedBranchHead != candidate.Record.Candidate || candidate.Record.CriteriaDigest != digestJSON(candidate.Criteria) || candidate.CriteriaDigest != candidate.Record.CriteriaDigest || candidate.Authority.WorkRevisionID != controljournal.WorkRevisionID(candidate.Record.WorkRevisionID) || candidate.Authority.WorkspaceID != controljournal.WorkspaceID(candidate.Record.WorkspaceID) || candidate.Authority.LeaseID != controljournal.LeaseID(candidate.Record.LeaseID) || candidate.RuntimeEpochID != candidate.Authority.RuntimeEpochID {
			return fmt.Errorf("candidate %s immutable evidence is incoherent", handoffID)
		}
	}
	for reviewID, review := range store.reviews {
		candidate, ok := store.candidates[review.Record.HandoffID]
		if !ok || review.Record.ReviewID != reviewID || review.Record.Candidate != candidate.Record.Candidate || review.Record.WorkRevisionID != candidate.Record.WorkRevisionID || review.CandidateDigest != candidate.CandidateDigest || review.CriteriaDigest != candidate.CriteriaDigest {
			return fmt.Errorf("review %s is not bound to its immutable candidate", reviewID)
		}
		for _, contributor := range candidate.Record.Contributors {
			if contributor == review.Record.ReviewerActorID {
				return fmt.Errorf("review %s is not ActorID-independent", reviewID)
			}
		}
	}
	activePlans := 0
	for aggregateID, authority := range store.planAuthorities {
		if !authority.complete() || aggregateID != planAuthorityAggregateID(authority.Plan.RepositoryID, authority.Plan.TargetRef) || !reflect.DeepEqual(store.plans[authority.Plan.OperationID], authority.Plan) {
			return fmt.Errorf("integration plan authority %s is incoherent", aggregateID)
		}
		if authority.Status == planAuthorityActive {
			activePlans++
		}
	}
	if activePlans > 1 {
		return fmt.Errorf("multiple active integration plan authorities observed: %d", activePlans)
	}
	repository := o.repository
	if repository == nil {
		repository = o.review.repository
	}
	target, err := repository.TargetHead(ctx)
	if err != nil || target == "" {
		return errors.New("real Git target is unavailable for integration invariants")
	}
	for operationID, integration := range store.integrations {
		record := integration.Record
		if record.OperationID != operationID || record.RepositoryID != repository.Binding().RepositoryID || record.TargetRef != repository.Binding().TargetRef {
			return fmt.Errorf("integration %s coordinate is incoherent", operationID)
		}
		if record.Status != baseintegration.StatusDone {
			continue
		}
		valid, verifyErr := repository.VerifyIntegrationResult(ctx, record.ResultCommit, record.PreflightTarget, record.Candidate)
		ancestor, ancestorErr := repository.IsAncestor(ctx, record.ResultCommit, target)
		if verifyErr != nil || ancestorErr != nil || !valid || (!ancestor && activePlans == 0) {
			return fmt.Errorf("integration %s completion contradicts real Git facts", operationID)
		}
		review, ok := store.reviews[record.ReviewID]
		if !ok || review.Record.HandoffID != record.HandoffID || review.Record.Candidate != record.Candidate || review.Record.Verdict != basereview.Approved {
			return fmt.Errorf("integration %s is not backed by an approved review", operationID)
		}
	}
	return nil
}

type wp08Store struct {
	mu         sync.Mutex
	revision   uint64
	records    map[string]storegapdb.Record
	applyCalls int
}

type restoreBoundMutantPort struct {
	*wp08Store
	mode  string
	calls int
}

func (p *restoreBoundMutantPort) Scan(_ context.Context, request storegapdb.ScanRequest) (storegapdb.ScanPage, error) {
	p.calls++
	page := storegapdb.ScanPage{ObservedRevision: p.revision, AsOf: time.Unix(1, 0).UTC()}
	switch p.mode {
	case "pages":
		page.Truncated = true
		page.Cursor = strings.Repeat("x", p.calls)
	case "events":
		page.Records = make([]storegapdb.Record, maxWorkflowRestoreEvents+1)
		for i := range page.Records {
			page.Records[i] = storegapdb.Record{Key: request.Prefix + "event", Value: []byte(`{}`), Revision: p.revision}
		}
	case "bytes":
		page.Records = []storegapdb.Record{{Key: request.Prefix + "event", Value: make([]byte, maxWorkflowRestoreBytes+1), Revision: p.revision}}
	}
	return page, nil
}

func newWP08Store() *wp08Store {
	return &wp08Store{revision: 1, records: make(map[string]storegapdb.Record)}
}

func newObservedEffectCoordinator(t *testing.T, ctx context.Context, runtime *Runtime, app batchApplier, port workflowJournalPort, provider tracker.AssignmentProjection, maxAttempts int) (*EffectCoordinator, error) {
	t.Helper()
	coordinator, err := newEffectCoordinator(ctx, runtime, app, port, provider, maxAttempts)
	if err == nil {
		registerWP08InvariantCleanup(t, wp08InvariantObservation{runtime: runtime, effect: coordinator, requireWP08: true})
	}
	return coordinator, err
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
	coordinator, err := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, provider, 3)
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
	restored, err := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, provider, 3)
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
	coordinator, _ := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, provider, 3)
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

func TestWP08EffectInvariantMutantsCannotFabricateBindingOrAttribution(t *testing.T) {
	journal := newWP08Store()
	runtime, capsule := wp08Runtime(t, journal, "/tmp", "lane-b", strings.Repeat("0", 40), "process-b")
	_, provider, ref := wp08Provider(t)
	coordinator, err := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, provider, 3)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := coordinator.PublishAssignment(t.Context(), wp08Intent(capsule, ref)); err != nil || result.Outcome != EffectApplied {
		t.Fatalf("establish applied effect: %#v %v", result, err)
	}
	state, _ := coordinator.State("effect-assign")
	if err := validateWP08EffectInvariant(state.EffectID, state, true); err != nil {
		t.Fatal(err)
	}
	mutants := map[string]func(*effectState){
		"binding":      func(s *effectState) { s.ProjectID = "" },
		"revision":     func(s *effectState) { s.Resolution.ProviderRevision = s.ExpectedRevision },
		"value-marker": func(s *effectState) { s.Resolution.ProviderMarker = "foreign-operation" },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			mutant := state
			resolution := *state.Resolution
			mutant.Resolution = &resolution
			mutate(&mutant)
			if err := validateWP08EffectInvariant(mutant.EffectID, mutant, true); err == nil {
				t.Fatal("effect invariant admitted falsified attribution")
			}
		})
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

func TestEffectRestoreRejectsNonExactOperationMembership(t *testing.T) {
	for _, mutation := range []string{"extra-effect", "extra-event", "wrong-event"} {
		t.Run(mutation, func(t *testing.T) {
			journal := newWP08Store()
			runtime, capsule := wp08Runtime(t, journal, "/tmp", "lane-b", strings.Repeat("0", 40), "process-b")
			_, provider, ref := wp08Provider(t)
			coordinator, _ := newEffectCoordinator(t.Context(), runtime, journal, journal, provider, 3)
			coordinator.WithFaults(&effectCutHook{cut: "GJ-05", err: errors.New("cut")})
			_, _ = coordinator.PublishAssignment(t.Context(), wp08Intent(capsule, ref))
			key := coordinator.journal.prefix() + "operations/operation-assign"
			journal.mu.Lock()
			stored := journal.records[key]
			record, err := controljournal.DecodeOperationRecordV1(stored.Value)
			if err != nil {
				journal.mu.Unlock()
				t.Fatal(err)
			}
			events, effects := record.EventIDs(), record.EffectIDs()
			switch mutation {
			case "extra-effect":
				effects = append(effects, "effect-foreign")
			case "extra-event":
				events = append(events, "event-foreign")
			case "wrong-event":
				events[0] = "event-foreign"
			}
			mutant, err := controljournal.NewOperationRecordV1(controljournal.OperationRecordInputV1{OperationID: record.OperationID, CommandType: record.CommandType, RequestDigest: record.RequestDigest, RuntimeEpochID: record.RuntimeEpochID, ExpectedPreconditionsDigest: record.ExpectedPreconditionsDigest, ResultType: record.ResultType, ResultSchemaVersion: record.ResultSchemaVersion, CanonicalResult: record.CanonicalResult(), EventIDs: events, EffectIDs: effects})
			if err != nil {
				journal.mu.Unlock()
				t.Fatal(err)
			}
			stored.Value, err = controljournal.EncodeOperationRecordV1(mutant)
			if err == nil {
				journal.records[key] = stored
			}
			journal.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, provider, 3); err == nil {
				t.Fatalf("%s operation membership survived restore", mutation)
			}
		})
	}
}

func TestWorkflowRestoreEnforcesGlobalPageEventAnd48MiBBounds(t *testing.T) {
	for _, mode := range []string{"pages", "events", "bytes"} {
		t.Run(mode, func(t *testing.T) {
			journal := newWP08Store()
			config := testConfig(t)
			mutant := &restoreBoundMutantPort{wp08Store: journal, mode: mode}
			workflow := newWorkflowJournal(config, journal, mutant)
			if _, err := workflow.loadAggregateEvents(t.Context(), "external-effect"); err == nil {
				t.Fatalf("%s restore budget mutant survived", mode)
			}
			if mutant.calls > maxWorkflowRestorePages+1 {
				t.Fatalf("page traversal exceeded declared bound: %d", mutant.calls)
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
			coordinator, _ := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, provider, 3)
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
	coordinator, _ := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, provider, 3)
	hook := &effectCutHook{cut: "GJ-05", err: errors.New("cut")}
	coordinator.WithFaults(hook)
	_, _ = coordinator.PublishAssignment(t.Context(), wp08Intent(capsule, ref))
	wrong := tracker.AssignmentObservation{ExternalRef: ref, Assignee: "actor-b", OperationMarker: "operation-assign", Observation: tracker.ProviderObservation{ExternalID: "wrong", Revision: 2, ObservedAt: time.Now()}}
	restored, _ := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, dishonestProjection{AssignmentProjection: provider, observation: wrong}, 3)
	results, err := restored.RecoverUnresolved(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Outcome != EffectAmbiguous {
		t.Fatalf("dishonest provider fabricated attribution: %#v", results[0])
	}
}

func TestEffectStaleDesiredValueAndMarkerCannotFabricateAppliedFact(t *testing.T) {
	journal := newWP08Store()
	runtime, capsule := wp08Runtime(t, journal, "/tmp", "lane-b", strings.Repeat("0", 40), "process-b")
	_, provider, ref := wp08Provider(t)
	coordinator, _ := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, provider, 3)
	coordinator.WithFaults(&effectCutHook{cut: "GJ-05", err: errors.New("cut")})
	_, _ = coordinator.PublishAssignment(t.Context(), wp08Intent(capsule, ref))
	stale := tracker.AssignmentObservation{
		ExternalRef: tracker.ExternalWorkRef{ProjectID: ref.ProjectID, WorkID: ref.WorkID, Provider: ref.Provider, ExternalID: ref.ExternalID, MetadataSchema: ref.MetadataSchema, CreatedByOperation: ref.CreatedByOperation, LastObservation: 1},
		Assignee:    "actor-b", OperationMarker: "operation-assign",
		Observation: tracker.ProviderObservation{ExternalID: ref.ExternalID, Revision: 1, ObservedAt: time.Date(2026, 8, 30, 12, 3, 0, 0, time.UTC), Assignee: "actor-b", OperationMarker: "operation-assign"},
	}
	restored, err := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, dishonestProjection{AssignmentProjection: provider, observation: stale}, 3)
	if err != nil {
		t.Fatal(err)
	}
	results, err := restored.RecoverUnresolved(t.Context())
	if err != nil || len(results) != 1 || results[0].Outcome == EffectApplied {
		t.Fatalf("stale desired state fabricated application: %#v err=%v", results, err)
	}
}

type slowProjection struct {
	tracker.AssignmentProjection
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type blockingMutationProjection struct {
	tracker.AssignmentProjection
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *blockingMutationProjection) ProjectAssignment(ctx context.Context, request tracker.ProjectAssignmentRequest) (tracker.AssignmentMutation, error) {
	p.once.Do(func() { close(p.started) })
	select {
	case <-ctx.Done():
		return tracker.AssignmentMutation{}, ctx.Err()
	case <-p.release:
		return p.AssignmentProjection.ProjectAssignment(ctx, request)
	}
}

func TestConcurrentExactEffectDispatchNeverRecordsTerminalAbsent(t *testing.T) {
	journal := newWP08Store()
	runtime, capsule := wp08Runtime(t, journal, "/tmp", "lane-b", strings.Repeat("0", 40), "process-b")
	providerStore, provider, ref := wp08Provider(t)
	blocked := &blockingMutationProjection{AssignmentProjection: provider, started: make(chan struct{}), release: make(chan struct{})}
	first, err := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, blocked, 3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, provider, 3)
	if err != nil {
		t.Fatal(err)
	}
	intent := wp08Intent(capsule, ref)
	firstResult := make(chan EffectResolution, 1)
	firstErr := make(chan error, 1)
	go func() {
		result, publishErr := first.PublishAssignment(t.Context(), intent)
		firstResult <- result
		firstErr <- publishErr
	}()
	<-blocked.started
	loser, loserErr := second.PublishAssignment(t.Context(), intent)
	if loserErr != nil || loser.Outcome != EffectAmbiguous {
		t.Fatalf("in-flight loser=%#v err=%v", loser, loserErr)
	}
	if loser.Outcome == EffectAbsent {
		t.Fatal("in-flight dispatch was durably classified absent")
	}
	close(blocked.release)
	winner := <-firstResult
	if err := <-firstErr; err != nil || winner.Outcome != EffectApplied {
		t.Fatalf("dispatch winner=%#v err=%v", winner, err)
	}
	restored, err := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, provider, 3)
	if err != nil {
		t.Fatal(err)
	}
	state, ok := restored.State(intent.EffectID)
	if !ok || state.Resolution == nil || state.Resolution.Outcome != EffectApplied {
		t.Fatalf("terminal effect=%#v", state)
	}
	history, _ := providerStore.History(t.Context())
	if len(history) != 2 { // seed plus exactly one assignment
		t.Fatalf("concurrent exact dispatch provider history=%#v", history)
	}
}

func TestCrashAfterDispatchReservationRemainsAmbiguousWithoutBlindRepeat(t *testing.T) {
	journal := newWP08Store()
	runtime, capsule := wp08Runtime(t, journal, "/tmp", "lane-b", strings.Repeat("0", 40), "process-b")
	providerStore, provider, ref := wp08Provider(t)
	coordinator, _ := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, provider, 3)
	crash := errors.New("crash after dispatch reservation")
	coordinator.WithFaults(&effectCutHook{cut: "EF-01", err: crash})
	if _, err := coordinator.PublishAssignment(t.Context(), wp08Intent(capsule, ref)); !errors.Is(err, crash) {
		t.Fatalf("want EF-01 crash: %v", err)
	}
	restored, err := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, provider, 3)
	if err != nil {
		t.Fatal(err)
	}
	results, err := restored.RecoverUnresolved(t.Context())
	if err != nil || len(results) != 1 || results[0].Outcome != EffectAmbiguous {
		t.Fatalf("reserved-dispatch recovery=%#v err=%v", results, err)
	}
	history, _ := providerStore.History(t.Context())
	if len(history) != 1 {
		t.Fatalf("reserved-dispatch crash blindly repeated provider mutation: %#v", history)
	}
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
		coordinator, _ := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, provider, 3)
		coordinator.WithFaults(&effectCutHook{cut: "GJ-05", err: errors.New("cut")})
		_, _ = coordinator.PublishAssignment(t.Context(), wp08Intent(capsule, ref))
		slow := &slowProjection{AssignmentProjection: provider, started: make(chan struct{}), release: make(chan struct{})}
		restored, _ := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, slow, 3)
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
		coordinator, _ := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, provider, 2)
		coordinator.WithFaults(&effectCutHook{cut: "GJ-05", err: errors.New("cut")})
		_, _ = coordinator.PublishAssignment(t.Context(), wp08Intent(capsule, ref))
		unavailable := dishonestProjection{AssignmentProjection: provider, err: &tracker.Error{Kind: tracker.ErrorUnavailable, Message: "provider unavailable"}}
		restored, _ := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, unavailable, 2)
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
		restarted, err := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, unavailable, 2)
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
	coordinator, _ := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, provider, 3)
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
	coordinator, _ := newObservedEffectCoordinator(t, t.Context(), runtime, journal, journal, provider, 3)
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

func TestWP08AttributionCampaignRunsOneHundredRealScenariosWithAntiVacuity(t *testing.T) {
	ambiguityTrials, reassignmentTrials, violations := 0, 0, 0
	err := IntegrationAttributionCampaign(t.Context(), 100, func(ctx context.Context, repetition int) error {
		journal := newWP08Store()
		runtime, capsule := wp08Runtime(t, journal, "/tmp", "lane-b", strings.Repeat("0", 40), "process-b")
		providerStore, provider, ref := wp08Provider(t)
		if injectErr := providerStore.Inject(ctx, trackerfake.FailureTimeoutAfterApply); injectErr != nil {
			return injectErr
		}
		coordinator, createErr := newObservedEffectCoordinator(t, ctx, runtime, journal, journal, provider, 3)
		if createErr != nil {
			return createErr
		}
		intent := wp08Intent(capsule, ref)
		ambiguous, publishErr := coordinator.PublishAssignment(ctx, intent)
		ambiguityTrials++
		if !tracker.IsKind(publishErr, tracker.ErrorAmbiguous) || terminalEffectOutcome(ambiguous.Outcome) {
			violations++
			return errors.New("ambiguity trial did not remain recoverable")
		}
		values, recoverErr := coordinator.RecoverUnresolved(ctx)
		if recoverErr != nil || len(values) != 1 || values[0].Outcome != EffectApplied {
			violations++
			return errors.New("ambiguity trial did not recover its attributable applied fact")
		}
		if humanErr := providerStore.HumanAssign(ctx, "ticket-a", "human-reassigned"); humanErr != nil {
			return humanErr
		}
		terminal, compensateErr := coordinator.Compensate(ctx, intent.EffectID, controljournal.OperationID("operation-compensate-"+strconv.Itoa(repetition)), intent.OccurredAt.Add(time.Minute))
		reassignmentTrials++
		if compensateErr != nil || terminal.Outcome != EffectAttributionLost {
			violations++
			return errors.New("reassignment trial did not preserve the human writer")
		}
		state, ok := coordinator.State(intent.EffectID)
		if !ok || !completeEffectState(state) || state.Resolution == nil || state.Resolution.Outcome != EffectAttributionLost {
			return errors.New("campaign terminal state violated universal effect invariants")
		}
		if _, restoreErr := coordinator.journal.loadAggregateEvents(ctx, "external-effect"); restoreErr != nil {
			return restoreErr
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if ambiguityTrials != 100 || reassignmentTrials != 100 || violations != 0 {
		t.Fatalf("dishonest campaign census ambiguity=%d reassignment=%d violations=%d", ambiguityTrials, reassignmentTrials, violations)
	}
}

func TestWorkflowJournalExactReplayAfterLaterTransitionAndRestoreTamperGuards(t *testing.T) {
	t.Run("sealed predecessor epoch lineage", func(t *testing.T) {
		publisher := workflowEpochLineageFixture(t, "")
		events, err := publisher.loadAggregateEvents(t.Context(), "external-effect")
		epochs := map[controljournal.RuntimeEpochID]bool{}
		for _, event := range events {
			epochs[event.RuntimeEpochID] = true
		}
		if err != nil || len(events) != 2 || !epochs["epoch-predecessor"] || !epochs[publisher.config.RuntimeEpochID] {
			t.Fatalf("sealed predecessor lineage restore=%d err=%v events=%+v", len(events), err, events)
		}
	})
	for _, mutation := range []string{"unlinked-epoch", "unlinked-first-predecessor", "foreign-run", "malformed-transition", "unsealed-head"} {
		t.Run(mutation, func(t *testing.T) {
			publisher := workflowEpochLineageFixture(t, mutation)
			if _, err := publisher.loadAggregateEvents(t.Context(), "external-effect"); err == nil {
				t.Fatalf("%s runtime epoch lineage mutant survived", mutation)
			}
		})
	}

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

func workflowEpochLineageFixture(t *testing.T, mutation string) *workflowJournal {
	t.Helper()
	journal := newWP08Store()
	current := testConfig(t)
	predecessor := current
	predecessor.RuntimeEpochID = "epoch-predecessor"
	oldPublisher := newWorkflowJournal(predecessor, journal, journal)
	if _, err := oldPublisher.publish(t.Context(), journalPublication{OperationID: "operation-admit-predecessor", CommandType: "runtime.epoch_admit", ResultType: "runtime.epoch_admitted", AggregateKind: "runtime", AggregateID: "host-runtime", EventType: "runtime.epoch_admitted", Payload: map[string]any{"runtime_epoch_id": predecessor.RuntimeEpochID}, Result: map[string]any{"runtime_epoch_id": predecessor.RuntimeEpochID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := oldPublisher.publish(t.Context(), journalPublication{OperationID: "operation-old-effect", CommandType: "effect.test", ResultType: "test-v1", AggregateKind: "external-effect", AggregateID: "effect-old", EventType: "effect.observed", Payload: map[string]any{"epoch": "predecessor"}, Result: map[string]any{"epoch": "predecessor"}}); err != nil {
		t.Fatal(err)
	}
	if mutation == "unlinked-epoch" {
		foreign := current
		foreign.RuntimeEpochID = "epoch-unlinked"
		if _, err := newWorkflowJournal(foreign, journal, journal).publish(t.Context(), journalPublication{OperationID: "operation-unlinked-effect", CommandType: "effect.test", ResultType: "test-v1", AggregateKind: "external-effect", AggregateID: "effect-unlinked", EventType: "effect.observed", Payload: map[string]any{"epoch": "unlinked"}, Result: map[string]any{"epoch": "unlinked"}}); err != nil {
			t.Fatal(err)
		}
	}
	currentPublisher := newWorkflowJournal(current, journal, journal)
	runtimeHeadKey := currentPublisher.prefix() + "heads/runtime/host-runtime"
	runtimeHeadRecord, err := journal.Get(t.Context(), runtimeHeadKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := currentPublisher.publish(t.Context(), journalPublication{OperationID: "operation-admit-current", CommandType: "runtime.epoch_admit", ResultType: "runtime.epoch_admitted", AggregateKind: "runtime", AggregateID: "host-runtime", EventType: "runtime.epoch_admitted", Payload: map[string]any{"runtime_epoch_id": current.RuntimeEpochID}, Result: map[string]any{"runtime_epoch_id": current.RuntimeEpochID}, ExpectedLogical: 1, ExpectedRevision: runtimeHeadRecord.Revision}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	operationID := controljournal.OperationID("operation-owner-restart")
	runID := current.RunID
	aggregateID := controljournal.AggregateID(runID)
	restartPredecessor := predecessor.RuntimeEpochID
	if mutation == "unlinked-first-predecessor" {
		restartPredecessor = "epoch-forged-predecessor"
	}
	if mutation == "foreign-run" {
		runID = "run-foreign"
		aggregateID = controljournal.AggregateID(runID)
	}
	digest, err := ownerRestartCommandDigest(operationID, runID, restartPredecessor, current.RuntimeEpochID, now)
	if err != nil {
		t.Fatal(err)
	}
	if mutation == "malformed-transition" {
		digest = strings.Repeat("0", 64)
	}
	restart := ownerRestartState{OperationID: operationID, RunID: runID, PredecessorEpoch: restartPredecessor, RuntimeEpochID: current.RuntimeEpochID, OccurredAt: now, CommandDigest: digest}
	if _, err := currentPublisher.publish(t.Context(), journalPublication{OperationID: operationID, CommandType: "runtime.owner_restart", ResultType: "runtime.owner_restarted", AggregateKind: "runtime-composition", AggregateID: aggregateID, EventType: "runtime.owner_restarted", Payload: restart, Result: restart}); err != nil {
		t.Fatal(err)
	}
	if mutation == "foreign-run" {
		digest, err = ownerRestartCommandDigest("operation-owner-restart-current", current.RunID, predecessor.RuntimeEpochID, current.RuntimeEpochID, now.Add(time.Nanosecond))
		if err != nil {
			t.Fatal(err)
		}
		restart = ownerRestartState{OperationID: "operation-owner-restart-current", RunID: current.RunID, PredecessorEpoch: predecessor.RuntimeEpochID, RuntimeEpochID: current.RuntimeEpochID, OccurredAt: now.Add(time.Nanosecond), CommandDigest: digest}
		if _, err := currentPublisher.publish(t.Context(), journalPublication{OperationID: restart.OperationID, CommandType: "runtime.owner_restart", ResultType: "runtime.owner_restarted", AggregateKind: "runtime-composition", AggregateID: controljournal.AggregateID(current.RunID), EventType: "runtime.owner_restarted", Payload: restart, Result: restart}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := currentPublisher.publish(t.Context(), journalPublication{OperationID: "operation-current-effect", CommandType: "effect.test", ResultType: "test-v1", AggregateKind: "external-effect", AggregateID: "effect-current", EventType: "effect.observed", Payload: map[string]any{"epoch": "current"}, Result: map[string]any{"epoch": "current"}}); err != nil {
		t.Fatal(err)
	}
	if mutation == "unsealed-head" {
		key := currentPublisher.prefix() + "heads/runtime-composition/" + string(current.RunID)
		journal.mu.Lock()
		record := journal.records[key]
		head, _ := controljournal.DecodeAggregateHeadV1(record.Value)
		head.StateDigest = sha256.Sum256([]byte("unsealed"))
		record.Value, _ = controljournal.EncodeAggregateHeadV1(head)
		journal.records[key] = record
		journal.mu.Unlock()
	}
	return currentPublisher
}

func TestM1RegistriesCoverEveryWP08ProductionPublication(t *testing.T) {
	coordinates := []struct {
		kind      controljournal.AggregateKind
		eventType controljournal.EventType
	}{
		{"external-effect", "effect.intent_recorded"},
		{"external-effect", "effect.dispatch_reserved"},
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
	if err := (wp08InvariantObservation{runtime: runtime, effect: restored, requireWP08: true}).inspect(t.Context()); err != nil {
		t.Fatalf("external GapDB terminal invariants: %v", err)
	}
}
