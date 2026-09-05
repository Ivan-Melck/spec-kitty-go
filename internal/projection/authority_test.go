package projection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type projectionCommitPortV2 struct {
	mu       sync.Mutex
	revision uint64
}

func (p *projectionCommitPortV2) Commit(_ context.Context, plan journalv2.SealedCommitPlanV2) (journalv2.CommitObservationV2, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.revision++
	return journalv2.NewCommitObservationV2(plan.PublicationID(), plan.Digest(), plan.RecordSetDigest(), p.revision, p.revision, plan.MutationCount())
}

func TestProjectionPublishesCompleteAcknowledgedRevisionsAndDefensiveQueries(t *testing.T) {
	authority, coordinator := projectionFixtureV2(t)
	first := publishProjectionFixtureV2(t, coordinator, "a", "mission", "mission-1")
	if err := authority.PublishAcknowledged(first); err != nil {
		t.Fatal(err)
	}
	coordinate := projectionCoordinateFixtureV2(t, "mission", "mission-1")
	view, err := authority.Get(GetQueryV2{Coordinate: coordinate, AtRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if view.DatabaseID != "database-1" || view.ProjectionRevision != 1 || view.RuntimeEpochID != "epoch-1" || view.ProjectionDigest == "" {
		t.Fatalf("get result lacks exact projection attribution: %+v", view)
	}
	view.CanonicalState[0] ^= 1
	again, err := authority.Get(GetQueryV2{Coordinate: coordinate})
	if err != nil || string(again.CanonicalState) != `{"name":"a"}` {
		t.Fatalf("view=%s err=%v", again.CanonicalState, err)
	}
	before := authority.Snapshot().CanonicalBytes()
	if err := authority.PublishAcknowledged(first); !IsKindV2(err, ErrorIncompleteRevisionV2) {
		t.Fatalf("duplicate acknowledgement err=%v", err)
	}
	if got := authority.Snapshot().CanonicalBytes(); string(got) != string(before) {
		t.Fatal("refusal changed published projection")
	}
}

func TestProjectionRefusesARevisionGapWithoutPoisoningLaterAdmission(t *testing.T) {
	authority, coordinator := projectionFixtureV2(t)
	first := publishProjectionFixtureV2(t, coordinator, "gap-a", "mission", "mission-gap")
	if err := authority.PublishAcknowledged(first); err != nil {
		t.Fatal(err)
	}
	second := publishProjectionFixtureV2(t, coordinator, "gap-b", "workgraph", "graph-gap-b")
	third := publishProjectionFixtureV2(t, coordinator, "gap-c", "artifact_catalog", "catalog-gap-c")
	before := authority.Snapshot().CanonicalBytes()
	if err := authority.PublishAcknowledged(third); !IsKindV2(err, ErrorIncompleteRevisionV2) {
		t.Fatalf("revision gap err=%v", err)
	}
	if got := authority.Snapshot().CanonicalBytes(); string(got) != string(before) {
		t.Fatal("revision-gap refusal changed projection")
	}
	if err := authority.PublishAcknowledged(second); err != nil {
		t.Fatalf("valid predecessor was poisoned: %v", err)
	}
	if err := authority.PublishAcknowledged(third); err != nil {
		t.Fatalf("valid successor was poisoned: %v", err)
	}
}

func TestProjectionListCursorIsBoundedStableAndRevisionScoped(t *testing.T) {
	authority, coordinator := projectionFixtureV2(t)
	ack := publishProjectionFixtureV2(t, coordinator, "a", "mission", "mission-1")
	if err := authority.PublishAcknowledged(ack); err != nil {
		t.Fatal(err)
	}
	page1, err := authority.List(ListQueryV2{Limit: 1})
	if err != nil || len(page1.Items) != 1 || page1.NextCursor == "" {
		t.Fatalf("page1=%+v err=%v", page1, err)
	}
	page2, err := authority.List(ListQueryV2{Limit: 1, Cursor: page1.NextCursor})
	if err != nil || len(page2.Items) != 1 || page2.NextCursor != "" {
		t.Fatalf("page2=%+v err=%v", page2, err)
	}
	filtered, err := authority.List(ListQueryV2{Family: "mission", MissionID: "mission-1", Limit: 10})
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].Coordinate.Kind != "mission" {
		t.Fatalf("filtered=%+v err=%v", filtered, err)
	}
	next := publishProjectionFixtureV2(t, coordinator, "b", "workgraph", "graph-1")
	if err := authority.PublishAcknowledged(next); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.List(ListQueryV2{Limit: 1, Cursor: page1.NextCursor}); !IsKindV2(err, ErrorStaleCursorV2) {
		t.Fatalf("stale cursor err=%v", err)
	}
	if _, err := authority.List(ListQueryV2{Limit: MaxQueryPageV2 + 1}); !IsKindV2(err, ErrorInvalidQueryV2) {
		t.Fatalf("over-budget list err=%v", err)
	}
	for name, query := range map[string]ListQueryV2{
		"project": {ProjectID: journalv2.ProjectID(strings.Repeat("p", journalv2.MaxIdentityBytes+1)), Limit: 1},
		"mission": {MissionID: journalv2.MissionID(strings.Repeat("m", journalv2.MaxIdentityBytes+1)), Limit: 1},
		"work":    {WorkID: journalv2.WorkID(strings.Repeat("w", journalv2.MaxIdentityBytes+1)), Limit: 1},
		"family":  {Family: "not_registered", Limit: 1},
	} {
		if _, err := authority.List(query); !IsKindV2(err, ErrorInvalidQueryV2) {
			t.Fatalf("%s filter escaped bound/registry check: %v", name, err)
		}
	}
}

func TestProjectionWatchSignalsChangeAndForcesLaggedOrEpochResync(t *testing.T) {
	authority, coordinator := projectionFixtureV2(t)
	first := publishProjectionFixtureV2(t, coordinator, "a", "mission", "mission-1")
	if err := authority.PublishAcknowledged(first); err != nil {
		t.Fatal(err)
	}
	watch, err := authority.Watch("database-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	second := publishProjectionFixtureV2(t, coordinator, "b", "workgraph", "graph-1")
	if err := authority.PublishAcknowledged(second); err != nil {
		t.Fatal(err)
	}
	notification := <-watch.C
	if notification.Revision != 2 || notification.ResyncRequired {
		t.Fatalf("notification=%+v", notification)
	}
	slow, err := authority.Watch("database-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	third := publishProjectionFixtureV2(t, coordinator, "c", "artifact_catalog", "spec")
	if err := authority.PublishAcknowledged(third); err != nil {
		t.Fatal(err)
	}
	fourth := publishProjectionFixtureV2(t, coordinator, "d", "artifact_revision", "revision-1")
	if err := authority.PublishAcknowledged(fourth); err != nil {
		t.Fatal(err)
	}
	lagged, ok := <-slow.C
	if !ok || !lagged.ResyncRequired || lagged.Revision != 4 {
		t.Fatalf("lagged=%+v open=%t", lagged, ok)
	}
	if _, open := <-slow.C; open {
		t.Fatal("lagged watch remained open")
	}
	watch.Cancel()
	if _, err := authority.Watch("database-other", 4); !IsKindV2(err, ErrorResyncRequiredV2) {
		t.Fatalf("wrong database watch err=%v", err)
	}
}

func TestProjectionWatchCancelIsConcurrentlyIdempotent(t *testing.T) {
	authority, coordinator := projectionFixtureV2(t)
	first := publishProjectionFixtureV2(t, coordinator, "cancel", "mission", "mission-cancel")
	if err := authority.PublishAcknowledged(first); err != nil {
		t.Fatal(err)
	}
	watch, err := authority.Watch("database-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	var callers sync.WaitGroup
	for index := 0; index < 32; index++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			watch.Cancel()
		}()
	}
	callers.Wait()
	if _, open := <-watch.C; open {
		t.Fatal("canceled watch remained open")
	}
}

func TestProgressBaseContainsOnlyExactProjectionAttribution(t *testing.T) {
	authority, coordinator := projectionFixtureV2(t)
	ack := publishProjectionFixtureV2(t, coordinator, "a", "mission", "mission-1")
	if err := authority.PublishAcknowledged(ack); err != nil {
		t.Fatal(err)
	}
	progress, err := authority.ProgressBase("mission-1")
	if err != nil || progress.Revision != 1 || progress.DatabaseID != "database-1" || progress.MissionID != "mission-1" {
		t.Fatalf("progress=%+v err=%v", progress, err)
	}
	if _, err := authority.ProgressBase("missing"); !IsKindV2(err, ErrorNotFoundV2) {
		t.Fatalf("missing mission err=%v", err)
	}
}

func TestQueryPortCannotMutateAuthorityOrCollapseProgressIntoCeremony(t *testing.T) {
	port := reflect.TypeOf((*QueryPortV2)(nil)).Elem()
	want := map[string]bool{"Get": true, "List": true, "ProgressBase": true, "Watch": true}
	if port.NumMethod() != len(want) {
		t.Fatalf("query method count=%d", port.NumMethod())
	}
	for index := 0; index < port.NumMethod(); index++ {
		method := port.Method(index)
		if !want[method.Name] {
			t.Fatalf("query port leaked %s", method.Name)
		}
		for _, forbidden := range []string{"CommitPlan", "AcknowledgedPublication", "AuthorityV2", "Adapter", "Repository"} {
			if strings.Contains(method.Type.String(), forbidden) {
				t.Fatalf("query method %s leaked %s: %s", method.Name, forbidden, method.Type)
			}
		}
	}
	progress := reflect.TypeOf(ProgressBaseV2{})
	for index := 0; index < progress.NumField(); index++ {
		name := strings.ToLower(progress.Field(index).Name)
		for _, forbidden := range []string{"phase", "step", "current", "next", "successor"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("progress reintroduced ceremony field %s", progress.Field(index).Name)
			}
		}
	}
}

func TestWatchCannotBecomeRecoveryStreamMutant(t *testing.T) {
	notification := reflect.TypeOf(NotificationV2{})
	want := map[string]bool{"DatabaseID": true, "Revision": true, "RuntimeEpochID": true, "ProjectionDigest": true, "ResyncRequired": true}
	if notification.NumField() != len(want) {
		t.Fatalf("watch recovery mutant changed notification width to %d", notification.NumField())
	}
	for index := 0; index < notification.NumField(); index++ {
		field := notification.Field(index)
		if !want[field.Name] {
			t.Fatalf("watch recovery mutant leaked %s", field.Name)
		}
		lower := strings.ToLower(field.Name + " " + field.Type.String())
		for _, forbidden := range []string{"event", "payload", "canonicalstate", "cursor", "history", "checkpoint"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("watch notification leaked recovery material %s through %s", forbidden, field.Name)
			}
		}
	}
}

func TestProjectionReadersNeverObservePartialPublication(t *testing.T) {
	authority, coordinator := projectionFixtureV2(t)
	first := publishProjectionFixtureV2(t, coordinator, "atomic-origin", "mission", "mission-atomic")
	if err := authority.PublishAcknowledged(first); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	errorsFound := make(chan string, 1)
	var readers sync.WaitGroup
	for reader := 0; reader < 8; reader++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					snapshot := authority.Snapshot()
					if snapshot == nil || snapshot.Revision() == 0 || !json.Valid(snapshot.CanonicalBytes()) {
						select {
						case errorsFound <- "reader observed an incomplete snapshot":
						default:
						}
						return
					}
				}
			}
		}()
	}
	for revision := 2; revision <= 100; revision++ {
		ack := publishProjectionFixtureV2(t, coordinator, fmt.Sprintf("atomic-%03d", revision), "workgraph", fmt.Sprintf("graph-%03d", revision))
		if err := authority.PublishAcknowledged(ack); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	readers.Wait()
	select {
	case failure := <-errorsFound:
		t.Fatal(failure)
	default:
	}
	if authority.Snapshot().Revision() != 100 {
		t.Fatalf("final revision=%d", authority.Snapshot().Revision())
	}
}

func TestProjectionGetAndListP95UnderTwoMillisecondsAcrossTenThousandObservations(t *testing.T) {
	authority, coordinator := projectionFixtureV2(t)
	ack := publishProjectionFixtureV2(t, coordinator, "latency", "mission", "mission-latency")
	if err := authority.PublishAcknowledged(ack); err != nil {
		t.Fatal(err)
	}
	coordinate := projectionCoordinateFixtureV2(t, "mission", "mission-latency")
	get := func() {
		if _, err := authority.Get(GetQueryV2{Coordinate: coordinate}); err != nil {
			t.Fatal(err)
		}
	}
	list := func() {
		if _, err := authority.List(ListQueryV2{MissionID: "mission-latency", Limit: 10}); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 1_000; index++ {
		get()
		list()
	}
	const observations = 10_000
	getSamples := make([]time.Duration, observations)
	listSamples := make([]time.Duration, observations)
	for index := 0; index < observations; index++ {
		started := time.Now()
		get()
		getSamples[index] = time.Since(started)
		started = time.Now()
		list()
		listSamples[index] = time.Since(started)
	}
	getP95, listP95 := percentile95V2(getSamples), percentile95V2(listSamples)
	t.Logf("NFR-010 reference=%s/%s gomaxprocs=%d samples=%d method=nearest-rank warmed=1000 get_p95=%s list_p95=%s threshold=2ms", runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), observations, getP95, listP95)
	if getP95 >= 2*time.Millisecond || listP95 >= 2*time.Millisecond {
		t.Fatalf("NFR-010 exceeded: get_p95=%s list_p95=%s", getP95, listP95)
	}
}

func percentile95V2(samples []time.Duration) time.Duration {
	ordered := append([]time.Duration(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	return ordered[(len(ordered)*95+99)/100-1]
}

func projectionFixtureV2(t *testing.T) (*AuthorityV2, *journalv2.PublicationCoordinatorV2) {
	t.Helper()
	registry := journalv2.DefaultEventRegistryV2()
	reducers, err := projectionReducersFixtureV2(registry)
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	port := &projectionCommitPortV2{}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(port, tracker)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := NewAuthorityV2("database-1", registry, reducers)
	if err != nil {
		t.Fatal(err)
	}
	return authority, coordinator
}

func projectionReducersFixtureV2(registry journalv2.EventRegistryV2) (journalv2.ReducerSetV2, error) {
	registrations := make([]journalv2.ReducerRegistration, 0, len(journalv2.ExpectedEventTuples()))
	for _, tuple := range journalv2.ExpectedEventTuples() {
		digest := projectionDigestFixtureV2(tuple.Family)
		reduce := journalv2.Reducer(func(_ []byte, event journalv2.EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil })
		if tuple.Family == "operation" {
			digest = journalv2.OperationReducerImplementationDigestV2()
			reduce = journalv2.ReduceOperationStateV2
		}
		registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: digest, Reduce: reduce})
	}
	return journalv2.NewReducerSetV2(registry, registrations)
}

func publishProjectionFixtureV2(t *testing.T, coordinator *journalv2.PublicationCoordinatorV2, suffix, family, aggregate string) journalv2.AcknowledgedPublicationV2 {
	t.Helper()
	plan := projectionPlanFixtureV2(t, suffix, family, aggregate)
	ack, err := coordinator.Publish(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	return ack
}

func projectionPlanFixtureV2(t *testing.T, suffix, family, aggregate string) journalv2.SealedCommitPlanV2 {
	t.Helper()
	operationID := journalv2.OperationID("operation-" + suffix)
	opEventID := journalv2.EventID("event-" + suffix + "-operation")
	productEventID := journalv2.EventID("event-" + suffix + "-product")
	eventIDs := []journalv2.EventID{opEventID, productEventID}
	sort.Slice(eventIDs, func(i, j int) bool { return eventIDs[i] < eventIDs[j] })
	result := []byte(`{"ok":true}`)
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: operationID, LogicalVersion: 1, Status: journalv2.StatusResolved, ResultKind: "fixture.result", ResultSchemaVersion: 1, CanonicalResult: result, EventIDs: eventIDs})
	if err != nil {
		t.Fatal(err)
	}
	scope := journalv2.Scope{Kind: journalv2.ScopeHost}
	intentDigest := projectionDigestFixtureV2("intent-" + suffix)
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: operationID, CommandKind: "fixture.command", CommandSchemaVersion: 1, CanonicalIntentDigest: intentDigest, Scope: scope, ActorID: "actor-1", ProcessID: "process-1", DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1", OriginatingRuntimeEpochID: "epoch-1", ObservedCreatedAt: "2026-09-05T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	opPayload, _ := journalv2.EncodeOperationStateEventPayloadV2(operation)
	productPayload := []byte(fmt.Sprintf(`{"name":%q}`, suffix))
	opCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(operationID), scope)
	productCoordinate := projectionCoordinateFixtureV2(t, family, aggregate)
	tuple, _ := journalv2.DefaultEventRegistryV2().Resolve(family, 1, 1)
	_ = tuple
	drafts := []journalv2.EventDraftV2{{EventID: opEventID, EventType: "operation.resolved", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: opCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-1", ActorID: "actor-1", ProcessID: "process-1", GovernanceRevisions: []string{"doctrine-1", "profile-1"}, Payload: opPayload}, {EventID: productEventID, EventType: family + ".changed", Family: family, EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: productCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-1", ActorID: "actor-1", ProcessID: "process-1", GovernanceRevisions: []string{"doctrine-1", "profile-1"}, Payload: productPayload}}
	opDigest, _ := journalv2.ReducerStateDigestV2(opPayload)
	productDigest, _ := journalv2.ReducerStateDigestV2(productPayload)
	heads := []journalv2.HeadTransitionV2{{Coordinate: opCoordinate, NextLogicalVersion: 1, EventID: opEventID, CommittedRuntimeEpochID: "epoch-1", CanonicalStateDigest: opDigest, AuthoritySummary: opPayload}, {Coordinate: productCoordinate, NextLogicalVersion: 1, EventID: productEventID, CommittedRuntimeEpochID: "epoch-1", CanonicalStateDigest: productDigest, AuthoritySummary: productPayload}}
	conditions := make([]journalv2.ProviderCondition, 0, 2)
	for _, head := range heads {
		condition, conditionErr := journalv2.ConditionForHeadTransitionV2(head)
		if conditionErr != nil {
			t.Fatal(conditionErr)
		}
		conditions = append(conditions, condition)
	}
	plan, err := journalv2.SealCommitPlanV2(journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: operation, Events: drafts, Heads: heads, Conditions: conditions}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func projectionCoordinateFixtureV2(t *testing.T, family, aggregate string) journalv2.AggregateCoordinate {
	t.Helper()
	coordinate, err := journalv2.NewAggregateCoordinate(journalv2.AggregateKind(family), journalv2.AggregateID(aggregate), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-1", MissionID: "mission-1"})
	if err != nil {
		t.Fatal(err)
	}
	return coordinate
}
func projectionDigestFixtureV2(value string) journalv2.Digest {
	sum := sha256.Sum256([]byte(value))
	digest, _ := journalv2.ParseDigest("fixture", hex.EncodeToString(sum[:]))
	return digest
}
