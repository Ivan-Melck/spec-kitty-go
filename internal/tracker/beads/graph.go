package beads

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

type workMappingIndex struct {
	byExternal          map[string]tracker.ExternalWorkRef
	byWork              map[workMappingKey]tracker.ExternalWorkRef
	conflictingExternal map[string]struct{}
	conflictingWork     map[workMappingKey]struct{}
}

type workMappingKey struct {
	projectID ids.ProjectID
	workID    ids.WorkID
}

// beginProviderMutation keeps observe/mutate/verify inside the adapter writer
// boundary in embedded mode. It is serialization among Spec Kitty processes,
// not provider CAS and not a fence against a human invoking bd directly.
func (bound *BoundAdapter) beginProviderMutation(ctx context.Context) (func(), error) {
	readiness, err := bound.Preflight(ctx)
	if err != nil {
		return nil, err
	}
	if err := bound.validateReadiness(readiness); err != nil {
		return nil, err
	}
	if bound.binding.ConcurrencyMode == tracker.ConcurrencyServerMultiwriter {
		return func() {}, nil
	}
	lock, err := bound.locker.acquire(ctx, bound.binding)
	if err != nil {
		return nil, err
	}
	if err := bound.validateReadiness(readiness); err != nil {
		lock.release()
		return nil, err
	}
	return lock.release, nil
}

func (bound *BoundAdapter) loadMappingIndex(ctx context.Context) (workMappingIndex, error) {
	request, err := newListCommand(bound.commandBinding())
	if err != nil {
		return workMappingIndex{}, err
	}
	response, err := bound.adapter.execute(ctx, request)
	if err != nil {
		return workMappingIndex{}, err
	}
	var encoded []json.RawMessage
	if err := json.Unmarshal(response.payload, &encoded); err != nil {
		return workMappingIndex{}, metadataProtocolError("provider issue list is malformed", err)
	}
	index := workMappingIndex{
		byExternal: make(map[string]tracker.ExternalWorkRef), byWork: make(map[workMappingKey]tracker.ExternalWorkRef),
		conflictingExternal: make(map[string]struct{}), conflictingWork: make(map[workMappingKey]struct{}),
	}
	for _, raw := range encoded {
		issue, err := decodeProviderIssue(raw)
		if err != nil {
			return workMappingIndex{}, err
		}
		if issue.document.namespace == nil {
			continue
		}
		kitty := issue.document.kitty
		reference := tracker.ExternalWorkRef{
			ProjectID: kitty.ProjectID, WorkID: kitty.WorkID, Provider: beadsProvider, ExternalID: issue.externalID,
			MetadataSchema: kitty.SchemaVersion, CreatedByOperation: kitty.CreatedByOperation,
		}
		key := workMappingKey{projectID: kitty.ProjectID, workID: kitty.WorkID}
		if prior, present := index.byExternal[issue.externalID]; present && (prior.ProjectID != reference.ProjectID || prior.WorkID != reference.WorkID) {
			delete(index.byExternal, issue.externalID)
			index.conflictingExternal[issue.externalID] = struct{}{}
			index.conflictingWork[workMappingKey{projectID: prior.ProjectID, workID: prior.WorkID}] = struct{}{}
			index.conflictingWork[key] = struct{}{}
			delete(index.byWork, workMappingKey{projectID: prior.ProjectID, workID: prior.WorkID})
			delete(index.byWork, key)
			continue
		}
		if _, conflicted := index.conflictingExternal[issue.externalID]; !conflicted {
			index.byExternal[issue.externalID] = reference
		}
		if prior, present := index.byWork[key]; present && prior.ExternalID != issue.externalID {
			delete(index.byWork, key)
			index.conflictingWork[key] = struct{}{}
			continue
		}
		if _, conflicted := index.conflictingWork[key]; !conflicted {
			index.byWork[key] = reference
		}
	}
	return index, nil
}

func (index workMappingIndex) resolve(projectID ids.ProjectID, workID ids.WorkID) (tracker.ExternalWorkRef, error) {
	key := workMappingKey{projectID: projectID, workID: workID}
	if _, conflicted := index.conflictingWork[key]; conflicted {
		return tracker.ExternalWorkRef{}, mappingConflict("multiple provider issues claim one stable work identity")
	}
	reference, present := index.byWork[key]
	if !present {
		for candidate := range index.byWork {
			if candidate.workID == workID && candidate.projectID != projectID {
				return tracker.ExternalWorkRef{}, mappingConflict("work mapping belongs to another project")
			}
		}
		for candidate := range index.conflictingWork {
			if candidate.workID == workID && candidate.projectID != projectID {
				return tracker.ExternalWorkRef{}, mappingConflict("work mapping belongs to another project")
			}
		}
		return tracker.ExternalWorkRef{}, &tracker.Error{Kind: tracker.ErrorNotFound, RecoveryCode: "reconcile_mapping", Message: "work has no provider mapping"}
	}
	if reference.ProjectID != projectID || reference.Provider != beadsProvider || reference.MetadataSchema != metadataSchemaV1 {
		return tracker.ExternalWorkRef{}, mappingConflict("work mapping belongs to another project or provider")
	}
	return reference, nil
}

func (index workMappingIndex) resolveExternal(externalID string) (tracker.ExternalWorkRef, error) {
	if _, conflicted := index.conflictingExternal[externalID]; conflicted {
		return tracker.ExternalWorkRef{}, mappingConflict("one external issue claims multiple stable work identities")
	}
	reference, present := index.byExternal[externalID]
	if !present {
		return tracker.ExternalWorkRef{}, &tracker.Error{Kind: tracker.ErrorNotFound, RecoveryCode: "reconcile_mapping", Message: "external issue has no stable provider mapping"}
	}
	resolved, err := index.resolve(reference.ProjectID, reference.WorkID)
	if err != nil {
		return tracker.ExternalWorkRef{}, err
	}
	if resolved.ExternalID != externalID {
		return tracker.ExternalWorkRef{}, mappingConflict("external issue mapping is not unique")
	}
	return reference, nil
}

func validateDependencyRequest(bound *BoundAdapter, request tracker.DependencyRequest) error {
	if bound == nil || request.ProjectID != bound.binding.ProjectID {
		return invalidCatalogRequest("dependency project does not match the provider binding", nil)
	}
	if _, err := ids.ParseProjectID(request.ProjectID.String()); err != nil {
		return invalidCatalogRequest("dependency project identity is invalid", err)
	}
	if _, err := ids.ParseOperationID(request.OperationID.String()); err != nil {
		return invalidCatalogRequest("dependency operation identity is invalid", err)
	}
	if err := request.Dependency.Validate(); err != nil {
		return invalidCatalogRequest("blocking dependency is invalid", err)
	}
	return nil
}

func (bound *BoundAdapter) PutBlockingDependency(ctx context.Context, request tracker.DependencyRequest) (tracker.DependencyMutation, error) {
	if err := validateDependencyRequest(bound, request); err != nil {
		return tracker.DependencyMutation{}, err
	}
	release, err := bound.beginProviderMutation(ctx)
	if err != nil {
		return tracker.DependencyMutation{}, err
	}
	defer release()
	return bound.mutateBlockingDependency(ctx, request, true)
}

func (bound *BoundAdapter) RemoveBlockingDependency(ctx context.Context, request tracker.DependencyRequest) (tracker.DependencyMutation, error) {
	if err := validateDependencyRequest(bound, request); err != nil {
		return tracker.DependencyMutation{}, err
	}
	release, err := bound.beginProviderMutation(ctx)
	if err != nil {
		return tracker.DependencyMutation{}, err
	}
	defer release()
	return bound.mutateBlockingDependency(ctx, request, false)
}

func (bound *BoundAdapter) mutateBlockingDependency(ctx context.Context, request tracker.DependencyRequest, put bool) (tracker.DependencyMutation, error) {
	index, err := bound.loadMappingIndex(ctx)
	if err != nil {
		return tracker.DependencyMutation{}, err
	}
	blocked, err := index.resolve(request.ProjectID, request.Dependency.BlockedWorkID)
	if err != nil {
		return tracker.DependencyMutation{}, err
	}
	blocker, err := index.resolve(request.ProjectID, request.Dependency.BlockingWorkID)
	if err != nil {
		return tracker.DependencyMutation{}, err
	}
	edges, observation, err := bound.observeDependencies(ctx, blocked, index)
	if err != nil {
		return tracker.DependencyMutation{}, err
	}
	present := containsDependency(edges, request.Dependency)
	if present == put {
		return tracker.DependencyMutation{Dependency: request.Dependency, Observation: observation, Replayed: true, Noop: true}, nil
	}
	var command command
	if put {
		command, err = newDependencyAddCommand(bound.commandBinding(), blocked.ExternalID, blocker.ExternalID)
	} else {
		command, err = newDependencyRemoveCommand(bound.commandBinding(), blocked.ExternalID, blocker.ExternalID)
	}
	if err != nil {
		return tracker.DependencyMutation{}, err
	}
	response, executeErr := bound.adapter.execute(ctx, command)
	if executeErr != nil && !mutationCouldHaveApplied(response) {
		return tracker.DependencyMutation{}, executeErr
	}
	observedEdges, observed, observeErr := bound.observeDependencies(ctx, blocked, index)
	if observeErr != nil {
		return tracker.DependencyMutation{}, &tracker.Error{Kind: tracker.ErrorAmbiguous, RecoveryCode: "observe_then_reconcile", Message: "dependency mutation could not be verified", Cause: errors.Join(executeErr, observeErr)}
	}
	applied := containsDependency(observedEdges, request.Dependency) == put
	if applied {
		return tracker.DependencyMutation{Dependency: request.Dependency, Observation: observed, Replayed: executeErr != nil}, nil
	}
	if executeErr != nil {
		return tracker.DependencyMutation{}, &tracker.Error{Kind: tracker.ErrorAmbiguous, RecoveryCode: "retry_dependency_after_observed_absence", Message: "dependency mutation was not observed after an ambiguous provider result", Cause: executeErr}
	}
	return tracker.DependencyMutation{}, &tracker.Error{Kind: tracker.ErrorAmbiguous, RecoveryCode: "observe_then_reconcile", Message: "provider confirmed dependency mutation but the requested edge was not observed"}
}

func containsDependency(edges []tracker.DependencyRef, want tracker.DependencyRef) bool {
	for _, edge := range edges {
		if edge.BlockedWorkID == want.BlockedWorkID && edge.BlockingWorkID == want.BlockingWorkID && edge.Kind == tracker.DependencyBlocks {
			return true
		}
	}
	return false
}

func validateGraphReference(bound *BoundAdapter, reference tracker.ExternalWorkRef) error {
	if bound == nil {
		return invalidCatalogRequest("graph binding is missing", nil)
	}
	if err := reference.Validate(); err != nil {
		return invalidCatalogRequest("external work reference is invalid", err)
	}
	if reference.ProjectID != bound.binding.ProjectID || reference.Provider != beadsProvider || reference.MetadataSchema != metadataSchemaV1 {
		return mappingConflict("external work reference does not belong to this graph binding")
	}
	return nil
}

func (bound *BoundAdapter) ListDependencies(ctx context.Context, reference tracker.ExternalWorkRef) ([]tracker.DependencyRef, error) {
	if err := validateGraphReference(bound, reference); err != nil {
		return nil, err
	}
	index, err := bound.loadMappingIndex(ctx)
	if err != nil {
		return nil, err
	}
	mapped, err := index.resolve(reference.ProjectID, reference.WorkID)
	if err != nil {
		return nil, err
	}
	if mapped.ExternalID != reference.ExternalID || mapped.CreatedByOperation != reference.CreatedByOperation {
		return nil, mappingConflict("external work reference conflicts with the live mapping")
	}
	edges, _, err := bound.observeDependencies(ctx, mapped, index)
	return edges, err
}

func (bound *BoundAdapter) observeDependencies(ctx context.Context, blocked tracker.ExternalWorkRef, index workMappingIndex) ([]tracker.DependencyRef, tracker.ProviderObservation, error) {
	request, err := newDependencyListCommand(bound.commandBinding(), blocked.ExternalID)
	if err != nil {
		return nil, tracker.ProviderObservation{}, err
	}
	response, err := bound.adapter.execute(ctx, request)
	if err != nil {
		return nil, tracker.ProviderObservation{}, err
	}
	var encoded []json.RawMessage
	if err := json.Unmarshal(response.payload, &encoded); err != nil {
		return nil, tracker.ProviderObservation{}, metadataProtocolError("provider dependency list is malformed", err)
	}
	rawEdges := make([]observationDependency, 0, len(encoded))
	edges := make([]tracker.DependencyRef, 0, len(encoded))
	for _, raw := range encoded {
		edge, err := decodeProviderDependency(raw, blocked.ExternalID)
		if err != nil {
			return nil, tracker.ProviderObservation{}, err
		}
		rawEdges = append(rawEdges, edge)
		if edge.Kind != string(tracker.DependencyBlocks) {
			continue
		}
		blockedMapping, err := index.resolveExternal(edge.Blocked)
		if err != nil {
			return nil, tracker.ProviderObservation{}, mappingConflict("blocking edge references an unmapped blocked issue")
		}
		blockingMapping, err := index.resolveExternal(edge.Blocking)
		if err != nil {
			return nil, tracker.ProviderObservation{}, mappingConflict("blocking edge references an unmapped prerequisite issue")
		}
		if blockedMapping.ProjectID != blocked.ProjectID || blockingMapping.ProjectID != blocked.ProjectID {
			return nil, tracker.ProviderObservation{}, mappingConflict("blocking edge crosses project mappings")
		}
		dependency := tracker.DependencyRef{
			BlockedWorkID: blockedMapping.WorkID, BlockingWorkID: blockingMapping.WorkID,
			Kind: tracker.DependencyBlocks, ExternalEdgeID: edge.EdgeID,
		}
		if err := dependency.Validate(); err != nil {
			return nil, tracker.ProviderObservation{}, metadataProtocolError("provider blocking edge is invalid", err)
		}
		edges = append(edges, dependency)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].BlockedWorkID != edges[j].BlockedWorkID {
			return edges[i].BlockedWorkID < edges[j].BlockedWorkID
		}
		if edges[i].BlockingWorkID != edges[j].BlockingWorkID {
			return edges[i].BlockingWorkID < edges[j].BlockingWorkID
		}
		return edges[i].ExternalEdgeID < edges[j].ExternalEdgeID
	})
	if len(edges) > 1 {
		normalized := edges[:1]
		for _, edge := range edges[1:] {
			prior := normalized[len(normalized)-1]
			if edge.BlockedWorkID == prior.BlockedWorkID && edge.BlockingWorkID == prior.BlockingWorkID && edge.Kind == prior.Kind {
				continue
			}
			normalized = append(normalized, edge)
		}
		edges = normalized
	}
	observation, err := bound.observationFromInput(catalogObservation{ExternalID: blocked.ExternalID, Dependencies: rawEdges})
	if err != nil {
		return nil, tracker.ProviderObservation{}, err
	}
	return edges, observation, nil
}

func decodeProviderDependency(payload []byte, defaultBlocked string) (observationDependency, error) {
	var fields map[string]json.RawMessage
	if err := decodeJSONObject(payload, &fields); err != nil {
		return observationDependency{}, metadataProtocolError("provider dependency must be a JSON object", err)
	}
	blocking, present, err := optionalString(fields, "id")
	if err != nil || !present || blocking == "" {
		return observationDependency{}, metadataProtocolError("provider blocking endpoint is missing or malformed", err)
	}
	kind, present, err := optionalString(fields, "dependency_type")
	if err != nil || !present || kind == "" {
		return observationDependency{}, metadataProtocolError("provider dependency kind is missing or malformed", err)
	}
	edgeID, err := firstOptionalString(fields, "edge_id")
	if err != nil {
		return observationDependency{}, metadataProtocolError("provider dependency identity is malformed", err)
	}
	return observationDependency{Blocked: defaultBlocked, Blocking: blocking, Kind: kind, EdgeID: edgeID}, nil
}

func firstOptionalString(fields map[string]json.RawMessage, keys ...string) (string, error) {
	for _, key := range keys {
		value, present, err := optionalString(fields, key)
		if err != nil {
			return "", err
		}
		if present {
			return value, nil
		}
	}
	return "", nil
}

func (bound *BoundAdapter) observationFromInput(input catalogObservation) (tracker.ProviderObservation, error) {
	identity := deriveObservationIdentity(input)
	observedAt := time.Now().UTC()
	if bound.now != nil {
		observedAt = bound.now().UTC()
	}
	observation, err := tracker.NewProviderObservation(input.ExternalID, identity.Canonical, identity.Revision, digestString(identity.Digest), "", observedAt)
	if err != nil {
		return tracker.ProviderObservation{}, metadataProtocolError("provider observation is invalid", err)
	}
	return observation, nil
}

func (bound *BoundAdapter) ReadyProjection(ctx context.Context, request tracker.ReadyRequest) (tracker.ReadyProjection, error) {
	if bound == nil || request.ProjectID != bound.binding.ProjectID {
		return tracker.ReadyProjection{}, invalidCatalogRequest("readiness project does not match the provider binding", nil)
	}
	if _, err := ids.ParseProjectID(request.ProjectID.String()); err != nil {
		return tracker.ReadyProjection{}, invalidCatalogRequest("readiness project identity is invalid", err)
	}
	pinned := make(map[ids.WorkID]struct{}, len(request.PinnedReadyIDs))
	for _, workID := range request.PinnedReadyIDs {
		if _, err := ids.ParseWorkID(workID.String()); err != nil {
			return tracker.ReadyProjection{}, invalidCatalogRequest("pinned ready identity is invalid", err)
		}
		pinned[workID] = struct{}{}
	}
	catalogIndex, err := bound.loadMappingIndex(ctx)
	if err != nil {
		return tracker.ReadyProjection{}, err
	}
	command, err := newReadyCommand(bound.commandBinding())
	if err != nil {
		return tracker.ReadyProjection{}, err
	}
	response, err := bound.adapter.execute(ctx, command)
	if err != nil {
		var portable *tracker.Error
		if errors.As(err, &portable) && (portable.Kind == tracker.ErrorUnavailable || portable.Kind == tracker.ErrorTimeout) {
			return tracker.ReadyProjection{Diagnostics: []tracker.Diagnostic{{Kind: portable.Kind, RecoveryCode: portable.RecoveryAction(), Message: "provider readiness is unavailable"}}}, nil
		}
		return tracker.ReadyProjection{}, err
	}
	var encoded []json.RawMessage
	if err := json.Unmarshal(response.payload, &encoded); err != nil {
		return tracker.ReadyProjection{}, metadataProtocolError("provider ready list is malformed", err)
	}
	providerSet := make(map[ids.WorkID]struct{})
	diagnostics := make([]tracker.Diagnostic, 0)
	observationLabels := make([]string, 0, len(encoded))
	for externalID, reference := range catalogIndex.byExternal {
		observationLabels = append(observationLabels, "catalog:"+externalID+"@"+reference.ProjectID.String()+"/"+reference.WorkID.String())
	}
	for key := range catalogIndex.conflictingWork {
		observationLabels = append(observationLabels, "catalog-conflict:"+key.projectID.String()+"/"+key.workID.String())
	}
	for externalID := range catalogIndex.conflictingExternal {
		observationLabels = append(observationLabels, "catalog-external-conflict:"+externalID)
	}
	for _, raw := range encoded {
		issue, err := decodeProviderIssue(raw)
		if err != nil {
			return tracker.ReadyProjection{}, err
		}
		if issue.document.namespace == nil {
			diagnostics = append(diagnostics, tracker.Diagnostic{Kind: tracker.ErrorRevisionConflict, RecoveryCode: "reconcile_mapping", Message: "provider-ready issue has no stable mapping", ExternalID: issue.externalID})
			observationLabels = append(observationLabels, issue.externalID+"@unmapped")
			continue
		}
		kitty := issue.document.kitty
		identity := deriveObservationIdentity(catalogObservation{ExternalID: issue.externalID, Metadata: issue.metadata, UpdatedAt: issue.updatedAt})
		observationLabels = append(observationLabels, issue.externalID+"@"+digestString(identity.Digest))
		if kitty.ProjectID != request.ProjectID {
			diagnostics = append(diagnostics, tracker.Diagnostic{Kind: tracker.ErrorRevisionConflict, RecoveryCode: "reconcile_mapping", Message: "provider-ready issue belongs to another project", ExternalID: issue.externalID})
			continue
		}
		catalogReference, resolveErr := catalogIndex.resolveExternal(issue.externalID)
		if resolveErr != nil || catalogReference.ProjectID != kitty.ProjectID || catalogReference.WorkID != kitty.WorkID || catalogReference.MetadataSchema != kitty.SchemaVersion || catalogReference.CreatedByOperation != kitty.CreatedByOperation {
			diagnostics = append(diagnostics, tracker.Diagnostic{Kind: tracker.ErrorRevisionConflict, RecoveryCode: "reconcile_mapping", Message: "provider-ready issue does not have one unique catalog mapping", ExternalID: issue.externalID})
			continue
		}
		providerSet[kitty.WorkID] = struct{}{}
	}
	providerIDs := make([]ids.WorkID, 0, len(providerSet))
	for workID := range providerSet {
		providerIDs = append(providerIDs, workID)
		if _, agreed := pinned[workID]; !agreed {
			diagnostics = append(diagnostics, tracker.Diagnostic{Kind: tracker.ErrorRevisionConflict, RecoveryCode: "reconcile_pinned_graph", Message: "provider reports work ready but the pinned graph does not", ExternalID: workID.String()})
		}
	}
	for workID := range pinned {
		if _, agreed := providerSet[workID]; !agreed {
			diagnostics = append(diagnostics, tracker.Diagnostic{Kind: tracker.ErrorRevisionConflict, RecoveryCode: "reconcile_pinned_graph", Message: "pinned graph reports work ready but the provider does not", ExternalID: workID.String()})
		}
	}
	sort.Slice(providerIDs, func(i, j int) bool { return providerIDs[i] < providerIDs[j] })
	sort.Slice(diagnostics, func(i, j int) bool {
		left := strings.Join([]string{string(diagnostics[i].Kind), diagnostics[i].RecoveryCode, diagnostics[i].ExternalID, diagnostics[i].Message}, "\x00")
		right := strings.Join([]string{string(diagnostics[j].Kind), diagnostics[j].RecoveryCode, diagnostics[j].ExternalID, diagnostics[j].Message}, "\x00")
		return left < right
	})
	observation, err := bound.observationFromInput(catalogObservation{ExternalID: "ready:" + request.ProjectID.String(), Labels: observationLabels})
	if err != nil {
		return tracker.ReadyProjection{}, err
	}
	return tracker.ReadyProjection{ProviderReadyIDs: providerIDs, Diagnostics: diagnostics, Observation: observation}, nil
}

var _ tracker.Graph = (*BoundAdapter)(nil)
