package productionruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/projection"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// Unconditional: this contract runs without a live provider or optional scenario.
// Reflection lets the old Host run and fail rather than hiding behind a build failure.
func TestAcknowledgedPublicationProofHostEntry(t *testing.T) {
	h := &Host{state: HostReady, databaseID: "database-proof", epoch: "epoch-proof"}
	method := reflect.ValueOf(h).MethodByName("VerifyAcknowledgedPublication")
	if !method.IsValid() {
		t.Fatal("Host cannot verify observer-only acknowledged evidence")
	}
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		if canceled {
			cancel()
		}
		out := method.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(journalv2.AcknowledgedPublicationV2{})})
		cancel()
		if len(out) != 2 || !out[0].IsZero() || out[1].IsNil() {
			t.Fatal("invalid ACK disclosed partial proof")
		}
		err := out[1].Interface().(error)
		if canceled && !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
		if !canceled && !IsProgressKind(err, ProgressIncomplete) {
			t.Fatalf("untyped refusal: %v", err)
		}
		if h.state != HostReady || h.epoch != "epoch-proof" {
			t.Fatal("read changed host admission")
		}
	}
}

type publicationProofFixture struct {
	records                  map[journalv2.RecordAddressV2]journalv2.RecoveryRecordV2
	captured                 journalv2.StoreRevisionV2
	commits, reads, verifies int
	verifyErr                error
	readErr                  error
	observerACKs             []journalv2.AcknowledgedPublicationV2
	coordinator              *journalv2.PublicationCoordinatorV2
}

func (f *publicationProofFixture) Commit(ctx context.Context, plan journalv2.SealedCommitPlanV2) (journalv2.CommitObservationV2, error) {
	if err := ctx.Err(); err != nil {
		return journalv2.CommitObservationV2{}, err
	}
	f.commits++
	f.captured, _ = journalv2.NewStoreRevisionV2("database-proof", uint64(f.commits), uint64(f.commits))
	for _, sealed := range plan.Records() {
		var address journalv2.RecordAddressV2
		switch sealed.Role() {
		case journalv2.RecordWorkspacePathClaim:
			continue // Closed claim observations are exercised separately, not recovery addresses.
		case journalv2.RecordOperationBinding:
			address, _ = journalv2.NewOperationBindingAddressV2(sealed.Target().OperationID())
		case journalv2.RecordOperationHead:
			address, _ = journalv2.NewOperationHeadAddressV2(sealed.Target().OperationID())
		case journalv2.RecordJournalEvent:
			address, _ = journalv2.NewJournalEventAddressV2(sealed.Target().Coordinate(), sealed.Target().LogicalVersion())
		case journalv2.RecordPublicationWitness:
			address, _ = sealed.Target().PublicationID().WitnessAddressV2()
		default:
			return journalv2.CommitObservationV2{}, fmt.Errorf("unexpected role %s", sealed.Role())
		}
		record, err := journalv2.NewRecoveryRecordV2(address, sealed.CanonicalValue(), uint64(f.commits), "")
		if err != nil {
			return journalv2.CommitObservationV2{}, err
		}
		f.records[address] = record
	}
	return journalv2.NewCommitObservationWithAssertionsV2(plan.PublicationID(), plan.Digest(), plan.RecordSetDigest(), uint64(f.commits), uint64(f.commits), plan.MutationCount(), plan.AssertionCount())
}
func (f *publicationProofFixture) ObservePublicationBoundary(_ context.Context, snapshot journalv2.PublicationBoundarySnapshotV2) {
	if snapshot.Stage == journalv2.PublicationAckCreatedBeforeProjectionV2 && snapshot.Ack != nil {
		f.observerACKs = append(f.observerACKs, *snapshot.Ack)
	}
}
func (f *publicationProofFixture) ReadV2(ctx context.Context, request journalv2.ReadRequestV2) (journalv2.RecoveryRecordV2, error) {
	f.reads++
	if err := ctx.Err(); err != nil {
		return journalv2.RecoveryRecordV2{}, err
	}
	if f.readErr != nil {
		return journalv2.RecoveryRecordV2{}, f.readErr
	}
	if request.AttemptRevision() != f.captured {
		return journalv2.RecoveryRecordV2{}, errors.New("wrong capture")
	}
	record, ok := f.records[request.Address()]
	if !ok {
		return journalv2.RecoveryRecordV2{}, &journalv2.Error{Kind: journalv2.ErrorNotFound}
	}
	return record, nil
}
func (f *publicationProofFixture) VerifyRevisionV2(ctx context.Context, captured journalv2.StoreRevisionV2) error {
	f.verifies++
	if err := ctx.Err(); err != nil {
		return err
	}
	if captured != f.captured {
		return errors.New("wrong verification capture")
	}
	return f.verifyErr
}
func (f *publicationProofFixture) DatabaseID() journalv2.DatabaseID { return "database-proof" }
func (f *publicationProofFixture) Probe(context.Context) error      { return nil }
func (f *publicationProofFixture) Close(context.Context) error      { return nil }
func (f *publicationProofFixture) verifyAcknowledgedPublication(ctx context.Context, captured journalv2.StoreRevisionV2, ack journalv2.AcknowledgedPublicationV2, origin ...journalv2.AcknowledgedPublicationV2) (AcknowledgedPublicationProof, error) {
	return verifyAcknowledgedPublication(ctx, f, captured, ack, origin...)
}

func newPublicationProofFixture(t *testing.T, continuation bool, scope journalv2.Scope, ids ...string) (*Host, *publicationProofFixture) {
	t.Helper()
	operationID := journalv2.OperationID("operation-proof")
	if len(ids) != 0 {
		operationID = journalv2.OperationID(ids[0])
	}
	registry := journalv2.DefaultEventRegistryV2()
	reducers, err := BuildReducerSetV2(registry)
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := projection.NewAuthorityV2("database-proof", registry, reducers)
	if err != nil {
		t.Fatal(err)
	}
	f := &publicationProofFixture{records: make(map[journalv2.RecordAddressV2]journalv2.RecoveryRecordV2)}
	coordinator, err := journalv2.NewPublicationCoordinatorWithObserverAndSinkV2(f, tracker, f, authority)
	if err != nil {
		t.Fatal(err)
	}
	f.coordinator = coordinator
	intent, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "proof.inspect", CommandSchemaVersion: 1, Scope: scope, Command: []byte(`{"proof":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: operationID, CommandKind: "proof.inspect", CommandSchemaVersion: 1, CanonicalIntentDigest: intent, Scope: scope, ActorID: "actor-proof", ProcessID: "process-proof", OriginatingRuntimeEpochID: "epoch-proof", ObservedCreatedAt: "2026-09-07T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	coordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(operationID), journalv2.Scope{Kind: journalv2.ScopeHost})
	var prior *journalv2.OperationStateV2
	versions := 1
	if continuation {
		versions = 2
	}
	for version := 1; version <= versions; version++ {
		eventID := journalv2.EventID(fmt.Sprintf("event-proof-%d", version))
		eventIDs := []journalv2.EventID{eventID}
		if prior != nil {
			eventIDs = append(prior.EventIDs(), eventID)
		}
		input := journalv2.OperationStateInput{OperationID: binding.OperationID(), LogicalVersion: journalv2.LogicalVersion(version), Status: journalv2.StatusAccepted, EventIDs: eventIDs}
		eventType := "operation.accepted"
		if version == 2 {
			input.Status = journalv2.StatusResolved
			input.ResultKind = "proof.result"
			input.ResultSchemaVersion = 1
			input.CanonicalResult = []byte(`{"verified":true}`)
			eventType = "operation.resolved"
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
		epoch, actor, process := binding.OriginatingEpoch(), binding.ActorID(), binding.ProcessID()
		if version > 1 {
			epoch, actor, process = binding.OriginatingEpoch(), "actor-recovery", "process-recovery"
		}
		head := journalv2.HeadTransitionV2{Coordinate: coordinate, ExpectedLogicalVersion: journalv2.LogicalVersion(version - 1), NextLogicalVersion: journalv2.LogicalVersion(version), EventID: eventID, CommittedRuntimeEpochID: epoch, CanonicalStateDigest: digest, AuthoritySummary: payload}
		if version > 1 {
			head.ExpectedRecordRevision = uint64(version - 1)
		}
		condition, err := journalv2.ConditionForHeadTransitionV2(head)
		if err != nil {
			t.Fatal(err)
		}
		planInput := journalv2.CommitPlanInput{OperationState: state, PriorOperationState: prior, Events: []journalv2.EventDraftV2{{EventID: eventID, EventType: eventType, Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: coordinate, AggregateLogicalVersion: journalv2.LogicalVersion(version), CommittedRuntimeEpochID: epoch, ActorID: actor, ProcessID: process, Payload: payload}}, Heads: []journalv2.HeadTransitionV2{head}, Conditions: []journalv2.ProviderCondition{condition}}
		if version == 1 {
			planInput.OperationBinding = &binding
			if len(ids) > 1 {
				digest, err := journalv2.ReducerStateDigestV2([]byte(`{"path":"/original/workspace"}`))
				if err != nil {
					t.Fatal(err)
				}
				claim, err := journalv2.NewWorkspacePathClaimV2(digest, []byte(`{"workspace_id":"workspace-proof"}`))
				if err != nil {
					t.Fatal(err)
				}
				planInput.WorkspacePathClaim = &claim
			}
		}
		plan, err := journalv2.SealCommitPlanV2(planInput, registry)
		if err != nil {
			t.Fatal(err)
		}
		ack, err := coordinator.Publish(t.Context(), plan)
		if err != nil {
			t.Fatal(err)
		}
		persisted := ack.Plan().OperationState()
		prior = &persisted
	}
	return &Host{state: HostReady, databaseID: f.DatabaseID(), epoch: "epoch-proof", owner: f, projection: authority}, f
}

func TestAcknowledgedPublicationProofHistoricalAndCurrentHead(t *testing.T) {
	for _, continuation := range []bool{false, true} {
		for _, scope := range []journalv2.Scope{{Kind: journalv2.ScopeHost}, {Kind: journalv2.ScopeMission, ProjectID: "project-proof", MissionID: "mission-proof"}} {
			h, f := newPublicationProofFixture(t, continuation, scope)
			ack := f.observerACKs[0]
			proof, err := h.VerifyAcknowledgedPublication(t.Context(), ack)
			if err != nil {
				t.Fatal(err)
			}
			if proof.Binding.Scope() != scope || !journalv2.EqualOperationStatesV2(proof.HistoricalOperationState, ack.Plan().OperationState()) || proof.CapturedCoordinate != f.captured || proof.CurrentState.LogicalVersion() != journalv2.LogicalVersion(f.commits) || proof.HistoricalOperationRecord.Address().Kind() != journalv2.AddressJournalEvent || f.verifies != 1 {
				t.Fatalf("inexact proof: %+v", proof)
			}
			// Returned copies cannot poison another proof or durable records.
			proof.CurrentProgressItem.CanonicalState[0] = '!'
			value := proof.HistoricalOperationState.EventIDs()
			value[0] = "event-mutated"
			valueBytes := proof.BindingRecord.CanonicalValue()
			valueBytes[0] = '!'
			proof.JournalRecords[0] = journalv2.RecoveryRecordV2{}
			again, err := h.VerifyAcknowledgedPublication(t.Context(), ack)
			if err != nil || !bytes.Equal(again.CurrentProgressItem.CanonicalState, mustProofStateBytes(t, again.CurrentState)) || again.JournalRecords[0].RecordRevision() != 1 {
				t.Fatalf("aliased proof: %v", err)
			}
			if f.commits != int(f.captured.CurrentRevision()) || h.state != HostReady {
				t.Fatal("proof wrote or changed admission")
			}
			if scope.Kind == journalv2.ScopeHost {
				if _, err := inspectOperation(t.Context(), f, f.captured, "project-proof", "mission-proof", "operation-proof"); !IsProgressKind(err, ProgressNotFound) {
					t.Fatalf("host proof widened mission inspection: %v", err)
				}
			}
			if continuation {
				continued, err := h.VerifyAcknowledgedPublication(t.Context(), f.observerACKs[1], ack)
				if err != nil || continued.BindingRecord.RecordRevision() != ack.ProviderRevision() || continued.WitnessRecord.RecordRevision() != f.observerACKs[1].ProviderRevision() || !journalv2.EqualOperationStatesV2(continued.HistoricalOperationState, f.observerACKs[1].Plan().OperationState()) || continued.CapturedCoordinate != f.captured {
					t.Fatalf("continuation proof not bound to verified origin: %v", err)
				}
				result, err := h.VerifyAcknowledgedPublication(t.Context(), f.observerACKs[1])
				if !IsProgressKind(err, ProgressIncomplete) || !reflect.ValueOf(result).IsZero() || err.(*ProgressError).Field != "continuation_requires_initial_ack" {
					t.Fatalf("continuation falsely attested immutable intent: %v", err)
				}
			}
		}
	}
}
func mustProofStateBytes(t *testing.T, state journalv2.OperationStateV2) []byte {
	t.Helper()
	value, err := journalv2.EncodeOperationStateV2(state)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAcknowledgedPublicationProofRefusesTampering(t *testing.T) {
	cases := []struct {
		role        journalv2.RecordAddressKindV2
		field       string
		replacement any
	}{
		{journalv2.AddressOperationBinding, "actor_id", "other-actor"}, {journalv2.AddressOperationBinding, "process_id", "other-process"},
		{journalv2.AddressOperationBinding, "originating_runtime_epoch_id", "other-epoch"}, {journalv2.AddressOperationBinding, "scope", map[string]any{"kind": "host"}},
		{journalv2.AddressOperationBinding, "canonical_intent_sha256", repeatByte('a', 64)}, {journalv2.AddressOperationBinding, "command_schema_version", 2},
		{journalv2.AddressPublicationWitness, "plan_sha256", repeatByte('a', 64)}, {journalv2.AddressPublicationWitness, "publication_id", repeatByte('a', 64)},
		{journalv2.AddressJournalEvent, "actor_id", "other-actor"}, {journalv2.AddressJournalEvent, "process_id", "other-process"},
		{journalv2.AddressJournalEvent, "aggregate_logical_version", 2}, {journalv2.AddressJournalEvent, "publication_id", repeatByte('a', 64)},
		{journalv2.AddressJournalEvent, "payload", []byte(`{"wrong":true}`)}, {journalv2.AddressOperationHead, "logical_version", 2},
		{journalv2.AddressOperationHead, "last_publication_id", repeatByte('a', 64)}, {journalv2.AddressOperationHead, "result_kind", "wrong.result"},
	}
	for _, tc := range cases {
		t.Run(string(tc.role)+"/"+tc.field, func(t *testing.T) {
			h, f := newPublicationProofFixture(t, false, journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-proof", MissionID: "mission-proof"})
			for address, record := range f.records {
				if address.Kind() != tc.role {
					continue
				}
				var wire map[string]any
				if err := json.Unmarshal(record.CanonicalValue(), &wire); err != nil {
					t.Fatal(err)
				}
				wire[tc.field] = tc.replacement
				value, _ := json.Marshal(wire)
				value, _ = journalv2.CanonicalObject(value)
				mutated, err := journalv2.NewRecoveryRecordV2(address, value, record.RecordRevision(), "")
				if err != nil {
					t.Fatal(err)
				}
				f.records[address] = mutated
			}
			assertProofRefused(t, h, f.observerACKs[0], ProgressIncomplete)
		})
	}
}
func assertProofRefused(t *testing.T, h *Host, ack journalv2.AcknowledgedPublicationV2, kind ProgressErrorKind) {
	t.Helper()
	proof, err := h.VerifyAcknowledgedPublication(t.Context(), ack)
	if !IsProgressKind(err, kind) || !reflect.ValueOf(proof).IsZero() {
		t.Fatalf("partial or untyped proof: %+v %v", proof, err)
	}
}

func TestAcknowledgedPublicationProofCoordinateAndCancellation(t *testing.T) {
	for _, kind := range []journalv2.RecordAddressKindV2{journalv2.AddressOperationBinding, journalv2.AddressPublicationWitness, journalv2.AddressJournalEvent, journalv2.AddressOperationHead} {
		for _, fault := range []string{"missing", "malformed", "wrong_address", "future_revision", "wrong_revision"} {
			t.Run(string(kind)+"/"+fault, func(t *testing.T) {
				h, f := newPublicationProofFixture(t, false, journalv2.Scope{Kind: journalv2.ScopeHost})
				for address, record := range f.records {
					if address.Kind() != kind {
						continue
					}
					actualAddress, value, revision := address, record.CanonicalValue(), record.RecordRevision()
					switch fault {
					case "missing":
						delete(f.records, address)
						continue
					case "malformed":
						value = []byte(`{}`)
					case "wrong_address":
						actualAddress, _ = journalv2.NewOperationHeadAddressV2("operation-wrong")
					case "future_revision", "wrong_revision":
						revision++
					}
					mutated, err := journalv2.NewRecoveryRecordV2(actualAddress, value, revision, "")
					if err != nil {
						t.Fatal(err)
					}
					f.records[address] = mutated
				}
				assertProofRefused(t, h, f.observerACKs[0], ProgressIncomplete)
			})
		}
	}
	h, f := newPublicationProofFixture(t, false, journalv2.Scope{Kind: journalv2.ScopeHost})
	assertProofRefused(t, h, journalv2.AcknowledgedPublicationV2{}, ProgressIncomplete)
	f.verifyErr = errors.New("provider moved")
	assertProofRefused(t, h, f.observerACKs[0], ProgressResyncRequired)
	f.verifyErr = context.DeadlineExceeded
	if p, err := h.VerifyAcknowledgedPublication(t.Context(), f.observerACKs[0]); !errors.Is(err, context.DeadlineExceeded) || !reflect.ValueOf(p).IsZero() {
		t.Fatalf("deadline lost: %v", err)
	}
	f.verifyErr = nil
	f.readErr = context.Canceled
	if p, err := h.VerifyAcknowledgedPublication(t.Context(), f.observerACKs[0]); !errors.Is(err, context.Canceled) || !reflect.ValueOf(p).IsZero() {
		t.Fatalf("read cancellation lost: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	reads := f.reads
	if _, err := h.VerifyAcknowledgedPublication(ctx, f.observerACKs[0]); !errors.Is(err, context.Canceled) || f.reads != reads {
		t.Fatalf("cancellation read: %v", err)
	}
	h.owner = nil
	assertProofRefused(t, h, f.observerACKs[0], ProgressIncomplete)
}

func TestAcknowledgedPublicationProofUnrelatedCommitsCannotExplainHeadVersionJump(t *testing.T) {
	scope := journalv2.Scope{Kind: journalv2.ScopeHost}
	h, f := newPublicationProofFixture(t, true, scope)
	initial := f.observerACKs[0]
	// Real unrelated canonical publications advance the capture, not this head.
	for index := 0; index < 4; index++ {
		_, other := newPublicationProofFixture(t, false, scope, fmt.Sprintf("operation-unrelated-%d", index))
		if _, err := f.coordinator.Publish(t.Context(), other.observerACKs[0].Plan()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.VerifyAcknowledgedPublication(t.Context(), initial); err != nil {
		t.Fatalf("valid head after unrelated commits refused: %v", err)
	}
	address, _ := journalv2.NewOperationHeadAddressV2(initial.Plan().OperationState().OperationID())
	record := f.records[address]
	var wire map[string]any
	if err := json.Unmarshal(record.CanonicalValue(), &wire); err != nil {
		t.Fatal(err)
	}
	wire["logical_version"] = 4
	value, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	value, err = journalv2.CanonicalObject(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journalv2.DecodeOperationStateV2(value); err != nil {
		t.Fatalf("mutation is not a valid operation DTO: %v", err)
	}
	f.records[address], err = journalv2.NewRecoveryRecordV2(address, value, record.RecordRevision(), "")
	if err != nil {
		t.Fatal(err)
	}
	assertProofRefused(t, h, initial, ProgressIncomplete)
}

func TestAcknowledgedPublicationProofOriginRefusals(t *testing.T) {
	scope := journalv2.Scope{Kind: journalv2.ScopeHost}
	for _, fault := range []string{"missing", "zero", "multiple", "extraneous", "wrong_root", "wrong_binding", "continuation_origin", "binding", "initial_witness", "initial_event", "continuation_witness", "continuation_event"} {
		t.Run(fault, func(t *testing.T) {
			h, f := newPublicationProofFixture(t, true, scope)
			ack, root := f.observerACKs[1], f.observerACKs[0]
			origins := []journalv2.AcknowledgedPublicationV2{root}
			switch fault {
			case "missing":
				origins = nil
			case "zero":
				origins[0] = journalv2.AcknowledgedPublicationV2{}
			case "multiple":
				origins = append(origins, root)
			case "extraneous":
				ack = root
			case "wrong_root":
				_, other := newPublicationProofFixture(t, false, scope, "operation-other")
				origins[0] = other.observerACKs[0]
			case "wrong_binding":
				_, other := newPublicationProofFixture(t, false, journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-other", MissionID: "mission-other"})
				origins[0] = other.observerACKs[0]
			case "continuation_origin":
				origins[0] = ack
			default:
				for address, record := range f.records {
					match := fault == "binding" && address.Kind() == journalv2.AddressOperationBinding ||
						fault == "initial_witness" && address.Kind() == journalv2.AddressPublicationWitness && record.RecordRevision() == root.ProviderRevision() ||
						fault == "initial_event" && address.Kind() == journalv2.AddressJournalEvent && record.RecordRevision() == root.ProviderRevision() ||
						fault == "continuation_witness" && address.Kind() == journalv2.AddressPublicationWitness && record.RecordRevision() == ack.ProviderRevision() ||
						fault == "continuation_event" && address.Kind() == journalv2.AddressJournalEvent && record.RecordRevision() == ack.ProviderRevision()
					if match {
						corrupted, err := journalv2.NewRecoveryRecordV2(address, []byte(`{}`), record.RecordRevision(), "")
						if err != nil {
							t.Fatal(err)
						}
						f.records[address] = corrupted
					}
				}
			}
			proof, err := h.VerifyAcknowledgedPublication(t.Context(), ack, origins...)
			if !IsProgressKind(err, ProgressIncomplete) || !reflect.ValueOf(proof).IsZero() {
				t.Fatalf("origin substitution served proof: %v", err)
			}
		})
	}
}
