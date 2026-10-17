package scenario

import (
	"bytes"
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

// Claims are retained independently on the actual Commit path, not copied
// from a caller's proof or fabricated address grammar. No external IO.
type terminalCoordinateFixturePort struct {
	terminalFixturePort
	claims         map[journalv2.Digest]journalv2.WorkspacePathClaimV2
	claimRevisions map[journalv2.Digest]uint64
}

func (p *terminalCoordinateFixturePort) Commit(ctx context.Context, plan journalv2.SealedCommitPlanV2) (journalv2.CommitObservationV2, error) {
	observation, err := p.terminalFixturePort.Commit(ctx, plan)
	if err != nil {
		return observation, err
	}
	if claim := plan.WorkspacePathClaim(); claim != nil {
		copy, err := journalv2.NewWorkspacePathClaimV2(claim.ClaimDigest(), claim.CanonicalValue())
		if err != nil {
			return journalv2.CommitObservationV2{}, err
		}
		p.claims[copy.ClaimDigest()] = copy
		p.claimRevisions[copy.ClaimDigest()] = p.revision
	}
	return observation, nil
}
func terminalCoordinateFixture(t *testing.T, root string, mutations ...func(int, *journalv2.CommitPlanInput)) (*terminalPublicationHistory, *portableHarness) {
	t.Helper()
	owner := &portableHarness{root: root}
	managed := filepath.Join(owner.root, "workspaces")
	binding := workspace.BindingV2{WorkspaceID: "workspace-a", ProjectID: "project-fixture", MissionID: "mission-fixture", WorkRevisionID: "revision-fixture", WorkID: "work-a", RunID: "run-a", LeaseID: "lease-a", Generation: 1, Fence: strings.Repeat("a", 32), RuntimeEpochID: "epoch-fixture", ActorID: "actor-a", ProcessID: "process-a", RepositoryID: "repository-a", LaneID: "lane-a", ManagedRoot: managed, CanonicalPath: filepath.Join(managed, "workspace-a"), PathIdentity: workspace.PathIdentityV2{Absolute: true, Canonical: true, WithinManagedRoot: true, AliasFree: true, SymlinkFree: true}, OwnedBranch: "kitty/work-a", PinnedBase: strings.Repeat("b", 40), Dependencies: []workspace.DependencyV2{{WorkID: "work-dependency", Commit: strings.Repeat("c", 40)}}}
	var err error
	binding.PathClaimDigest, err = workspace.WorkspacePathClaimDigestV2(binding.RepositoryID, managed, binding.CanonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	reducers, err := productionruntime.BuildReducerSetV2(journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	port := &terminalCoordinateFixturePort{terminalFixturePort: terminalFixturePort{records: make(map[journalv2.RecordAddressV2]journalv2.RecoveryRecordV2)}, claims: make(map[journalv2.Digest]journalv2.WorkspacePathClaimV2), claimRevisions: make(map[journalv2.Digest]uint64)}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(port, tracker)
	if err != nil {
		t.Fatal(err)
	}
	scope := journalv2.Scope{Kind: journalv2.ScopeHost}
	intent, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "fixture.workspace", CommandSchemaVersion: 1, Scope: scope, Command: []byte(`{"work_id":"work-a"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var receipts []terminalPublicationReceipt
	var prior *workspace.StateV2
	for version := 1; version <= 2; version++ {
		operationID := journalv2.OperationID("operation-workspace-create")
		operationEvent := journalv2.EventID("event-workspace-operation-create")
		workspaceEvent := journalv2.EventID("event-workspace-create")
		if version == 2 {
			operationID = "operation-workspace-rebind"
			operationEvent = "event-workspace-operation-rebind"
			workspaceEvent = "event-workspace-rebind"
		}
		var contribution workspace.PlanContributionV2
		at := time.Date(2026, 9, 7, 0, version, 0, 0, time.UTC)
		if prior == nil {
			contribution, err = workspace.NewBindingContributionV2(workspace.BindingContributionInputV2{Binding: binding, EffectID: "effect-create-a", EventID: workspaceEvent, OccurredAt: at})
		} else {
			contribution, err = workspace.NewEpochRebindContributionV2(workspace.EpochRebindContributionInputV2{Prior: *prior, RecordRevision: 1, EventID: workspaceEvent, RuntimeEpochID: "epoch-next", Observation: prior.Observation, OccurredAt: at})
		}
		if err != nil {
			t.Fatal(err)
		}
		state, err := contribution.State()
		if err != nil {
			t.Fatal(err)
		}
		b, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: operationID, CommandKind: "fixture.workspace", CommandSchemaVersion: 1, CanonicalIntentDigest: intent, Scope: scope, ActorID: "actor-a", ProcessID: "process-a", OriginatingRuntimeEpochID: state.Binding.RuntimeEpochID, ObservedCreatedAt: at.Format(time.RFC3339)})
		if err != nil {
			t.Fatal(err)
		}
		eventIDs := []journalv2.EventID{operationEvent, workspaceEvent}
		sort.Slice(eventIDs, func(i, j int) bool { return eventIDs[i] < eventIDs[j] })
		var effectIDs []journalv2.EffectID
		if version == 1 {
			effectIDs = []journalv2.EffectID{"effect-create-a"}
		}
		op, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: operationID, LogicalVersion: 1, Status: journalv2.StatusResolved, ResultKind: "workspace.stage.result", ResultSchemaVersion: 1, CanonicalResult: state.CanonicalBytes(), EventIDs: eventIDs, EffectIDs: effectIDs})
		if err != nil {
			t.Fatal(err)
		}
		payload, err := journalv2.EncodeOperationStateEventPayloadV2(op)
		if err != nil {
			t.Fatal(err)
		}
		coordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(operationID), scope)
		digest, _ := journalv2.ReducerStateDigestV2(payload)
		head := journalv2.HeadTransitionV2{Coordinate: coordinate, NextLogicalVersion: 1, EventID: operationEvent, CommittedRuntimeEpochID: state.Binding.RuntimeEpochID, CanonicalStateDigest: digest, AuthoritySummary: payload}
		condition, err := journalv2.ConditionForHeadTransitionV2(head)
		if err != nil {
			t.Fatal(err)
		}
		input := journalv2.CommitPlanInput{OperationBinding: &b, OperationState: op, Events: []journalv2.EventDraftV2{{EventID: operationEvent, EventType: "operation.resolved", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: coordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: state.Binding.RuntimeEpochID, ActorID: "actor-a", ProcessID: "process-a", Payload: payload}}, Heads: []journalv2.HeadTransitionV2{head}, Conditions: []journalv2.ProviderCondition{condition}}
		input, err = workspace.AppendPlanContributionV2(input, contribution)
		if err != nil {
			t.Fatal(err)
		}
		for _, mutate := range mutations {
			mutate(version, &input)
		}
		// Mutant plans are genuinely sealed and Published, not damaged proof
		// fragments. Rebuild the operation's exact event/effect census first.
		eventIDs = nil
		effectIDs = nil
		for _, event := range input.Events {
			eventIDs = append(eventIDs, event.EventID)
		}
		for _, effect := range input.EffectIntents {
			effectIDs = append(effectIDs, effect.EffectID)
		}
		sort.Slice(eventIDs, func(i, j int) bool { return eventIDs[i] < eventIDs[j] })
		sort.Slice(effectIDs, func(i, j int) bool { return effectIDs[i] < effectIDs[j] })
		op, err = journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: operationID, LogicalVersion: 1, Status: journalv2.StatusResolved, ResultKind: "workspace.stage.result", ResultSchemaVersion: 1, CanonicalResult: state.CanonicalBytes(), EventIDs: eventIDs, EffectIDs: effectIDs})
		if err != nil {
			t.Fatal(err)
		}
		input.OperationState = op
		payload, err = journalv2.EncodeOperationStateEventPayloadV2(op)
		if err != nil {
			t.Fatal(err)
		}
		digest, _ = journalv2.ReducerStateDigestV2(payload)
		for i := range input.Events {
			if input.Events[i].Family == "operation" {
				input.Events[i].Payload = payload
			}
		}
		for i := range input.Heads {
			if input.Heads[i].Coordinate.Kind == "operation" {
				input.Heads[i].CanonicalStateDigest = digest
				input.Heads[i].AuthoritySummary = payload
			}
		}
		plan, err := journalv2.SealCommitPlanV2(input, journalv2.DefaultEventRegistryV2())
		if err != nil {
			t.Fatal(err)
		}
		ack, err := coordinator.Publish(t.Context(), plan)
		if err != nil {
			t.Fatal(err)
		}
		historical := ack.Plan().OperationState()
		encoded, _ := journalv2.EncodeOperationStateV2(historical)
		item := productionruntime.ProgressItem{Family: "operation", AggregateID: string(operationID), LogicalVersion: 1, LastPublicationID: string(ack.PublicationID()), AuthorityState: string(historical.Status()), CanonicalState: encoded}
		ba, _ := journalv2.NewOperationBindingAddressV2(operationID)
		wa, _ := ack.PublicationID().WitnessAddressV2()
		proof := productionruntime.AcknowledgedPublicationProof{Binding: b, BindingRecord: port.records[ba], Witness: ack.Plan().PublicationWitness(), WitnessRecord: port.records[wa], HistoricalOperationState: historical, CurrentState: historical, CurrentProgressItem: item}
		for _, event := range ack.Plan().Events() {
			address, _ := journalv2.NewJournalEventAddressV2(event.Coordinate(), event.LogicalVersion())
			record := port.records[address]
			proof.JournalRecords = append(proof.JournalRecords, record)
			if event.Family() == "operation" {
				proof.HistoricalOperationRecord = record
			}
		}
		if claim := ack.Plan().WorkspacePathClaim(); claim != nil {
			observed := port.claims[claim.ClaimDigest()]
			proof.WorkspacePathClaim = &observed
			proof.ClaimRecordRevision = port.claimRevisions[claim.ClaimDigest()]
		}
		receipts = append(receipts, terminalPublicationReceipt{Ack: ack, Proof: proof, Source: "typed-command-success"})
		prior = &state
	}
	capture, _ := journalv2.NewStoreRevisionV2("database-coordinate", port.revision, port.revision)
	for i := range receipts {
		receipts[i].Proof.CapturedCoordinate = capture
	}
	history := &terminalPublicationHistory{Publications: receipts, Progress: productionruntime.MissionProgress{DatabaseID: capture.DatabaseID(), ProjectionRevision: capture.CurrentRevision(), ProjectID: "project-fixture", MissionID: "mission-fixture"}, Inspections: make(map[string]productionruntime.ProgressItem), Recovery: make(map[journalv2.OperationID]scenarioRecoveryCommand)}
	history.HistorySHA256 = terminalHistoryDigest(history)
	if _, err := validateTerminalHistory(history); err != nil {
		t.Fatal(err)
	}
	return history, owner
}

func TestTerminalCoordinateFoundationOwnedPair(t *testing.T) {
	left, lo := terminalCoordinateFixture(t, t.TempDir())
	right, ro := terminalCoordinateFixture(t, t.TempDir())
	a, err := newTerminalCoordinateProof(left, lo, "repository-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := newTerminalCoordinateProof(right, ro, "repository-a")
	if err != nil {
		t.Fatal(err)
	}
	if a.historySHA == b.historySHA {
		t.Fatal("independent actual Publish histories must differ")
	}
	pairs, err := pairTerminalWorkspaceBindings(a, b)
	if err != nil || len(pairs) != 1 {
		t.Fatalf("proved different physical roots: %v", err)
	}
	for c, inventory := range a.workspaces {
		if len(inventory.versions) != 2 || len(a.states) != 4 {
			t.Fatal("historical census lost")
		}
		first := a.states[inventory.versions[0]]
		last := a.states[inventory.versions[1]]
		if bytes.Equal(first.canonical, last.canonical) || first.digest == last.digest || first.key.revision != 1 || last.key.revision != 2 {
			t.Fatal("historical state replaced by latest")
		}
		ref := terminalStateReference{a.historySHA, a.capture, integration.AcceptanceEvidenceV2{Disposition: integration.EvidenceSatisfiedV2, Coordinate: c, LogicalVersion: first.key.version, RecordRevision: first.key.revision, StateDigestSHA256: first.digest.String()}}
		resolved, err := a.resolveAcceptanceReference(ref)
		if err != nil || !bytes.Equal(resolved.canonical, first.canonical) {
			t.Fatalf("exact historical evidence: %v", err)
		}
		resolved.canonical[0] = '!'
		if a.states[first.key].canonical[0] == '!' {
			t.Fatal("resolver returned mutable proof alias")
		}
		mutations := map[string]func(*terminalStateReference){
			"wrong_revision":             func(r *terminalStateReference) { r.evidence.RecordRevision++ },
			"unresolved_SHA":             func(r *terminalStateReference) { r.evidence.StateDigestSHA256 = strings.Repeat("f", 64) },
			"latest_SHA_for_old_version": func(r *terminalStateReference) { r.evidence.StateDigestSHA256 = last.digest.String() },
			"foreign_scope": func(r *terminalStateReference) {
				r.evidence.Coordinate.Scope = journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "foreign", MissionID: "mission-fixture"}
			},
			"cross_snapshot": func(r *terminalStateReference) { r.historySHA = b.historySHA; r.capture = b.capture },
		}
		for name, mutate := range mutations {
			t.Run(name, func(t *testing.T) {
				bad := ref
				mutate(&bad)
				if _, err := a.resolveAcceptanceReference(bad); err == nil {
					t.Fatal("unproved reference accepted")
				}
			})
		}
	}
	// Every input graph may be discarded/mutated after construction.
	left.Publications[0].Proof.JournalRecords = nil
	left.Publications[0].Proof.CurrentProgressItem.CanonicalState[0] = '!'
	*left.Publications[0].Proof.WorkspacePathClaim = journalv2.WorkspacePathClaimV2{}
	lo.root = "/changed"
	if _, err := pairTerminalWorkspaceBindings(a, b); err != nil {
		t.Fatal("proof retained input graph alias", err)
	}
}

func TestTerminalCoordinatePublicationCensus(t *testing.T) {
	stripWorkspace := func(input *journalv2.CommitPlanInput) {
		input.Events = input.Events[:1]
		input.Heads = input.Heads[:1]
		input.Conditions = input.Conditions[:1]
		input.EffectIntents = nil
	}
	cases := map[string]func(int, *journalv2.CommitPlanInput){
		"operation_only_declared_claim": func(_ int, input *journalv2.CommitPlanInput) { stripWorkspace(input) },
		"unowned_host_operation":        func(_ int, input *journalv2.CommitPlanInput) { stripWorkspace(input); input.WorkspacePathClaim = nil },
		"foreign_typed_workspace_owner": func(version int, input *journalv2.CommitPlanInput) {
			if version != 1 {
				stripWorkspace(input)
				return
			}
			e, err := workspace.DecodeEventV2(input.Events[1].Payload)
			if err != nil {
				t.Fatal(err)
			}
			b := e.State.Binding
			b.ProjectID = "project-foreign"
			at, _ := time.Parse(time.RFC3339Nano, e.State.OccurredAt)
			c, err := workspace.NewBindingContributionV2(workspace.BindingContributionInputV2{Binding: b, EffectID: e.State.EffectID, EventID: input.Events[1].EventID, OccurredAt: at})
			if err != nil {
				t.Fatal(err)
			}
			stripWorkspace(input)
			input.WorkspacePathClaim = nil
			*input, err = workspace.AppendPlanContributionV2(*input, c)
			if err != nil {
				t.Fatal(err)
			}
		},
		"two_creations_one_claim": func(version int, input *journalv2.CommitPlanInput) {
			if version != 1 {
				return
			}
			e, err := workspace.DecodeEventV2(input.Events[1].Payload)
			if err != nil {
				t.Fatal(err)
			}
			b := e.State.Binding
			b.WorkspaceID = "workspace-b"
			b.CanonicalPath = filepath.Join(b.ManagedRoot, "workspace-b")
			b.PathClaimDigest, err = workspace.WorkspacePathClaimDigestV2(b.RepositoryID, b.ManagedRoot, b.CanonicalPath)
			if err != nil {
				t.Fatal(err)
			}
			at, _ := time.Parse(time.RFC3339Nano, e.State.OccurredAt)
			c, err := workspace.NewBindingContributionV2(workspace.BindingContributionInputV2{Binding: b, EffectID: "effect-create-b", EventID: "event-workspace-create-b", OccurredAt: at})
			if err != nil {
				t.Fatal(err)
			}
			extra, err := workspace.AppendPlanContributionV2(journalv2.CommitPlanInput{}, c)
			if err != nil {
				t.Fatal(err)
			}
			input.Events = append(input.Events, extra.Events...)
			input.Heads = append(input.Heads, extra.Heads...)
			input.Conditions = append(input.Conditions, extra.Conditions...)
			input.EffectIntents = append(input.EffectIntents, extra.EffectIntents...)
		},
	}
	var firstClaim *journalv2.WorkspacePathClaimV2
	cases["continuation_with_declared_claim"] = func(version int, input *journalv2.CommitPlanInput) {
		if version == 1 {
			c := *input.WorkspacePathClaim
			firstClaim = &c
		} else {
			input.WorkspacePathClaim = firstClaim
		}
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h, owner := terminalCoordinateFixture(t, t.TempDir(), mutate)
			if _, err := validateTerminalHistory(h); err != nil {
				t.Fatalf("control is not a genuine generic publication history: %v", err)
			}
			if _, err := newTerminalCoordinateProof(h, owner, "repository-a"); err == nil || !strings.Contains(err.Error(), "UNSUPPORTED") {
				t.Fatalf("unsupported owner/census survived: %v", err)
			}
		})
	}
}

func TestTerminalCoordinateFoundationClosedControls(t *testing.T) {
	mutants := map[string]func(*terminalPublicationHistory, *portableHarness){
		"missing_permanent_claim": func(h *terminalPublicationHistory, _ *portableHarness) {
			h.Publications[0].Proof.WorkspacePathClaim = nil
		},
		"wrong_claim_revision": func(h *terminalPublicationHistory, _ *portableHarness) {
			h.Publications[0].Proof.ClaimRecordRevision = 2
		},
		"wrong_claim_digest": func(h *terminalPublicationHistory, _ *portableHarness) {
			d, _ := workspace.WorkspacePathClaimDigestV2("repository-a", "/different", "/different/workspace-a")
			c, _ := journalv2.NewWorkspacePathClaimV2(d, h.Publications[0].Proof.WorkspacePathClaim.CanonicalValue())
			h.Publications[0].Proof.WorkspacePathClaim = &c
		},
		"wrong_claim_ownership": func(h *terminalPublicationHistory, _ *portableHarness) {
			c, _ := journalv2.NewWorkspacePathClaimV2(h.Publications[0].Proof.WorkspacePathClaim.ClaimDigest(), []byte(`{"workspace_id":"foreign"}`))
			h.Publications[0].Proof.WorkspacePathClaim = &c
		},
		"undeclared_root": func(_ *terminalPublicationHistory, o *portableHarness) { o.root = filepath.Join(o.root, "foreign") },
		"alias_root":      func(_ *terminalPublicationHistory, o *portableHarness) { o.root += "/../alias" },
		"missing_historical_generation": func(h *terminalPublicationHistory, _ *portableHarness) {
			h.Publications = h.Publications[1:]
			h.HistorySHA256 = terminalHistoryDigest(h)
		},
		"cross_capture": func(h *terminalPublicationHistory, _ *portableHarness) {
			c, _ := journalv2.NewStoreRevisionV2("foreign", 2, 2)
			h.Publications[1].Proof.CapturedCoordinate = c
			h.HistorySHA256 = terminalHistoryDigest(h)
		},
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			h, o := terminalCoordinateFixture(t, t.TempDir())
			mutate(h, o)
			if _, err := newTerminalCoordinateProof(h, o, "repository-a"); err == nil {
				t.Fatal("unproved owner accepted")
			}
		})
	}
	t.Run("undeclared_repository", func(t *testing.T) {
		h, o := terminalCoordinateFixture(t, t.TempDir())
		if _, err := newTerminalCoordinateProof(h, o); err == nil {
			t.Fatal("undeclared repository silently omitted")
		}
	})
	t.Run("nonvolatile_binding_facts", func(t *testing.T) {
		h, o := terminalCoordinateFixture(t, t.TempDir())
		a, err := newTerminalCoordinateProof(h, o, ids.RepositoryID("repository-a"))
		if err != nil {
			t.Fatal(err)
		}
		h2, o2 := terminalCoordinateFixture(t, t.TempDir())
		b, err := newTerminalCoordinateProof(h2, o2, "repository-a")
		if err != nil {
			t.Fatal(err)
		}
		for c, w := range b.workspaces {
			w.first.PinnedBase = strings.Repeat("d", 40)
			b.workspaces[c] = w
		}
		if _, err := pairTerminalWorkspaceBindings(a, b); err == nil {
			t.Fatal("raw business Git SHA ignored")
		}
	})
}
