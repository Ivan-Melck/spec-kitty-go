package integration

import (
	"bytes"
	"context"
	"sort"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

// IntegrationCleanupRequestV2 starts a separate retry operation over the
// cleanup effect already recorded by the terminal integration publication.
// The integration OperationID remains the effect's attribution identity.
type IntegrationCleanupRequestV2 struct {
	CleanupOperationID     journalv2.OperationID
	IntegrationOperationID journalv2.OperationID
	ProjectID              ids.ProjectID
	MissionID              ids.MissionID
	WorkRevisionID         ids.WorkRevisionID
	WorkID                 ids.WorkID
	AttemptID              string
	RepositoryID           ids.RepositoryID
	TargetRef              string
	RuntimeEpochID         journalv2.RuntimeEpochID
	ActorID                ids.ActorID
	ProcessID              ids.ProcessID
	Governance             execution.ExecutionGovernanceV2
	ObservedAt             time.Time
}

type IntegrationCleanupAuthorityObservationV2 struct {
	Attempt                       IntegrationAttemptStateV2
	AttemptRecordRevision         uint64
	SourceOperation               journalv2.OperationStateV2
	SourceOperationRecordRevision uint64
	SourceOperationValueDigest    journalv2.Digest
}

type IntegrationCleanupPlanV2 struct{ sealed journalv2.SealedCommitPlanV2 }

func (p IntegrationCleanupPlanV2) Publication() (journalv2.SealedCommitPlanV2, error) {
	if p.sealed.Digest().IsZero() {
		return p.sealed, acceptanceErrorV2(KindInvalid, "cleanup_plan")
	}
	return p.sealed, nil
}

type IntegrationCleanupAuthorityPortV2 interface {
	ResolveIntegrationCleanupOperation(context.Context, journalv2.OperationBindingV2) (IntegrationOperationResolutionV2, error)
	ObserveIntegrationCleanupAuthority(context.Context, IntegrationCleanupRequestV2) (IntegrationCleanupAuthorityObservationV2, error)
	PublishIntegrationCleanup(context.Context, IntegrationCleanupPlanV2) (IntegrationAckV2, error)
	ReconcileIntegrationCleanup(context.Context, IntegrationCleanupPlanV2) (IntegrationAckV2, error)
}

type IntegrationCleanupServiceV2 struct {
	port     IntegrationCleanupAuthorityPortV2
	epochs   AcceptanceEpochSourceV2
	git      IntegrationGitPortV2
	registry journalv2.EventRegistryV2
}

func NewIntegrationCleanupServiceV2(port IntegrationCleanupAuthorityPortV2, epochs AcceptanceEpochSourceV2, git IntegrationGitPortV2, registry journalv2.EventRegistryV2) (*IntegrationCleanupServiceV2, error) {
	if port == nil || epochs == nil || git == nil {
		return nil, acceptanceErrorV2(KindInvalid, "cleanup_dependencies")
	}
	if _, err := registry.Resolve("integration_attempt", 1, 1); err != nil {
		return nil, err
	}
	return &IntegrationCleanupServiceV2{port: port, epochs: epochs, git: git, registry: registry}, nil
}

func (s *IntegrationCleanupServiceV2) Cleanup(ctx context.Context, request IntegrationCleanupRequestV2) (IntegrationAttemptStateV2, error) {
	binding, err := prepareCleanupBindingV2(request)
	if err != nil {
		return IntegrationAttemptStateV2{}, err
	}
	resolution, err := s.port.ResolveIntegrationCleanupOperation(ctx, binding)
	if err != nil {
		return IntegrationAttemptStateV2{}, acceptanceErrorV2(KindStoreFailure, "cleanup_operation")
	}
	if resolution.Kind == AcceptanceOperationConflictV2 {
		return IntegrationAttemptStateV2{}, acceptanceErrorV2(KindIdempotencyConflict, "cleanup_operation")
	}
	if resolution.Kind == AcceptanceOperationExactV2 && resolution.State.Status() == journalv2.StatusResolved {
		state, decodeErr := DecodeIntegrationAttemptStateV2(resolution.State.Result())
		if decodeErr != nil || state.CleanupStatus != CleanupComplete || !cleanupRequestMatchesV2(request, state) {
			return IntegrationAttemptStateV2{}, acceptanceErrorV2(KindConflict, "cleanup_replay")
		}
		return state, nil
	}
	epoch, err := s.epochs.CurrentRuntimeEpoch(ctx)
	if err != nil || epoch != request.RuntimeEpochID {
		return IntegrationAttemptStateV2{}, acceptanceErrorV2(KindStaleAuthority, "cleanup_epoch")
	}
	authority, err := s.port.ObserveIntegrationCleanupAuthority(ctx, request)
	if err != nil {
		return IntegrationAttemptStateV2{}, acceptanceErrorV2(KindEvidenceUnavailable, "cleanup_authority")
	}
	if err = validateIntegrationCleanupAuthorityV2(request, resolution, authority); err != nil {
		return IntegrationAttemptStateV2{}, err
	}
	current := authority.Attempt
	currentRevision := authority.AttemptRecordRevision
	var priorOperation *journalv2.OperationStateV2
	var priorOperationRevision uint64
	if resolution.Kind == AcceptanceOperationAbsentV2 {
		next := current
		next.LogicalVersion++
		next.CleanupStatus = CleanupPending
		plan, planErr := buildIntegrationCleanupPlanV2(request, binding, current, currentRevision, nil, 0, next, s.registry)
		if planErr != nil {
			return IntegrationAttemptStateV2{}, planErr
		}
		ack, publishErr := publishIntegrationCleanupV2(ctx, s.port, plan)
		if publishErr != nil {
			return IntegrationAttemptStateV2{}, publishErr
		}
		sealed, _ := plan.Publication()
		op := sealed.OperationState()
		priorOperation, priorOperationRevision = &op, ack.ProviderRevision
		current, currentRevision = next, ack.ProviderRevision
	} else {
		op := resolution.State
		priorOperation, priorOperationRevision = &op, resolution.RecordRevision
	}
	intent := IntegrationCleanupIntentV2{EffectID: current.CleanupEffectID, OperationID: current.OperationID, AttemptID: current.AttemptID, RepositoryID: current.RepositoryID, TargetRef: current.TargetRef, WorkspacePath: current.Workspace.Path, Generation: current.TargetGeneration, FenceTokenHash: current.FenceTokenHash}
	var observation IntegrationCleanupObservationV2
	observedCompletion := false
	if resolution.Kind == AcceptanceOperationExactV2 {
		var found bool
		observation, found, err = s.git.ObserveCleanup(ctx, intent)
		if err != nil {
			return IntegrationAttemptStateV2{}, boundedGitFailureV2("observe cleanup integration workspace", err)
		}
		observedCompletion = found
	}
	if !observedCompletion {
		var effectErr error
		observation, effectErr = s.git.CleanupWorkspace(ctx, intent)
		if effectErr != nil {
			var found bool
			observation, found, effectErr = s.git.ObserveCleanup(ctx, intent)
			if effectErr != nil || !found {
				return IntegrationAttemptStateV2{}, boundedGitFailureV2("cleanup integration workspace", effectErr)
			}
		}
	}
	if !observation.validates(intent) {
		return IntegrationAttemptStateV2{}, acceptanceErrorV2(KindStaleAuthority, "cleanup_effect")
	}
	next := current
	next.LogicalVersion++
	next.CleanupStatus = CleanupComplete
	next.CleanupMarker = observation.ProviderMarker
	plan, err := buildIntegrationCleanupPlanV2(request, binding, current, currentRevision, priorOperation, priorOperationRevision, next, s.registry)
	if err != nil {
		return IntegrationAttemptStateV2{}, err
	}
	if _, err = publishIntegrationCleanupV2(ctx, s.port, plan); err != nil {
		return IntegrationAttemptStateV2{}, err
	}
	return next, nil
}

func prepareCleanupBindingV2(r IntegrationCleanupRequestV2) (journalv2.OperationBindingV2, error) {
	if r.ObservedAt.IsZero() || !acceptanceText(r.AttemptID, journalv2.MaxIdentityBytes) || !validTargetRef(r.TargetRef) {
		return journalv2.OperationBindingV2{}, acceptanceErrorV2(KindInvalid, "cleanup_request")
	}
	coordinate, err := IntegrationAttemptCoordinateV2(r.ProjectID, r.MissionID, r.WorkRevisionID, r.WorkID, r.AttemptID)
	if err != nil {
		return journalv2.OperationBindingV2{}, err
	}
	command := acceptanceCanonical(struct {
		IntegrationOperationID journalv2.OperationID `json:"integration_operation_id"`
		AttemptID              string                `json:"attempt_id"`
		RepositoryID           ids.RepositoryID      `json:"repository_id"`
		TargetRef              string                `json:"target_ref"`
	}{r.IntegrationOperationID, r.AttemptID, r.RepositoryID, r.TargetRef})
	digest, _ := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "integration.cleanup", CommandSchemaVersion: 1, Scope: coordinate.Scope, Command: command})
	return journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: r.CleanupOperationID, CommandKind: "integration.cleanup", CommandSchemaVersion: 1, CanonicalIntentDigest: digest, Scope: coordinate.Scope, ActorID: journalv2.ActorID(r.ActorID), ProcessID: journalv2.ProcessID(r.ProcessID), DoctrineRevision: r.Governance.DoctrineRevision, ProfileRevision: r.Governance.ProfileRevision, PolicyRevision: r.Governance.PolicyRevision, OriginatingRuntimeEpochID: r.RuntimeEpochID, ObservedCreatedAt: r.ObservedAt.UTC().Truncate(time.Second).Format(time.RFC3339)})
}

func validateIntegrationCleanupAuthorityV2(r IntegrationCleanupRequestV2, resolution IntegrationOperationResolutionV2, observed IntegrationCleanupAuthorityObservationV2) error {
	a := observed.Attempt
	if a.Validate() != nil || !cleanupRequestMatchesV2(r, a) || observed.AttemptRecordRevision == 0 || (resolution.Kind == AcceptanceOperationAbsentV2 && a.CleanupStatus != CleanupEligible) || (resolution.Kind == AcceptanceOperationExactV2 && a.CleanupStatus != CleanupPending) {
		return acceptanceErrorV2(KindRepairRequired, "cleanup_attempt")
	}
	op := observed.SourceOperation
	if op.OperationID() != r.IntegrationOperationID || op.Status() != journalv2.StatusResolved || observed.SourceOperationRecordRevision == 0 || !containsCleanupEffectV2(op.EffectIDs(), a.CleanupEffectID) {
		return acceptanceErrorV2(KindStaleAuthority, "cleanup_source_operation")
	}
	sourceResult, err := DecodeIntegrationAttemptStateV2(op.Result())
	if err != nil || sourceResult.CleanupStatus != CleanupEligible || !cleanupRequestMatchesV2(r, sourceResult) || !cleanupIdentityEqualV2(sourceResult, a) {
		return acceptanceErrorV2(KindStaleAuthority, "cleanup_source_result")
	}
	opValue, opErr := journalv2.EncodeOperationStateV2(op)
	opValueDigest, digestErr := journalv2.ReducerStateDigestV2(opValue)
	if opErr != nil || digestErr != nil || opValueDigest != observed.SourceOperationValueDigest {
		return acceptanceErrorV2(KindStaleAuthority, "cleanup_source_assertion")
	}
	return nil
}

func cleanupRequestMatchesV2(r IntegrationCleanupRequestV2, a IntegrationAttemptStateV2) bool {
	return a.Stage == IntegrationAttemptCompletedV2 && a.OperationID == r.IntegrationOperationID && a.ProjectID == r.ProjectID && a.MissionID == r.MissionID && a.WorkRevisionID == r.WorkRevisionID && a.WorkID == r.WorkID && a.AttemptID == r.AttemptID && a.RepositoryID == r.RepositoryID && a.TargetRef == r.TargetRef && a.RuntimeEpochID == r.RuntimeEpochID && bytes.Equal(acceptanceCanonical(a.Governance), acceptanceCanonical(r.Governance))
}

func buildIntegrationCleanupPlanV2(r IntegrationCleanupRequestV2, binding journalv2.OperationBindingV2, priorAttempt IntegrationAttemptStateV2, priorAttemptRevision uint64, priorOperation *journalv2.OperationStateV2, priorOperationRevision uint64, next IntegrationAttemptStateV2, registry journalv2.EventRegistryV2) (IntegrationCleanupPlanV2, error) {
	opVersion := journalv2.LogicalVersion(1)
	if priorOperation != nil {
		opVersion = priorOperation.LogicalVersion() + 1
	}
	attemptCoordinate, _ := IntegrationAttemptCoordinateV2(next.ProjectID, next.MissionID, next.WorkRevisionID, next.WorkID, next.AttemptID)
	priorDigest, _ := journalv2.ReducerStateDigestV2(priorAttempt.CanonicalBytes())
	attemptPayload := acceptanceCanonical(integrationStateEventV2{SchemaVersion: 1, Kind: string(next.Stage), PriorDigest: priorDigest.String(), State: next.CanonicalBytes()})
	attemptEventID := integrationEventIDV2("integration_attempt_cleanup", r.CleanupOperationID, next.LogicalVersion)
	opEventID := integrationEventIDV2("operation_cleanup", r.CleanupOperationID, uint64(opVersion))
	eventIDs := []journalv2.EventID{attemptEventID, opEventID}
	if priorOperation != nil {
		eventIDs = append(eventIDs, priorOperation.EventIDs()...)
	}
	sort.Slice(eventIDs, func(i, j int) bool { return eventIDs[i] < eventIDs[j] })
	status, resultKind := journalv2.StatusInProgress, ""
	var result []byte
	if next.CleanupStatus == CleanupComplete {
		status, resultKind, result = journalv2.StatusResolved, "integration.cleanup.result", next.CanonicalBytes()
	}
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: r.CleanupOperationID, LogicalVersion: opVersion, Status: status, ResultKind: resultKind, ResultSchemaVersion: func() uint32 {
		if resultKind != "" {
			return 1
		}
		return 0
	}(), CanonicalResult: result, EventIDs: eventIDs})
	if err != nil {
		return IntegrationCleanupPlanV2{}, err
	}
	opPayload, _ := journalv2.EncodeOperationStateEventPayloadV2(operation)
	opCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(r.CleanupOperationID), journalv2.Scope{Kind: journalv2.ScopeHost})
	governance := []string{"config." + r.Governance.PolicyRevision, "doctrine." + r.Governance.DoctrineRevision, "mission_type." + r.Governance.MissionTypeRevision, "profile." + r.Governance.ProfileRevision}
	sort.Strings(governance)
	events := []journalv2.EventDraftV2{
		{EventID: attemptEventID, EventType: "integration_attempt." + string(next.Stage), Family: "integration_attempt", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: attemptCoordinate, AggregateLogicalVersion: journalv2.LogicalVersion(next.LogicalVersion), CommittedRuntimeEpochID: r.RuntimeEpochID, ActorID: journalv2.ActorID(next.ActorID), ProcessID: journalv2.ProcessID(next.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(next.WorkRevisionID), PolicyRevision: r.Governance.PolicyRevision, GovernanceRevisions: governance, Payload: attemptPayload},
		{EventID: opEventID, EventType: "operation." + string(status), Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: opCoordinate, AggregateLogicalVersion: opVersion, CommittedRuntimeEpochID: r.RuntimeEpochID, ActorID: journalv2.ActorID(r.ActorID), ProcessID: journalv2.ProcessID(r.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(r.WorkRevisionID), PolicyRevision: r.Governance.PolicyRevision, GovernanceRevisions: governance, Payload: opPayload},
	}
	attemptDigest, _ := journalv2.ReducerStateDigestV2(next.CanonicalBytes())
	opDigest, _ := journalv2.ReducerStateDigestV2(opPayload)
	heads := []journalv2.HeadTransitionV2{
		{Coordinate: attemptCoordinate, ExpectedLogicalVersion: journalv2.LogicalVersion(priorAttempt.LogicalVersion), NextLogicalVersion: journalv2.LogicalVersion(next.LogicalVersion), EventID: attemptEventID, ExpectedRecordRevision: priorAttemptRevision, CommittedRuntimeEpochID: r.RuntimeEpochID, CanonicalStateDigest: attemptDigest, AuthoritySummary: next.CanonicalBytes()},
		{Coordinate: opCoordinate, ExpectedLogicalVersion: func() journalv2.LogicalVersion {
			if priorOperation != nil {
				return priorOperation.LogicalVersion()
			}
			return 0
		}(), NextLogicalVersion: opVersion, EventID: opEventID, ExpectedRecordRevision: priorOperationRevision, CommittedRuntimeEpochID: r.RuntimeEpochID, CanonicalStateDigest: opDigest, AuthoritySummary: opPayload},
	}
	conditions := make([]journalv2.ProviderCondition, 0, len(heads))
	for _, head := range heads {
		condition, conditionErr := journalv2.ConditionForHeadTransitionV2(head)
		if conditionErr != nil {
			return IntegrationCleanupPlanV2{}, conditionErr
		}
		conditions = append(conditions, condition)
	}
	input := journalv2.CommitPlanInput{OperationState: operation, Events: events, Heads: heads, Conditions: conditions}
	if priorOperation == nil {
		input.OperationBinding = &binding
	} else {
		input.PriorOperationState = priorOperation
	}
	sealed, err := journalv2.SealCommitPlanV2(input, registry)
	if err != nil {
		return IntegrationCleanupPlanV2{}, err
	}
	return IntegrationCleanupPlanV2{sealed: sealed}, nil
}

func publishIntegrationCleanupV2(ctx context.Context, port IntegrationCleanupAuthorityPortV2, plan IntegrationCleanupPlanV2) (IntegrationAckV2, error) {
	ack, err := port.PublishIntegrationCleanup(ctx, plan)
	if err != nil {
		ack, err = port.ReconcileIntegrationCleanup(ctx, plan)
	}
	if err != nil {
		return IntegrationAckV2{}, acceptanceErrorV2(KindStoreFailure, "cleanup_publication")
	}
	sealed, _ := plan.Publication()
	if ack.ProviderRevision == 0 || ack.PublicationID != sealed.PublicationID() || ack.PlanDigest != sealed.Digest() || ack.MutationCount != sealed.MutationCount() {
		return IntegrationAckV2{}, acceptanceErrorV2(KindConflict, "cleanup_ack")
	}
	return ack, nil
}

func containsCleanupEffectV2(values []journalv2.EffectID, wanted journalv2.EffectID) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func cleanupIdentityEqualV2(a, b IntegrationAttemptStateV2) bool {
	a.LogicalVersion, b.LogicalVersion = 0, 0
	a.CleanupStatus, b.CleanupStatus = "", ""
	a.CleanupMarker, b.CleanupMarker = "", ""
	return bytes.Equal(acceptanceCanonical(a), acceptanceCanonical(b))
}
