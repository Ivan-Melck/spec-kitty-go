package productionruntime

import (
	"context"
	"errors"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type operationObservationProbe struct {
	binding, state journalv2.RecoveryRecordV2
	captured       journalv2.StoreRevisionV2
	verifyErr      error
}

func (p operationObservationProbe) ReadV2(ctx context.Context, request journalv2.ReadRequestV2) (journalv2.RecoveryRecordV2, error) {
	if err := ctx.Err(); err != nil {
		return journalv2.RecoveryRecordV2{}, err
	}
	if request.AttemptRevision() != p.captured {
		return journalv2.RecoveryRecordV2{}, errors.New("wrong capture")
	}
	if request.Address().Kind() == journalv2.AddressOperationBinding {
		return p.binding, nil
	}
	return p.state, nil
}

func (p operationObservationProbe) VerifyRevisionV2(_ context.Context, capture journalv2.StoreRevisionV2) error {
	if capture != p.captured {
		return errors.New("wrong capture")
	}
	return p.verifyErr
}

func operationInspectionFixture(t *testing.T) operationObservationProbe {
	t.Helper()
	digest, err := journalv2.ReducerStateDigestV2([]byte(`{"intent":"inspect"}`))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{
		OperationID: "operation-inspect", CommandKind: "mission.create", CommandSchemaVersion: 1,
		CanonicalIntentDigest: digest, Scope: journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-inspect", MissionID: "mission-inspect"},
		OriginatingRuntimeEpochID: "epoch-inspect", ObservedCreatedAt: "2026-09-05T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: binding.OperationID(), LogicalVersion: 1, Status: journalv2.StatusAccepted, LastPublicationID: journalv2.PublicationID(repeatByte('b', 64))})
	if err != nil {
		t.Fatal(err)
	}
	bindingValue, err := journalv2.EncodeOperationBindingV2(binding)
	if err != nil {
		t.Fatal(err)
	}
	stateValue, err := journalv2.EncodeOperationStateV2(state)
	if err != nil {
		t.Fatal(err)
	}
	bindingAddress, _ := journalv2.NewOperationBindingAddressV2(binding.OperationID())
	stateAddress, _ := journalv2.NewOperationHeadAddressV2(binding.OperationID())
	bindingRecord, err := journalv2.NewRecoveryRecordV2(bindingAddress, bindingValue, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	stateRecord, err := journalv2.NewRecoveryRecordV2(stateAddress, stateValue, 3, "")
	if err != nil {
		t.Fatal(err)
	}
	captured, _ := journalv2.NewStoreRevisionV2("database-inspect", 3, 3)
	return operationObservationProbe{binding: bindingRecord, state: stateRecord, captured: captured}
}

func TestOperationInspectionUsesBindingMembershipAndStableCapture(t *testing.T) {
	p := operationInspectionFixture(t)
	item, err := inspectOperation(t.Context(), p, p.captured, "project-inspect", "mission-inspect", "operation-inspect")
	if err != nil || item.AggregateID != "operation-inspect" || item.AuthorityState != "accepted" || item.Family != "operation" || item.LogicalVersion != 1 {
		t.Fatalf("bound operation not observable: item=%+v err=%v", item, err)
	}
	for _, scope := range []struct{ project, mission string }{{"project-other", "mission-inspect"}, {"project-inspect", "mission-other"}} {
		item, err := inspectOperation(t.Context(), p, p.captured, journalv2.ProjectID(scope.project), journalv2.MissionID(scope.mission), "operation-inspect")
		if !IsProgressKind(err, ProgressNotFound) || item.AggregateID != "" {
			t.Fatalf("operation disclosed outside immutable binding: item=%+v err=%v", item, err)
		}
	}
	p.verifyErr = errors.New("provider advanced")
	if item, err := inspectOperation(t.Context(), p, p.captured, "project-inspect", "mission-inspect", "operation-inspect"); !IsProgressKind(err, ProgressResyncRequired) || item.AggregateID != "" {
		t.Fatalf("unstable capture served an operation: item=%+v err=%v", item, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := inspectOperation(ctx, p, p.captured, "project-inspect", "mission-inspect", "operation-inspect"); !errors.Is(err, context.Canceled) {
		t.Fatalf("query cancellation lost: %v", err)
	}
}

func TestOperationInspectionRefusesMisattributedRecoveryRecords(t *testing.T) {
	for _, change := range []string{"binding_address", "state_address", "binding_revision", "state_revision"} {
		t.Run(change, func(t *testing.T) {
			p := operationInspectionFixture(t)
			target := &p.binding
			if change == "state_address" || change == "state_revision" {
				target = &p.state
			}
			address, revision := target.Address(), target.RecordRevision()
			switch change {
			case "binding_address":
				address, _ = journalv2.NewOperationBindingAddressV2("operation-other")
			case "state_address":
				address, _ = journalv2.NewOperationHeadAddressV2("operation-other")
			default:
				revision = p.captured.CurrentRevision() + 1
			}
			var err error
			*target, err = journalv2.NewRecoveryRecordV2(address, target.CanonicalValue(), revision, "")
			if err != nil {
				t.Fatal(err)
			}
			if item, err := inspectOperation(t.Context(), p, p.captured, "project-inspect", "mission-inspect", "operation-inspect"); !IsProgressKind(err, ProgressIncomplete) || item.AggregateID != "" {
				t.Fatalf("misattributed recovery record was served: item=%+v err=%v", item, err)
			}
		})
	}
}
