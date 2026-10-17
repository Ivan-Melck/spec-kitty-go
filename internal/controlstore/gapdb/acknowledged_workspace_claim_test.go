package gapdb

import (
	"bytes"
	"context"
	"errors"
	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"reflect"
	"testing"
	"time"
)

func TestAcknowledgedWorkspacePathClaimV2BoundAPI(t *testing.T) {
	method, ok := reflect.TypeOf((*Adapter)(nil)).MethodByName("ObserveAcknowledgedWorkspacePathClaimV2")
	if !ok {
		t.Fatal("missing ACK-bound closed claim observation")
	}
	if method.Type.NumIn() != 4 || method.Type.NumOut() != 3 || method.Type.In(2) != reflect.TypeFor[journalv2.StoreRevisionV2]() || method.Type.In(3) != reflect.TypeFor[journalv2.AcknowledgedPublicationV2]() {
		t.Fatal("claim observation admits caller selection")
	}
}

type claimACKPort struct{}

func (claimACKPort) Commit(_ context.Context, p journalv2.SealedCommitPlanV2) (journalv2.CommitObservationV2, error) {
	return journalv2.NewCommitObservationWithAssertionsV2(p.PublicationID(), p.Digest(), p.RecordSetDigest(), 7, 7, p.MutationCount(), p.AssertionCount())
}
func genuineClaimACK(t *testing.T, withClaim bool) journalv2.AcknowledgedPublicationV2 {
	t.Helper()
	var claim *journalv2.WorkspacePathClaimV2
	if withClaim {
		c, err := journalv2.NewWorkspacePathClaimV2(v2TestDigest("original-physical-path"), []byte(`{"workspace_id":"original-workspace"}`))
		if err != nil {
			t.Fatal(err)
		}
		claim = &c
	}
	// The older adapter codec fixture deliberately uses synthetic head digests;
	// this ACK fixture must instead satisfy the live projection reducer.
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: "operation-claim", CommandKind: "proof.claim", CommandSchemaVersion: 1, CanonicalIntentDigest: v2TestDigest("intent"), Scope: journalv2.Scope{Kind: journalv2.ScopeHost}, ActorID: "actor-claim", ProcessID: "process-claim", OriginatingRuntimeEpochID: "epoch-claim", ObservedCreatedAt: "2026-09-07T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	state, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: binding.OperationID(), LogicalVersion: 1, Status: journalv2.StatusAccepted, EventIDs: []journalv2.EventID{"event-claim"}})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := journalv2.EncodeOperationStateEventPayloadV2(state)
	if err != nil {
		t.Fatal(err)
	}
	coordinate, err := journalv2.NewAggregateCoordinate("operation", "operation-claim", journalv2.Scope{Kind: journalv2.ScopeHost})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := journalv2.ReducerStateDigestV2(payload)
	if err != nil {
		t.Fatal(err)
	}
	head := journalv2.HeadTransitionV2{Coordinate: coordinate, NextLogicalVersion: 1, EventID: "event-claim", CommittedRuntimeEpochID: "epoch-claim", CanonicalStateDigest: digest, AuthoritySummary: payload}
	condition, err := journalv2.ConditionForHeadTransitionV2(head)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := journalv2.SealCommitPlanV2(journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: state, Events: []journalv2.EventDraftV2{{EventID: "event-claim", EventType: "operation.accepted", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: coordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-claim", ActorID: "actor-claim", ProcessID: "process-claim", Payload: payload}}, Heads: []journalv2.HeadTransitionV2{head}, Conditions: []journalv2.ProviderCondition{condition}, WorkspacePathClaim: claim}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	registry := journalv2.DefaultEventRegistryV2()
	var registrations []journalv2.ReducerRegistration
	for _, tuple := range journalv2.ExpectedEventTuples() {
		reduce := journalv2.Reducer(func(_ []byte, e journalv2.EventEnvelopeV2) ([]byte, error) { return e.Payload(), nil })
		if tuple.Family == "operation" {
			reduce = journalv2.ReduceOperationStateV2
		}
		registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: v2TestDigest(tuple.Family), Reduce: reduce})
	}
	reducers, err := journalv2.NewReducerSetV2(registry, registrations)
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(claimACKPort{}, tracker)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := coordinator.Publish(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	return ack
}

func TestAcknowledgedWorkspacePathClaimV2BoundObservation(t *testing.T) {
	for _, fault := range []string{"valid", "missing", "wrong_bytes", "wrong_key", "alias_key", "expiry", "wrong_revision", "future_revision", "zero_revision", "crosscapture", "moved", "zero_ack", "absent_foreign_claim", "canceled", "get_canceled", "oversize"} {
		t.Run(fault, func(t *testing.T) {
			ack := genuineClaimACK(t, fault != "absent_foreign_claim")
			claim := ack.Plan().WorkspacePathClaim()
			foreign := genuineClaimACK(t, true).Plan().WorkspacePathClaim()
			if claim == nil {
				claim = foreign
			}
			key, _ := v2WorkspacePathClaimKey(claim.ClaimDigest())
			providerBytes := claim.CanonicalValue()
			record := gapdbapi.Record{Key: key.String(), Value: providerBytes, Revision: 7}
			captured, _ := journalv2.NewStoreRevisionV2("db-claim", 9, 9)
			status := gapdbapi.StatusResult{DatabaseID: "db-claim", CurrentRevision: 9, DurableThroughRevision: 9}
			gets, statuses := 0, 0
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch fault {
			case "wrong_bytes":
				record.Value = []byte(`{"workspace_id":"other"}`)
			case "wrong_key":
				record.Key = "spk/v2/workspace-path-claims/" + v2TestDigest("foreign").String()
			case "alias_key":
				record.Key += "/"
			case "expiry":
				expiry := time.Now().Add(time.Hour)
				record.ExpiresAt = &expiry
			case "wrong_revision":
				record.Revision = 6
			case "future_revision":
				record.Revision = 10
			case "zero_revision":
				record.Revision = 0
			case "crosscapture":
				status.DatabaseID = "other-database"
			case "zero_ack":
				ack = journalv2.AcknowledgedPublicationV2{}
			case "canceled":
				cancel()
			case "oversize":
				record.Value = bytes.Repeat([]byte("x"), MaxOrdinaryValueBytes+1)
			}
			provider := &fakeProvider{getFn: func(c context.Context, got string) (gapdbapi.Record, error) {
				gets++
				if got != key.String() {
					t.Fatal("not derived exact claim key")
				}
				if _, ok := c.Deadline(); !ok {
					t.Fatal("unbounded request")
				}
				if fault == "missing" {
					return gapdbapi.Record{}, &gapdbapi.Error{Code: gapdbapi.CodeNotFound}
				}
				if fault == "get_canceled" {
					cancel()
					return gapdbapi.Record{}, context.Canceled
				}
				return record, nil
			}, statusFn: func(context.Context) (gapdbapi.StatusResult, error) {
				statuses++
				s := status
				if fault == "moved" && statuses == 2 {
					s.CurrentRevision++
				}
				return s, nil
			}}
			a, _ := newAdapter(provider, "/explicit/claim/socket", time.Second)
			got, revision, err := a.ObserveAcknowledgedWorkspacePathClaimV2(ctx, captured, ack)
			if fault == "valid" {
				if err != nil || revision != 7 || got.ClaimDigest() != claim.ClaimDigest() || !bytes.Equal(got.CanonicalValue(), providerBytes) || gets != 1 || statuses != 2 {
					t.Fatalf("inexact observation: %v", err)
				}
				providerBytes[0] = '!'
				clone := got.CanonicalValue()
				clone[0] = '?'
				if !bytes.Equal(got.CanonicalValue(), ack.Plan().WorkspacePathClaim().CanonicalValue()) {
					t.Fatal("aliased observation")
				}
			} else {
				if err == nil || revision != 0 || !reflect.ValueOf(got).IsZero() {
					t.Fatalf("partial observation: %v", err)
				}
				if (fault == "canceled" || fault == "get_canceled") && !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
				if (fault == "zero_ack" || fault == "absent_foreign_claim" || fault == "canceled" || fault == "crosscapture") && gets != 0 {
					t.Fatal("unauthorized disclosure")
				}
			}
		})
	}
}
