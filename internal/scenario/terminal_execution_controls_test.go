package scenario

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
)

func TestTerminalExecutionFoundationServiceHistory(t *testing.T) {
	for _, rebind := range []bool{false, true} {
		t.Run(fmt.Sprintf("rebind_%t", rebind), func(t *testing.T) {
			h := terminalRecoveryFixture(t, rebind).History
			p, err := newTerminalCoordinateProof(h, &portableHarness{root: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			var first, last terminalHistoricalState
			count := 0
			for _, s := range p.states {
				if s.envelope.Family() != "execution_slot" {
					continue
				}
				count++
				if s.key.version == 1 {
					first = s
				}
				if s.key.version > last.key.version {
					last = s
				}
			}
			want := 1
			if rebind {
				want = 2
				// This ACK came from Execute, not a synthetic payload-summary plan.
				service := h.Publications[len(h.Publications)-1].Ack.Plan()
				found := false
				for _, head := range service.Heads() {
					if head.Coordinate.Kind == "operation" {
						found = bytes.Equal(head.AuthoritySummary, []byte(`{"status":"resolved"}`))
					}
				}
				if !found || first.key.revision != 2 || last.key.revision != 3 || first.digest == last.digest || bytes.Equal(first.canonical, last.canonical) {
					t.Fatal("service compact summary or old/new state preimages lost")
				}
			}
			if count != want || len(p.states) != len(h.Publications)+want || len(p.workspaces) != 0 {
				t.Fatal("complete operation/execution census lost or invented workspace owner")
			}
			ref := terminalStateReference{p.historySHA, p.capture, integration.AcceptanceEvidenceV2{Disposition: integration.EvidenceSatisfiedV2, Coordinate: first.key.coordinate, LogicalVersion: first.key.version, RecordRevision: first.key.revision, StateDigestSHA256: first.digest.String()}}
			resolved, err := p.resolveAcceptanceReference(ref)
			if err != nil || !bytes.Equal(resolved.canonical, first.canonical) {
				t.Fatalf("exact old execution acceptance reference: %v", err)
			}
			resolved.canonical[0] = '!'
			if p.states[first.key].canonical[0] == '!' {
				t.Fatal("reference aliases historical preimage")
			}
			if rebind {
				latest := ref
				latest.evidence.LogicalVersion = last.key.version
				latest.evidence.RecordRevision = last.key.revision
				latest.evidence.StateDigestSHA256 = last.digest.String()
				if _, err := p.resolveAcceptanceReference(latest); err != nil {
					t.Fatal("exact new execution reference", err)
				}
				for name, mutate := range map[string]func(*terminalStateReference){
					"latest_digest_for_old":   func(r *terminalStateReference) { r.evidence.StateDigestSHA256 = last.digest.String() },
					"latest_revision_for_old": func(r *terminalStateReference) { r.evidence.RecordRevision = last.key.revision },
					"foreign_mission":         func(r *terminalStateReference) { r.evidence.Coordinate.Scope.MissionID = "mission-foreign" },
					"cross_snapshot":          func(r *terminalStateReference) { r.historySHA = strings.Repeat("f", 64) },
				} {
					t.Run(name, func(t *testing.T) {
						bad := ref
						mutate(&bad)
						if _, err := p.resolveAcceptanceReference(bad); err == nil {
							t.Fatal("latest/foreign reference substituted for historical state")
						}
					})
				}
			}
		})
	}
}

// Re-seal actual seed/service plans and publish through the real typed tracker.
// No cached proof fields, opaque ACK internals, or historical evidence are
// patched. Invalid tuples/transitions must fail at sealing/publication; valid
// publications with insufficient ownership must fail at the private proof.
func terminalExecutionControlHistory(t *testing.T, mutate func(int, *journalv2.CommitPlanInput), finalMutations ...func(int, *journalv2.CommitPlanInput)) (*terminalPublicationHistory, string, error) {
	t.Helper()
	template := terminalRecoveryFixture(t, true).History
	reducers, err := productionruntime.BuildReducerSetV2(journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	port := &terminalFixturePort{records: make(map[journalv2.RecordAddressV2]journalv2.RecoveryRecordV2)}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(port, tracker)
	if err != nil {
		t.Fatal(err)
	}
	h := &terminalPublicationHistory{Progress: productionruntime.MissionProgress{DatabaseID: "database-fixture", ProjectID: "project-fixture", MissionID: "mission-fixture"}, Inspections: make(map[string]productionruntime.ProgressItem)}
	for index, original := range template.Publications {
		plan := original.Ack.Plan()
		b := *plan.BindingCreate()
		s := plan.OperationState()
		input := journalv2.CommitPlanInput{OperationBinding: &b, Heads: plan.Heads()}
		for _, e := range plan.Events() {
			input.Events = append(input.Events, journalv2.EventDraftV2{EventID: e.EventID(), EventType: e.EventType(), Family: e.Family(), EventSchemaVersion: e.EventSchemaVersion(), ReducerVersion: e.ReducerVersion(), Coordinate: e.Coordinate(), AggregateLogicalVersion: e.LogicalVersion(), CommittedRuntimeEpochID: e.CommittedRuntimeEpochID(), ActorID: e.ActorID(), ProcessID: e.ProcessID(), WorkRevisionID: e.WorkRevisionID(), PolicyRevision: e.PolicyRevision(), GovernanceRevisions: e.GovernanceRevisions(), Observations: e.Observations(), Payload: e.Payload()})
		}
		// Remember which summary uses the exact payload preimage before edits.
		payloadSummary := make(map[journalv2.EventID]bool)
		for _, head := range input.Heads {
			for _, e := range input.Events {
				if head.EventID == e.EventID && e.Family == "operation" {
					payloadSummary[e.EventID] = bytes.Equal(head.AuthoritySummary, e.Payload)
				}
			}
		}
		if mutate != nil {
			mutate(index, &input)
		}
		result := s.Result()
		for _, e := range input.Events {
			if e.Family == "execution_slot" {
				event, err := execution.DecodeExecutionSlotEventV2(e.Payload)
				if err != nil {
					t.Fatal("control must retain a schema-valid payload", err)
				}
				result = event.State.CanonicalBytes()
			}
		}
		op, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: s.OperationID(), LogicalVersion: s.LogicalVersion(), Status: s.Status(), ResultKind: s.ResultKind(), ResultSchemaVersion: s.ResultSchemaVersion(), CanonicalResult: result, EventIDs: s.EventIDs(), EffectIDs: s.EffectIDs()})
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
			if e.Family == "operation" {
				e.Payload = payload
			}
			for j := range input.Heads {
				head := &input.Heads[j]
				if head.EventID != e.EventID {
					continue
				}
				preimage := e.Payload
				if e.Family == "execution_slot" {
					preimage = result
					head.AuthoritySummary = result
				} else if payloadSummary[e.EventID] {
					// Only retain payload-summary form if the mutation did not
					// deliberately replace it with a different summary.
					for _, old := range plan.Heads() {
						if old.EventID == e.EventID && bytes.Equal(old.AuthoritySummary, head.AuthoritySummary) {
							head.AuthoritySummary = payload
						}
					}
				}
				head.CanonicalStateDigest, _ = journalv2.ReducerStateDigestV2(preimage)
			}
		}
		for _, mutateFinal := range finalMutations {
			mutateFinal(index, &input)
		}
		for _, head := range input.Heads {
			condition, err := journalv2.ConditionForHeadTransitionV2(head)
			if err != nil {
				t.Fatal(err)
			}
			input.Conditions = append(input.Conditions, condition)
		}
		sealed, err := journalv2.SealCommitPlanV2(input, journalv2.DefaultEventRegistryV2())
		if err != nil {
			return nil, "seal", err
		}
		ack, err := coordinator.Publish(t.Context(), sealed)
		if err != nil {
			return nil, "publish", err
		}
		state := ack.Plan().OperationState()
		encoded, _ := journalv2.EncodeOperationStateV2(state)
		item := productionruntime.ProgressItem{Family: "operation", AggregateID: string(state.OperationID()), LogicalVersion: uint64(state.LogicalVersion()), LastPublicationID: string(state.LastPublicationID()), AuthorityState: string(state.Status()), CanonicalState: encoded}
		ba, _ := journalv2.NewOperationBindingAddressV2(state.OperationID())
		wa, _ := ack.PublicationID().WitnessAddressV2()
		proof := productionruntime.AcknowledgedPublicationProof{Binding: *input.OperationBinding, BindingRecord: port.records[ba], Witness: ack.Plan().PublicationWitness(), WitnessRecord: port.records[wa], HistoricalOperationState: state, CurrentState: state, CurrentProgressItem: item}
		for _, e := range ack.Plan().Events() {
			a, _ := journalv2.NewJournalEventAddressV2(e.Coordinate(), e.LogicalVersion())
			r := port.records[a]
			proof.JournalRecords = append(proof.JournalRecords, r)
			if e.Family() == "operation" {
				proof.HistoricalOperationRecord = r
			}
		}
		h.Publications = append(h.Publications, terminalPublicationReceipt{Ack: ack, Proof: proof, Source: "typed-command-success"})
		if input.OperationBinding.Scope().Kind != journalv2.ScopeHost {
			h.Progress.Operations = append(h.Progress.Operations, item)
			h.Progress.Items = append(h.Progress.Items, item)
			h.Inspections[item.AggregateID] = item
		}
	}
	capture, _ := journalv2.NewStoreRevisionV2(h.Progress.DatabaseID, port.revision, port.revision)
	h.Progress.ProjectionRevision = port.revision
	for i := range h.Publications {
		h.Publications[i].Proof.CapturedCoordinate = capture
	}
	h.HistorySHA256 = terminalHistoryDigest(h)
	if _, err := validateTerminalHistory(h); err != nil {
		t.Fatal("published control must pass generic history validity", err)
	}
	return h, "published", nil
}

func terminalExecutionControlEvent(t *testing.T, input *journalv2.CommitPlanInput, change func(*execution.ExecutionSlotEventV2, *journalv2.EventDraftV2)) {
	t.Helper()
	for i := range input.Events {
		e := &input.Events[i]
		if e.Family != "execution_slot" {
			continue
		}
		payload, err := execution.DecodeExecutionSlotEventV2(e.Payload)
		if err != nil {
			t.Fatal(err)
		}
		change(&payload, e)
		e.Payload, err = journalv2.CanonicalObject(mustTerminalJSON(payload))
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestTerminalExecutionFoundationPublishedOwnerControls(t *testing.T) {
	cases := map[string]func(int, *journalv2.CommitPlanInput){
		"foreign_project": func(index int, input *journalv2.CommitPlanInput) {
			if index == 0 {
				return
			}
			terminalExecutionControlEvent(t, input, func(p *execution.ExecutionSlotEventV2, e *journalv2.EventDraftV2) {
				p.State.ProjectID = "project-foreign"
				e.Coordinate, _ = execution.ExecutionSlotCoordinateV2(p.State.ProjectID, p.State.MissionID, p.State.WorkRevisionID, p.State.WorkID)
			})
			for i := range input.Heads {
				if input.Heads[i].Coordinate.Kind == "execution_slot" {
					input.Heads[i].Coordinate.Scope.ProjectID = "project-foreign"
				}
			}
		},
		"foreign_mission": func(index int, input *journalv2.CommitPlanInput) {
			if index == 0 {
				return
			}
			terminalExecutionControlEvent(t, input, func(p *execution.ExecutionSlotEventV2, e *journalv2.EventDraftV2) {
				p.State.MissionID = "mission-foreign"
				e.Coordinate, _ = execution.ExecutionSlotCoordinateV2(p.State.ProjectID, p.State.MissionID, p.State.WorkRevisionID, p.State.WorkID)
			})
			for i := range input.Heads {
				if input.Heads[i].Coordinate.Kind == "execution_slot" {
					input.Heads[i].Coordinate.Scope.MissionID = "mission-foreign"
				}
			}
		},
		"host_binding_execution_only": func(index int, input *journalv2.CommitPlanInput) {
			if index != 1 {
				return
			}
			b := input.OperationBinding
			binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: b.OperationID(), CommandKind: b.CommandKind(), CommandSchemaVersion: b.CommandSchemaVersion(), CanonicalIntentDigest: b.IntentDigest(), Scope: journalv2.Scope{Kind: journalv2.ScopeHost}, ActorID: b.ActorID(), ProcessID: b.ProcessID(), OriginatingRuntimeEpochID: b.OriginatingEpoch(), ObservedCreatedAt: b.ObservedCreatedAt()})
			if err != nil {
				t.Fatal(err)
			}
			input.OperationBinding = &binding
		},
		"workspace_bound": func(index int, input *journalv2.CommitPlanInput) {
			if index != 2 {
				return
			}
			terminalExecutionControlEvent(t, input, func(p *execution.ExecutionSlotEventV2, e *journalv2.EventDraftV2) {
				p.Kind = "workspace_bound"
				p.State.RuntimeEpochID = "epoch-old"
				p.State.WorkspaceID = "workspace-a"
				p.State.WorkspacePathClaimDigest, _ = journalv2.ReducerStateDigestV2([]byte(`{"claim":"unproved"}`))
				e.EventType = "execution_slot.workspace_bound"
				e.CommittedRuntimeEpochID = p.State.RuntimeEpochID
			})
			for i := range input.Heads {
				if input.Heads[i].Coordinate.Kind == "execution_slot" {
					input.Heads[i].CommittedRuntimeEpochID = "epoch-old"
				}
			}
		},
	}
	for _, summary := range []string{`{"business":"extra","status":"resolved"}`, `{"business":"unsupported"}`, `{"status":"accepted"}`} {
		cases["operation_summary_"+summary] = func(index int, input *journalv2.CommitPlanInput) {
			if index != 2 {
				return
			}
			for i := range input.Heads {
				if input.Heads[i].Coordinate.Kind == "operation" {
					input.Heads[i].AuthoritySummary = []byte(summary)
				}
			}
		}
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h, stage, err := terminalExecutionControlHistory(t, mutate)
			if err != nil || stage != "published" {
				t.Fatalf("owner control must be genuinely published, stage=%s: %v", stage, err)
			}
			_, err = newTerminalCoordinateProof(h, &portableHarness{root: t.TempDir()})
			if err == nil {
				t.Fatal("unproved execution owner/summary accepted")
			}
			if strings.HasPrefix(name, "operation_summary_") {
				if !strings.Contains(err.Error(), "operation head domain") {
					t.Fatalf("summary control failed at unrelated boundary: %v", err)
				}
			} else if !strings.Contains(err.Error(), "UNSUPPORTED") {
				t.Fatalf("owner control failed at unrelated boundary: %v", err)
			}
			if name == "workspace_bound" && (!strings.Contains(err.Error(), "UNSUPPORTED") || !strings.Contains(err.Error(), "workspace-bound execution")) {
				t.Fatalf("bound execution must explicitly fail unsupported: %v", err)
			}
		})
	}
}

func TestTerminalExecutionFoundationPublisherControls(t *testing.T) {
	cases := map[string]struct {
		stage  string
		change func(*execution.ExecutionSlotEventV2, *journalv2.EventDraftV2)
	}{
		"unsupported_schema":  {"seal", func(_ *execution.ExecutionSlotEventV2, e *journalv2.EventDraftV2) { e.EventSchemaVersion = 2 }},
		"unsupported_reducer": {"seal", func(_ *execution.ExecutionSlotEventV2, e *journalv2.EventDraftV2) { e.ReducerVersion = 2 }},
		"broken_coordinate":   {"publish", func(_ *execution.ExecutionSlotEventV2, e *journalv2.EventDraftV2) { e.Coordinate.ID = "slot-unproved" }},
		"cross_scope_coordinate": {"publish", func(_ *execution.ExecutionSlotEventV2, e *journalv2.EventDraftV2) {
			e.Coordinate.Scope.MissionID = "mission-foreign"
		}},
		"missing_predecessor": {"publish", func(p *execution.ExecutionSlotEventV2, _ *journalv2.EventDraftV2) { p.Predecessor = nil }},
		"wrong_predecessor": {"publish", func(p *execution.ExecutionSlotEventV2, _ *journalv2.EventDraftV2) {
			p.Predecessor.Fence = strings.Repeat("b", 32)
		}},
		"actor":   {"publish", func(_ *execution.ExecutionSlotEventV2, e *journalv2.EventDraftV2) { e.ActorID = "actor-foreign" }},
		"process": {"publish", func(_ *execution.ExecutionSlotEventV2, e *journalv2.EventDraftV2) { e.ProcessID = "process-foreign" }},
		"epoch": {"publish", func(_ *execution.ExecutionSlotEventV2, e *journalv2.EventDraftV2) {
			e.CommittedRuntimeEpochID = "epoch-unproved"
		}},
		"work_revision": {"publish", func(_ *execution.ExecutionSlotEventV2, e *journalv2.EventDraftV2) {
			e.WorkRevisionID = "revision-foreign"
		}},
		"policy": {"publish", func(_ *execution.ExecutionSlotEventV2, e *journalv2.EventDraftV2) {
			e.PolicyRevision = "policy-foreign"
		}},
		"governance": {"publish", func(_ *execution.ExecutionSlotEventV2, e *journalv2.EventDraftV2) {
			e.GovernanceRevisions = []string{"config.foreign"}
		}},
	}
	for name, control := range cases {
		t.Run(name, func(t *testing.T) {
			_, stage, err := terminalExecutionControlHistory(t, func(index int, input *journalv2.CommitPlanInput) {
				if index != 2 {
					return
				}
				terminalExecutionControlEvent(t, input, control.change)
				for _, e := range input.Events {
					if e.Family != "execution_slot" {
						continue
					}
					for i := range input.Heads {
						if input.Heads[i].EventID == e.EventID {
							input.Heads[i].Coordinate = e.Coordinate
							input.Heads[i].CommittedRuntimeEpochID = e.CommittedRuntimeEpochID
						}
					}
				}
			})
			if err == nil || stage != control.stage {
				t.Fatalf("invalid tuple/typed authority survived expected %s boundary: stage=%s err=%v", control.stage, stage, err)
			}
		})
	}
}

func TestTerminalExecutionFoundationHeadDomains(t *testing.T) {
	h, stage, err := terminalExecutionControlHistory(t, nil)
	if err != nil || stage != "published" {
		t.Fatalf("unmodified re-sealed control history: %s %v", stage, err)
	}
	if _, err := newTerminalCoordinateProof(h, &portableHarness{root: t.TempDir()}); err != nil {
		t.Fatal("unmodified control must qualify", err)
	}
	for _, format := range []string{"payload", "compact"} {
		t.Run("wrong_operation_digest_"+format, func(t *testing.T) {
			h, stage, err := terminalExecutionControlHistory(t, nil, func(index int, input *journalv2.CommitPlanInput) {
				if index != 2 {
					return
				}
				for i := range input.Heads {
					head := &input.Heads[i]
					if head.Coordinate.Kind != "operation" {
						continue
					}
					if format == "payload" {
						for _, e := range input.Events {
							if e.EventID == head.EventID {
								head.AuthoritySummary = e.Payload
							}
						}
					}
					// A valid summary never changes the operation's payload
					// digest domain into summary or reconstructed-state bytes.
					head.CanonicalStateDigest, _ = journalv2.ReducerStateDigestV2([]byte(`{"status":"resolved"}`))
				}
			})
			if err != nil || stage != "published" {
				t.Fatalf("digest control must be genuinely published: %s %v", stage, err)
			}
			if _, err := newTerminalCoordinateProof(h, &portableHarness{root: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "operation head domain") {
				t.Fatalf("summary admitted without independent payload digest: %v", err)
			}
		})
	}
	t.Run("nonexact_execution_summary", func(t *testing.T) {
		h, stage, err := terminalExecutionControlHistory(t, nil, func(index int, input *journalv2.CommitPlanInput) {
			if index != 2 {
				return
			}
			for i := range input.Heads {
				if input.Heads[i].Coordinate.Kind == "execution_slot" {
					input.Heads[i].AuthoritySummary = []byte(`{"status":"active"}`)
				}
			}
		})
		if err != nil || stage != "published" {
			t.Fatalf("summary control must be genuinely published: %s %v", stage, err)
		}
		if _, err := newTerminalCoordinateProof(h, &portableHarness{root: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "unsupported/nonexact head digest domain execution_slot") {
			t.Fatalf("nonexact typed execution summary admitted: %v", err)
		}
	})
}

func TestTerminalExecutionFoundationMissingFirstVersion(t *testing.T) {
	h := terminalRecoveryFixture(t, true).History
	// Preserve the actual service ACK and all its historical bytes. Remove only
	// the seed receipt and its operation census, then re-commit the capture.
	seedID := string(h.Publications[1].Ack.Plan().OperationState().OperationID())
	h.Publications = append(h.Publications[:1:1], h.Publications[2:]...)
	filter := func(items []productionruntime.ProgressItem) []productionruntime.ProgressItem {
		var kept []productionruntime.ProgressItem
		for _, item := range items {
			if item.AggregateID != seedID {
				kept = append(kept, item)
			}
		}
		return kept
	}
	h.Progress.Operations = filter(h.Progress.Operations)
	h.Progress.Items = filter(h.Progress.Items)
	delete(h.Inspections, seedID)
	h.HistorySHA256 = terminalHistoryDigest(h)
	if _, err := validateTerminalHistory(h); err != nil {
		t.Fatal("opaque service ACK must remain generically valid", err)
	}
	if _, err := newTerminalCoordinateProof(h, &portableHarness{root: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "noncontiguous") {
		t.Fatalf("missing execution version 1 accepted: %v", err)
	}
}
