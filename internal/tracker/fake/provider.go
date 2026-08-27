package fake

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

type Provider struct{ store *Store }

func NewProvider(store *Store) *Provider { return &Provider{store: store} }

const providerIdentity = "durable_fake"

var _ tracker.Provider = (*Provider)(nil)
var _ tracker.CapabilityReporter = (*Provider)(nil)
var _ tracker.Catalog = (*Provider)(nil)
var _ tracker.Graph = (*Provider)(nil)
var _ tracker.Discussion = (*Provider)(nil)
var _ tracker.AssignmentProjection = (*Provider)(nil)
var _ tracker.Inspector = (*Provider)(nil)
var _ tracker.Exporter = (*Provider)(nil)
var _ tracker.WorkDefinitionPort = (*Provider)(nil)

func (p *Provider) DefinitionCapabilities(context.Context) (tracker.WorkDefinitionCapabilities, error) {
	return tracker.WorkDefinitionCapabilities{Create: true, GuardedUpdate: true, BlockingGraph: true}, nil
}

func (p *Provider) PrepareDefinitionOperation(ctx context.Context, operationID ids.OperationID, requestDigest string) error {
	if _, err := ids.ParseOperationID(operationID.String()); err != nil || len(requestDigest) != 64 {
		return &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "operation and lowercase SHA-256 request digest are required", Cause: err}
	}
	return p.store.update(ctx, func(state *diskState) error {
		if previous, exists := state.DefinitionRequests[operationID.String()]; exists {
			if previous != requestDigest {
				return &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: "decomposition operation was reused for a different request"}
			}
			return nil
		}
		state.DefinitionRequests[operationID.String()] = requestDigest
		return nil
	})
}

func (p *Provider) CreateDefinition(ctx context.Context, request tracker.CreateWorkDefinitionRequest) (tracker.WorkDefinitionMutation, error) {
	if _, err := ids.ParseOperationID(request.OperationID.String()); err != nil || len(request.Definition.CanonicalData()) == 0 {
		return tracker.WorkDefinitionMutation{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "operation and complete work definition are required", Cause: err}
	}
	digest := definitionRequestDigest(request.Definition)
	workKey := "fake-" + request.Definition.WorkID().String()
	var result tracker.WorkDefinitionMutation
	var outcome error
	err := p.store.update(ctx, func(state *diskState) error {
		if prior, exists := state.DefinitionOperations[request.OperationID.String()]; exists {
			if prior.Digest != digest || prior.WorkKey != workKey {
				return &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: "operation identity was reused for different work definition content"}
			}
			work, found := state.Works[prior.WorkKey]
			if !found || work.Definition == nil {
				return &tracker.Error{Kind: tracker.ErrorAttributionLost, Message: "replayed operation no longer has attributable provider state"}
			}
			current, err := definitionFromDisk(work.Definition)
			if err != nil || definitionRequestDigest(current) != prior.Digest {
				return &tracker.Error{Kind: tracker.ErrorAttributionLost, Message: "replayed operation no longer matches current provider content", Cause: err}
			}
			observed, err := p.definitionObservation(work)
			if err != nil {
				return err
			}
			result = tracker.WorkDefinitionMutation{Observation: observed, Replayed: true, Noop: true}
			return nil
		}
		failure := consumeFailure(state)
		if failure != "" && failure != FailureTimeoutAfterApply {
			outcome = failureError(failure)
			return nil
		}
		if _, exists := state.Works[workKey]; exists {
			return &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: "stable work identity is already mapped"}
		}
		now := time.Now().UTC()
		disk, err := diskDefinitionFrom(request.Definition, request.OperationID, now)
		if err != nil {
			return err
		}
		state.Works[workKey] = &diskWork{Key: workKey, Title: disk.Title, Description: disk.Description, Revision: 1, Definition: disk}
		state.DefinitionOperations[request.OperationID.String()] = definitionOperation{Digest: digest, WorkKey: workKey}
		state.History = append(state.History, HistoryEntry{Kind: "definition_create", WorkKey: workKey, Marker: request.OperationID.String(), Revision: 1})
		observed, err := p.definitionObservation(state.Works[workKey])
		if err != nil {
			return err
		}
		result = tracker.WorkDefinitionMutation{Observation: observed}
		if failure == FailureTimeoutAfterApply {
			outcome = failureError(failure)
		}
		return nil
	})
	if err != nil {
		return tracker.WorkDefinitionMutation{}, err
	}
	return result, outcome
}

func (p *Provider) UpdateDefinition(ctx context.Context, request tracker.UpdateWorkDefinitionRequest) (tracker.WorkDefinitionMutation, error) {
	if _, err := ids.ParseOperationID(request.OperationID.String()); err != nil || validateDefinitionReference(request.ExternalRef) != nil || request.ExpectedObservation == 0 || len(request.Definition.CanonicalData()) == 0 {
		return tracker.WorkDefinitionMutation{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "operation, mapping, expected observation, and complete definition are required", Cause: err}
	}
	if request.Definition.ProjectID() != request.ExternalRef.ProjectID || request.Definition.WorkID() != request.ExternalRef.WorkID {
		return tracker.WorkDefinitionMutation{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "work definition disagrees with stable mapping"}
	}
	digest := definitionRequestDigest(request.Definition)
	var result tracker.WorkDefinitionMutation
	var outcome error
	err := p.store.update(ctx, func(state *diskState) error {
		if prior, exists := state.DefinitionOperations[request.OperationID.String()]; exists {
			if prior.Digest != digest || prior.WorkKey != request.ExternalRef.ExternalID {
				return &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: "operation identity was reused for different work definition content"}
			}
			work, found := state.Works[prior.WorkKey]
			if !found || work.Definition == nil {
				return &tracker.Error{Kind: tracker.ErrorAttributionLost, Message: "replayed update lost provider attribution"}
			}
			current, err := definitionFromDisk(work.Definition)
			if err != nil || definitionRequestDigest(current) != prior.Digest {
				return &tracker.Error{Kind: tracker.ErrorAttributionLost, Message: "replayed update no longer matches current provider content", Cause: err}
			}
			observed, err := p.definitionObservation(work)
			if err != nil {
				return err
			}
			result = tracker.WorkDefinitionMutation{Observation: observed, Replayed: true, Noop: true}
			return nil
		}
		work, exists := state.Works[request.ExternalRef.ExternalID]
		if !exists || work.Definition == nil {
			return &tracker.Error{Kind: tracker.ErrorNotFound, Message: "work definition does not exist"}
		}
		failure := consumeFailure(state)
		if failure != "" && failure != FailureTimeoutAfterApply {
			outcome = failureError(failure)
			return nil
		}
		if work.Revision != request.ExpectedObservation {
			return &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: "provider observation changed before guarded update"}
		}
		now := time.Now().UTC()
		disk, err := diskDefinitionFrom(request.Definition, work.Definition.CreatedByOperation, now)
		if err != nil {
			return err
		}
		work.Revision++
		work.Title, work.Description, work.Definition = disk.Title, disk.Description, disk
		state.DefinitionOperations[request.OperationID.String()] = definitionOperation{Digest: digest, WorkKey: work.Key}
		state.History = append(state.History, HistoryEntry{Kind: "definition_update", WorkKey: work.Key, Marker: request.OperationID.String(), Revision: work.Revision})
		observed, err := p.definitionObservation(work)
		if err != nil {
			return err
		}
		result = tracker.WorkDefinitionMutation{Observation: observed}
		if failure == FailureTimeoutAfterApply {
			outcome = failureError(failure)
		}
		return nil
	})
	if err != nil {
		return tracker.WorkDefinitionMutation{}, err
	}
	return result, outcome
}

func (p *Provider) ObserveDefinition(ctx context.Context, reference tracker.ExternalWorkRef) (tracker.WorkDefinitionObservation, error) {
	if err := validateDefinitionReference(reference); err != nil {
		return tracker.WorkDefinitionObservation{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "external work reference is invalid", Cause: err}
	}
	var observed tracker.WorkDefinitionObservation
	err := p.store.read(ctx, func(state diskState) error {
		work, exists := state.Works[reference.ExternalID]
		if !exists || work.Definition == nil {
			return &tracker.Error{Kind: tracker.ErrorNotFound, Message: "work definition does not exist"}
		}
		if work.Definition.ProjectID != reference.ProjectID || work.Definition.WorkID != reference.WorkID {
			return &tracker.Error{Kind: tracker.ErrorAttributionLost, Message: "stored work mapping disagrees with requested stable identity"}
		}
		var err error
		observed, err = p.definitionObservation(work)
		return err
	})
	return observed, err
}

func definitionRequestDigest(definition tracker.WorkDefinition) string {
	digest := sha256.Sum256(append([]byte("spec-kitty/fake-work-definition/v1\x00"), definition.CanonicalData()...))
	return fmt.Sprintf("%x", digest)
}

func diskDefinitionFrom(definition tracker.WorkDefinition, createdBy ids.OperationID, observedAt time.Time) (*diskDefinition, error) {
	metadata := definition.ProviderMetadata().Bytes()
	return &diskDefinition{
		ProjectID: definition.ProjectID(), WorkID: definition.WorkID(), Title: definition.Title(), Description: definition.Description(),
		DesignContext: definition.DesignContext(), CrossDomainReason: definition.CrossDomainReason(), Criteria: definition.Criteria(),
		Dependencies: definition.Dependencies(), WriteScopes: definition.WriteScopes(), Provenance: definition.Provenance(),
		MetadataSchema: definition.MetadataSchema(), ProviderMetadata: append([]byte(nil), metadata...), CreatedByOperation: createdBy, ObservedAt: observedAt,
	}, nil
}

func (p *Provider) definitionObservation(work *diskWork) (tracker.WorkDefinitionObservation, error) {
	definition, err := definitionFromDisk(work.Definition)
	if err != nil {
		return tracker.WorkDefinitionObservation{}, &tracker.Error{Kind: tracker.ErrorSchemaSkew, Message: "stored work definition is invalid", Cause: err}
	}
	reference := tracker.ExternalWorkRef{
		ProjectID: definition.ProjectID(), WorkID: definition.WorkID(), Provider: providerIdentity, ExternalID: work.Key,
		MetadataSchema: definition.MetadataSchema(), CreatedByOperation: work.Definition.CreatedByOperation, LastObservation: work.Revision,
	}
	providerObservation, err := tracker.NewProviderObservation(work.Key, definition.CanonicalData(), work.Revision, definitionRequestDigest(definition), fmt.Sprintf("revision-%d", work.Revision), work.Definition.ObservedAt)
	if err != nil {
		return tracker.WorkDefinitionObservation{}, err
	}
	return tracker.NewWorkDefinitionObservation(definition, reference, providerObservation, work.Definition.ObservedAt)
}

func validateDefinitionReference(reference tracker.ExternalWorkRef) error {
	if err := reference.Validate(); err != nil {
		return err
	}
	if reference.Provider != providerIdentity {
		return fmt.Errorf("fake work definition: foreign provider reference")
	}
	return nil
}

func definitionFromDisk(stored *diskDefinition) (tracker.WorkDefinition, error) {
	metadata, err := tracker.NewOpaqueMetadata(stored.ProviderMetadata)
	if err != nil {
		return tracker.WorkDefinition{}, err
	}
	return tracker.NewWorkDefinition(tracker.WorkDefinitionParams{
		ProjectID: stored.ProjectID, WorkID: stored.WorkID, Title: stored.Title, Description: stored.Description,
		DesignContext: stored.DesignContext, CrossDomainReason: stored.CrossDomainReason, Criteria: stored.Criteria,
		Dependencies: stored.Dependencies, WriteScopes: stored.WriteScopes, Provenance: stored.Provenance,
		MetadataSchema: stored.MetadataSchema, ProviderMetadata: metadata,
	})
}

func (p *Provider) Capabilities(context.Context) (tracker.ProviderCapabilities, error) {
	capabilities := tracker.ProviderCapabilities{
		CatalogRead:         tracker.SupportSupported,
		CatalogCreate:       tracker.SupportUnsupported,
		CatalogUpdate:       tracker.UpdateUnsupported,
		BlockingGraph:       tracker.SupportUnsupported,
		Discussion:          tracker.DiscussionIdempotent,
		Assignment:          tracker.AssignmentAtomicClaim,
		ObservationRevision: tracker.ObservationRevisionProviderNative,
		SafeCompensation:    tracker.CompensationConditional,
		ConcurrentWriters:   tracker.ConcurrentWritersProviderNative,
		PortableExport:      tracker.SupportUnsupported,
		RestorableBackup:    tracker.SupportUnsupported,
	}
	return capabilities, capabilities.Validate()
}

// RequireCapability is a non-mutating preflight. Unsupported requirements are
// rejected before the durable store lock, failure queue, or revision is touched.
func (p *Provider) RequireCapability(capability tracker.CapabilityName) error {
	capabilities, err := p.Capabilities(context.Background())
	if err != nil {
		return err
	}
	return capabilities.Require(capability)
}

func (p *Provider) RequireCatalogUpdate(required tracker.UpdateLevel) error {
	capabilities, err := p.Capabilities(context.Background())
	if err != nil {
		return err
	}
	return capabilities.RequireCatalogUpdate(required)
}

func (p *Provider) CreateWork(context.Context, tracker.CreateWorkRequest) (tracker.WorkMutation, error) {
	return tracker.WorkMutation{}, p.RequireCapability(tracker.CapabilityCatalogCreate)
}

func (p *Provider) ReadWork(ctx context.Context, reference tracker.ExternalWorkRef) (tracker.WorkRecord, error) {
	if err := reference.Validate(); err != nil {
		return tracker.WorkRecord{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "external work reference is invalid", Cause: err}
	}
	work, err := p.Read(ctx, reference.ExternalID)
	if err != nil {
		return tracker.WorkRecord{}, err
	}
	observation, err := portableObservation(reference.ExternalID, work.Revision, work.Assignee, work.AssignmentMarker)
	if err != nil {
		return tracker.WorkRecord{}, err
	}
	reference.LastObservation = work.Revision
	criteria := make([]tracker.Criterion, len(work.Criteria))
	for index, text := range work.Criteria {
		criteria[index] = tracker.Criterion{ID: fmt.Sprintf("criterion-%04d", index+1), Text: text, Verifier: tracker.VerifierReview, Required: true}
	}
	discussion := make([]tracker.DiscussionEntry, len(work.Comments))
	for index, comment := range work.Comments {
		discussion[index] = portableDiscussionEntry(comment)
	}
	return tracker.NewWorkRecord(reference, work.Title, work.Description, "", criteria, nil, work.Assignee, work.AssignmentMarker, discussion, observation)
}

func (p *Provider) UpdateWork(_ context.Context, request tracker.UpdateWorkRequest) (tracker.WorkMutation, error) {
	if request.RequiredGuard == tracker.UpdateGuarded {
		return tracker.WorkMutation{}, p.RequireCatalogUpdate(request.RequiredGuard)
	}
	return tracker.WorkMutation{}, p.RequireCapability(tracker.CapabilityCatalogUpdate)
}

func (p *Provider) PutBlockingDependency(context.Context, tracker.DependencyRequest) (tracker.DependencyMutation, error) {
	return tracker.DependencyMutation{}, p.RequireCapability(tracker.CapabilityBlockingGraph)
}

func (p *Provider) RemoveBlockingDependency(context.Context, tracker.DependencyRequest) (tracker.DependencyMutation, error) {
	return tracker.DependencyMutation{}, p.RequireCapability(tracker.CapabilityBlockingGraph)
}

func (p *Provider) ListDependencies(context.Context, tracker.ExternalWorkRef) ([]tracker.DependencyRef, error) {
	return nil, p.RequireCapability(tracker.CapabilityBlockingGraph)
}

func (p *Provider) ReadyProjection(context.Context, tracker.ReadyRequest) (tracker.ReadyProjection, error) {
	return tracker.ReadyProjection{}, p.RequireCapability(tracker.CapabilityBlockingGraph)
}

func (p *Provider) AppendDiscussion(ctx context.Context, request tracker.AppendDiscussionRequest) (tracker.DiscussionMutation, error) {
	if err := request.ExternalRef.Validate(); err != nil || request.OperationID == "" {
		return tracker.DiscussionMutation{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "discussion reference and operation are required", Cause: err}
	}
	mutation, mutationErr := p.CompareAndAddComment(ctx, tracker.CommentRequest{
		WorkKey: request.ExternalRef.ExternalID, ExpectedRevision: request.ExpectedObservation,
		Author: request.Author, Body: request.Body, Marker: request.OperationID.String(),
	})
	if mutation.Revision == 0 {
		return tracker.DiscussionMutation{}, mutationErr
	}
	persisted, err := p.persistedComment(ctx, request.ExternalRef.ExternalID, request.OperationID.String())
	if err != nil {
		return tracker.DiscussionMutation{}, err
	}
	observation, err := portableObservation(request.ExternalRef.ExternalID, mutation.Revision, mutation.Assignee, mutation.Marker)
	if err != nil {
		return tracker.DiscussionMutation{}, err
	}
	result := tracker.DiscussionMutation{
		Entry:       portableDiscussionEntry(persisted),
		Observation: observation, Replayed: mutation.Replayed, Noop: mutation.Noop,
	}
	return result, mutationErr
}

func (p *Provider) ListDiscussion(ctx context.Context, reference tracker.ExternalWorkRef) ([]tracker.DiscussionEntry, error) {
	record, err := p.ReadWork(ctx, reference)
	if err != nil {
		return nil, err
	}
	return record.Discussion(), nil
}

func (p *Provider) ObserveAssignment(ctx context.Context, reference tracker.ExternalWorkRef) (tracker.AssignmentObservation, error) {
	record, err := p.ReadWork(ctx, reference)
	if err != nil {
		return tracker.AssignmentObservation{}, err
	}
	return tracker.AssignmentObservation{ExternalRef: record.ExternalRef, Assignee: record.VisibleAssignee, OperationMarker: record.AssignmentMarker, Observation: record.Observation}, nil
}

func (p *Provider) ProjectAssignment(ctx context.Context, request tracker.ProjectAssignmentRequest) (tracker.AssignmentMutation, error) {
	if err := request.ExternalRef.Validate(); err != nil || request.OperationID == "" {
		return tracker.AssignmentMutation{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "assignment reference and operation are required", Cause: err}
	}
	capabilities, err := p.Capabilities(ctx)
	if err != nil {
		return tracker.AssignmentMutation{}, err
	}
	if request.RequiredGuarantee != tracker.AssignmentUnguarded && request.RequiredGuarantee != tracker.AssignmentAtomicClaim {
		return tracker.AssignmentMutation{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "assignment guarantee is invalid"}
	}
	if request.RequiredGuarantee == tracker.AssignmentAtomicClaim && capabilities.Assignment != tracker.AssignmentAtomicClaim {
		return tracker.AssignmentMutation{}, &tracker.Error{Kind: tracker.ErrorGuardedUpdateUnsupported, Message: "provider cannot enforce atomic assignment", Capability: tracker.CapabilityAssignment}
	}
	mutation, mutationErr := p.CompareAndSetAssignment(ctx, tracker.AssignmentRequest{
		WorkKey: request.ExternalRef.ExternalID, ExpectedRevision: request.ExpectedObservation,
		Assignee: request.Assignee, Marker: request.OperationID.String(),
	})
	return portableAssignmentMutation(request.ExternalRef, mutation, capabilities.Assignment, mutationErr)
}

func (p *Provider) CompensateAssignment(ctx context.Context, request tracker.CompensateAssignmentRequest) (tracker.AssignmentMutation, error) {
	if err := request.ExternalRef.Validate(); err != nil || request.OperationID == "" {
		return tracker.AssignmentMutation{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "compensation reference and operation are required", Cause: err}
	}
	mutation, mutationErr := p.CompareBeforeCompensate(ctx, tracker.CompensationRequest{
		WorkKey: request.ExternalRef.ExternalID, ExpectedRevision: request.ExpectedObservation,
		ExpectedAssignee: request.ExpectedAssignee, Marker: request.ExpectedMarker, RestoreAssignee: request.RestoreAssignee,
	})
	return portableAssignmentMutation(request.ExternalRef, mutation, tracker.AssignmentAtomicClaim, mutationErr)
}

func (p *Provider) Inspect(ctx context.Context, request tracker.InspectRequest) (tracker.Inspection, error) {
	if _, err := ids.ParseProjectID(request.ProjectID.String()); err != nil {
		return tracker.Inspection{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "inspection project is invalid", Cause: err}
	}
	capabilities, err := p.Capabilities(ctx)
	if err != nil {
		return tracker.Inspection{}, err
	}
	identity := sha256.Sum256([]byte(p.store.path))
	binding := tracker.TrackerBinding{
		ProjectID: request.ProjectID, Provider: providerIdentity, ExecutablePath: "/in-process/tracker-provider",
		ExecutableVersion: "1", ProtocolSchema: 1, WorkspacePath: p.store.path,
		WorkspaceIdentity: fmt.Sprintf("%x", identity), ConcurrencyMode: tracker.ConcurrencyServerMultiwriter,
		ObservedAt: time.Now().UTC(),
	}
	return tracker.NewInspection(binding, capabilities, tracker.HealthHealthy, nil)
}

func (p *Provider) ExportPortable(context.Context, tracker.ExportRequest, io.Writer) (tracker.ExportResult, error) {
	return tracker.ExportResult{}, p.RequireCapability(tracker.CapabilityPortableExport)
}

func (p *Provider) Backup(context.Context, tracker.BackupRequest) (tracker.BackupResult, error) {
	return tracker.BackupResult{}, p.RequireCapability(tracker.CapabilityRestorableBackup)
}

func (p *Provider) InspectBackup(context.Context, tracker.BackupInspectRequest) (tracker.BackupInspection, error) {
	return tracker.BackupInspection{}, p.RequireCapability(tracker.CapabilityRestorableBackup)
}

func portableObservation(externalID string, revision tracker.Revision, assignee, marker string) (tracker.ProviderObservation, error) {
	observation, err := tracker.NewProviderObservation(externalID, []byte(fmt.Sprintf("%s\x00%d", externalID, revision)), revision, "", fmt.Sprintf("revision-%d", revision), time.Now().UTC())
	if err != nil {
		return tracker.ProviderObservation{}, err
	}
	observation.Assignee = assignee
	observation.OperationMarker = marker
	return observation, nil
}

func portableAssignmentMutation(reference tracker.ExternalWorkRef, mutation tracker.Mutation, guarantee tracker.AssignmentLevel, mutationErr error) (tracker.AssignmentMutation, error) {
	if mutation.Revision == 0 {
		return tracker.AssignmentMutation{}, mutationErr
	}
	observation, err := portableObservation(reference.ExternalID, mutation.Revision, mutation.Assignee, mutation.Marker)
	if err != nil {
		return tracker.AssignmentMutation{}, err
	}
	reference.LastObservation = mutation.Revision
	result := tracker.AssignmentMutation{
		Observation: tracker.AssignmentObservation{ExternalRef: reference, Assignee: mutation.Assignee, OperationMarker: mutation.Marker, Observation: observation},
		Guarantee:   guarantee, Replayed: mutation.Replayed, Noop: mutation.Noop,
	}
	return result, mutationErr
}

func (p *Provider) Read(ctx context.Context, workKey string) (tracker.Work, error) {
	var result tracker.Work
	err := p.store.read(ctx, func(state diskState) error {
		work, ok := state.Works[workKey]
		if !ok {
			return &tracker.Error{Kind: tracker.ErrorNotFound, Message: "work does not exist"}
		}
		result = publicWork(work)
		return nil
	})
	return result, err
}

func (p *Provider) CompareAndSetAssignment(ctx context.Context, request tracker.AssignmentRequest) (tracker.Mutation, error) {
	if request.WorkKey == "" || request.ExpectedRevision == 0 || request.Marker == "" {
		return tracker.Mutation{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "work key, expected revision, and marker are required"}
	}
	var result tracker.Mutation
	var outcome error
	err := p.store.update(ctx, func(state *diskState) error {
		work, ok := state.Works[request.WorkKey]
		if !ok {
			return &tracker.Error{Kind: tracker.ErrorNotFound, Message: "work does not exist"}
		}
		if work.AssignmentMarker == request.Marker && work.Assignee == request.Assignee {
			result = mutation(work, true, false)
			return nil
		}
		if markerAlreadyUsed(state.History, request.WorkKey, request.Marker) {
			return &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: "operation marker was reused for a different mutation"}
		}
		failure := consumeFailure(state)
		if failure != "" && failure != FailureTimeoutAfterApply {
			outcome = failureError(failure)
			return nil
		}
		if work.Revision != request.ExpectedRevision {
			return &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: fmt.Sprintf("expected revision %d, observed %d", request.ExpectedRevision, work.Revision)}
		}
		work.Revision++
		work.Assignee = request.Assignee
		work.AssignmentMarker = request.Marker
		state.History = append(state.History, HistoryEntry{Kind: "assignment", WorkKey: work.Key, Value: request.Assignee, Marker: request.Marker, Revision: work.Revision})
		result = mutation(work, false, false)
		if failure == FailureTimeoutAfterApply {
			outcome = failureError(failure)
		}
		return nil
	})
	if err != nil {
		return tracker.Mutation{}, err
	}
	if outcome != nil && result.Revision == 0 {
		return tracker.Mutation{}, outcome
	}
	return result, outcome
}

func (p *Provider) CompareAndAddComment(ctx context.Context, request tracker.CommentRequest) (tracker.Mutation, error) {
	if request.WorkKey == "" || request.ExpectedRevision == 0 || request.Marker == "" || request.Body == "" {
		return tracker.Mutation{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "work key, expected revision, body, and marker are required"}
	}
	var result tracker.Mutation
	var outcome error
	err := p.store.update(ctx, func(state *diskState) error {
		work, ok := state.Works[request.WorkKey]
		if !ok {
			return &tracker.Error{Kind: tracker.ErrorNotFound, Message: "work does not exist"}
		}
		for _, comment := range work.Comments {
			if comment.Marker != request.Marker {
				continue
			}
			if comment.Body == request.Body && comment.Author == request.Author {
				result = mutation(work, true, false)
				result.Marker = request.Marker
				return nil
			}
			return &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: "operation marker was reused for a different mutation"}
		}
		if markerAlreadyUsed(state.History, request.WorkKey, request.Marker) {
			return &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: "operation marker was reused for a different mutation"}
		}
		failure := consumeFailure(state)
		if failure != "" && failure != FailureTimeoutAfterApply {
			outcome = failureError(failure)
			return nil
		}
		if work.Revision != request.ExpectedRevision {
			return &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: fmt.Sprintf("expected revision %d, observed %d", request.ExpectedRevision, work.Revision)}
		}
		work.Revision++
		work.Comments = append(work.Comments, tracker.Comment{Author: request.Author, Body: request.Body, Marker: request.Marker, Revision: work.Revision})
		state.History = append(state.History, HistoryEntry{Kind: "comment", WorkKey: work.Key, Value: request.Body, Marker: request.Marker, Revision: work.Revision})
		result = mutation(work, false, false)
		result.Marker = request.Marker
		if failure == FailureTimeoutAfterApply {
			outcome = failureError(failure)
		}
		return nil
	})
	if err != nil {
		return tracker.Mutation{}, err
	}
	if outcome != nil && result.Revision == 0 {
		return tracker.Mutation{}, outcome
	}
	return result, outcome
}

func (p *Provider) persistedComment(ctx context.Context, workKey, marker string) (tracker.Comment, error) {
	work, err := p.Read(ctx, workKey)
	if err != nil {
		return tracker.Comment{}, err
	}
	for _, comment := range work.Comments {
		if comment.Marker == marker {
			return comment, nil
		}
	}
	return tracker.Comment{}, &tracker.Error{Kind: tracker.ErrorAmbiguous, Message: "applied discussion entry could not be observed"}
}

func portableDiscussionEntry(comment tracker.Comment) tracker.DiscussionEntry {
	return tracker.DiscussionEntry{
		Author: comment.Author, Body: comment.Body,
		OperationMarker: comment.Marker, Observation: comment.Revision,
	}
}

func (p *Provider) CompareBeforeCompensate(ctx context.Context, request tracker.CompensationRequest) (tracker.Mutation, error) {
	if request.WorkKey == "" || request.ExpectedRevision == 0 || request.Marker == "" {
		return tracker.Mutation{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "work key, expected revision, and marker are required"}
	}
	var result tracker.Mutation
	var outcome error
	err := p.store.update(ctx, func(state *diskState) error {
		work, ok := state.Works[request.WorkKey]
		if !ok {
			return &tracker.Error{Kind: tracker.ErrorNotFound, Message: "work does not exist"}
		}
		compensationKey := request.WorkKey + "\x00" + request.Marker
		if revision, compensated := state.Compensated[compensationKey]; compensated {
			result, ok = historicalCompensation(state.History, request, revision)
			if !ok {
				return &tracker.Error{Kind: tracker.ErrorAttributionLost, Message: "compensation record has no coherent provider history"}
			}
			return nil
		}
		failure := consumeFailure(state)
		if failure != "" && failure != FailureTimeoutAfterApply {
			outcome = failureError(failure)
			return nil
		}
		if work.Revision != request.ExpectedRevision || work.Assignee != request.ExpectedAssignee || work.AssignmentMarker != request.Marker {
			return &tracker.Error{Kind: tracker.ErrorAttributionLost, Message: "current revision, assignee, or operation marker no longer proves attribution"}
		}
		work.Revision++
		work.Assignee = request.RestoreAssignee
		work.AssignmentMarker = ""
		state.Compensated[compensationKey] = work.Revision
		state.History = append(state.History, HistoryEntry{Kind: "compensation", WorkKey: work.Key, Value: request.RestoreAssignee, Marker: request.Marker, Revision: work.Revision})
		result = mutation(work, false, false)
		if failure == FailureTimeoutAfterApply {
			outcome = failureError(failure)
		}
		return nil
	})
	if err != nil {
		return tracker.Mutation{}, err
	}
	if outcome != nil && result.Revision == 0 {
		return tracker.Mutation{}, outcome
	}
	return result, outcome
}

func publicWork(work *diskWork) tracker.Work {
	return tracker.Work{Key: work.Key, Title: work.Title, Description: work.Description, Criteria: clone(work.Criteria), Dependencies: clone(work.Dependencies), Assignee: work.Assignee, AssignmentMarker: work.AssignmentMarker, Comments: append([]tracker.Comment(nil), work.Comments...), Revision: work.Revision}
}

func mutation(work *diskWork, replayed, noop bool) tracker.Mutation {
	return tracker.Mutation{WorkKey: work.Key, Assignee: work.Assignee, Marker: work.AssignmentMarker, Revision: work.Revision, Replayed: replayed, Noop: noop}
}

func historicalCompensation(history []HistoryEntry, request tracker.CompensationRequest, revision tracker.Revision) (tracker.Mutation, bool) {
	if revision <= request.ExpectedRevision || revision-request.ExpectedRevision != 1 {
		return tracker.Mutation{}, false
	}
	assignmentFound, compensationFound := false, false
	for _, entry := range history {
		if entry.WorkKey != request.WorkKey {
			continue
		}
		if entry.Revision == request.ExpectedRevision {
			if entry.Kind != "assignment" || entry.Marker != request.Marker || entry.Value != request.ExpectedAssignee {
				return tracker.Mutation{}, false
			}
			assignmentFound = true
		}
		if entry.Revision == revision {
			if entry.Kind != "compensation" || entry.Marker != request.Marker || entry.Value != request.RestoreAssignee {
				return tracker.Mutation{}, false
			}
			compensationFound = true
		}
		if entry.Marker == request.Marker {
			switch entry.Kind {
			case "assignment":
				if entry.Revision != request.ExpectedRevision || entry.Value != request.ExpectedAssignee {
					return tracker.Mutation{}, false
				}
			case "compensation":
				if entry.Revision != revision || entry.Value != request.RestoreAssignee {
					return tracker.Mutation{}, false
				}
			default:
				return tracker.Mutation{}, false
			}
		}
	}
	if !assignmentFound || !compensationFound {
		return tracker.Mutation{}, false
	}
	return tracker.Mutation{WorkKey: request.WorkKey, Assignee: request.RestoreAssignee, Revision: revision, Replayed: true, Noop: true}, true
}

func consumeFailure(state *diskState) FailureMode {
	if len(state.Failures) == 0 {
		return ""
	}
	failure := state.Failures[0]
	state.Failures = state.Failures[1:]
	return failure
}

func failureError(failure FailureMode) error {
	switch failure {
	case FailureUnavailable:
		return &tracker.Error{Kind: tracker.ErrorUnavailable, Message: "provider is unavailable"}
	case FailureTimeoutBeforeApply:
		return &tracker.Error{Kind: tracker.ErrorAmbiguous, Message: "request timed out before apply; caller must observe state"}
	case FailureTimeoutAfterApply:
		return &tracker.Error{Kind: tracker.ErrorAmbiguous, Message: "request timed out after apply; caller must observe state"}
	case FailureRevisionConflict:
		return &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: "injected provider revision conflict"}
	default:
		return nil
	}
}

func markerAlreadyUsed(history []HistoryEntry, workKey, marker string) bool {
	for _, entry := range history {
		if entry.WorkKey == workKey && entry.Marker == marker {
			return true
		}
	}
	return false
}
