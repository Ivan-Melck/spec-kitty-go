package helpercmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
	"github.com/spec-kitty/go-kitty3/internal/projection"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

const missionMethodSchemaVersion uint16 = 1
const maxMissionMethodRequest = 2 << 20

type MethodDiagnostic struct {
	Kind      string `json:"kind"`
	Recovery  string `json:"recovery"`
	Field     string `json:"field,omitempty"`
	RetrySafe bool   `json:"retry_safe"`
	Message   string `json:"message"`
}

type MethodResponse struct {
	SchemaVersion uint16            `json:"schema_version"`
	OK            bool              `json:"ok"`
	Kind          string            `json:"kind"`
	CorrelationID string            `json:"correlation_id"`
	OperationID   ids.OperationID   `json:"operation_id,omitempty"`
	AggregateIDs  map[string]string `json:"aggregate_ids"`
	RetrySafety   string            `json:"retry_safety"`
	Recovery      string            `json:"recovery"`
	Result        any               `json:"result,omitempty"`
	Error         *MethodDiagnostic `json:"error,omitempty"`
}

type initializeRequest struct {
	Database          string                    `json:"database"`
	CorrelationID     string                    `json:"correlation_id"`
	MissionID         ids.MissionID             `json:"mission_id"`
	ProjectID         ids.ProjectID             `json:"project_id"`
	DefinitionID      ids.MissionDefinitionID   `json:"definition_id"`
	DefinitionVersion mission.DefinitionVersion `json:"definition_version"`
	OperationID       ids.OperationID           `json:"operation_id"`
	ActorID           ids.ActorID               `json:"actor_id"`
	ProcessID         ids.ProcessID             `json:"process_id"`
	Governance        governancecontext.Context `json:"governance_context"`
	OccurredAt        time.Time                 `json:"occurred_at"`
}

type transitionRequest struct {
	Database             string                       `json:"database"`
	CorrelationID        string                       `json:"correlation_id"`
	MissionID            ids.MissionID                `json:"mission_id"`
	DefinitionID         ids.MissionDefinitionID      `json:"definition_id"`
	DefinitionVersion    mission.DefinitionVersion    `json:"definition_version"`
	DefinitionCommitment mission.DefinitionCommitment `json:"definition_commitment"`
	ExpectedSnapshot     mission.MissionSnapshot      `json:"expected_snapshot"`
	ExpectedCurrentStep  mission.StepKey              `json:"expected_current_step"`
	ExpectedStateVersion uint64                       `json:"expected_state_version"`
	Successor            mission.StepKey              `json:"successor"`
	EventID              ids.MissionEventID           `json:"event_id"`
	OperationID          ids.OperationID              `json:"operation_id"`
	ActorID              ids.ActorID                  `json:"actor_id"`
	GovernanceContextID  ids.GovernanceContextID      `json:"governance_context_id"`
	OccurredAt           time.Time                    `json:"occurred_at"`
	Evidence             []mission.EvidenceRef        `json:"evidence"`
	WorkRevisionID       ids.WorkRevisionID           `json:"work_revision_id"`
}

type approvedScopeInput struct {
	ID   string              `json:"id"`
	Kind workgraph.ScopeKind `json:"kind"`
}

type workDefinitionInput struct {
	WorkID            ids.WorkID              `json:"work_id"`
	Title             string                  `json:"title"`
	Description       string                  `json:"description"`
	DesignContext     string                  `json:"design_context"`
	CrossDomainReason string                  `json:"cross_domain_reason"`
	Criteria          []tracker.WorkCriterion `json:"criteria"`
	Dependencies      []ids.WorkID            `json:"dependencies"`
	WriteScopes       []tracker.WriteScope    `json:"write_scopes"`
	Provenance        []tracker.ScopeRef      `json:"provenance"`
}

type decomposeRequest struct {
	Database            string                  `json:"database"`
	TrackerStore        string                  `json:"tracker_store"`
	CorrelationID       string                  `json:"correlation_id"`
	MissionID           ids.MissionID           `json:"mission_id"`
	ProjectID           ids.ProjectID           `json:"project_id"`
	Provider            string                  `json:"provider"`
	ExpectedMission     mission.MissionSnapshot `json:"expected_mission"`
	PlanRevisionID      string                  `json:"plan_revision_id"`
	PlanContentDigest   string                  `json:"plan_content_digest"`
	ApprovedScope       []approvedScopeInput    `json:"approved_scope"`
	Work                []workDefinitionInput   `json:"work"`
	ActorID             ids.ActorID             `json:"actor_id"`
	OperationID         ids.OperationID         `json:"operation_id"`
	GovernanceContextID ids.GovernanceContextID `json:"governance_context_id"`
	OccurredAt          time.Time               `json:"occurred_at"`
}

type nextRequest struct {
	Database      string        `json:"database"`
	CorrelationID string        `json:"correlation_id"`
	MissionID     ids.MissionID `json:"mission_id"`
}

type decompositionObservationResult struct {
	WorkID             ids.WorkID          `json:"work_id"`
	Provider           string              `json:"provider"`
	ExternalID         string              `json:"external_id"`
	ProviderRevision   tracker.Revision    `json:"provider_revision"`
	Definition         workDefinitionInput `json:"definition"`
	MetadataSchema     uint16              `json:"metadata_schema"`
	CreatedByOperation ids.OperationID     `json:"created_by_operation"`
}

type transitionResultView struct {
	Mission   mission.MissionSnapshot  `json:"mission"`
	Audit     transitionAuditView      `json:"audit"`
	Authority *transitionAuthorityView `json:"authority,omitempty"`
	Replayed  bool                     `json:"replayed"`
}

type transitionAuditView struct {
	MissionID            ids.MissionID                         `json:"mission_id"`
	OldStep              mission.StepKey                       `json:"old_step"`
	NewStep              mission.StepKey                       `json:"new_step"`
	OldStateVersion      uint64                                `json:"old_state_version"`
	NewStateVersion      uint64                                `json:"new_state_version"`
	OperationID          ids.OperationID                       `json:"operation_id"`
	EventID              ids.MissionEventID                    `json:"event_id"`
	ActorID              ids.ActorID                           `json:"actor_id"`
	DefinitionCommitment mission.DefinitionCommitment          `json:"definition_commitment"`
	GovernanceContextID  ids.GovernanceContextID               `json:"governance_context_id"`
	ArtifactRevisionID   string                                `json:"artifact_revision_id,omitempty"`
	ApprovalEvidenceID   ids.EvidenceID                        `json:"approval_evidence_id,omitempty"`
	AuthorityCommitment  mission.TransitionAuthorityCommitment `json:"authority_commitment,omitempty"`
	OccurredAt           time.Time                             `json:"occurred_at"`
}

type transitionAuthorityView struct {
	RequestCommitment          mission.RequestCommitment             `json:"request_commitment"`
	Commitment                 mission.TransitionAuthorityCommitment `json:"commitment"`
	MissionID                  string                                `json:"mission_id"`
	ArtifactKind               artifact.Kind                         `json:"artifact_kind"`
	ArtifactRevisionID         string                                `json:"artifact_revision_id"`
	ApprovalID                 string                                `json:"approval_id"`
	ArtifactEvidenceCommitment string                                `json:"artifact_evidence_commitment"`
}

func RunMission(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: mission initialize|transition|decompose|next --request-json <json>")
		return 2
	}
	switch args[0] {
	case "initialize":
		var request initializeRequest
		if code := decodeMethodRequest(args[1:], &request, stdout, stderr); code >= 0 {
			return code
		}
		return runMissionInitialize(ctx, request, stdout)
	case "transition":
		var request transitionRequest
		if code := decodeMethodRequest(args[1:], &request, stdout, stderr); code >= 0 {
			return code
		}
		return runMissionTransition(ctx, request, stdout)
	case "decompose":
		var request decomposeRequest
		if code := decodeMethodRequest(args[1:], &request, stdout, stderr); code >= 0 {
			return code
		}
		return runMissionDecompose(ctx, request, stdout)
	case "next":
		var request nextRequest
		if code := decodeMethodRequest(args[1:], &request, stdout, stderr); code >= 0 {
			return code
		}
		return runMissionNext(ctx, request, stdout)
	default:
		fmt.Fprintln(stderr, "usage: mission initialize|transition|decompose|next --request-json <json>")
		return 2
	}
}

func runMissionInitialize(ctx context.Context, request initializeRequest, stdout io.Writer) int {
	if request.DefinitionID != softwaredev.DefinitionID || request.DefinitionVersion != softwaredev.DefinitionVersion {
		return encodeMethodFailure(stdout, "mission_initialize_refused", request.CorrelationID, request.OperationID, map[string]string{"mission_id": request.MissionID.String()}, errors.New("unsupported mission definition"))
	}
	if _, err := ids.ParseProcessID(request.ProcessID.String()); err != nil || request.Governance.Validate() != nil {
		return encodeMethodFailure(stdout, "mission_initialize_refused", request.CorrelationID, request.OperationID, map[string]string{"mission_id": request.MissionID.String()}, errors.New("complete creation provenance is required"))
	}
	definition, err := softwaredev.Definition()
	if err != nil {
		return encodeMethodFailure(stdout, "mission_initialize_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	registry, err := mission.NewRegistry(definition)
	if err != nil {
		return encodeMethodFailure(stdout, "mission_initialize_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	store, err := sqlite.Open(ctx, request.Database)
	if err != nil {
		return encodeMethodFailure(stdout, "mission_initialize_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	defer store.Close()
	result, err := mission.NewService(registry, store).Create(ctx, mission.CreateRequest{MissionID: request.MissionID, ProjectID: request.ProjectID, DefinitionID: request.DefinitionID, DefinitionVersion: request.DefinitionVersion, OperationID: request.OperationID, ActorID: request.ActorID, ProcessID: request.ProcessID, Governance: request.Governance, OccurredAt: request.OccurredAt.UTC()})
	if err != nil {
		return encodeMethodFailure(stdout, "mission_initialize_refused", request.CorrelationID, request.OperationID, map[string]string{"mission_id": request.MissionID.String()}, err)
	}
	return encodeMethodSuccess(stdout, "mission_initialized", request.CorrelationID, request.OperationID, map[string]string{"mission_id": request.MissionID.String(), "project_id": request.ProjectID.String()}, struct {
		Mission             mission.MissionSnapshot `json:"mission"`
		ActorID             ids.ActorID             `json:"actor_id"`
		ProcessID           ids.ProcessID           `json:"process_id"`
		GovernanceContextID ids.GovernanceContextID `json:"governance_context_id"`
		GovernanceAggregate string                  `json:"governance_aggregate"`
		Replayed            bool                    `json:"replayed"`
	}{normalizeMissionSnapshot(result.Instance), result.ActorID, result.ProcessID, result.Governance.ID(), result.Governance.Aggregate().Algorithm() + ":" + result.Governance.Aggregate().Digest(), result.Replayed})
}

func runMissionTransition(ctx context.Context, request transitionRequest, stdout io.Writer) int {
	definition, err := softwaredev.Definition()
	if err != nil || request.DefinitionID != definition.ID() || request.DefinitionVersion != definition.Version() || request.DefinitionCommitment != definition.Commitment() {
		return encodeMethodFailure(stdout, "mission_transition_refused", request.CorrelationID, request.OperationID, map[string]string{"mission_id": request.MissionID.String()}, errors.New("definition mismatch"))
	}
	registry, _ := mission.NewRegistry(definition)
	store, err := sqlite.Open(ctx, request.Database)
	if err != nil {
		return encodeMethodFailure(stdout, "mission_transition_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	defer store.Close()
	result, err := mission.NewServiceWithArtifactApprovals(registry, store, store).Transition(ctx, mission.TransitionRequest{MissionID: request.MissionID, DefinitionID: request.DefinitionID, DefinitionVersion: request.DefinitionVersion, DefinitionCommitment: request.DefinitionCommitment, ExpectedSnapshot: request.ExpectedSnapshot, ExpectedCurrentStep: request.ExpectedCurrentStep, ExpectedStateVersion: request.ExpectedStateVersion, Successor: request.Successor, EventID: request.EventID, OperationID: request.OperationID, ActorID: request.ActorID, GovernanceContextID: request.GovernanceContextID, OccurredAt: request.OccurredAt.UTC(), Evidence: request.Evidence, WorkRevisionID: request.WorkRevisionID})
	if err != nil {
		return encodeMethodFailure(stdout, "mission_transition_refused", request.CorrelationID, request.OperationID, map[string]string{"mission_id": request.MissionID.String()}, err)
	}
	view := transitionResultView{Mission: normalizeMissionSnapshot(result.Instance), Audit: transitionAuditView{MissionID: result.Audit.MissionID, OldStep: result.Audit.OldStep, NewStep: result.Audit.NewStep, OldStateVersion: result.Audit.OldStateVersion, NewStateVersion: result.Audit.NewStateVersion, OperationID: result.Audit.OperationID, EventID: result.Audit.EventID, ActorID: result.Audit.ActorID, DefinitionCommitment: result.Audit.DefinitionCommitment, GovernanceContextID: result.Audit.GovernanceContextID, ArtifactRevisionID: result.Audit.ArtifactRevisionID, ApprovalEvidenceID: result.Audit.ApprovalEvidenceID, AuthorityCommitment: result.Audit.AuthorityCommitment, OccurredAt: result.Audit.OccurredAt}, Replayed: result.Replayed}
	if result.Authority.Required() {
		evidence := result.Authority.ArtifactApproval()
		view.Authority = &transitionAuthorityView{RequestCommitment: result.Authority.RequestCommitment(), Commitment: result.Authority.Commitment(), MissionID: evidence.MissionID(), ArtifactKind: evidence.Kind(), ArtifactRevisionID: evidence.ArtifactRevisionID(), ApprovalID: evidence.ApprovalID(), ArtifactEvidenceCommitment: evidence.Commitment()}
	}
	return encodeMethodSuccess(stdout, "mission_transitioned", request.CorrelationID, request.OperationID, map[string]string{"mission_id": request.MissionID.String()}, view)
}

func runMissionDecompose(ctx context.Context, request decomposeRequest, stdout io.Writer) int {
	if request.Provider != "durable_fake" {
		return encodeMethodFailure(stdout, "mission_decomposition_refused", request.CorrelationID, request.OperationID, map[string]string{"mission_id": request.MissionID.String()}, errors.New("unsupported portable provider"))
	}
	definitions, proposals, err := buildWorkDefinitions(request.ProjectID, request.Work)
	if err != nil {
		return encodeMethodFailure(stdout, "mission_decomposition_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	approved := make([]workgraph.ApprovedScope, len(request.ApprovedScope))
	for index, scope := range request.ApprovedScope {
		approved[index] = workgraph.ApprovedScope{ID: scope.ID, Kind: scope.Kind, MissionID: request.MissionID}
	}
	store, err := sqlite.Open(ctx, request.Database)
	if err != nil {
		return encodeMethodFailure(stdout, "mission_decomposition_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	defer store.Close()
	providerStore, err := fake.Open(request.TrackerStore)
	if err != nil {
		return encodeMethodFailure(stdout, "mission_decomposition_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	result, err := mission.NewDecompositionService(store, store, fake.NewProvider(providerStore)).Decompose(ctx, mission.DecompositionCommand{MissionID: request.MissionID, ProjectID: request.ProjectID, Provider: request.Provider, ExpectedMission: request.ExpectedMission, PlanRevisionID: request.PlanRevisionID, PlanContentDigest: request.PlanContentDigest, ApprovedScope: approved, Work: proposals, ActorID: request.ActorID, OperationID: request.OperationID, GovernanceContextID: request.GovernanceContextID, OccurredAt: request.OccurredAt.UTC()})
	if err != nil {
		return encodeMethodFailure(stdout, "mission_decomposition_refused", request.CorrelationID, request.OperationID, map[string]string{"mission_id": request.MissionID.String()}, err)
	}
	items := make([]decompositionObservationResult, 0, len(result.Observations))
	byID := make(map[ids.WorkID]workDefinitionInput, len(definitions))
	for index, definition := range definitions {
		byID[definition.WorkID()] = request.Work[index]
	}
	for _, observation := range result.Observations {
		ref, provider := observation.ExternalRef(), observation.ProviderObservation()
		items = append(items, decompositionObservationResult{WorkID: ref.WorkID, Provider: ref.Provider, ExternalID: ref.ExternalID, ProviderRevision: provider.Revision, Definition: byID[ref.WorkID], MetadataSchema: ref.MetadataSchema, CreatedByOperation: ref.CreatedByOperation})
	}
	return encodeMethodSuccess(stdout, "mission_decomposed", request.CorrelationID, request.OperationID, map[string]string{"mission_id": request.MissionID.String()}, struct {
		Status        mission.DecompositionStatus      `json:"status"`
		RequestDigest string                           `json:"request_digest"`
		Observations  []decompositionObservationResult `json:"observations"`
		Findings      []workgraph.Finding              `json:"findings"`
		Replayed      bool                             `json:"replayed"`
	}{result.Status, result.RequestDigest, items, result.Findings, result.Replayed})
}

type providerRefInput struct {
	WorkID             ids.WorkID       `json:"work_id"`
	ExternalID         string           `json:"external_id"`
	MetadataSchema     uint16           `json:"metadata_schema"`
	CreatedByOperation ids.OperationID  `json:"created_by_operation"`
	LastObservation    tracker.Revision `json:"last_observation"`
}

type finalizeRequest struct {
	Database                  string                  `json:"database"`
	TrackerStore              string                  `json:"tracker_store"`
	CorrelationID             string                  `json:"correlation_id"`
	MissionID                 ids.MissionID           `json:"mission_id"`
	ProjectID                 ids.ProjectID           `json:"project_id"`
	ExpectedMission           mission.MissionSnapshot `json:"expected_mission"`
	ExpectedCurrentGeneration uint64                  `json:"expected_current_generation"`
	ProposedRevisionID        ids.WorkRevisionID      `json:"proposed_revision_id"`
	PlanningSourceCommit      string                  `json:"planning_source_commit"`
	PlanRevisionID            string                  `json:"plan_revision_id"`
	PlanDigest                string                  `json:"plan_digest"`
	ApprovedScope             []approvedScopeInput    `json:"approved_scope"`
	Work                      []workDefinitionInput   `json:"work"`
	ProviderRefs              []providerRefInput      `json:"provider_refs"`
	ActorID                   ids.ActorID             `json:"actor_id"`
	OperationID               ids.OperationID         `json:"operation_id"`
	GovernanceContextID       ids.GovernanceContextID `json:"governance_context_id"`
	OccurredAt                time.Time               `json:"occurred_at"`
}

type driftRequest struct {
	Database          string             `json:"database"`
	TrackerStore      string             `json:"tracker_store"`
	CorrelationID     string             `json:"correlation_id"`
	MissionID         ids.MissionID      `json:"mission_id"`
	OldRevision       json.RawMessage    `json:"old_revision"`
	CandidateRevision json.RawMessage    `json:"candidate_revision"`
	ProviderRefs      []providerRefInput `json:"provider_refs"`
	ObservedAt        time.Time          `json:"observed_at"`
}

type reconcileDecisionInput struct {
	OldWorkID   ids.WorkID            `json:"old_work_id"`
	Status      workgraph.WorkStatus  `json:"status"`
	Disposition workgraph.Disposition `json:"disposition"`
	NewWorkIDs  []ids.WorkID          `json:"new_work_ids"`
	ForkEdges   []workgraph.ForkEdge  `json:"fork_edges"`
	Rationale   string                `json:"rationale"`
}

type reconcileRequest struct {
	Database                  string                   `json:"database"`
	CorrelationID             string                   `json:"correlation_id"`
	MissionID                 ids.MissionID            `json:"mission_id"`
	ProjectID                 ids.ProjectID            `json:"project_id"`
	ExpectedMission           mission.MissionSnapshot  `json:"expected_mission"`
	ExpectedCurrentGeneration uint64                   `json:"expected_current_generation"`
	OldRevision               json.RawMessage          `json:"old_revision"`
	CandidateRevision         json.RawMessage          `json:"candidate_revision"`
	DriftReport               json.RawMessage          `json:"drift_report"`
	ReconciliationID          ids.OperationID          `json:"reconciliation_id"`
	Decisions                 []reconcileDecisionInput `json:"decisions"`
	OperationID               ids.OperationID          `json:"operation_id"`
	ActorID                   ids.ActorID              `json:"actor_id"`
	GovernanceContextID       ids.GovernanceContextID  `json:"governance_context_id"`
	Rationale                 string                   `json:"rationale"`
	OccurredAt                time.Time                `json:"occurred_at"`
}

func RunMissionWork(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: work finalize|drift|reconcile --request-json <json>")
		return 2
	}
	switch args[0] {
	case "finalize":
		var request finalizeRequest
		if code := decodeMethodRequest(args[1:], &request, stdout, stderr); code >= 0 {
			return code
		}
		return runWorkFinalize(ctx, request, stdout)
	case "drift":
		var request driftRequest
		if code := decodeMethodRequest(args[1:], &request, stdout, stderr); code >= 0 {
			return code
		}
		return runWorkDrift(ctx, request, stdout)
	case "reconcile":
		var request reconcileRequest
		if code := decodeMethodRequest(args[1:], &request, stdout, stderr); code >= 0 {
			return code
		}
		return runWorkReconcile(ctx, request, stdout)
	default:
		fmt.Fprintln(stderr, "usage: work finalize|drift|reconcile --request-json <json>")
		return 2
	}
}

type fakeFinalizationSource struct {
	projectID      ids.ProjectID
	missionID      ids.MissionID
	planRevisionID string
	planDigest     string
	approved       []workgraph.ApprovedScope
	definitions    map[ids.WorkID]tracker.WorkDefinition
	refs           []providerRefInput
	provider       *fake.Provider
}

func (source fakeFinalizationSource) ObserveFinalization(ctx context.Context, missionID ids.MissionID) (workgraph.FinalizationObservation, error) {
	if missionID != source.missionID {
		return workgraph.FinalizationObservation{}, errors.New("mission observation scope changed")
	}
	work := make([]workgraph.WorkObservation, 0, len(source.refs))
	evidence := make([]workgraph.ObservationEvidence, 0, len(source.refs))
	for _, input := range source.refs {
		definition, ok := source.definitions[input.WorkID]
		if !ok {
			return workgraph.FinalizationObservation{}, errors.New("provider mapping references unknown work")
		}
		ref := tracker.ExternalWorkRef{ProjectID: source.projectID, WorkID: input.WorkID, Provider: "durable_fake", ExternalID: input.ExternalID, MetadataSchema: input.MetadataSchema, CreatedByOperation: input.CreatedByOperation, LastObservation: input.LastObservation}
		observed, err := source.provider.ObserveDefinition(ctx, ref)
		if err != nil {
			return workgraph.FinalizationObservation{}, err
		}
		if !bytes.Equal(observed.Definition().CanonicalData(), definition.CanonicalData()) || observed.ExternalRef().Provider != "durable_fake" {
			return workgraph.FinalizationObservation{}, errors.New("provider semantic definition changed")
		}
		semantic, err := semanticWorkObservation(definition)
		if err != nil {
			return workgraph.FinalizationObservation{}, err
		}
		providerObservation := observed.ProviderObservation()
		digest := fmt.Sprintf("%x", sha256.Sum256(definition.CanonicalData()))
		hints := map[string]string{}
		for _, criterion := range definition.Criteria() {
			if criterion.EvidenceHint != "" {
				hints[criterion.ID] = criterion.EvidenceHint
			}
		}
		work = append(work, semantic)
		evidence = append(evidence, workgraph.ObservationEvidence{WorkID: input.WorkID, Provider: "durable_fake", ExternalID: input.ExternalID, Revision: fmt.Sprint(providerObservation.Revision), MetadataSchema: input.MetadataSchema, DefinitionDigest: digest, CriterionEvidenceHints: hints})
	}
	draft, err := workgraph.NewDraftObservation(source.projectID, source.missionID, source.planRevisionID, source.planDigest, source.approved, work)
	if err != nil {
		return workgraph.FinalizationObservation{}, err
	}
	return workgraph.NewFinalizationObservation(draft, evidence)
}

func semanticWorkObservation(definition tracker.WorkDefinition) (workgraph.WorkObservation, error) {
	criteria := make([]workgraph.CriterionObservation, 0, len(definition.Criteria()))
	for _, item := range definition.Criteria() {
		requirement := workgraph.CriterionOptional
		if item.Required {
			requirement = workgraph.CriterionRequired
		}
		criteria = append(criteria, workgraph.CriterionObservation{ID: item.ID, Text: item.Text, Outcome: item.Outcome, Verifier: string(item.Verifier), Requirement: requirement})
	}
	scopes := make([]workgraph.WriteScopeObservation, 0, len(definition.WriteScopes()))
	for _, item := range definition.WriteScopes() {
		scopes = append(scopes, workgraph.WriteScopeObservation{Path: item.Path, Domain: item.Domain, Access: workgraph.ScopeAccess(item.Access), SerializationKey: item.SerializationKey})
	}
	provenance := make([]workgraph.ScopeReference, 0, len(definition.Provenance()))
	for _, item := range definition.Provenance() {
		provenance = append(provenance, workgraph.ScopeReference{ID: item.ID, Kind: workgraph.ScopeKind(item.Kind)})
	}
	return workgraph.NewWorkObservation(workgraph.WorkObservationParams{ProjectID: definition.ProjectID(), WorkID: definition.WorkID(), Title: definition.Title(), Description: definition.Description(), DesignContext: definition.DesignContext(), CrossDomainReason: definition.CrossDomainReason(), Criteria: criteria, Dependencies: definition.Dependencies(), WriteScopes: scopes, Provenance: provenance})
}

func runWorkFinalize(ctx context.Context, request finalizeRequest, stdout io.Writer) int {
	definitions, _, err := buildWorkDefinitions(request.ProjectID, request.Work)
	if err != nil {
		return encodeMethodFailure(stdout, "work_finalization_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	definitionMap := make(map[ids.WorkID]tracker.WorkDefinition, len(definitions))
	for _, definition := range definitions {
		definitionMap[definition.WorkID()] = definition
	}
	approved := make([]workgraph.ApprovedScope, len(request.ApprovedScope))
	for index, item := range request.ApprovedScope {
		approved[index] = workgraph.ApprovedScope{ID: item.ID, Kind: item.Kind, MissionID: request.MissionID}
	}
	providerStore, err := fake.Open(request.TrackerStore)
	if err != nil {
		return encodeMethodFailure(stdout, "work_finalization_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	store, err := sqlite.Open(ctx, request.Database)
	if err != nil {
		return encodeMethodFailure(stdout, "work_finalization_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	defer store.Close()
	source := fakeFinalizationSource{request.ProjectID, request.MissionID, request.PlanRevisionID, request.PlanDigest, approved, definitionMap, request.ProviderRefs, fake.NewProvider(providerStore)}
	result, err := mission.NewFinalizationService(source, store).Finalize(ctx, mission.FinalizationCommand{MissionID: request.MissionID, ProjectID: request.ProjectID, ExpectedMission: request.ExpectedMission, ExpectedCurrentGeneration: request.ExpectedCurrentGeneration, ProposedRevisionID: request.ProposedRevisionID, RequestedGeneration: 1, PlanningSourceCommit: request.PlanningSourceCommit, ActorID: request.ActorID, OperationID: request.OperationID, GovernanceContextID: request.GovernanceContextID, OccurredAt: request.OccurredAt.UTC()})
	if err != nil {
		return encodeMethodFailure(stdout, "work_finalization_refused", request.CorrelationID, request.OperationID, map[string]string{"mission_id": request.MissionID.String()}, err)
	}
	encoded, _ := result.Revision.Encode()
	return encodeMethodSuccess(stdout, "work_finalized", request.CorrelationID, request.OperationID, map[string]string{"mission_id": request.MissionID.String(), "work_revision_id": result.Revision.ID().String()}, struct {
		Revision      json.RawMessage             `json:"revision"`
		Mission       mission.MissionSnapshot     `json:"mission"`
		RequestDigest string                      `json:"request_digest"`
		Receipt       mission.FinalizationReceipt `json:"receipt"`
		Replayed      bool                        `json:"replayed"`
	}{encoded, normalizeMissionSnapshot(result.Mission), result.RequestDigest, result.Receipt, result.Replayed})
}

func runWorkDrift(ctx context.Context, request driftRequest, stdout io.Writer) int {
	oldRevision, err := workgraph.DecodeRevisionV2(request.OldRevision)
	if err != nil {
		return encodeMethodFailure(stdout, "work_drift_refused", request.CorrelationID, "", nil, err)
	}
	candidate, err := workgraph.DecodeRevisionV2(request.CandidateRevision)
	if err != nil {
		return encodeMethodFailure(stdout, "work_drift_refused", request.CorrelationID, "", nil, err)
	}
	store, err := sqlite.Open(ctx, request.Database)
	if err != nil {
		return encodeMethodFailure(stdout, "work_drift_refused", request.CorrelationID, "", nil, err)
	}
	persisted, err := store.WorkRevisionV2(ctx, oldRevision.ID())
	if err != nil {
		_ = store.Close()
		return encodeMethodFailure(stdout, "work_drift_refused", request.CorrelationID, "", nil, err)
	}
	current, currentErr := store.ObserveMissionMethod(ctx, request.MissionID)
	_ = store.Close()
	if currentErr != nil || current.Workgraph.RevisionID() != oldRevision.ID() {
		return encodeMethodFailure(stdout, "work_drift_refused", request.CorrelationID, "", nil, errors.New("old revision is not the exact current generation"))
	}
	persistedBytes, _ := persisted.Encode()
	if !bytes.Equal(persistedBytes, request.OldRevision) || oldRevision.MissionID() != request.MissionID {
		return encodeMethodFailure(stdout, "work_drift_refused", request.CorrelationID, "", nil, errors.New("old revision is not exact current authority"))
	}
	providerStore, err := fake.Open(request.TrackerStore)
	if err != nil {
		return encodeMethodFailure(stdout, "work_drift_refused", request.CorrelationID, "", nil, err)
	}
	provider := fake.NewProvider(providerStore)
	observations := make(map[ids.WorkID]workgraph.ProviderObservationV2)
	for _, item := range candidate.ProviderObservations() {
		observations[item.WorkID] = item
	}
	if len(request.ProviderRefs) != len(observations) {
		return encodeMethodFailure(stdout, "work_drift_refused", request.CorrelationID, "", nil, errors.New("candidate provider observation set is incomplete"))
	}
	seenRefs := make(map[ids.WorkID]struct{}, len(request.ProviderRefs))
	for _, input := range request.ProviderRefs {
		if _, duplicate := seenRefs[input.WorkID]; duplicate {
			return encodeMethodFailure(stdout, "work_drift_refused", request.CorrelationID, "", nil, errors.New("candidate provider observation set is ambiguous"))
		}
		seenRefs[input.WorkID] = struct{}{}
		observation, observeErr := provider.ObserveDefinition(ctx, tracker.ExternalWorkRef{ProjectID: candidate.ProjectID(), WorkID: input.WorkID, Provider: "durable_fake", ExternalID: input.ExternalID, MetadataSchema: input.MetadataSchema, CreatedByOperation: input.CreatedByOperation, LastObservation: input.LastObservation})
		want, ok := observations[input.WorkID]
		if observeErr != nil || !ok || want.ExternalID != input.ExternalID || want.Revision != fmt.Sprint(observation.ProviderObservation().Revision) {
			return encodeMethodFailure(stdout, "work_drift_refused", request.CorrelationID, "", nil, errors.New("candidate provider observation is not current"))
		}
	}
	report, err := workgraph.DetectDrift(oldRevision, candidate, request.ObservedAt.UTC())
	if err != nil {
		return encodeMethodFailure(stdout, "work_drift_refused", request.CorrelationID, "", nil, err)
	}
	encoded, _ := report.Encode()
	return encodeMethodSuccess(stdout, "work_drift_observed", request.CorrelationID, "", map[string]string{"mission_id": request.MissionID.String(), "old_work_revision_id": oldRevision.ID().String(), "candidate_work_revision_id": candidate.ID().String()}, json.RawMessage(encoded))
}

func runWorkReconcile(ctx context.Context, request reconcileRequest, stdout io.Writer) int {
	oldRevision, err := workgraph.DecodeRevisionV2(request.OldRevision)
	if err != nil {
		return encodeMethodFailure(stdout, "work_reconciliation_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	candidate, err := workgraph.DecodeRevisionV2(request.CandidateRevision)
	if err != nil {
		return encodeMethodFailure(stdout, "work_reconciliation_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	var driftHeader struct {
		ObservedAt time.Time `json:"observed_at"`
	}
	if err = json.Unmarshal(request.DriftReport, &driftHeader); err != nil {
		return encodeMethodFailure(stdout, "work_reconciliation_refused", request.CorrelationID, request.OperationID, nil, errors.New("drift report is invalid"))
	}
	drift, err := workgraph.DetectDrift(oldRevision, candidate, driftHeader.ObservedAt.UTC())
	canonicalDrift, encodeErr := drift.Encode()
	if err != nil || encodeErr != nil || !bytes.Equal(canonicalDrift, request.DriftReport) {
		return encodeMethodFailure(stdout, "work_reconciliation_refused", request.CorrelationID, request.OperationID, nil, errors.New("drift report is not exact canonical evidence"))
	}
	statuses := make([]workgraph.WorkStatusAssignment, len(request.Decisions))
	decisions := make([]workgraph.WorkDispositionDecisionParams, len(request.Decisions))
	for index, item := range request.Decisions {
		statuses[index] = workgraph.WorkStatusAssignment{WorkID: item.OldWorkID, Status: item.Status}
		decisions[index] = workgraph.WorkDispositionDecisionParams{OldWorkID: item.OldWorkID, Disposition: item.Disposition, NewWorkIDs: item.NewWorkIDs, ForkEdges: item.ForkEdges, Rationale: item.Rationale}
	}
	plan, err := workgraph.ReconcileMixed(workgraph.MixedReconcileRequest{ID: request.ReconciliationID.String(), Old: oldRevision, New: candidate, Drift: drift, Statuses: statuses, Decisions: decisions, ActorID: request.ActorID, Rationale: request.Rationale, OccurredAt: request.OccurredAt.UTC()})
	if err != nil {
		return encodeMethodFailure(stdout, "work_reconciliation_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	store, err := sqlite.Open(ctx, request.Database)
	if err != nil {
		return encodeMethodFailure(stdout, "work_reconciliation_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	defer store.Close()
	result, err := mission.NewSuccessorPublicationService(store).Publish(ctx, mission.SuccessorPublicationCommand{MissionID: request.MissionID, ProjectID: request.ProjectID, ExpectedMission: request.ExpectedMission, ExpectedCurrentGeneration: request.ExpectedCurrentGeneration, ExpectedOld: oldRevision, Candidate: candidate, Drift: drift, ReconciliationID: request.ReconciliationID, Reconciliation: plan, OperationID: request.OperationID, ActorID: request.ActorID, GovernanceContextID: request.GovernanceContextID, Rationale: request.Rationale, OccurredAt: request.OccurredAt.UTC()})
	if err != nil {
		return encodeMethodFailure(stdout, "work_reconciliation_refused", request.CorrelationID, request.OperationID, map[string]string{"mission_id": request.MissionID.String()}, err)
	}
	encodedRevision, _ := result.Revision.Encode()
	return encodeMethodSuccess(stdout, "work_reconciled", request.CorrelationID, request.OperationID, map[string]string{"mission_id": request.MissionID.String(), "old_work_revision_id": oldRevision.ID().String(), "new_work_revision_id": result.Revision.ID().String(), "reconciliation_id": request.ReconciliationID.String()}, struct {
		Revision      json.RawMessage           `json:"revision"`
		Mission       mission.MissionSnapshot   `json:"mission"`
		Actions       []mission.AuthorityAction `json:"actions"`
		RequestDigest string                    `json:"request_digest"`
		Replayed      bool                      `json:"replayed"`
	}{encodedRevision, normalizeMissionSnapshot(result.Mission), result.Actions, result.RequestDigest, result.Replayed})
}

func normalizeMissionSnapshot(snapshot mission.MissionSnapshot) mission.MissionSnapshot {
	snapshot.ActiveBlockers = append([]ids.BlockerID{}, snapshot.ActiveBlockers...)
	snapshot.UnresolvedDecisions = append([]ids.DecisionID{}, snapshot.UnresolvedDecisions...)
	return snapshot
}

func runMissionNext(ctx context.Context, request nextRequest, stdout io.Writer) int {
	definition, err := softwaredev.Definition()
	if err != nil {
		return encodeMethodFailure(stdout, "mission_next_refused", request.CorrelationID, "", nil, err)
	}
	registry, _ := mission.NewRegistry(definition)
	store, err := sqlite.Open(ctx, request.Database)
	if err != nil {
		return encodeMethodFailure(stdout, "mission_next_refused", request.CorrelationID, "", nil, err)
	}
	defer store.Close()
	adapter, err := sqlite.NewMissionActionAdapter(store, registry)
	if err != nil {
		return encodeMethodFailure(stdout, "mission_next_refused", request.CorrelationID, "", nil, err)
	}
	result, err := mission.Next(ctx, missionNextSource{adapter}, request.MissionID)
	if err != nil {
		return encodeMethodFailure(stdout, "mission_next_refused", request.CorrelationID, "", map[string]string{"mission_id": request.MissionID.String()}, err)
	}
	return encodeMethodSuccess(stdout, "mission_next", request.CorrelationID, "", map[string]string{"mission_id": request.MissionID.String()}, result)
}

type missionNextSource struct{ adapter *sqlite.MissionActionAdapter }

func (source missionNextSource) ObserveNext(ctx context.Context, missionID ids.MissionID) (mission.NextSnapshot, error) {
	return source.adapter.ObserveAction(ctx, missionID)
}

func buildWorkDefinitions(projectID ids.ProjectID, inputs []workDefinitionInput) ([]tracker.WorkDefinition, []mission.WorkProposal, error) {
	definitions := make([]tracker.WorkDefinition, len(inputs))
	proposals := make([]mission.WorkProposal, len(inputs))
	for index, input := range inputs {
		metadata, err := tracker.NewOpaqueMetadata([]byte(`{}`))
		if err != nil {
			return nil, nil, err
		}
		definition, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{ProjectID: projectID, WorkID: input.WorkID, Title: input.Title, Description: input.Description, DesignContext: input.DesignContext, CrossDomainReason: input.CrossDomainReason, Criteria: input.Criteria, Dependencies: input.Dependencies, WriteScopes: input.WriteScopes, Provenance: input.Provenance, MetadataSchema: tracker.WorkDefinitionMetadataSchemaV1, ProviderMetadata: metadata})
		if err != nil {
			return nil, nil, err
		}
		definitions[index] = definition
		proposals[index] = mission.WorkProposal{Definition: definition}
	}
	return definitions, proposals, nil
}

func decodeMethodRequest(args []string, target any, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mission-method", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var raw string
	flags.StringVar(&raw, "request-json", "", "strict typed JSON request")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if len(flags.Args()) != 0 || raw == "" || len(raw) > maxMissionMethodRequest {
		return encodeInvalidMethodInvocation(stdout, errors.New("request-json is required and bounded"))
	}
	if err := rejectDuplicateKeys([]byte(raw)); err != nil {
		return encodeInvalidMethodInvocation(stdout, err)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return encodeInvalidMethodInvocation(stdout, errors.New("request JSON is invalid"))
	}
	var header struct {
		CorrelationID string `json:"correlation_id"`
	}
	if err := json.Unmarshal([]byte(raw), &header); err != nil || !boundedCorrelationID(header.CorrelationID) {
		return encodeInvalidMethodInvocation(stdout, errors.New("correlation identity is required and bounded"))
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return encodeInvalidMethodInvocation(stdout, errors.New("request JSON has trailing content"))
	}
	return -1
}

func encodeInvalidMethodInvocation(stdout io.Writer, err error) int {
	encodeMethodFailure(stdout, "invalid_invocation", "", "", nil, err)
	return 2
}

func rejectDuplicateKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if token == nil {
			return errors.New("null JSON values are not accepted")
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("invalid object key")
				}
				if _, exists := seen[key]; exists {
					return errors.New("duplicate JSON field")
				}
				seen[key] = struct{}{}
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return errors.New("invalid JSON delimiter")
		}
	}
	return walk()
}

func encodeMethodSuccess(stdout io.Writer, kind, correlation string, operation ids.OperationID, aggregates map[string]string, result any) int {
	if aggregates == nil {
		aggregates = map[string]string{}
	}
	retry := "replay_same_operation"
	if operation == "" {
		retry = "safe_repeat_query"
	}
	_ = json.NewEncoder(stdout).Encode(MethodResponse{SchemaVersion: missionMethodSchemaVersion, OK: true, Kind: kind, CorrelationID: correlation, OperationID: operation, AggregateIDs: aggregates, RetrySafety: retry, Recovery: "none", Result: result})
	return 0
}

func encodeMethodFailure(stdout io.Writer, kind, correlation string, operation ids.OperationID, aggregates map[string]string, err error) int {
	if aggregates == nil {
		aggregates = map[string]string{}
	}
	diagnostic := boundedMethodDiagnostic(err)
	_ = json.NewEncoder(stdout).Encode(MethodResponse{SchemaVersion: missionMethodSchemaVersion, OK: false, Kind: kind, CorrelationID: correlation, OperationID: operation, AggregateIDs: aggregates, RetrySafety: retrySafetyForDiagnostic(diagnostic), Recovery: diagnostic.Recovery, Error: &diagnostic})
	return 0
}

func boundedCorrelationID(value string) bool {
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00")
}

func retrySafetyForDiagnostic(diagnostic MethodDiagnostic) string {
	if !diagnostic.RetrySafe {
		return "not_safe"
	}
	if strings.Contains(diagnostic.Recovery, "same_operation") || strings.Contains(diagnostic.Recovery, "retry") {
		return "retry_same_operation"
	}
	return "refresh_then_retry"
}

func boundedMethodDiagnostic(err error) MethodDiagnostic {
	result := MethodDiagnostic{Kind: "invalid_request", Recovery: "correct_input", Message: "the typed request was refused"}
	var missionError *mission.Error
	var decompositionError *mission.DecompositionError
	var finalizationError *mission.FinalizationError
	var artifactError *artifact.Error
	var graphError *workgraph.Error
	var projectionError *projection.Error
	var trackerError *tracker.Error
	switch {
	case errors.As(err, &missionError):
		result.Kind, result.Recovery, result.Field, result.Message = string(missionError.Kind), string(missionError.Recovery), missionError.Field, missionError.Message
	case errors.As(err, &decompositionError):
		result.Kind, result.Recovery, result.Field, result.Message = string(decompositionError.Kind), string(decompositionError.Recovery), decompositionError.Field, decompositionError.Message
	case errors.As(err, &finalizationError):
		result.Kind, result.Recovery, result.Field, result.Message = string(finalizationError.Kind), string(finalizationError.Recovery), finalizationError.Field, finalizationError.Message
	case errors.As(err, &artifactError):
		result.Kind, result.Recovery, result.Field, result.Message = string(artifactError.Kind), string(artifactError.Recovery), artifactError.Field, artifactError.Message
	case errors.As(err, &graphError):
		result.Kind, result.Recovery, result.Message = string(graphError.Kind), "correct_input", graphError.Message
	case errors.As(err, &projectionError):
		result.Kind, result.Recovery, result.Field, result.Message, result.RetrySafe = string(projectionError.Kind), string(projectionError.Recovery), projectionError.Field, projectionError.Message, projectionError.RetrySafe
	case errors.As(err, &trackerError):
		result.Kind, result.Recovery, result.Message = string(trackerError.Kind), trackerError.RecoveryCode, "the provider request was refused"
	}
	if result.Recovery == "" {
		result.Recovery = "inspect_state"
	}
	result.RetrySafe = result.RetrySafe || strings.Contains(result.Recovery, "retry") || strings.Contains(result.Recovery, "refresh") || strings.Contains(result.Recovery, "observe")
	if len(result.Message) > 512 {
		result.Message = result.Message[:512]
	}
	if len(result.Field) > 128 {
		result.Field = "request"
	}
	return result
}
