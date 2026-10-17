package scenario

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

// Fixtures mint opaque ACKs through the real coordinator and typed commit
// observations. The persisted records are copies of the sealed record set;
// fixtures do not manufacture provider keys or durable request grammar.
type terminalFixturePort struct {
	revision uint64
	records  map[journalv2.RecordAddressV2]journalv2.RecoveryRecordV2
}

func (p *terminalFixturePort) Commit(_ context.Context, plan journalv2.SealedCommitPlanV2) (journalv2.CommitObservationV2, error) {
	p.revision++
	for _, s := range plan.Records() {
		var address journalv2.RecordAddressV2
		switch s.Role() {
		case journalv2.RecordOperationBinding:
			address, _ = journalv2.NewOperationBindingAddressV2(s.Target().OperationID())
		case journalv2.RecordOperationHead:
			address, _ = journalv2.NewOperationHeadAddressV2(s.Target().OperationID())
		case journalv2.RecordJournalEvent:
			address, _ = journalv2.NewJournalEventAddressV2(s.Target().Coordinate(), s.Target().LogicalVersion())
		case journalv2.RecordPublicationWitness:
			address, _ = s.Target().PublicationID().WitnessAddressV2()
		default:
			continue
		}
		record, err := journalv2.NewRecoveryRecordV2(address, s.CanonicalValue(), p.revision, "")
		if err != nil {
			return journalv2.CommitObservationV2{}, err
		}
		p.records[address] = record
	}
	return journalv2.NewCommitObservationWithAssertionsV2(plan.PublicationID(), plan.Digest(), plan.RecordSetDigest(), p.revision, p.revision, plan.MutationCount(), plan.AssertionCount())
}
func terminalFixture(t *testing.T, continuation bool, resultBody ...[]byte) terminalSemanticProgress {
	t.Helper()
	return terminalFixtureRoot(t, "operation-common", 0, continuation, resultBody...)
}

func terminalFixtureRoot(t *testing.T, operationID journalv2.OperationID, startingRevision uint64, continuation bool, resultBody ...[]byte) terminalSemanticProgress {
	t.Helper()
	body := []byte(`{"outcome":"completed"}`)
	if len(resultBody) > 1 {
		t.Fatal("fixture accepts at most one result body")
	}
	if len(resultBody) == 1 {
		body = resultBody[0]
	}
	reducers, err := productionruntime.BuildReducerSetV2(journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	port := &terminalFixturePort{revision: startingRevision, records: make(map[journalv2.RecordAddressV2]journalv2.RecoveryRecordV2)}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(port, tracker)
	if err != nil {
		t.Fatal(err)
	}
	scope := journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-fixture", MissionID: "mission-fixture"}
	intent, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "fixture.command", CommandSchemaVersion: 1, Scope: scope, Command: []byte(`{"outcome":"completed"}`)})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: operationID, CommandKind: "fixture.command", CommandSchemaVersion: 1, CanonicalIntentDigest: intent, Scope: scope, ActorID: "actor-fixture", ProcessID: "process-fixture", OriginatingRuntimeEpochID: "epoch-fixture", ObservedCreatedAt: "2026-09-07T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	coordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(operationID), journalv2.Scope{Kind: journalv2.ScopeHost})
	var prior *journalv2.OperationStateV2
	var acks []journalv2.AcknowledgedPublicationV2
	versions := 1
	if continuation {
		versions = 2
	}
	for version := 1; version <= versions; version++ {
		eventID := journalv2.EventID(fmt.Sprintf("event-%s-%d", operationID, version))
		eventIDs := []journalv2.EventID{eventID}
		if prior != nil {
			eventIDs = append(prior.EventIDs(), eventID)
		}
		input := journalv2.OperationStateInput{OperationID: binding.OperationID(), LogicalVersion: journalv2.LogicalVersion(version), Status: journalv2.StatusResolved, ResultKind: "fixture.result", ResultSchemaVersion: 1, CanonicalResult: body, EventIDs: eventIDs}
		eventType := "operation.resolved"
		if continuation && version == 1 {
			input.Status = journalv2.StatusAccepted
			input.ResultKind = ""
			input.ResultSchemaVersion = 0
			input.CanonicalResult = nil
			eventType = "operation.accepted"
		}
		state, err := journalv2.NewOperationStateV2(input)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := journalv2.EncodeOperationStateEventPayloadV2(state)
		if err != nil {
			t.Fatal(err)
		}
		digest, _ := journalv2.ReducerStateDigestV2(payload)
		head := journalv2.HeadTransitionV2{Coordinate: coordinate, ExpectedLogicalVersion: journalv2.LogicalVersion(version - 1), NextLogicalVersion: journalv2.LogicalVersion(version), EventID: eventID, CommittedRuntimeEpochID: binding.OriginatingEpoch(), CanonicalStateDigest: digest, AuthoritySummary: payload}
		if prior != nil {
			head.ExpectedRecordRevision = startingRevision + uint64(version-1)
		}
		condition, err := journalv2.ConditionForHeadTransitionV2(head)
		if err != nil {
			t.Fatal(err)
		}
		planInput := journalv2.CommitPlanInput{OperationState: state, PriorOperationState: prior, Events: []journalv2.EventDraftV2{{EventID: eventID, EventType: eventType, Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: coordinate, AggregateLogicalVersion: journalv2.LogicalVersion(version), CommittedRuntimeEpochID: binding.OriginatingEpoch(), ActorID: binding.ActorID(), ProcessID: binding.ProcessID(), Payload: payload}}, Heads: []journalv2.HeadTransitionV2{head}, Conditions: []journalv2.ProviderCondition{condition}}
		if prior == nil {
			planInput.OperationBinding = &binding
		}
		plan, err := journalv2.SealCommitPlanV2(planInput, journalv2.DefaultEventRegistryV2())
		if err != nil {
			t.Fatal(err)
		}
		ack, err := coordinator.Publish(t.Context(), plan)
		if err != nil {
			t.Fatal(err)
		}
		acks = append(acks, ack)
		next := ack.Plan().OperationState()
		prior = &next
	}
	capture, _ := journalv2.NewStoreRevisionV2("database-fixture", port.revision, port.revision)
	current := acks[len(acks)-1].Plan().OperationState()
	encoded, _ := journalv2.EncodeOperationStateV2(current)
	item := productionruntime.ProgressItem{Family: "operation", AggregateID: string(current.OperationID()), LogicalVersion: uint64(current.LogicalVersion()), LastPublicationID: string(current.LastPublicationID()), AuthorityState: string(current.Status()), CanonicalState: encoded}
	progress := productionruntime.MissionProgress{DatabaseID: "database-fixture", ProjectionRevision: port.revision, ProjectID: scope.ProjectID, MissionID: scope.MissionID, Lifecycle: "completed", WorkRevisionID: "revision-fixture", Generation: 1, Complete: true, TotalWork: 1, WorkCounts: []productionruntime.StateCount{{State: "completed", Count: 1}}, Operations: []productionruntime.ProgressItem{item}, Items: []productionruntime.ProgressItem{item}}
	history := &terminalPublicationHistory{Progress: progress, Inspections: map[string]productionruntime.ProgressItem{item.AggregateID: item}, Recovery: make(map[journalv2.OperationID]scenarioRecoveryCommand)}
	for _, ack := range acks {
		ba, _ := journalv2.NewOperationBindingAddressV2(binding.OperationID())
		wa, _ := ack.PublicationID().WitnessAddressV2()
		proof := productionruntime.AcknowledgedPublicationProof{Binding: binding, BindingRecord: port.records[ba], Witness: ack.Plan().PublicationWitness(), WitnessRecord: port.records[wa], HistoricalOperationState: ack.Plan().OperationState(), CurrentState: current, CurrentProgressItem: item, CapturedCoordinate: capture}
		for _, event := range ack.Plan().Events() {
			address, _ := journalv2.NewJournalEventAddressV2(event.Coordinate(), event.LogicalVersion())
			record := port.records[address]
			proof.JournalRecords = append(proof.JournalRecords, record)
			if event.Family() == "operation" {
				proof.HistoricalOperationRecord = record
			}
		}
		history.Publications = append(history.Publications, terminalPublicationReceipt{Ack: ack, Proof: proof, Source: "typed-command-success"})
	}
	history.HistorySHA256 = terminalHistoryDigest(history)
	excluded, err := validateTerminalHistory(history)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := terminalBusinessOutcome(history, nil, repositoryTerminalEvidence{}, excluded)
	if err != nil {
		t.Fatal(err)
	}
	outcome.History = history
	return outcome
}

func TestTerminalHistoryClaimCommitmentAndEvidence(t *testing.T) {
	mutants := map[string]func(*terminalPublicationHistory){
		"nil_claim": func(h *terminalPublicationHistory) { h.Publications[0].Proof.WorkspacePathClaim = nil },
		"zero_claim": func(h *terminalPublicationHistory) {
			h.Publications[0].Proof.WorkspacePathClaim = &journalv2.WorkspacePathClaimV2{}
		},
		"missing_revision": func(h *terminalPublicationHistory) { h.Publications[0].Proof.ClaimRecordRevision = 0 },
		"wrong_revision":   func(h *terminalPublicationHistory) { h.Publications[0].Proof.ClaimRecordRevision++ },
		"corrupt_digest": func(h *terminalPublicationHistory) {
			p := &h.Publications[0].Proof
			d, _ := journalv2.ReducerStateDigestV2([]byte(`{"foreign":true}`))
			c, err := journalv2.NewWorkspacePathClaimV2(d, p.WorkspacePathClaim.CanonicalValue())
			if err != nil {
				panic(err)
			}
			p.WorkspacePathClaim = &c
		},
		"corrupt_canonical": func(h *terminalPublicationHistory) {
			p := &h.Publications[0].Proof
			c, err := journalv2.NewWorkspacePathClaimV2(p.WorkspacePathClaim.ClaimDigest(), []byte(`{"foreign":true}`))
			if err != nil {
				panic(err)
			}
			p.WorkspacePathClaim = &c
		},
		"extraneous_claim": func(h *terminalPublicationHistory) {
			c := *h.Publications[0].Proof.WorkspacePathClaim
			h.Publications[1].Proof.WorkspacePathClaim = &c
			h.Publications[1].Proof.ClaimRecordRevision = h.Publications[1].Ack.ProviderRevision()
		},
		"extraneous_revision_only": func(h *terminalPublicationHistory) { h.Publications[1].Proof.ClaimRecordRevision = 2 },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			h, owner := terminalCoordinateFixture(t, t.TempDir())
			original := h.HistorySHA256
			if _, err := newTerminalCoordinateProof(h, owner, "repository-a"); err != nil {
				t.Fatal(err)
			}
			mutate(h)
			if _, err := validateTerminalHistory(h); err == nil || !strings.Contains(err.Error(), "history digest") {
				t.Fatalf("unhashed claim mutation not refused by commitment: %v", err)
			}
			h.HistorySHA256 = terminalHistoryDigest(h)
			if h.HistorySHA256 == original {
				t.Fatal("claim evidence omitted from commitment")
			}
			if _, err := validateTerminalHistory(h); err == nil || !strings.Contains(err.Error(), "claim evidence") {
				t.Fatalf("recommitted claim mutation not refused by evidence: %v", err)
			}
			if _, err := newTerminalCoordinateProof(h, owner, "repository-a"); err == nil {
				t.Fatal("coordinate constructor bypassed claim validation")
			}
		})
	}
}

func TestTerminalHistoryGlobalProviderRevision(t *testing.T) {
	merge := func(a, b *terminalPublicationHistory) *terminalPublicationHistory {
		capture, err := journalv2.NewStoreRevisionV2(a.Progress.DatabaseID, b.Progress.ProjectionRevision, b.Progress.ProjectionRevision)
		if err != nil {
			t.Fatal(err)
		}
		h := &terminalPublicationHistory{Progress: a.Progress, Inspections: make(map[string]productionruntime.ProgressItem), Recovery: make(map[journalv2.OperationID]scenarioRecoveryCommand)}
		h.Progress.ProjectionRevision = capture.CurrentRevision()
		h.Progress.Operations = nil
		h.Progress.Items = nil
		for _, source := range []*terminalPublicationHistory{a, b} {
			for _, receipt := range source.Publications {
				receipt.Proof.CapturedCoordinate = capture
				h.Publications = append(h.Publications, receipt)
			}
			h.Progress.Operations = append(h.Progress.Operations, source.Progress.Operations...)
			h.Progress.Items = append(h.Progress.Items, source.Progress.Items...)
			for id, item := range source.Inspections {
				h.Inspections[id] = item
			}
		}
		h.HistorySHA256 = terminalHistoryDigest(h)
		return h
	}
	a := terminalFixtureRoot(t, "operation-root-a", 0, false).History
	b := terminalFixtureRoot(t, "operation-root-b", 1, false).History
	good := merge(a, b)
	// Input order is not global revision order; playback sorts only later.
	good.Publications[0], good.Publications[1] = good.Publications[1], good.Publications[0]
	good.HistorySHA256 = terminalHistoryDigest(good)
	if _, err := validateTerminalHistory(good); err != nil {
		t.Fatal(err)
	}
	if _, err := newTerminalCoordinateProof(good, &portableHarness{root: t.TempDir()}); err != nil {
		t.Fatalf("mission binding on physical host operation: %v", err)
	}
	// Both roots have independently genuine CommitObservations at revision 1,
	// distinct publication IDs and complete persisted proofs, never patched ACKs.
	duplicate := terminalFixtureRoot(t, "operation-root-b", 0, false).History
	bad := merge(a, duplicate)
	if bad.Publications[0].Ack.PublicationID() == bad.Publications[1].Ack.PublicationID() || bad.Publications[0].Ack.ProviderRevision() != bad.Publications[1].Ack.ProviderRevision() {
		t.Fatal("bad fixture did not capture distinct publications at the same revision")
	}
	if _, err := validateTerminalHistory(bad); err == nil || !strings.Contains(err.Error(), "duplicate or fabricated ACK") {
		t.Fatalf("same global revision survived: %v", err)
	}
}

func TestTerminalRestartValidatorUnconditional(t *testing.T) {
	a, b := terminalFixture(t, false), terminalFixture(t, true)
	if a.History.HistorySHA256 == b.History.HistorySHA256 {
		t.Fatal("fixture histories are identical")
	}
	if err := validateTerminalRestartEquivalence(a, b); err != nil {
		t.Fatalf("proved unequal histories with equal business outcome: %v", err)
	}
	mutants := map[string]func(*terminalSemanticProgress){
		"missing_history": func(v *terminalSemanticProgress) { v.History = nil },
		"omitted_ACK":     func(v *terminalSemanticProgress) { v.History.Publications = v.History.Publications[1:] },
		"duplicate_ACK": func(v *terminalSemanticProgress) {
			v.History.Publications = append(v.History.Publications, v.History.Publications[0])
		},
		"fabricated_ACK": func(v *terminalSemanticProgress) {
			v.History.Publications[0].Ack = journalv2.AcknowledgedPublicationV2{}
		},
		"planned_without_success":   func(v *terminalSemanticProgress) { v.History.Publications[0].Source = "" },
		"public_operations_missing": func(v *terminalSemanticProgress) { v.History.Progress.Operations = nil },
		"public_items_missing":      func(v *terminalSemanticProgress) { v.History.Progress.Items = nil },
		"public_operations_duplicate": func(v *terminalSemanticProgress) {
			v.History.Progress.Operations = append(v.History.Progress.Operations, v.History.Progress.Operations[0])
		},
		"extra_unrecorded_root": func(v *terminalSemanticProgress) {
			i := v.History.Progress.Operations[0]
			i.AggregateID = "operation-extra"
			v.History.Progress.Operations = append(v.History.Progress.Operations, i)
		},
		"inspection_missing": func(v *terminalSemanticProgress) { v.History.Inspections = nil },
		"cross_scope":        func(v *terminalSemanticProgress) { v.History.Progress.ProjectID = "other-project" },
		"head_version": func(v *terminalSemanticProgress) {
			v.History.Publications[0].Proof.CurrentProgressItem.LogicalVersion++
		},
		"head_publication": func(v *terminalSemanticProgress) {
			v.History.Publications[0].Proof.CurrentProgressItem.LastPublicationID = strings.Repeat("f", 64)
		},
		"result_bytes": func(v *terminalSemanticProgress) {
			v.History.Publications[0].Proof.CurrentProgressItem.CanonicalState = []byte(`{"result_sha256":"fake"}`)
		},
		"binding_revision": func(v *terminalSemanticProgress) {
			p := &v.History.Publications[0].Proof
			p.BindingRecord, _ = journalv2.NewRecoveryRecordV2(p.BindingRecord.Address(), p.BindingRecord.CanonicalValue(), 99, "")
		},
		"event_revision": func(v *terminalSemanticProgress) {
			p := &v.History.Publications[0].Proof
			e := p.JournalRecords[0]
			p.JournalRecords[0], _ = journalv2.NewRecoveryRecordV2(e.Address(), e.CanonicalValue(), 99, "")
		},
		"business_outcome": func(v *terminalSemanticProgress) { v.Complete = false },
		"business_hash":    func(v *terminalSemanticProgress) { v.SemanticSHA256 = "fake" },
		"fake_rebind_name": func(v *terminalSemanticProgress) {
			q := execution.ExecutionAuthorityRequestV2{Kind: execution.ExecutionRebindV2, OperationID: "operation-common"}
			v.History.Recovery[q.OperationID] = scenarioRecoveryCommand{ExecutionRequest: &q}
		},
		"recovery_unrecorded": func(v *terminalSemanticProgress) {
			v.History.Recovery["operation-rebind-fake"] = scenarioRecoveryCommand{}
		},
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			got := terminalFixture(t, true)
			mutate(&got)
			if got.History != nil {
				got.History.HistorySHA256 = terminalHistoryDigest(got.History)
			}
			if err := validateTerminalRestartEquivalence(b, got); err == nil {
				t.Fatal("mutant survived")
			}
		})
	}
	t.Run("history_hash", func(t *testing.T) {
		got := terminalFixture(t, true)
		got.History.HistorySHA256 = "fake"
		if validateTerminalRestartEquivalence(b, got) == nil {
			t.Fatal("history hash mutant survived")
		}
	})
}

// Each differing result is sealed, published, and persisted independently.
// These failures must be business mismatches, never corrupt hash/proof failures.
func TestTerminalRestartRejectsUnknownBusinessFieldsWithValidProofs(t *testing.T) {
	cases := map[string][2]string{
		"unknown_scalar": {"{\"outcome\":\"completed\",\"future_business\":\"blue\"}", "{\"outcome\":\"completed\",\"future_business\":\"red\"}"},
		"nested_color":   {"{\"outcome\":\"completed\",\"future_business\":{\"color\":\"blue\",\"count\":3}}", "{\"outcome\":\"completed\",\"future_business\":{\"color\":\"red\",\"count\":3}}"},
		"nested_count":   {"{\"outcome\":\"completed\",\"future_business\":{\"color\":\"blue\",\"count\":3}}", "{\"outcome\":\"completed\",\"future_business\":{\"color\":\"blue\",\"count\":4}}"},
		"max_uint64":     {"{\"outcome\":\"completed\",\"count\":18446744073709551615}", "{\"outcome\":\"completed\",\"count\":18446744073709551614}"},
	}
	for name, bodies := range cases {
		t.Run(name, func(t *testing.T) {
			a, b := terminalFixture(t, false, []byte(bodies[0])), terminalFixture(t, true, []byte(bodies[1]))
			for _, value := range []terminalSemanticProgress{a, b} {
				if _, err := validateTerminalHistory(value.History); err != nil {
					t.Fatalf("invalid persisted fixture: %v", err)
				}
				if err := validateTerminalRestartEquivalence(value, value); err != nil {
					t.Fatalf("invalid derived fixture: %v", err)
				}
			}
			if a.Operations[0].ResultSHA256 == b.Operations[0].ResultSHA256 {
				t.Fatal("different full result bodies collapsed")
			}
			if err := validateTerminalRestartEquivalence(a, b); err == nil {
				t.Fatal("valid business difference survived")
			}
		})
	}
}

func TestTerminalSemanticNormalizationIsOwnerScopedAndClosed(t *testing.T) {
	raw := []byte(`{"runtime_epoch_id":"epoch-a","logical_version":1,"future":{"runtime_epoch_id":"business-a","logical_version":18446744073709551615},"observed_at":"time-a","fence":"fence-a"}`)
	got, err := terminalSemanticBytes(raw, "result:execution_slot.result")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"fence":"fence-a","future":{"logical_version":18446744073709551615,"runtime_epoch_id":"business-a"},"observed_at":"time-a"}`
	if string(got) != want {
		t.Fatalf("owner paths lost business fields: %s", got)
	}
	unknown, err := terminalSemanticBytes(raw, "result:future.result")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := journalv2.CanonicalObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(unknown) != string(canonical) {
		t.Fatal("unknown owner changed")
	}
	for _, suffix := range []string{" {}", " trailing"} {
		if _, err := terminalSemanticBytes(append(append([]byte(nil), raw...), suffix...), "result:execution_slot.result"); err == nil {
			t.Fatal("decoder accepted trailing input")
		}
	}
}

// The observer chain is exercised concurrently without any provider/query.
type terminalCountingObserver struct {
	mu sync.Mutex
	n  int
}

func (o *terminalCountingObserver) ObservePublicationBoundary(context.Context, journalv2.PublicationBoundarySnapshotV2) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.n++
}
func TestTerminalPublicationCollectorRaceAndChaining(t *testing.T) {
	fixture := terminalFixture(t, true)
	next := &terminalCountingObserver{}
	c := &scenarioPublicationCollector{next: next, initial: make(map[journalv2.OperationID]journalv2.AcknowledgedPublicationV2), accepted: make(map[journalv2.PublicationID]string), recovery: make(map[journalv2.OperationID]scenarioRecoveryCommand)}
	var wg sync.WaitGroup
	for _, r := range fixture.History.Publications {
		wg.Add(1)
		go func(r terminalPublicationReceipt) {
			defer wg.Done()
			ack := r.Ack
			c.ObservePublicationBoundary(t.Context(), journalv2.PublicationBoundarySnapshotV2{Stage: journalv2.PublicationAckCreatedBeforeProjectionV2, Plan: ack.Plan(), Ack: &ack})
		}(r)
	}
	wg.Wait()
	if c.count() != 2 || next.n != 2 || len(c.initial) != 1 || len(c.accepted) != 0 {
		t.Fatal("collector lost ACK/chained observer or invented success")
	}
	c.accept("operation-common", "typed-command-success")
	if len(c.accepted) != 2 {
		t.Fatal("success did not attribute all actual versions")
	}
}

func TestTerminalTypedRecoveryAuthorityMutants(t *testing.T) {
	_, context := twoItemRevision(t, "project-fixture", "mission-fixture")
	governance, err := execution.ExecutionGovernanceFromContextV2(context)
	if err != nil {
		t.Fatal(err)
	}
	before := execution.ExecutionSlotStateV2{SchemaVersion: 1, ProjectID: "project-fixture", MissionID: "mission-fixture", WorkRevisionID: "revision-fixture", WorkID: "work-a", LogicalVersion: 1, RunID: "run-a", ActorID: "actor-a", ProcessID: "process-a", LeaseID: "lease-a", Generation: 1, Fence: strings.Repeat("a", 32), HeartbeatAt: "2026-09-05T12:00:00Z", Deadline: "2026-09-05T13:00:00Z", RuntimeEpochID: "epoch-old", Status: execution.ExecutionActiveV2, Governance: governance}
	after := before
	after.LogicalVersion++
	after.RuntimeEpochID = "epoch-new"
	deadline, _ := time.Parse(time.RFC3339Nano, before.Deadline)
	q := execution.ExecutionAuthorityRequestV2{Kind: execution.ExecutionRebindV2, OperationID: "operation-recovery", ProjectID: before.ProjectID, MissionID: before.MissionID, WorkRevisionID: before.WorkRevisionID, WorkID: before.WorkID, RunID: before.RunID, ActorID: before.ActorID, ProcessID: before.ProcessID, LeaseID: before.LeaseID, Generation: before.Generation, Fence: before.Fence, ObservedAt: time.Date(2026, 9, 5, 12, 20, 0, 0, time.UTC), Deadline: deadline, RuntimeEpochID: after.RuntimeEpochID, Governance: context, OperationEventID: "event-recovery-op", SlotEventID: "event-recovery-slot"}
	binding, err := scenarioRecoveryBinding(q.OperationID, scenarioRecoveryCommand{ExecutionRequest: &q})
	if err != nil {
		t.Fatal(err)
	}
	state, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: q.OperationID, LogicalVersion: 1, Status: journalv2.StatusResolved, ResultKind: "execution_slot.result", ResultSchemaVersion: 1, CanonicalResult: after.CanonicalBytes(), EventIDs: []journalv2.EventID{q.OperationEventID, q.SlotEventID}})
	if err != nil {
		t.Fatal(err)
	}
	receipt := terminalPublicationReceipt{Proof: productionruntime.AcknowledgedPublicationProof{Binding: binding, HistoricalOperationState: state}}
	command := scenarioRecoveryCommand{ExecutionRequest: &q, Before: before, After: after}
	if err := validateScenarioRecovery(q.OperationID, command, receipt); err != nil {
		t.Fatal(err)
	}
	mutants := map[string]func(*scenarioRecoveryCommand){
		"actor": func(c *scenarioRecoveryCommand) { c.After.ActorID = "actor-other" }, "process": func(c *scenarioRecoveryCommand) { c.After.ProcessID = "process-other" }, "work_revision": func(c *scenarioRecoveryCommand) { c.After.WorkRevisionID = "revision-other" }, "work": func(c *scenarioRecoveryCommand) { c.After.WorkID = "work-other" }, "run": func(c *scenarioRecoveryCommand) { c.After.RunID = "run-other" }, "lease": func(c *scenarioRecoveryCommand) { c.After.LeaseID = "lease-other" }, "generation": func(c *scenarioRecoveryCommand) { c.After.Generation++ }, "fence": func(c *scenarioRecoveryCommand) { c.After.Fence = strings.Repeat("b", 32) }, "workspace_binding": func(c *scenarioRecoveryCommand) { c.After.WorkspaceID = "workspace-other" }, "governance": func(c *scenarioRecoveryCommand) { c.After.Governance.PolicyRevision = "policy-other" }, "heartbeat": func(c *scenarioRecoveryCommand) { c.After.HeartbeatAt = "2026-09-05T12:01:00Z" }, "deadline": func(c *scenarioRecoveryCommand) { c.After.Deadline = "2026-09-05T14:00:00Z" }, "status": func(c *scenarioRecoveryCommand) { c.After.Status = execution.ExecutionExpiredV2 }, "request_kind": func(c *scenarioRecoveryCommand) { c.ExecutionRequest.Kind = execution.ExecutionHeartbeatV2 },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			c := command
			request := q
			c.ExecutionRequest = &request
			mutate(&c)
			if err := validateScenarioRecovery(q.OperationID, c, receipt); err == nil {
				t.Fatal("authority mutant survived")
			}
		})
	}
	// A workspace recovery preserves the entire binding and observed stage.
	claim, _ := workspace.WorkspacePathClaimDigestV2("repository-a", "/tmp/managed", "/tmp/managed/workspace-a")
	wb := workspace.BindingV2{WorkspaceID: "workspace-a", ProjectID: before.ProjectID, MissionID: before.MissionID, WorkRevisionID: before.WorkRevisionID, WorkID: before.WorkID, RunID: before.RunID, ActorID: before.ActorID, ProcessID: before.ProcessID, LeaseID: before.LeaseID, Generation: before.Generation, Fence: before.Fence, RuntimeEpochID: before.RuntimeEpochID, RepositoryID: "repository-a", LaneID: "lane-a", ManagedRoot: "/tmp/managed", CanonicalPath: "/tmp/managed/workspace-a", PathIdentity: workspace.PathIdentityV2{Absolute: true, Canonical: true, WithinManagedRoot: true, AliasFree: true, SymlinkFree: true}, PathClaimDigest: claim, OwnedBranch: "kitty/work-a", PinnedBase: strings.Repeat("a", 40)}
	priorWorkspace := workspace.StateV2{SchemaVersion: 1, LogicalVersion: 1, Binding: wb, Stage: workspace.StageBindingCreatedV2, OccurredAt: before.HeartbeatAt}
	wq := execution.WorkspaceEpochRebindRequestV2{ProjectID: before.ProjectID, MissionID: before.MissionID, WorkRevisionID: before.WorkRevisionID, WorkID: before.WorkID, ActorID: before.ActorID, ProcessID: before.ProcessID, OriginatingRuntimeEpochID: after.RuntimeEpochID, OperationID: "operation-workspace-recovery", OperationEventID: "event-workspace-recovery-op", ExecutionEventID: "event-workspace-recovery-slot", WorkspaceEventID: "event-workspace-recovery-domain", Governance: governance, ObservedAt: q.ObservedAt}
	contribution, err := workspace.NewEpochRebindContributionV2(workspace.EpochRebindContributionInputV2{Prior: priorWorkspace, RecordRevision: 1, EventID: wq.WorkspaceEventID, RuntimeEpochID: wq.OriginatingRuntimeEpochID, Observation: priorWorkspace.Observation, OccurredAt: wq.ObservedAt})
	if err != nil {
		t.Fatal(err)
	}
	nextWorkspace, err := contribution.State()
	if err != nil {
		t.Fatal(err)
	}
	wbBefore, wbAfter := before, after
	wbBefore.WorkspaceID = wb.WorkspaceID
	wbBefore.WorkspacePathClaimDigest = claim
	wbAfter.WorkspaceID = wb.WorkspaceID
	wbAfter.WorkspacePathClaimDigest = claim
	wc := scenarioRecoveryCommand{WorkspaceRequest: &wq, Before: wbBefore, After: wbAfter, BeforeWorkspace: &priorWorkspace, AfterWorkspace: &nextWorkspace, WorkspaceRecordRevision: 1}
	wbinding, err := scenarioRecoveryBinding(wq.OperationID, wc)
	if err != nil {
		t.Fatal(err)
	}
	body, err := journalv2.CanonicalObject(mustTerminalJSON(execution.WorkspaceAuthorityResultV2{Execution: wbAfter, Workspace: nextWorkspace}))
	if err != nil {
		t.Fatal(err)
	}
	workspaceEventIDs := []journalv2.EventID{wq.OperationEventID, wq.ExecutionEventID, wq.WorkspaceEventID}
	sort.Slice(workspaceEventIDs, func(i, j int) bool { return workspaceEventIDs[i] < workspaceEventIDs[j] })
	wstate, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: wq.OperationID, LogicalVersion: 1, Status: journalv2.StatusResolved, ResultKind: "workspace.result", ResultSchemaVersion: 1, CanonicalResult: body, EventIDs: workspaceEventIDs})
	if err != nil {
		t.Fatal(err)
	}
	wr := terminalPublicationReceipt{Proof: productionruntime.AcknowledgedPublicationProof{Binding: wbinding, HistoricalOperationState: wstate}}
	if err := validateScenarioRecovery(wq.OperationID, wc, wr); err != nil {
		t.Fatal(err)
	}
	workspaceMutants := map[string]func(*workspace.StateV2){"path": func(s *workspace.StateV2) { s.Binding.CanonicalPath = "/tmp/managed/other" }, "workspace_id": func(s *workspace.StateV2) { s.Binding.WorkspaceID = "workspace-other" }, "repository": func(s *workspace.StateV2) { s.Binding.RepositoryID = "repository-other" }, "owned_branch": func(s *workspace.StateV2) { s.Binding.OwnedBranch = "kitty/other" }, "run": func(s *workspace.StateV2) { s.Binding.RunID = "run-other" }, "lease": func(s *workspace.StateV2) { s.Binding.LeaseID = "lease-other" }, "generation": func(s *workspace.StateV2) { s.Binding.Generation++ }, "fence": func(s *workspace.StateV2) { s.Binding.Fence = strings.Repeat("b", 32) }, "process": func(s *workspace.StateV2) { s.Binding.ProcessID = "process-other" }, "stage": func(s *workspace.StateV2) { s.Stage = workspace.StageQuarantinedV2; s.QuarantineReason = "changed" }, "observation": func(s *workspace.StateV2) { s.Observation.Registered = true }}
	for name, mutate := range workspaceMutants {
		t.Run("workspace_"+name, func(t *testing.T) {
			c := wc
			next := nextWorkspace
			c.AfterWorkspace = &next
			mutate(c.AfterWorkspace)
			if validateScenarioRecovery(wq.OperationID, c, wr) == nil {
				t.Fatal("workspace authority mutant survived")
			}
		})
	}
}
