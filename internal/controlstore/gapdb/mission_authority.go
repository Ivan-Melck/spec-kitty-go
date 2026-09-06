package gapdb

import (
	"context"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

type missionAuthorityJournalV2 interface {
	restore.ReaderV2
	ReconcileCommitV2(context.Context, journalv2.SealedCommitPlanV2) (journalv2.CommitObservationV2, error)
}

type ReadinessObservationPortV2 interface {
	ObserveReadinessAuthority(context.Context, journalv2.ProjectID, journalv2.MissionID) (mission.ReadinessAuthorityObservationV2, error)
}

type readinessObservationAdapterV2 struct{ reader restore.ReaderV2 }

func NewReadinessObservationPortV2(reader restore.ReaderV2) (ReadinessObservationPortV2, error) {
	if reader == nil {
		return nil, adapterError("readiness_observer_new", "", OutcomeInvalid, RecoveryFixRequest, "reader is required", nil)
	}
	return &readinessObservationAdapterV2{reader: reader}, nil
}

func (a *readinessObservationAdapterV2) ObserveReadinessAuthority(ctx context.Context, projectID journalv2.ProjectID, missionID journalv2.MissionID) (mission.ReadinessAuthorityObservationV2, error) {
	return observeReadinessAuthorityV2(ctx, a.reader, projectID, missionID)
}

// missionAuthorityAdapterV2 implements only the mission-owned semantic port.
// Provider keys, revisions, and ambiguity mechanics remain inside this
// adapter; mission policy remains in internal/mission.
type missionAuthorityAdapterV2 struct {
	store     missionAuthorityJournalV2
	publisher *journalv2.PublicationCoordinatorV2
}

var _ mission.MissionAuthorityPortV2 = (*missionAuthorityAdapterV2)(nil)
var _ mission.ReadinessAuthorityPortV2 = (*missionAuthorityAdapterV2)(nil)

// NewMissionAuthorityPortV2 assembles the provider adapter without exposing a
// GapDB implementation type in the mission/foundation contract. The supplied
// reader must also implement the foundation reconciliation operation.
func NewMissionAuthorityPortV2(reader restore.ReaderV2, publisher *journalv2.PublicationCoordinatorV2) (mission.MissionAuthorityPortV2, error) {
	store, ok := reader.(missionAuthorityJournalV2)
	if reader == nil || !ok || publisher == nil {
		return nil, adapterError("mission_authority_new", "", OutcomeInvalid, RecoveryFixRequest, "store and publication coordinator are required", nil)
	}
	return &missionAuthorityAdapterV2{store: store, publisher: publisher}, nil
}

func (a *missionAuthorityAdapterV2) ResolveOperation(ctx context.Context, candidate journalv2.OperationBindingV2) (mission.OperationResolutionV2, error) {
	attempt, err := a.store.CaptureRevisionV2(ctx)
	if err != nil {
		return mission.OperationResolutionV2{}, err
	}
	address, err := journalv2.NewOperationBindingAddressV2(candidate.OperationID())
	if err != nil {
		return mission.OperationResolutionV2{}, adapterError("mission_operation", "", OutcomeInvalid, RecoveryFixRequest, "operation binding address is invalid", err)
	}
	request, _ := journalv2.NewReadRequestV2(address, attempt)
	record, err := a.store.ReadV2(ctx, request)
	if err != nil {
		if journalv2.IsKind(err, journalv2.ErrorNotFound) {
			if verifyErr := a.store.VerifyRevisionV2(ctx, attempt); verifyErr != nil {
				return mission.OperationResolutionV2{}, verifyErr
			}
			return mission.OperationResolutionV2{Kind: mission.OperationAbsentV2, Binding: candidate}, nil
		}
		return mission.OperationResolutionV2{}, err
	}
	resolution, resolveErr := journalv2.ResolveOperationBinding(candidate, record.CanonicalValue(), true)
	if resolution == journalv2.BindingConflict {
		if verifyErr := a.store.VerifyRevisionV2(ctx, attempt); verifyErr != nil {
			return mission.OperationResolutionV2{}, verifyErr
		}
		return mission.OperationResolutionV2{Kind: mission.OperationConflictV2}, nil
	}
	if resolveErr != nil || resolution != journalv2.BindingExact || record.ExpiresAt() != "" {
		return mission.OperationResolutionV2{}, adapterError("mission_operation", string(candidate.OperationID()), OutcomeMalformedResponse, RecoveryOperator, "operation binding is malformed", resolveErr)
	}
	headAddress, _ := journalv2.NewOperationHeadAddressV2(candidate.OperationID())
	headRequest, _ := journalv2.NewReadRequestV2(headAddress, attempt)
	headRecord, err := a.store.ReadV2(ctx, headRequest)
	if err != nil || headRecord.ExpiresAt() != "" {
		return mission.OperationResolutionV2{}, adapterError("mission_operation", string(candidate.OperationID()), OutcomeMalformedResponse, RecoveryOperator, "exact binding has no valid operation head", err)
	}
	state, err := journalv2.DecodeOperationStateV2(headRecord.CanonicalValue())
	if err != nil || state.OperationID() != candidate.OperationID() {
		return mission.OperationResolutionV2{}, adapterError("mission_operation", string(candidate.OperationID()), OutcomeMalformedResponse, RecoveryOperator, "operation head is malformed", err)
	}
	if err := a.store.VerifyRevisionV2(ctx, attempt); err != nil {
		return mission.OperationResolutionV2{}, err
	}
	return mission.OperationResolutionV2{Kind: mission.OperationExactV2, Binding: candidate, State: state, StateRecordRevision: headRecord.RecordRevision()}, nil
}

func (a *missionAuthorityAdapterV2) ObserveMission(ctx context.Context, projectID journalv2.ProjectID, missionID journalv2.MissionID) (mission.MissionObservationV2, error) {
	attempt, err := a.store.CaptureRevisionV2(ctx)
	if err != nil {
		return mission.MissionObservationV2{}, err
	}
	coordinate, err := journalv2.NewAggregateCoordinate("mission", journalv2.AggregateID(missionID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: projectID, MissionID: missionID})
	if err != nil {
		return mission.MissionObservationV2{}, adapterError("mission_observe", string(missionID), OutcomeInvalid, RecoveryFixRequest, "mission coordinate is invalid", err)
	}
	address, _ := journalv2.NewAggregateHeadAddressV2(coordinate)
	request, _ := journalv2.NewReadRequestV2(address, attempt)
	record, err := a.store.ReadV2(ctx, request)
	if err != nil {
		return mission.MissionObservationV2{}, err
	}
	head, err := journalv2.DecodeAggregateHeadV2(record.CanonicalValue())
	if err != nil || head.Coordinate() != coordinate || head.LogicalVersion() == 0 || record.ExpiresAt() != "" {
		return mission.MissionObservationV2{}, adapterError("mission_observe", string(missionID), OutcomeMalformedResponse, RecoveryOperator, "mission head is malformed", err)
	}
	state, err := mission.DecodeMissionAuthorityStateV2(head.AuthoritySummary())
	if err != nil || state.ProjectID() != projectID || state.MissionID() != missionID || state.Version() != head.LogicalVersion() || state.Digest() != head.CanonicalStateDigest() {
		return mission.MissionObservationV2{}, adapterError("mission_observe", string(missionID), OutcomeMalformedResponse, RecoveryOperator, "mission authority summary is invalid", err)
	}
	if err := a.store.VerifyRevisionV2(ctx, attempt); err != nil {
		return mission.MissionObservationV2{}, err
	}
	return mission.MissionObservationV2{State: state, RecordRevision: record.RecordRevision(), LastEventID: head.LastEventID()}, nil
}

func (a *missionAuthorityAdapterV2) ObserveReadinessAuthority(ctx context.Context, projectID journalv2.ProjectID, missionID journalv2.MissionID) (mission.ReadinessAuthorityObservationV2, error) {
	return observeReadinessAuthorityV2(ctx, a.store, projectID, missionID)
}

func observeReadinessAuthorityV2(ctx context.Context, reader restore.ReaderV2, projectID journalv2.ProjectID, missionID journalv2.MissionID) (mission.ReadinessAuthorityObservationV2, error) {
	attempt, err := reader.CaptureRevisionV2(ctx)
	if err != nil {
		return mission.ReadinessAuthorityObservationV2{}, err
	}
	coordinate, err := journalv2.NewAggregateCoordinate("mission", journalv2.AggregateID(missionID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: projectID, MissionID: missionID})
	if err != nil {
		return mission.ReadinessAuthorityObservationV2{}, adapterError("readiness_observe", string(missionID), OutcomeInvalid, RecoveryFixRequest, "readiness coordinate is invalid", err)
	}
	address, _ := journalv2.NewAggregateHeadAddressV2(coordinate)
	request, _ := journalv2.NewReadRequestV2(address, attempt)
	record, err := reader.ReadV2(ctx, request)
	if err != nil {
		return mission.ReadinessAuthorityObservationV2{}, err
	}
	head, err := journalv2.DecodeAggregateHeadV2(record.CanonicalValue())
	if err != nil || head.Coordinate() != coordinate || head.LogicalVersion() == 0 || record.ExpiresAt() != "" {
		return mission.ReadinessAuthorityObservationV2{}, adapterError("readiness_observe", string(missionID), OutcomeMalformedResponse, RecoveryOperator, "readiness head is malformed", err)
	}
	state, err := mission.DecodeReadinessAuthorityStateV2(head.AuthoritySummary())
	if err != nil || state.ProjectID() != projectID || state.MissionID() != missionID || state.Version() != head.LogicalVersion() || state.Digest() != head.CanonicalStateDigest() {
		return mission.ReadinessAuthorityObservationV2{}, adapterError("readiness_observe", string(missionID), OutcomeMalformedResponse, RecoveryOperator, "readiness authority summary is invalid", err)
	}
	if err := reader.VerifyRevisionV2(ctx, attempt); err != nil {
		return mission.ReadinessAuthorityObservationV2{}, err
	}
	observation, err := mission.NewReadinessAuthorityObservationV2(state, attempt, record.RecordRevision(), head.LastEventID())
	if err != nil {
		return mission.ReadinessAuthorityObservationV2{}, adapterError("readiness_observe", string(missionID), OutcomeMalformedResponse, RecoveryOperator, "readiness observation is invalid", err)
	}
	return observation, nil
}

func (a *missionAuthorityAdapterV2) ObserveMissionFact(ctx context.Context, projectID journalv2.ProjectID, missionID journalv2.MissionID, kind mission.MissionFactKindV2, factID string) (mission.MissionFactObservationV2, error) {
	if kind != mission.MissionFactBlockerV2 && kind != mission.MissionFactDecisionV2 {
		return mission.MissionFactObservationV2{}, adapterError("mission_fact_observe", factID, OutcomeInvalid, RecoveryFixRequest, "mission fact kind is invalid", nil)
	}
	attempt, err := a.store.CaptureRevisionV2(ctx)
	if err != nil {
		return mission.MissionFactObservationV2{}, err
	}
	coordinate, err := journalv2.NewAggregateCoordinate("mission", journalv2.AggregateID(string(kind)+":"+factID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: projectID, MissionID: missionID})
	if err != nil {
		return mission.MissionFactObservationV2{}, adapterError("mission_fact_observe", factID, OutcomeInvalid, RecoveryFixRequest, "mission fact coordinate is invalid", err)
	}
	address, _ := journalv2.NewAggregateHeadAddressV2(coordinate)
	request, _ := journalv2.NewReadRequestV2(address, attempt)
	record, err := a.store.ReadV2(ctx, request)
	if err != nil {
		if journalv2.IsKind(err, journalv2.ErrorNotFound) {
			if verifyErr := a.store.VerifyRevisionV2(ctx, attempt); verifyErr != nil {
				return mission.MissionFactObservationV2{}, verifyErr
			}
			return mission.MissionFactObservationV2{}, nil
		}
		return mission.MissionFactObservationV2{}, err
	}
	head, err := journalv2.DecodeAggregateHeadV2(record.CanonicalValue())
	if err != nil || head.Coordinate() != coordinate || head.LogicalVersion() == 0 || record.ExpiresAt() != "" {
		return mission.MissionFactObservationV2{}, adapterError("mission_fact_observe", factID, OutcomeMalformedResponse, RecoveryOperator, "mission fact head is malformed", err)
	}
	state, err := mission.DecodeMissionFactStateV2(head.AuthoritySummary())
	if err != nil || state.ProjectID() != projectID || state.MissionID() != missionID || state.Kind() != kind || state.FactID() != factID || state.LogicalVersion() != head.LogicalVersion() || state.Digest() != head.CanonicalStateDigest() {
		return mission.MissionFactObservationV2{}, adapterError("mission_fact_observe", factID, OutcomeMalformedResponse, RecoveryOperator, "mission fact authority summary is invalid", err)
	}
	if err := a.store.VerifyRevisionV2(ctx, attempt); err != nil {
		return mission.MissionFactObservationV2{}, err
	}
	return mission.MissionFactObservationV2{State: state, RecordRevision: record.RecordRevision(), LastEventID: head.LastEventID(), Found: true}, nil
}

func (a *missionAuthorityAdapterV2) PublishMission(ctx context.Context, plan journalv2.SealedCommitPlanV2) (mission.MissionPublicationV2, error) {
	acknowledgement, err := a.publisher.Publish(ctx, plan)
	if err != nil {
		observation, reconcileErr := a.store.ReconcileCommitV2(ctx, plan)
		if reconcileErr == nil {
			acknowledgement, err = a.publisher.ReconcileComplete(plan, observation)
		} else {
			err = reconcileErr
		}
	}
	if err != nil {
		if IsKind(err, OutcomeConflict) {
			return mission.MissionPublicationV2{}, &mission.MissionAuthorityErrorV2{Kind: mission.MissionAuthorityVersionConflictV2, Field: "mission_head", Cause: err}
		}
		return mission.MissionPublicationV2{}, err
	}
	return mission.MissionPublicationV2{
		PublicationID: plan.PublicationID(), ProviderRevision: acknowledgement.ProviderRevision(),
		DurableThroughRevision: acknowledgement.DurableThroughRevision(), MutationCount: acknowledgement.MutationCount(),
	}, nil
}

func (a *missionAuthorityAdapterV2) PublishReadinessAuthority(ctx context.Context, plan journalv2.SealedCommitPlanV2) (mission.MissionPublicationV2, error) {
	return a.PublishMission(ctx, plan)
}
