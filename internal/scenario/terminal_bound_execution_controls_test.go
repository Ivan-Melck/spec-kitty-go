package scenario

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type terminalBoundFixturePort struct {
	terminalCouplingPort
	coordinator *journalv2.PublicationCoordinatorV2
	acks        []journalv2.AcknowledgedPublicationV2
}

func (p *terminalBoundFixturePort) PublishWorkspace(ctx context.Context, plan execution.WorkspaceAuthorityPlanV2) (execution.WorkspaceAuthorityAckV2, error) {
	sealed, err := plan.Publication()
	if err != nil {
		return execution.WorkspaceAuthorityAckV2{}, err
	}
	ack, err := p.coordinator.Publish(ctx, sealed)
	if err != nil {
		return execution.WorkspaceAuthorityAckV2{}, err
	}
	p.acks = append(p.acks, ack)
	for _, e := range ack.Plan().Events() {
		switch e.Family() {
		case "execution_slot":
			s, err := execution.DecodeExecutionSlotEventV2(e.Payload())
			if err != nil {
				return execution.WorkspaceAuthorityAckV2{}, err
			}
			p.execution = execution.ExecutionSlotObservationV2{State: &s.State, RecordRevision: ack.ProviderRevision()}
		case "workspace":
			s, err := workspace.DecodeEventV2(e.Payload())
			if err != nil {
				return execution.WorkspaceAuthorityAckV2{}, err
			}
			p.workspace = execution.WorkspaceAuthorityObservationV2{State: &s.State, RecordRevision: ack.ProviderRevision()}
		}
	}
	return execution.NewWorkspaceAuthorityAckV2(plan, ack)
}

func terminalBoundCoordinator(t *testing.T) (*terminalCoordinateFixturePort, *journalv2.PublicationCoordinatorV2) {
	t.Helper()
	reducers, err := productionruntime.BuildReducerSetV2(journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	durable := &terminalCoordinateFixturePort{terminalFixturePort: terminalFixturePort{records: make(map[journalv2.RecordAddressV2]journalv2.RecoveryRecordV2)}, claims: make(map[journalv2.Digest]journalv2.WorkspacePathClaimV2), claimRevisions: make(map[journalv2.Digest]uint64)}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(durable, tracker)
	if err != nil {
		t.Fatal(err)
	}
	return durable, coordinator
}

func terminalBoundHistory(t *testing.T, durable *terminalCoordinateFixturePort, acks []journalv2.AcknowledgedPublicationV2) *terminalPublicationHistory {
	t.Helper()
	capture, _ := journalv2.NewStoreRevisionV2("database-fixture", durable.revision, durable.revision)
	h := &terminalPublicationHistory{Progress: productionruntime.MissionProgress{DatabaseID: capture.DatabaseID(), ProjectionRevision: capture.CurrentRevision(), ProjectID: "project-fixture", MissionID: "mission-fixture"}, Inspections: make(map[string]productionruntime.ProgressItem)}
	for _, ack := range acks {
		b := *ack.Plan().BindingCreate()
		s := ack.Plan().OperationState()
		encoded, _ := journalv2.EncodeOperationStateV2(s)
		item := productionruntime.ProgressItem{Family: "operation", AggregateID: string(s.OperationID()), LogicalVersion: uint64(s.LogicalVersion()), LastPublicationID: string(s.LastPublicationID()), AuthorityState: string(s.Status()), CanonicalState: encoded}
		ba, _ := journalv2.NewOperationBindingAddressV2(s.OperationID())
		wa, _ := ack.PublicationID().WitnessAddressV2()
		proof := productionruntime.AcknowledgedPublicationProof{Binding: b, BindingRecord: durable.records[ba], Witness: ack.Plan().PublicationWitness(), WitnessRecord: durable.records[wa], HistoricalOperationState: s, CurrentState: s, CurrentProgressItem: item, CapturedCoordinate: capture}
		for _, e := range ack.Plan().Events() {
			a, _ := journalv2.NewJournalEventAddressV2(e.Coordinate(), e.LogicalVersion())
			r := durable.records[a]
			proof.JournalRecords = append(proof.JournalRecords, r)
			if e.Family() == "operation" {
				proof.HistoricalOperationRecord = r
			}
		}
		if claim := ack.Plan().WorkspacePathClaim(); claim != nil {
			c := durable.claims[claim.ClaimDigest()]
			proof.WorkspacePathClaim = &c
			proof.ClaimRecordRevision = durable.claimRevisions[claim.ClaimDigest()]
		}
		h.Publications = append(h.Publications, terminalPublicationReceipt{Ack: ack, Proof: proof, Source: "typed-command-success"})
		if b.Scope().Kind != journalv2.ScopeHost {
			h.Progress.Operations = append(h.Progress.Operations, item)
			h.Progress.Items = append(h.Progress.Items, item)
			h.Inspections[item.AggregateID] = item
		}
	}
	h.HistorySHA256 = terminalHistoryDigest(h)
	if _, err := validateTerminalHistory(h); err != nil {
		t.Fatal("actual proof/census", err)
	}
	return h
}

func terminalBoundFixture(t *testing.T, quarantine bool) (*terminalPublicationHistory, *portableHarness) {
	t.Helper()
	owner := &portableHarness{root: t.TempDir()}
	durable, coordinator := terminalBoundCoordinator(t)
	// Complete typed unbound first version; not a missing-prefix snapshot.
	template := terminalRecoveryFixture(t, false).History.Publications[1].Ack.Plan()
	seed, err := coordinator.Publish(t.Context(), template)
	if err != nil {
		t.Fatal(err)
	}
	var before execution.ExecutionSlotStateV2
	var c journalv2.AggregateCoordinate
	for _, e := range seed.Plan().Events() {
		if e.Family() == "execution_slot" {
			event, err := execution.DecodeExecutionSlotEventV2(e.Payload())
			if err != nil {
				t.Fatal(err)
			}
			before = event.State
			c = e.Coordinate()
		}
	}
	root := filepath.Join(owner.root, "workspaces")
	b := workspace.BindingV2{WorkspaceID: "workspace-a", ProjectID: before.ProjectID, MissionID: before.MissionID, WorkRevisionID: before.WorkRevisionID, WorkID: before.WorkID, RunID: before.RunID, LeaseID: before.LeaseID, Generation: before.Generation, Fence: before.Fence, ActorID: before.ActorID, ProcessID: before.ProcessID, RuntimeEpochID: before.RuntimeEpochID, RepositoryID: "repository-a", LaneID: "lane-a", ManagedRoot: root, CanonicalPath: filepath.Join(root, "workspace-a"), PathIdentity: workspace.PathIdentityV2{Absolute: true, Canonical: true, WithinManagedRoot: true, AliasFree: true, SymlinkFree: true}, OwnedBranch: "kitty/work-a", PinnedBase: strings.Repeat("b", 40)}
	b.PathClaimDigest, err = workspace.WorkspacePathClaimDigestV2(b.RepositoryID, b.ManagedRoot, b.CanonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	wc, _ := workspace.WorkspaceCoordinateV2(b)
	port := &terminalBoundFixturePort{terminalCouplingPort: terminalCouplingPort{executionCoordinate: c, workspaceCoordinate: wc, execution: execution.ExecutionSlotObservationV2{State: &before, RecordRevision: seed.ProviderRevision()}, epoch: before.RuntimeEpochID}, coordinator: coordinator, acks: []journalv2.AcknowledgedPublicationV2{seed}}
	service, err := execution.NewWorkspaceAuthorityServiceV2(port, port, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 5, 12, 10, 0, 0, time.UTC)
	contribution, err := workspace.NewBindingContributionV2(workspace.BindingContributionInputV2{Binding: b, EffectID: "effect-create-a", EventID: "event-bind-workspace", OccurredAt: at})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(t.Context(), execution.WorkspaceAuthorityRequestV2{Kind: execution.WorkspaceBindV2, OperationID: "operation-bind", OperationEventID: "event-bind-operation", ExecutionEventID: "event-bind-execution", Governance: before.Governance, Contribution: contribution, ObservedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if result.RecordRevision != 2 || result.Replayed {
		t.Fatal("bind is not an actual fresh service result")
	}
	port.epoch = "epoch-new"
	result, err = service.RebindRuntimeEpoch(t.Context(), execution.WorkspaceEpochRebindRequestV2{ProjectID: before.ProjectID, MissionID: before.MissionID, WorkRevisionID: before.WorkRevisionID, WorkID: before.WorkID, ActorID: before.ActorID, ProcessID: before.ProcessID, OriginatingRuntimeEpochID: port.epoch, OperationID: "operation-bound-rebind", OperationEventID: "event-bound-rebind-operation", ExecutionEventID: "event-bound-rebind-execution", WorkspaceEventID: "event-bound-rebind-workspace", Governance: before.Governance, ObservedAt: at.Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if result.RecordRevision != 3 || result.Replayed {
		t.Fatal("rebind is not actual fresh service result")
	}
	if quarantine {
		at = time.Date(2026, 9, 5, 13, 1, 0, 0, time.UTC)
		contribution, err = workspace.NewQuarantineContributionV2(workspace.QuarantineContributionInputV2{Prior: *port.workspace.State, RecordRevision: port.workspace.RecordRevision, EventID: "event-expire-workspace", Reason: "lease expired", OccurredAt: at})
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.Execute(t.Context(), execution.WorkspaceAuthorityRequestV2{Kind: execution.WorkspaceQuarantineV2, OperationID: "operation-expire", OperationEventID: "event-expire-operation", ExecutionEventID: "event-expire-execution", Governance: before.Governance, Contribution: contribution, ObservedAt: at})
		if err != nil {
			t.Fatal(err)
		}
	}
	return terminalBoundHistory(t, durable, port.acks), owner
}

func terminalBoundReference(p *terminalCoordinateProof, s terminalHistoricalState) terminalStateReference {
	return terminalStateReference{p.historySHA, p.capture, integration.AcceptanceEvidenceV2{Disposition: integration.EvidenceSatisfiedV2, Coordinate: s.key.coordinate, LogicalVersion: s.key.version, RecordRevision: s.key.revision, StateDigestSHA256: s.digest.String()}}
}

func TestTerminalBoundExecutionActualServiceClosure(t *testing.T) {
	for _, quarantine := range []bool{false, true} {
		t.Run(fmt.Sprint(quarantine), func(t *testing.T) {
			h, owner := terminalBoundFixture(t, quarantine)
			p, err := newTerminalCoordinateProof(h, owner, "repository-a")
			if err != nil {
				t.Fatal(err)
			}
			want := 2
			if quarantine {
				want = 3
			}
			if len(p.boundExecutions) != want || len(p.states) != 2+3*want || len(p.workspaces) != 1 {
				t.Fatal("complete typed census")
			}
			old := p.states[p.events["event-bind-execution"]]
			next := p.states[p.events["event-bound-rebind-execution"]]
			a, err := p.resolveBoundExecutionWorkspace(terminalBoundReference(p, old))
			if err != nil {
				t.Fatal(err)
			}
			b, err := p.resolveBoundExecutionWorkspace(terminalBoundReference(p, next))
			if err != nil {
				t.Fatal(err)
			}
			if a.key == b.key || a.key.revision != 2 || b.key.revision != 3 || a.digest == b.digest {
				t.Fatal("old/new links aliased")
			}
			for _, s := range []terminalHistoricalState{old, next} {
				r := terminalBoundReference(p, s)
				resolved, err := p.resolveAcceptanceReference(r)
				if err != nil || !bytes.Equal(resolved.canonical, s.canonical) {
					t.Fatal("acceptance reference", err)
				}
			}
			for name, mutate := range map[string]func(*terminalStateReference){"latest_for_old": func(r *terminalStateReference) { r.evidence.StateDigestSHA256 = next.digest.String() }, "capture": func(r *terminalStateReference) {
				r.capture, _ = journalv2.NewStoreRevisionV2("database-other", p.capture.CurrentRevision(), p.capture.DurableThroughRevision())
			}, "history": func(r *terminalStateReference) { r.historySHA = strings.Repeat("f", 64) }} {
				t.Run(name, func(t *testing.T) {
					r := terminalBoundReference(p, old)
					mutate(&r)
					if _, err := p.resolveBoundExecutionWorkspace(r); err == nil {
						t.Fatal("bad reference accepted")
					}
				})
			}
			a.canonical[0] = '!'
			if p.states[a.key].canonical[0] == '!' {
				t.Fatal("linked resolution aliases state")
			}
			original := p.boundExecutions[old.key]
			p.boundExecutions[old.key] = b.key
			if _, err := p.resolveBoundExecutionWorkspace(terminalBoundReference(p, old)); err == nil {
				t.Fatal("latest historical link substituted")
			}
			p.boundExecutions[old.key] = original
			for _, inventory := range p.workspaces {
				claim := h.Publications[1].Ack.Plan().WorkspacePathClaim()
				if inventory.claimRevision != 2 || inventory.claim.ClaimDigest() != claim.ClaimDigest() || !bytes.Equal(inventory.claim.CanonicalValue(), claim.CanonicalValue()) {
					t.Fatal("original claim replaced")
				}
			}
			for _, r := range h.Publications[2:] {
				if r.Ack.Plan().WorkspacePathClaim() != nil || r.Proof.WorkspacePathClaim != nil {
					t.Fatal("continuation claim invented")
				}
			}
			h.Publications[1].Proof.JournalRecords = nil
			*h.Publications[1].Proof.WorkspacePathClaim = journalv2.WorkspacePathClaimV2{}
			owner.root = "/changed"
			if _, err := p.resolveBoundExecutionWorkspace(terminalBoundReference(p, old)); err != nil {
				t.Fatal("input capture alias", err)
			}
		})
	}
}

// Mutate sealed source-service plans, then really seal/publish again. Generic
// publisher controls are reported separately; owner controls must reach the
// fully validated ACK-history boundary before the private foundation refuses.
func terminalBoundReseal(t *testing.T, h *terminalPublicationHistory, mutate func(int, *journalv2.CommitPlanInput)) (*terminalPublicationHistory, string, error) {
	t.Helper()
	durable, coordinator := terminalBoundCoordinator(t)
	var acks []journalv2.AcknowledgedPublicationV2
	for index, r := range h.Publications {
		plan := r.Ack.Plan()
		binding := *plan.BindingCreate()
		input := journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: plan.OperationState(), Heads: plan.Heads(), EffectIntents: plan.EffectIntents(), WorkspacePathClaim: plan.WorkspacePathClaim(), Assertions: plan.Assertions()}
		for _, e := range plan.Events() {
			input.Events = append(input.Events, terminalCouplingDraft(e))
		}
		if mutate != nil {
			mutate(index, &input)
		}
		terminalBoundRefresh(t, &input)
		sealed, err := journalv2.SealCommitPlanV2(input, journalv2.DefaultEventRegistryV2())
		if err != nil {
			return nil, "seal", err
		}
		ack, err := coordinator.Publish(t.Context(), sealed)
		if err != nil {
			return nil, "publish", err
		}
		acks = append(acks, ack)
	}
	return terminalBoundHistory(t, durable, acks), "published", nil
}
func terminalBoundRefresh(t *testing.T, input *journalv2.CommitPlanInput) {
	t.Helper()
	s := input.OperationState
	var eventIDs []journalv2.EventID
	var effects []journalv2.EffectID
	for _, e := range input.Events {
		eventIDs = append(eventIDs, e.EventID)
	}
	for _, e := range input.EffectIntents {
		effects = append(effects, e.EffectID)
	}
	sort.Slice(eventIDs, func(i, j int) bool { return eventIDs[i] < eventIDs[j] })
	sort.Slice(effects, func(i, j int) bool { return effects[i] < effects[j] })
	op, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: s.OperationID(), LogicalVersion: s.LogicalVersion(), Status: s.Status(), ResultKind: s.ResultKind(), ResultSchemaVersion: s.ResultSchemaVersion(), CanonicalResult: s.Result(), EventIDs: eventIDs, EffectIDs: effects})
	if err != nil {
		t.Fatal(err)
	}
	input.OperationState = op
	payload, err := journalv2.EncodeOperationStateEventPayloadV2(op)
	if err != nil {
		t.Fatal(err)
	}
	for i := range input.Events {
		e := &input.Events[i]
		var state []byte
		switch e.Family {
		case "operation":
			e.Payload = payload
			state = payload
		case "workspace":
			s, err := workspace.DecodeEventV2(e.Payload)
			if err != nil {
				t.Fatal(err)
			}
			state = s.State.CanonicalBytes()
		case "execution_slot":
			s, err := execution.DecodeExecutionSlotEventV2(e.Payload)
			if err != nil {
				t.Fatal(err)
			}
			state = s.State.CanonicalBytes()
		}
		for j := range input.Heads {
			head := &input.Heads[j]
			if head.EventID != e.EventID {
				continue
			}
			head.Coordinate = e.Coordinate
			head.CommittedRuntimeEpochID = e.CommittedRuntimeEpochID
			head.CanonicalStateDigest, _ = journalv2.ReducerStateDigestV2(state)
			if e.Family != "operation" || !bytes.Equal(head.AuthoritySummary, []byte(`{"status":"resolved"}`)) {
				head.AuthoritySummary = state
			}
		}
	}
	input.Conditions = nil
	for _, head := range input.Heads {
		condition, err := journalv2.ConditionForHeadTransitionV2(head)
		if err != nil {
			t.Fatal(err)
		}
		input.Conditions = append(input.Conditions, condition)
	}
}
func terminalBoundSetResult(t *testing.T, input *journalv2.CommitPlanInput, kind string, schema uint32, result []byte) {
	t.Helper()
	s := input.OperationState
	op, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: s.OperationID(), LogicalVersion: s.LogicalVersion(), Status: s.Status(), ResultKind: kind, ResultSchemaVersion: schema, CanonicalResult: result, EventIDs: s.EventIDs(), EffectIDs: s.EffectIDs()})
	if err != nil {
		t.Fatal(err)
	}
	input.OperationState = op
}
func terminalBoundCorrectResult(t *testing.T, input *journalv2.CommitPlanInput) {
	t.Helper()
	var result execution.WorkspaceAuthorityResultV2
	for _, e := range input.Events {
		switch e.Family {
		case "execution_slot":
			s, err := execution.DecodeExecutionSlotEventV2(e.Payload)
			if err != nil {
				t.Fatal(err)
			}
			result.Execution = s.State
		case "workspace":
			s, err := workspace.DecodeEventV2(e.Payload)
			if err != nil {
				t.Fatal(err)
			}
			result.Workspace = s.State
		}
	}
	terminalBoundSetResult(t, input, "workspace.result", 1, mustTerminalJSON(result))
}
func terminalBoundDropFamily(input *journalv2.CommitPlanInput, family string) {
	var events []journalv2.EventDraftV2
	var heads []journalv2.HeadTransitionV2
	for _, e := range input.Events {
		if e.Family != family {
			events = append(events, e)
		}
	}
	for _, h := range input.Heads {
		if string(h.Coordinate.Kind) != family {
			heads = append(heads, h)
		}
	}
	input.Events = events
	input.Heads = heads
	if family == "workspace" {
		input.WorkspacePathClaim = nil
		input.EffectIntents = nil
	}
}

// Draft reversal is canonicalized away by SealCommitPlanV2. This checks
// canonical draft-order invariance, NOT a different sealed visitation order.
func TestTerminalBoundExecutionCanonicalDraftOrder(t *testing.T) {
	template, owner := terminalBoundFixture(t, true)
	h, stage, err := terminalBoundReseal(t, template, func(index int, input *journalv2.CommitPlanInput) {
		for i, j := 0, len(input.Events)-1; i < j; i, j = i+1, j-1 {
			input.Events[i], input.Events[j] = input.Events[j], input.Events[i]
		}
		if index > 0 {
			terminalBoundCorrectResult(t, input)
		}
	})
	if err != nil || stage != "published" {
		t.Fatalf("canonical draft-order reseal %s: %v", stage, err)
	}
	for i, r := range h.Publications {
		actual, original := r.Ack.Plan().Events(), template.Publications[i].Ack.Plan().Events()
		if !reflect.DeepEqual(actual, original) {
			t.Fatalf("canonical sealing changed event arrays at publication %d", i)
		}
	}
	t.Log("reversed drafts produce identical sealed event arrays, including EventIndex and publication IDs")
	p, err := newTerminalCoordinateProof(h, owner, "repository-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []journalv2.EventID{"event-bind-execution", "event-bound-rebind-execution", "event-expire-execution"} {
		s := p.states[p.events[id]]
		w, err := p.resolveBoundExecutionWorkspace(terminalBoundReference(p, s))
		if err != nil || w.key.revision != s.key.revision {
			t.Fatal("canonical draft-order history lost exact link", err)
		}
	}
}

func TestTerminalBoundExecutionEventVisitationOrderIndependent(t *testing.T) {
	h, owner := terminalBoundFixture(t, true)
	p, err := newTerminalCoordinateProof(h, owner, "repository-a")
	if err != nil {
		t.Fatal(err)
	}
	normalLinks := make(map[terminalHistoricalKey]terminalHistoricalKey, len(p.boundExecutions))
	for execution, workspace := range p.boundExecutions {
		normalLinks[execution] = workspace
	}
	// Assert the actual chosen arrays differ BEFORE invoking the reverse pass.
	// Also prove that reversal has not edited envelopes or their EventIndex,
	// and has not mutated the authoritative sealed array.
	for i, r := range h.Publications {
		original := r.Ack.Plan().Events()
		normal := terminalBoundPublicationVisits(r, false)
		reversed := terminalBoundPublicationVisits(r, true)
		if len(normal) < 2 || reflect.DeepEqual(normal, reversed) || !reflect.DeepEqual(normal, original) {
			t.Fatalf("visitation arrays are not genuinely different at publication %d", i)
		}
		for j, event := range reversed {
			actual, err := journalv2.EncodeEventEnvelopeV2(event)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := journalv2.EncodeEventEnvelopeV2(original[len(original)-1-j])
			if err != nil || !bytes.Equal(actual, expected) {
				t.Fatal("reversal edited retained authority", err)
			}
		}
		if !reflect.DeepEqual(original, r.Ack.Plan().Events()) {
			t.Fatal("visitation mutated sealed event array")
		}
		t.Logf("publication %d visits original first=%s/index=%d; reversed first=%s/index=%d", i, normal[0].EventID(), normal[0].EventIndex(), reversed[0].EventID(), reversed[0].EventIndex())
	}
	if err := p.closeBoundExecutionReferencesWithVisitation(h, true); err != nil {
		t.Fatal("reversed visitation current-owner/coupling closure", err)
	}
	if !reflect.DeepEqual(normalLinks, p.boundExecutions) {
		t.Fatal("reverse visitation changed exact historical links")
	}
	for execution, workspace := range normalLinks {
		s := p.states[execution]
		linked, err := p.resolveBoundExecutionWorkspace(terminalBoundReference(p, s))
		if err != nil || linked.key != workspace || !bytes.Equal(linked.canonical, p.states[workspace].canonical) {
			t.Fatal("reversed pass lost historical bytes/key", err)
		}
	}
}

func TestTerminalBoundExecutionWorkspaceOnlyQuarantineRefused(t *testing.T) {
	template, owner := terminalBoundFixture(t, true)
	h, stage, err := terminalBoundReseal(t, template, func(index int, input *journalv2.CommitPlanInput) {
		if index == 3 {
			terminalBoundDropFamily(input, "execution_slot")
		}
	})
	if err != nil || stage != "published" {
		t.Fatalf("workspace-only quarantine must genuinely publish: %s %v", stage, err)
	}
	if _, err := validateTerminalHistory(h); err != nil {
		t.Fatal("control did not reach foundation", err)
	}
	last := h.Publications[3].Ack.Plan().Events()
	if len(last) != 2 {
		t.Fatal("workspace-only quarantine census")
	}
	for _, e := range last {
		if e.Family() == "execution_slot" {
			t.Fatal("control retained coupled execution")
		}
	}
	_, err = newTerminalCoordinateProof(h, owner, "repository-a")
	if err == nil || !strings.Contains(err.Error(), "workspace quarantine needs same-publication bound lease expiry") {
		t.Fatal("workspace-only quarantine not refused by workspace-led coupling gate", err)
	}
	t.Logf("genuinely published; workspace-led foundation refusal: %v", err)
}

func TestTerminalBoundExecutionPublishedCouplingControls(t *testing.T) {
	cases := map[string]func(*journalv2.CommitPlanInput){
		"fake_result": func(input *journalv2.CommitPlanInput) {
			terminalBoundSetResult(t, input, "workspace.result", 1, []byte(`{"fake":"publisher accepted"}`))
		},
		"extra_result_field": func(input *journalv2.CommitPlanInput) {
			result := input.OperationState.Result()
			result = append(append([]byte(nil), result[:len(result)-1]...), []byte(`,"extra":true}`)...)
			terminalBoundSetResult(t, input, "workspace.result", 1, result)
		},
		"wrong_result_kind": func(input *journalv2.CommitPlanInput) {
			terminalBoundSetResult(t, input, "publisher.result", 1, input.OperationState.Result())
		},
		"wrong_result_schema": func(input *journalv2.CommitPlanInput) {
			terminalBoundSetResult(t, input, "workspace.result", 2, input.OperationState.Result())
		},
		"standalone_execution_rebind": func(input *journalv2.CommitPlanInput) { terminalBoundDropFamily(input, "workspace") },
		"standalone_workspace_rebind": func(input *journalv2.CommitPlanInput) { terminalBoundDropFamily(input, "execution_slot") },
	}
	for _, field := range []string{"command", "intent", "actor", "process", "origin", "doctrine", "profile", "policy", "schema", "time", "scope"} {
		field := field
		cases["binding_"+field] = func(input *journalv2.CommitPlanInput) {
			b := input.OperationBinding
			q := journalv2.OperationBindingInput{OperationID: b.OperationID(), CommandKind: b.CommandKind(), CommandSchemaVersion: b.CommandSchemaVersion(), CanonicalIntentDigest: b.IntentDigest(), Scope: b.Scope(), ActorID: b.ActorID(), ProcessID: b.ProcessID(), OriginatingRuntimeEpochID: b.OriginatingEpoch(), DoctrineRevision: b.DoctrineRevision(), ProfileRevision: b.ProfileRevision(), PolicyRevision: b.PolicyRevision(), ObservedCreatedAt: b.ObservedCreatedAt()}
			switch field {
			case "command":
				q.CommandKind = "workspace.bind"
			case "intent":
				q.CanonicalIntentDigest, _ = journalv2.ReducerStateDigestV2([]byte(`{"intent":"unrelated"}`))
			case "actor":
				q.ActorID = "actor-other"
			case "process":
				q.ProcessID = "process-other"
			case "origin":
				q.OriginatingRuntimeEpochID = "epoch-other"
			case "doctrine":
				q.DoctrineRevision = "doctrine-other"
			case "profile":
				q.ProfileRevision = "profile-other"
			case "policy":
				q.PolicyRevision = "policy-other"
			case "schema":
				q.CommandSchemaVersion = 2
			case "time":
				q.ObservedCreatedAt = "2026-09-05T12:21:00Z"
			case "scope":
				q.Scope = journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-fixture", MissionID: "mission-fixture"}
			}
			next, err := journalv2.NewOperationBindingV2(q)
			if err != nil {
				t.Fatal(err)
			}
			input.OperationBinding = &next
		}
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			template, owner := terminalBoundFixture(t, false)
			h, stage, err := terminalBoundReseal(t, template, func(index int, input *journalv2.CommitPlanInput) {
				if index == 2 {
					mutate(input)
				}
			})
			if err != nil || stage != "published" {
				t.Fatalf("must reach owner layer: %s %v", stage, err)
			}
			_, err = newTerminalCoordinateProof(h, owner, "repository-a")
			if err == nil {
				t.Fatal("invalid coupling accepted")
			}
			t.Logf("genuinely published; foundation refusal: %v", err)
			if name == "standalone_workspace_rebind" && !strings.Contains(err.Error(), "current owner mismatch") {
				t.Fatal("standalone workspace must fail at current bound head boundary", err)
			}
		})
	}
}

func terminalBoundReplaceContribution(t *testing.T, input *journalv2.CommitPlanInput, contribution workspace.PlanContributionV2) {
	t.Helper()
	var policy string
	var governance []string
	for _, e := range input.Events {
		if e.Family == "workspace" {
			policy = e.PolicyRevision
			governance = e.GovernanceRevisions
		}
	}
	terminalBoundDropFamily(input, "workspace")
	var err error
	*input, err = workspace.AppendPlanContributionV2(*input, contribution)
	if err != nil {
		t.Fatal(err)
	}
	for i := range input.Events {
		if input.Events[i].Family == "workspace" {
			input.Events[i].PolicyRevision = policy
			input.Events[i].GovernanceRevisions = governance
		}
	}
}

func TestTerminalBoundExecutionPublishedReferenceControls(t *testing.T) {
	cases := map[string]func(*journalv2.CommitPlanInput){
		"missing_claim": func(input *journalv2.CommitPlanInput) { input.WorkspacePathClaim = nil },
		"altered_claim_bytes": func(input *journalv2.CommitPlanInput) {
			claim, err := journalv2.NewWorkspacePathClaimV2(input.WorkspacePathClaim.ClaimDigest(), []byte(`{"ownership":"unrelated"}`))
			if err != nil {
				t.Fatal(err)
			}
			input.WorkspacePathClaim = &claim
		},
		"altered_claim_digest": func(input *journalv2.CommitPlanInput) {
			digest, _ := journalv2.ReducerStateDigestV2([]byte(`{"claim":"unrelated"}`))
			claim, err := journalv2.NewWorkspacePathClaimV2(digest, input.WorkspacePathClaim.CanonicalValue())
			if err != nil {
				t.Fatal(err)
			}
			input.WorkspacePathClaim = &claim
		},
		"execution_claim_digest": func(input *journalv2.CommitPlanInput) {
			terminalExecutionControlEvent(t, input, func(p *execution.ExecutionSlotEventV2, _ *journalv2.EventDraftV2) {
				p.State.WorkspacePathClaimDigest, _ = journalv2.ReducerStateDigestV2([]byte(`{"claim":"unrelated"}`))
			})
			terminalBoundCorrectResult(t, input)
		},
		"missing_workspace": func(input *journalv2.CommitPlanInput) {
			terminalBoundDropFamily(input, "workspace")
			terminalBoundMissionBinding(t, input)
		},
	}
	for _, field := range []string{"project", "mission", "work_revision", "work", "run", "lease", "workspace", "generation", "fence", "actor", "process", "epoch"} {
		field := field
		cases["workspace_authority_"+field] = func(input *journalv2.CommitPlanInput) {
			var event journalv2.EventDraftV2
			for _, e := range input.Events {
				if e.Family == "workspace" {
					event = e
				}
			}
			payload, err := workspace.DecodeEventV2(event.Payload)
			if err != nil {
				t.Fatal(err)
			}
			b := payload.State.Binding
			switch field {
			case "project":
				b.ProjectID = "project-other"
			case "mission":
				b.MissionID = "mission-other"
			case "work_revision":
				b.WorkRevisionID = "revision-other"
			case "work":
				b.WorkID = "work-other"
			case "run":
				b.RunID = "run-other"
			case "lease":
				b.LeaseID = "lease-other"
			case "workspace":
				b.WorkspaceID = "workspace-other"
				b.CanonicalPath = filepath.Join(b.ManagedRoot, "workspace-other")
				b.PathClaimDigest, err = workspace.WorkspacePathClaimDigestV2(b.RepositoryID, b.ManagedRoot, b.CanonicalPath)
			case "generation":
				b.Generation++
			case "fence":
				b.Fence = strings.Repeat("c", 32)
			case "actor":
				b.ActorID = "actor-other"
			case "process":
				b.ProcessID = "process-other"
			case "epoch":
				b.RuntimeEpochID = "epoch-other"
			}
			if err != nil {
				t.Fatal(err)
			}
			at, _ := time.Parse(time.RFC3339Nano, payload.State.OccurredAt)
			contribution, err := workspace.NewBindingContributionV2(workspace.BindingContributionInputV2{Binding: b, EffectID: payload.State.EffectID, EventID: event.EventID, OccurredAt: at})
			if err != nil {
				t.Fatal(err)
			}
			terminalBoundReplaceContribution(t, input, contribution)
			terminalBoundCorrectResult(t, input)
		}
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			template, owner := terminalBoundFixture(t, false)
			template.Publications = template.Publications[:2]
			h, stage, err := terminalBoundReseal(t, template, func(index int, input *journalv2.CommitPlanInput) {
				if index == 1 {
					mutate(input)
				}
			})
			if err != nil || stage != "published" {
				t.Fatalf("reference control must be published: %s %v", stage, err)
			}
			_, err = newTerminalCoordinateProof(h, owner, "repository-a")
			if err == nil {
				t.Fatal("unclosed bound reference accepted")
			}
			if name == "missing_workspace" && !strings.Contains(err.Error(), "historical permanent-claim reference") {
				t.Fatal("missing owner refused at unrelated boundary", err)
			}
			t.Logf("genuinely published; foundation refusal: %v", err)
		})
	}
	t.Run("future_workspace_and_claim", func(t *testing.T) {
		template, owner := terminalBoundFixture(t, false)
		creation := template.Publications[1].Ack.Plan()
		h, stage, err := terminalBoundReseal(t, template, func(index int, input *journalv2.CommitPlanInput) {
			if index == 1 {
				terminalBoundDropFamily(input, "workspace")
				terminalBoundMissionBinding(t, input)
			} else if index == 2 {
				terminalBoundDropFamily(input, "execution_slot")
				terminalBoundDropFamily(input, "workspace")
				for _, e := range creation.Events() {
					if e.Family() == "workspace" {
						input.Events = append(input.Events, terminalCouplingDraft(e))
					}
				}
				for _, head := range creation.Heads() {
					if head.Coordinate.Kind == "workspace" {
						input.Heads = append(input.Heads, head)
					}
				}
				input.WorkspacePathClaim = creation.WorkspacePathClaim()
				input.EffectIntents = creation.EffectIntents()
			}
		})
		if err != nil || stage != "published" {
			t.Fatalf("future claim control %s: %v", stage, err)
		}
		_, err = newTerminalCoordinateProof(h, owner, "repository-a")
		if err == nil || !strings.Contains(err.Error(), "historical permanent-claim reference") {
			t.Fatal("future workspace/claim not refused at reference boundary", err)
		}
		t.Logf("genuinely published; foundation refusal: %v", err)
	})
}

func TestTerminalBoundExecutionWrongPairedWorkspace(t *testing.T) {
	for _, control := range []string{"rebind_observation", "rebind_wrong_type", "expiry_wrong_type", "standalone_bound_expiry"} {
		t.Run(control, func(t *testing.T) {
			template, owner := terminalBoundFixture(t, true)
			target := 2
			if strings.Contains(control, "expiry") {
				target = 3
			}
			template.Publications = template.Publications[:target+1]
			h, stage, err := terminalBoundReseal(t, template, func(index int, input *journalv2.CommitPlanInput) {
				if index != target {
					return
				}
				if control == "standalone_bound_expiry" {
					terminalBoundDropFamily(input, "workspace")
					terminalBoundMissionBinding(t, input)
					return
				}
				if control == "rebind_observation" {
					for i := range input.Events {
						e := &input.Events[i]
						if e.Family == "workspace" {
							payload, err := workspace.DecodeEventV2(e.Payload)
							if err != nil {
								t.Fatal(err)
							}
							payload.State.Observation.Registered = true
							e.Payload, err = journalv2.CanonicalObject(mustTerminalJSON(payload))
							if err != nil {
								t.Fatal(err)
							}
						}
					}
				} else {
					var old workspace.StateV2
					for _, e := range template.Publications[index-1].Ack.Plan().Events() {
						if e.Family() == "workspace" {
							p, err := workspace.DecodeEventV2(e.Payload())
							if err != nil {
								t.Fatal(err)
							}
							old = p.State
						}
					}
					var event journalv2.EventDraftV2
					for _, e := range input.Events {
						if e.Family == "workspace" {
							event = e
						}
					}
					payload, err := workspace.DecodeEventV2(event.Payload)
					if err != nil {
						t.Fatal(err)
					}
					at, _ := time.Parse(time.RFC3339Nano, payload.State.OccurredAt)
					var contribution workspace.PlanContributionV2
					if control == "rebind_wrong_type" {
						contribution, err = workspace.NewQuarantineContributionV2(workspace.QuarantineContributionInputV2{Prior: old, RecordRevision: uint64(index), EventID: event.EventID, Reason: "not epoch rebind", OccurredAt: at})
					} else {
						contribution, err = workspace.NewTransitionContributionV2(workspace.TransitionContributionInputV2{Prior: old, RecordRevision: uint64(index), EventID: event.EventID, NextStage: workspace.StageWorktreeObservedV2, Observation: workspace.ObservationV2{Registered: true}, OccurredAt: at})
					}
					if err != nil {
						t.Fatal(err)
					}
					terminalBoundReplaceContribution(t, input, contribution)
				}
				terminalBoundCorrectResult(t, input)
			})
			// Wrong-rebind controls deliberately stop at the altered publication, not
			// an unrelated following expiry transition using the original prior digest.
			if err != nil {
				t.Fatalf("wrong pair must publish before owner refusal: %s %v", stage, err)
			}
			_, err = newTerminalCoordinateProof(h, owner, "repository-a")
			if err == nil {
				t.Fatal("wrong workspace pair accepted")
			}
			t.Logf("genuinely published; foundation refusal: %v", err)
		})
	}
}

func TestTerminalBoundExecutionPublisherControls(t *testing.T) {
	for _, control := range []string{"execution_actor", "workspace_actor", "workspace_type", "predecessor", "workspace_schema", "execution_reducer"} {
		t.Run(control, func(t *testing.T) {
			template, _ := terminalBoundFixture(t, false)
			_, stage, err := terminalBoundReseal(t, template, func(index int, input *journalv2.CommitPlanInput) {
				if index != 2 {
					return
				}
				for i := range input.Events {
					e := &input.Events[i]
					switch control {
					case "execution_actor":
						if e.Family == "execution_slot" {
							e.ActorID = "actor-other"
						}
					case "workspace_actor":
						if e.Family == "workspace" {
							e.ActorID = "actor-other"
						}
					case "workspace_type":
						if e.Family == "workspace" {
							e.EventType = "workspace.quarantined"
						}
					case "predecessor":
						terminalExecutionControlEvent(t, input, func(p *execution.ExecutionSlotEventV2, _ *journalv2.EventDraftV2) { p.Predecessor = nil })
					case "workspace_schema":
						if e.Family == "workspace" {
							e.EventSchemaVersion = 2
						}
					case "execution_reducer":
						if e.Family == "execution_slot" {
							e.ReducerVersion = 2
						}
					}
				}
			})
			expected := "publish"
			if control == "workspace_schema" || control == "execution_reducer" {
				expected = "seal"
			}
			if err == nil || stage != expected {
				t.Fatalf("expected publisher %s refusal, got %s %v", expected, stage, err)
			}
			t.Logf("publisher %s refusal: %v", stage, err)
		})
	}
}

func terminalBoundMissionBinding(t *testing.T, input *journalv2.CommitPlanInput) {
	t.Helper()
	b := input.OperationBinding
	q, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: b.OperationID(), CommandKind: b.CommandKind(), CommandSchemaVersion: b.CommandSchemaVersion(), CanonicalIntentDigest: b.IntentDigest(), Scope: journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-fixture", MissionID: "mission-fixture"}, ActorID: b.ActorID(), ProcessID: b.ProcessID(), OriginatingRuntimeEpochID: b.OriginatingEpoch(), DoctrineRevision: b.DoctrineRevision(), ProfileRevision: b.ProfileRevision(), PolicyRevision: b.PolicyRevision(), ObservedCreatedAt: b.ObservedCreatedAt()})
	if err != nil {
		t.Fatal(err)
	}
	input.OperationBinding = &q
}

func TestTerminalBoundExecutionCaptureCensusControls(t *testing.T) {
	for _, control := range []string{"claim_revision", "claim_proof_bytes", "missing_journal", "missing_operation_census", "capture"} {
		t.Run(control, func(t *testing.T) {
			h, owner := terminalBoundFixture(t, false)
			switch control {
			case "claim_revision":
				h.Publications[1].Proof.ClaimRecordRevision++
			case "claim_proof_bytes":
				claim := h.Publications[1].Proof.WorkspacePathClaim
				c, err := journalv2.NewWorkspacePathClaimV2(claim.ClaimDigest(), []byte(`{"altered":true}`))
				if err != nil {
					t.Fatal(err)
				}
				h.Publications[1].Proof.WorkspacePathClaim = &c
			case "missing_journal":
				h.Publications[1].Proof.JournalRecords = h.Publications[1].Proof.JournalRecords[:2]
			case "missing_operation_census":
				h.Progress.Operations = nil
				h.Progress.Items = nil
				h.Inspections = make(map[string]productionruntime.ProgressItem)
			case "capture":
				h.Publications[1].Proof.CapturedCoordinate, _ = journalv2.NewStoreRevisionV2("database-fixture", 4, 4)
			}
			h.HistorySHA256 = terminalHistoryDigest(h)
			if _, err := newTerminalCoordinateProof(h, owner, "repository-a"); err == nil {
				t.Fatal("altered ACK/capture/census accepted")
			}
		})
	}
}

func TestTerminalBoundExecutionWorkspaceOnlyStage(t *testing.T) {
	template, owner := terminalBoundFixture(t, false)
	h, stage, err := terminalBoundReseal(t, template, func(index int, input *journalv2.CommitPlanInput) {
		if index != 2 {
			return
		}
		terminalBoundDropFamily(input, "execution_slot")
		var old workspace.StateV2
		for _, e := range template.Publications[1].Ack.Plan().Events() {
			if e.Family() == "workspace" {
				payload, err := workspace.DecodeEventV2(e.Payload())
				if err != nil {
					t.Fatal(err)
				}
				old = payload.State
			}
		}
		contribution, err := workspace.NewTransitionContributionV2(workspace.TransitionContributionInputV2{Prior: old, RecordRevision: 2, EventID: "event-bound-rebind-workspace", NextStage: workspace.StageWorktreeObservedV2, Observation: workspace.ObservationV2{Registered: true}, OccurredAt: time.Date(2026, 9, 5, 12, 20, 0, 0, time.UTC)})
		if err != nil {
			t.Fatal(err)
		}
		terminalBoundReplaceContribution(t, input, contribution)
	})
	if err != nil || stage != "published" {
		t.Fatalf("non-owner stage %s: %v", stage, err)
	}
	p, err := newTerminalCoordinateProof(h, owner, "repository-a")
	if err != nil {
		t.Fatal("non-owner-changing stage must remain legal", err)
	}
	s := p.states[p.events["event-bind-execution"]]
	linked, err := p.resolveBoundExecutionWorkspace(terminalBoundReference(p, s))
	if err != nil || linked.key.revision != 2 {
		t.Fatal("later ordinary stage overwrote historic link", err)
	}
}

func TestTerminalBoundExecutionReplacedByUnboundGeneration(t *testing.T) {
	template, owner := terminalBoundFixture(t, true)
	// The final placeholder is re-sealed into a complete typed replacement,
	// not admitted as a duplicate receipt or missing-prefix snapshot.
	template.Publications = append(template.Publications, template.Publications[2])
	h, stage, err := terminalBoundReseal(t, template, func(index int, input *journalv2.CommitPlanInput) {
		if index != 4 {
			return
		}
		terminalBoundDropFamily(input, "workspace")
		var expired execution.ExecutionSlotEventV2
		for _, e := range template.Publications[3].Ack.Plan().Events() {
			if e.Family() == "execution_slot" {
				var err error
				expired, err = execution.DecodeExecutionSlotEventV2(e.Payload())
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		next := expired.State
		next.LogicalVersion++
		next.Generation++
		next.RunID = "run-replacement"
		next.LeaseID = "lease-replacement"
		next.Fence = strings.Repeat("c", 32)
		next.WorkspaceID = ""
		next.WorkspacePathClaimDigest = journalv2.Digest{}
		next.Status = execution.ExecutionActiveV2
		next.RuntimeEpochID = "epoch-replacement"
		next.HeartbeatAt = "2026-09-05T13:10:00Z"
		next.Deadline = "2026-09-05T14:10:00Z"
		prior := execution.ExecutionAuthorityCoordinatesV2{RunID: expired.State.RunID, ActorID: expired.State.ActorID, ProcessID: expired.State.ProcessID, LeaseID: expired.State.LeaseID, Generation: expired.State.Generation, Fence: expired.State.Fence, HeartbeatAt: expired.State.HeartbeatAt, Deadline: expired.State.Deadline, RuntimeEpochID: expired.State.RuntimeEpochID}
		binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: "operation-replacement", CommandKind: "fixture.replace", CommandSchemaVersion: 1, CanonicalIntentDigest: input.OperationBinding.IntentDigest(), Scope: journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-fixture", MissionID: "mission-fixture"}, ActorID: input.OperationBinding.ActorID(), ProcessID: input.OperationBinding.ProcessID(), OriginatingRuntimeEpochID: next.RuntimeEpochID, ObservedCreatedAt: next.HeartbeatAt})
		if err != nil {
			t.Fatal(err)
		}
		input.OperationBinding = &binding
		operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: binding.OperationID(), LogicalVersion: 1, Status: journalv2.StatusResolved, ResultKind: "execution_slot.result", ResultSchemaVersion: 1, CanonicalResult: next.CanonicalBytes(), EventIDs: []journalv2.EventID{"event-replacement-execution", "event-replacement-operation"}})
		if err != nil {
			t.Fatal(err)
		}
		input.OperationState = operation
		for i := range input.Events {
			e := &input.Events[i]
			e.CommittedRuntimeEpochID = next.RuntimeEpochID
			if e.Family == "operation" {
				e.EventID = "event-replacement-operation"
				e.Coordinate.ID = journalv2.AggregateID(binding.OperationID())
			} else {
				e.EventID = "event-replacement-execution"
				e.EventType = "execution_slot.run_replaced_lease_granted"
				e.AggregateLogicalVersion = journalv2.LogicalVersion(next.LogicalVersion)
				e.Payload, err = journalv2.CanonicalObject(mustTerminalJSON(execution.ExecutionSlotEventV2{SchemaVersion: 1, Kind: "run_replaced_lease_granted", ObservedAt: next.HeartbeatAt, Predecessor: &prior, State: next}))
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		for i := range input.Heads {
			head := &input.Heads[i]
			if head.Coordinate.Kind == "operation" {
				head.EventID = "event-replacement-operation"
				head.Coordinate.ID = journalv2.AggregateID(binding.OperationID())
			} else {
				head.EventID = "event-replacement-execution"
				head.ExpectedLogicalVersion = journalv2.LogicalVersion(expired.State.LogicalVersion)
				head.NextLogicalVersion = journalv2.LogicalVersion(next.LogicalVersion)
				head.ExpectedRecordRevision = 4
			}
		}
	})
	if err != nil || stage != "published" {
		t.Fatalf("replacement %s: %v", stage, err)
	}
	p, err := newTerminalCoordinateProof(h, owner, "repository-a")
	if err != nil {
		t.Fatal("obsolete historical bound generation checked as current", err)
	}
	if len(p.boundExecutions) != 3 {
		t.Fatal("replacement lost old bound historical links")
	}
	for _, key := range p.boundExecutions {
		if key.revision > 4 {
			t.Fatal("replacement invented a new workspace link")
		}
	}
	replacement := p.states[p.events["event-replacement-execution"]]
	if _, err := p.resolveBoundExecutionWorkspace(terminalBoundReference(p, replacement)); err == nil {
		t.Fatal("unbound replacement assigned obsolete workspace")
	}
}

func TestTerminalBoundExecutionPublishedCASControls(t *testing.T) {
	for _, family := range []string{"execution_slot", "workspace"} {
		t.Run(family, func(t *testing.T) {
			template, owner := terminalBoundFixture(t, false)
			h, stage, err := terminalBoundReseal(t, template, func(index int, input *journalv2.CommitPlanInput) {
				if index == 2 {
					for i := range input.Heads {
						if string(input.Heads[i].Coordinate.Kind) == family {
							input.Heads[i].ExpectedRecordRevision++
						}
					}
				}
			})
			// The fixture Commit records the actual sealed plan; it does not pretend to
			// enforce provider CAS. Hence this genuinely published control isolates the
			// foundation's independent retained-head contiguity check.
			if err != nil || stage != "published" {
				t.Fatalf("CAS control %s: %v", stage, err)
			}
			_, err = newTerminalCoordinateProof(h, owner, "repository-a")
			if err == nil || !strings.Contains(err.Error(), "noncontiguous full coordinate") {
				t.Fatal("wrong historical CAS not refused at contiguous owner boundary", err)
			}
			t.Logf("genuinely published; foundation refusal: %v", err)
		})
	}
}

func TestTerminalBoundExecutionBindContractControls(t *testing.T) {
	for _, control := range []string{"intent", "command", "result", "record_revision", "replayed", "extra_effect"} {
		t.Run(control, func(t *testing.T) {
			template, owner := terminalBoundFixture(t, false)
			template.Publications = template.Publications[:2]
			h, stage, err := terminalBoundReseal(t, template, func(index int, input *journalv2.CommitPlanInput) {
				if index != 1 {
					return
				}
				switch control {
				case "intent", "command":
					b := input.OperationBinding
					q := journalv2.OperationBindingInput{OperationID: b.OperationID(), CommandKind: b.CommandKind(), CommandSchemaVersion: b.CommandSchemaVersion(), CanonicalIntentDigest: b.IntentDigest(), Scope: b.Scope(), ActorID: b.ActorID(), ProcessID: b.ProcessID(), OriginatingRuntimeEpochID: b.OriginatingEpoch(), DoctrineRevision: b.DoctrineRevision(), ProfileRevision: b.ProfileRevision(), PolicyRevision: b.PolicyRevision(), ObservedCreatedAt: b.ObservedCreatedAt()}
					if control == "intent" {
						q.CanonicalIntentDigest, _ = journalv2.ReducerStateDigestV2([]byte(`{"wrong":"bind intent"}`))
					} else {
						q.CommandKind = "workspace.rebind_runtime_epoch"
					}
					bnext, err := journalv2.NewOperationBindingV2(q)
					if err != nil {
						t.Fatal(err)
					}
					input.OperationBinding = &bnext
				case "result":
					terminalBoundSetResult(t, input, "workspace.result", 1, []byte(`{"generic":"resolved"}`))
				case "record_revision", "replayed":
					var result execution.WorkspaceAuthorityResultV2
					for _, e := range input.Events {
						switch e.Family {
						case "execution_slot":
							s, err := execution.DecodeExecutionSlotEventV2(e.Payload)
							if err != nil {
								t.Fatal(err)
							}
							result.Execution = s.State
						case "workspace":
							s, err := workspace.DecodeEventV2(e.Payload)
							if err != nil {
								t.Fatal(err)
							}
							result.Workspace = s.State
						}
					}
					if control == "record_revision" {
						result.RecordRevision = 2
					} else {
						result.Replayed = true
					}
					terminalBoundSetResult(t, input, "workspace.result", 1, mustTerminalJSON(result))
				case "extra_effect":
					canonical := []byte(`{"extra":"unowned effect"}`)
					digest, _ := journalv2.ReducerStateDigestV2(canonical)
					input.EffectIntents = append(input.EffectIntents, journalv2.EffectIntentReference{EffectID: "effect-extra", Digest: digest, Canonical: canonical})
				}
			})
			if err != nil || stage != "published" {
				t.Fatalf("bind contract control %s: %v", stage, err)
			}
			_, err = newTerminalCoordinateProof(h, owner, "repository-a")
			if err == nil || !strings.Contains(err.Error(), "coupling") {
				t.Fatal("bad bind contract not refused at coupling boundary", err)
			}
			t.Logf("genuinely published; foundation refusal: %v", err)
		})
	}
}
