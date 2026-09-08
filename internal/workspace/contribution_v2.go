package workspace

import (
	"bytes"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type BindingContributionInputV2 struct {
	Binding    BindingV2
	EffectID   journalv2.EffectID
	EventID    journalv2.EventID
	OccurredAt time.Time
}

// PlanContributionV2 is an opaque, workspace-validated set of publication
// members. Execution owns composition with lease authority; callers cannot
// construct a contribution by filling journal drafts directly.
type PlanContributionV2 struct {
	state     StateV2
	event     journalv2.EventDraftV2
	head      journalv2.HeadTransitionV2
	condition journalv2.ProviderCondition
	effect    *journalv2.EffectIntentReference
	claim     *journalv2.WorkspacePathClaimV2
}

func NewBindingContributionV2(input BindingContributionInputV2) (PlanContributionV2, error) {
	if err := input.Binding.Validate(); err != nil || input.EffectID == "" || input.EventID == "" || input.OccurredAt.IsZero() {
		return PlanContributionV2{}, workspaceV2Error(KindInvalid, "binding_contribution", err)
	}
	state := StateV2{SchemaVersion: workspaceStateSchemaV2, LogicalVersion: 1, Binding: input.Binding, Stage: StageCreationRequestedV2, EffectID: input.EffectID, OccurredAt: input.OccurredAt.UTC().Format(time.RFC3339Nano)}
	state.Binding.Dependencies = append([]DependencyV2(nil), input.Binding.Dependencies...)
	stateBytes, err := encodeStateV2(state)
	if err != nil {
		return PlanContributionV2{}, err
	}
	eventBytes, err := encodeEventV2(EventV2{SchemaVersion: 1, Kind: "preparation_requested", State: state})
	if err != nil {
		return PlanContributionV2{}, err
	}
	coordinate, err := WorkspaceCoordinateV2(input.Binding)
	if err != nil {
		return PlanContributionV2{}, err
	}
	stateDigest, _ := journalv2.ReducerStateDigestV2(stateBytes)
	effectCanonical, err := journalv2.CanonicalObject(mustJSONV2(struct {
		SchemaVersion uint16             `json:"schema_version"`
		EffectID      journalv2.EffectID `json:"effect_id"`
		WorkspaceID   string             `json:"workspace_id"`
		Action        string             `json:"action"`
		PathClaim     string             `json:"path_claim"`
	}{1, input.EffectID, input.Binding.WorkspaceID.String(), "create_workspace", input.Binding.PathClaimDigest.String()}))
	if err != nil {
		return PlanContributionV2{}, err
	}
	effectDigest, _ := journalv2.ReducerStateDigestV2(effectCanonical)
	claimCanonical, err := journalv2.CanonicalObject(mustJSONV2(struct {
		SchemaVersion  uint16 `json:"schema_version"`
		ClaimDigest    string `json:"claim_digest"`
		WorkspaceID    string `json:"workspace_id"`
		RepositoryID   string `json:"repository_id"`
		ProjectID      string `json:"project_id"`
		MissionID      string `json:"mission_id"`
		WorkRevisionID string `json:"work_revision_id"`
		WorkID         string `json:"work_id"`
		RunID          string `json:"run_id"`
		LeaseID        string `json:"lease_id"`
		Generation     uint64 `json:"generation"`
		RuntimeEpochID string `json:"runtime_epoch_id"`
	}{1, input.Binding.PathClaimDigest.String(), input.Binding.WorkspaceID.String(), input.Binding.RepositoryID.String(), input.Binding.ProjectID.String(), input.Binding.MissionID.String(), input.Binding.WorkRevisionID.String(), input.Binding.WorkID.String(), input.Binding.RunID.String(), input.Binding.LeaseID.String(), input.Binding.Generation, string(input.Binding.RuntimeEpochID)}))
	if err != nil {
		return PlanContributionV2{}, err
	}
	claim, err := journalv2.NewWorkspacePathClaimV2(input.Binding.PathClaimDigest, claimCanonical)
	if err != nil {
		return PlanContributionV2{}, err
	}
	head := journalv2.HeadTransitionV2{Coordinate: coordinate, ExpectedLogicalVersion: 0, NextLogicalVersion: 1, EventID: input.EventID, CommittedRuntimeEpochID: input.Binding.RuntimeEpochID, CanonicalStateDigest: stateDigest, AuthoritySummary: stateBytes}
	condition, err := journalv2.ConditionForHeadTransitionV2(head)
	if err != nil {
		return PlanContributionV2{}, err
	}
	event := journalv2.EventDraftV2{EventID: input.EventID, EventType: "workspace.preparation_requested", Family: "workspace", EventSchemaVersion: WorkspaceEventSchemaV2, ReducerVersion: WorkspaceReducerV2, Coordinate: coordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: input.Binding.RuntimeEpochID, ActorID: journalv2.ActorID(input.Binding.ActorID), ProcessID: journalv2.ProcessID(input.Binding.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(input.Binding.WorkRevisionID), Payload: eventBytes}
	effect := journalv2.EffectIntentReference{EffectID: input.EffectID, Digest: effectDigest, Canonical: effectCanonical}
	return PlanContributionV2{state: state, event: event, head: head, condition: condition, effect: &effect, claim: &claim}, nil
}

type QuarantineContributionInputV2 struct {
	Prior          StateV2
	RecordRevision uint64
	EventID        journalv2.EventID
	Reason         string
	OccurredAt     time.Time
}

type TransitionContributionInputV2 struct {
	Prior          StateV2
	RecordRevision uint64
	EventID        journalv2.EventID
	NextStage      StageV2
	Observation    ObservationV2
	EffectID       journalv2.EffectID
	OccurredAt     time.Time
}

// NewTransitionContributionV2 constructs an ordinary preparation transition.
// Quarantine is deliberately excluded: it is valid only through the
// execution-owned expiry composition.
func NewTransitionContributionV2(input TransitionContributionInputV2) (PlanContributionV2, error) {
	if input.Prior.Validate() != nil || input.RecordRevision == 0 || input.EventID == "" || input.OccurredAt.IsZero() || input.NextStage == StageQuarantinedV2 {
		return PlanContributionV2{}, workspaceV2Error(KindInvalid, "transition_contribution", nil)
	}
	next := input.Prior
	next.Binding.Dependencies = append([]DependencyV2(nil), input.Prior.Binding.Dependencies...)
	next.LogicalVersion++
	next.Stage = input.NextStage
	next.Observation = input.Observation
	next.OccurredAt = input.OccurredAt.UTC().Format(time.RFC3339Nano)
	if input.EffectID != "" {
		next.EffectID = input.EffectID
	}
	kind := transitionEventKindV2(input.Prior.Stage, input.NextStage)
	if kind == "" || !validTransitionObservationV2(input.Prior.Stage, input.NextStage, next.Binding, input.Observation, input.EffectID) {
		return PlanContributionV2{}, workspaceV2Error(KindWorkspaceConflict, "stage_transition", nil)
	}
	priorDigest, _ := journalv2.ReducerStateDigestV2(input.Prior.CanonicalBytes())
	eventBytes, err := encodeEventV2(EventV2{SchemaVersion: 1, Kind: kind, PriorDigest: priorDigest.String(), State: next})
	if err != nil {
		return PlanContributionV2{}, err
	}
	stateBytes := next.CanonicalBytes()
	stateDigest, _ := journalv2.ReducerStateDigestV2(stateBytes)
	coordinate, _ := WorkspaceCoordinateV2(next.Binding)
	head := journalv2.HeadTransitionV2{Coordinate: coordinate, ExpectedLogicalVersion: journalv2.LogicalVersion(input.Prior.LogicalVersion), NextLogicalVersion: journalv2.LogicalVersion(next.LogicalVersion), EventID: input.EventID, ExpectedRecordRevision: input.RecordRevision, CommittedRuntimeEpochID: next.Binding.RuntimeEpochID, CanonicalStateDigest: stateDigest, AuthoritySummary: stateBytes}
	condition, err := journalv2.ConditionForHeadTransitionV2(head)
	if err != nil {
		return PlanContributionV2{}, err
	}
	event := journalv2.EventDraftV2{EventID: input.EventID, EventType: "workspace." + kind, Family: "workspace", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: coordinate, AggregateLogicalVersion: journalv2.LogicalVersion(next.LogicalVersion), CommittedRuntimeEpochID: next.Binding.RuntimeEpochID, ActorID: journalv2.ActorID(next.Binding.ActorID), ProcessID: journalv2.ProcessID(next.Binding.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(next.Binding.WorkRevisionID), Payload: eventBytes}
	contribution := PlanContributionV2{state: next, event: event, head: head, condition: condition}
	if input.EffectID != "" {
		canonical, e := journalv2.CanonicalObject(mustJSONV2(struct {
			SchemaVersion uint16             `json:"schema_version"`
			EffectID      journalv2.EffectID `json:"effect_id"`
			WorkspaceID   string             `json:"workspace_id"`
			Action        string             `json:"action"`
		}{1, input.EffectID, next.Binding.WorkspaceID.String(), string(input.NextStage)}))
		if e != nil {
			return PlanContributionV2{}, e
		}
		digest, _ := journalv2.ReducerStateDigestV2(canonical)
		effect := journalv2.EffectIntentReference{EffectID: input.EffectID, Digest: digest, Canonical: canonical}
		contribution.effect = &effect
	}
	return contribution, nil
}

func validTransitionObservationV2(prior, next StageV2, binding BindingV2, observation ObservationV2, effectID journalv2.EffectID) bool {
	switch next {
	case StageWorktreeObservedV2:
		return observation.Registered
	case StageBaseVerifiedV2:
		return observation.Registered && observation.Head == binding.PinnedBase
	case StageDependenciesRequestedV2:
		return prior == StageBaseVerifiedV2 && effectID != ""
	case StageDependenciesObservedV2:
		return dependenciesPreparedV2(binding, observation)
	case StageOwnershipVerifiedV2:
		return ownedWorkspaceObservationV2(binding, observation)
	case StageReadyV2:
		return ownedWorkspaceObservationV2(binding, observation) && observation.TargetBefore == binding.PinnedBase && observation.TargetAfter == observation.TargetBefore
	case StageReconciliationV2:
		return effectID != ""
	default:
		return false
	}
}

func transitionEventKindV2(prior, next StageV2) string {
	allowed := map[StageV2]map[StageV2]string{
		StageCreationRequestedV2:     {StageWorktreeObservedV2: "preparation_observed", StageReconciliationV2: "reconciliation_required"},
		StageWorktreeObservedV2:      {StageBaseVerifiedV2: "preparation_advanced"},
		StageBaseVerifiedV2:          {StageDependenciesRequestedV2: "preparation_requested"},
		StageDependenciesRequestedV2: {StageDependenciesObservedV2: "preparation_observed", StageReconciliationV2: "reconciliation_required"},
		StageDependenciesObservedV2:  {StageOwnershipVerifiedV2: "preparation_advanced"},
		StageOwnershipVerifiedV2:     {StageReadyV2: "ready_published"},
		StageReconciliationV2:        {StageWorktreeObservedV2: "preparation_observed", StageDependenciesObservedV2: "preparation_observed"},
	}
	return allowed[prior][next]
}

func NewQuarantineContributionV2(input QuarantineContributionInputV2) (PlanContributionV2, error) {
	if input.Prior.Validate() != nil || input.Prior.Stage == StageQuarantinedV2 || input.RecordRevision == 0 || input.EventID == "" || input.Reason == "" || len(input.Reason) > journalv2.MaxIdentityBytes || input.OccurredAt.IsZero() {
		return PlanContributionV2{}, workspaceV2Error(KindInvalid, "quarantine_contribution", nil)
	}
	next := input.Prior
	next.Binding.Dependencies = append([]DependencyV2(nil), input.Prior.Binding.Dependencies...)
	next.LogicalVersion++
	next.Stage = StageQuarantinedV2
	next.QuarantineReason = input.Reason
	next.OccurredAt = input.OccurredAt.UTC().Format(time.RFC3339Nano)
	priorDigest, _ := journalv2.ReducerStateDigestV2(input.Prior.CanonicalBytes())
	eventBytes, err := encodeEventV2(EventV2{SchemaVersion: 1, Kind: "quarantined", PriorDigest: priorDigest.String(), State: next})
	if err != nil {
		return PlanContributionV2{}, err
	}
	stateBytes := next.CanonicalBytes()
	stateDigest, _ := journalv2.ReducerStateDigestV2(stateBytes)
	coordinate, _ := WorkspaceCoordinateV2(next.Binding)
	head := journalv2.HeadTransitionV2{Coordinate: coordinate, ExpectedLogicalVersion: journalv2.LogicalVersion(input.Prior.LogicalVersion), NextLogicalVersion: journalv2.LogicalVersion(next.LogicalVersion), EventID: input.EventID, ExpectedRecordRevision: input.RecordRevision, CommittedRuntimeEpochID: next.Binding.RuntimeEpochID, CanonicalStateDigest: stateDigest, AuthoritySummary: stateBytes}
	condition, err := journalv2.ConditionForHeadTransitionV2(head)
	if err != nil {
		return PlanContributionV2{}, err
	}
	event := journalv2.EventDraftV2{EventID: input.EventID, EventType: "workspace.quarantined", Family: "workspace", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: coordinate, AggregateLogicalVersion: journalv2.LogicalVersion(next.LogicalVersion), CommittedRuntimeEpochID: next.Binding.RuntimeEpochID, ActorID: journalv2.ActorID(next.Binding.ActorID), ProcessID: journalv2.ProcessID(next.Binding.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(next.Binding.WorkRevisionID), Payload: eventBytes}
	return PlanContributionV2{state: next, event: event, head: head, condition: condition}, nil
}

func (c PlanContributionV2) State() (StateV2, error) {
	if err := c.validate(); err != nil {
		return StateV2{}, err
	}
	state := c.state
	state.Binding.Dependencies = append([]DependencyV2(nil), state.Binding.Dependencies...)
	return state, nil
}

func (c PlanContributionV2) validatedMembers() (journalv2.EventDraftV2, journalv2.HeadTransitionV2, journalv2.ProviderCondition, *journalv2.EffectIntentReference, *journalv2.WorkspacePathClaimV2, error) {
	if err := c.validate(); err != nil {
		return journalv2.EventDraftV2{}, journalv2.HeadTransitionV2{}, journalv2.ProviderCondition{}, nil, nil, err
	}
	event := c.event
	event.Payload = append([]byte(nil), event.Payload...)
	head := c.head
	head.AuthoritySummary = append([]byte(nil), head.AuthoritySummary...)
	var effect *journalv2.EffectIntentReference
	if c.effect != nil {
		copyEffect := *c.effect
		copyEffect.Canonical = append([]byte(nil), copyEffect.Canonical...)
		effect = &copyEffect
	}
	var claim *journalv2.WorkspacePathClaimV2
	if c.claim != nil {
		copied, err := journalv2.NewWorkspacePathClaimV2(c.claim.ClaimDigest(), c.claim.CanonicalValue())
		if err != nil {
			return journalv2.EventDraftV2{}, journalv2.HeadTransitionV2{}, journalv2.ProviderCondition{}, nil, nil, err
		}
		claim = &copied
	}
	return event, head, c.condition, effect, claim, nil
}

func (c PlanContributionV2) validate() error {
	if c.state.Validate() != nil || c.event.Coordinate != c.head.Coordinate || c.event.AggregateLogicalVersion != c.head.NextLogicalVersion || c.event.EventID != c.head.EventID || !bytes.Equal(c.head.AuthoritySummary, c.state.CanonicalBytes()) {
		return workspaceV2Error(KindInvalid, "contribution", nil)
	}
	return nil
}

// AppendPlanContributionV2 is intentionally the only wire crossing. It copies
// validated semantic members into an execution-owned commit input.
func AppendPlanContributionV2(input journalv2.CommitPlanInput, contribution PlanContributionV2) (journalv2.CommitPlanInput, error) {
	event, head, condition, effect, claim, err := contribution.validatedMembers()
	if err != nil {
		return journalv2.CommitPlanInput{}, err
	}
	input.Events = append(input.Events, event)
	input.Heads = append(input.Heads, head)
	input.Conditions = append(input.Conditions, condition)
	if effect != nil {
		input.EffectIntents = append(input.EffectIntents, *effect)
	}
	if claim != nil {
		if input.WorkspacePathClaim != nil {
			return journalv2.CommitPlanInput{}, workspaceV2Error(KindInvalid, "duplicate_path_claim", nil)
		}
		input.WorkspacePathClaim = claim
	}
	return input, nil
}
