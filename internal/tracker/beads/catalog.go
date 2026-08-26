package beads

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

const beadsProvider = "beads"

type providerIssue struct {
	externalID    string
	title         *string
	description   *string
	acceptance    *string
	metadata      []byte
	document      metadataDocument
	labels        []string
	dependencies  []observationDependency
	assignee      *string
	comments      []observationComment
	updatedAt     *time.Time
	providerToken string
}

func (bound *BoundAdapter) CreateWork(ctx context.Context, request tracker.CreateWorkRequest) (tracker.WorkMutation, error) {
	if err := validateCreateRequest(bound, request); err != nil {
		return tracker.WorkMutation{}, err
	}
	contentDigest := requestContentDigest(request.Title, request.Description, request.DesignContext, request.Criteria)
	desired := kittyMetadata{
		SchemaVersion: request.MetadataSchema, ProjectID: request.ProjectID, WorkID: request.WorkID,
		CreatedByOperation: request.OperationID, LastOperation: request.OperationID,
		Criteria: append([]tracker.Criterion(nil), request.Criteria...), DesignContext: request.DesignContext,
		ContentDigest: contentDigest,
	}
	metadata, err := mergeMetadata(request.ProviderMetadata.Bytes(), desired)
	if err != nil {
		return tracker.WorkMutation{}, err
	}
	readiness, err := bound.Preflight(ctx)
	if err != nil {
		return tracker.WorkMutation{}, err
	}
	return bound.withCatalogMutation(ctx, readiness, request.WorkID, func() (tracker.WorkMutation, error) {
		matches, err := bound.observeMapping(ctx, request.ProjectID, request.WorkID)
		if err != nil {
			return tracker.WorkMutation{}, err
		}
		if len(matches) != 0 {
			return classifyCreateReplay(bound, request, contentDigest, matches)
		}
		command, err := newCreateCommand(bound.commandBinding(), createValues{
			title: request.Title, description: request.Description,
			acceptance: renderCriteria(request.Criteria), metadata: string(metadata),
		})
		if err != nil {
			return tracker.WorkMutation{}, err
		}
		response, executeErr := bound.adapter.execute(ctx, command)
		if executeErr != nil {
			if !mutationCouldHaveApplied(response) {
				return tracker.WorkMutation{}, executeErr
			}
			return bound.recoverAmbiguousCreate(ctx, request, contentDigest, executeErr)
		}
		id, err := decodeMutationExternalID(response.payload)
		if err != nil {
			return bound.recoverAmbiguousCreate(ctx, request, contentDigest, err)
		}
		issue, err := bound.readIssue(ctx, id)
		if err != nil {
			return tracker.WorkMutation{}, &tracker.Error{Kind: tracker.ErrorAmbiguous, RecoveryCode: "observe_then_reconcile", Message: "created provider issue could not be verified", Cause: err}
		}
		if err := validateIssueMapping(issue, request.ProjectID, request.WorkID, request.OperationID); err != nil {
			return tracker.WorkMutation{}, err
		}
		if issue.document.kitty.ContentDigest != contentDigest {
			return tracker.WorkMutation{}, mappingConflict("created provider issue content differs from the requested mapping")
		}
		record, err := bound.issueRecord(issue)
		if err != nil {
			return tracker.WorkMutation{}, err
		}
		return tracker.WorkMutation{Record: record, Guarantee: tracker.UpdateUnguarded}, nil
	})
}

func (bound *BoundAdapter) ReadWork(ctx context.Context, reference tracker.ExternalWorkRef) (tracker.WorkRecord, error) {
	if bound == nil {
		return tracker.WorkRecord{}, invalidCatalogRequest("catalog binding is missing", nil)
	}
	if err := reference.Validate(); err != nil {
		return tracker.WorkRecord{}, invalidCatalogRequest("external work reference is invalid", err)
	}
	if reference.ProjectID != bound.binding.ProjectID || reference.Provider != beadsProvider || reference.MetadataSchema != metadataSchemaV1 {
		return tracker.WorkRecord{}, mappingConflict("external work reference does not belong to this binding")
	}
	if err := validatePinnedBinding(bound.binding); err != nil {
		return tracker.WorkRecord{}, err
	}
	issue, err := bound.readIssue(ctx, reference.ExternalID)
	if err != nil {
		return tracker.WorkRecord{}, err
	}
	if err := validateIssueMapping(issue, reference.ProjectID, reference.WorkID, reference.CreatedByOperation); err != nil {
		return tracker.WorkRecord{}, err
	}
	return bound.issueRecord(issue)
}

func (bound *BoundAdapter) UpdateWork(ctx context.Context, request tracker.UpdateWorkRequest) (tracker.WorkMutation, error) {
	if err := validateUpdateRequest(bound, request); err != nil {
		return tracker.WorkMutation{}, err
	}
	capabilities := bound.catalogCapabilities()
	// This safety refusal deliberately happens before provider preflight and
	// before the writer lock: a read followed by bd update is not CAS.
	if err := capabilities.RequireCatalogUpdate(request.RequiredGuard); err != nil {
		return tracker.WorkMutation{}, err
	}
	readiness, err := bound.Preflight(ctx)
	if err != nil {
		return tracker.WorkMutation{}, err
	}
	return bound.withCatalogMutation(ctx, readiness, request.ExternalRef.WorkID, func() (tracker.WorkMutation, error) {
		current, err := bound.readIssue(ctx, request.ExternalRef.ExternalID)
		if err != nil {
			return tracker.WorkMutation{}, err
		}
		if err := validateIssueMapping(current, request.ExternalRef.ProjectID, request.ExternalRef.WorkID, request.ExternalRef.CreatedByOperation); err != nil {
			return tracker.WorkMutation{}, err
		}
		currentRecord, err := bound.issueRecord(current)
		if err != nil {
			return tracker.WorkMutation{}, err
		}
		if currentRecord.Observation.Revision != request.ExpectedObservation {
			return tracker.WorkMutation{}, &tracker.Error{
				Kind: tracker.ErrorRevisionConflict, RecoveryCode: "refresh_then_reconcile", Message: "provider observation changed before unguarded update",
			}
		}
		contentDigest := requestContentDigest(request.Title, request.Description, request.DesignContext, request.Criteria)
		if current.document.kitty.LastOperation == request.OperationID {
			if current.document.kitty.ContentDigest != contentDigest {
				return tracker.WorkMutation{}, invalidCatalogRequest("operation replay has different semantic input", nil)
			}
			if !issueMatchesUpdate(current, request, contentDigest) {
				return tracker.WorkMutation{}, mappingConflict("provider work drifted after the recorded update operation")
			}
			return tracker.WorkMutation{Record: currentRecord, Guarantee: tracker.UpdateUnguarded, Replayed: true, Noop: true}, nil
		}
		desired := current.document.kitty
		desired.LastOperation = request.OperationID
		desired.Criteria = append([]tracker.Criterion(nil), request.Criteria...)
		desired.DesignContext = request.DesignContext
		desired.ContentDigest = contentDigest
		metadata, err := mergeMetadata(current.metadata, desired)
		if err != nil {
			return tracker.WorkMutation{}, err
		}
		command, err := newUpdateCommand(bound.commandBinding(), updateValues{
			id: request.ExternalRef.ExternalID, title: presentUpdateValue(request.Title), description: presentUpdateValue(request.Description),
			acceptance: presentUpdateValue(renderCriteria(request.Criteria)), metadata: presentUpdateValue(string(metadata)),
		})
		if err != nil {
			return tracker.WorkMutation{}, err
		}
		response, executeErr := bound.adapter.execute(ctx, command)
		if executeErr != nil {
			if !mutationCouldHaveApplied(response) {
				return tracker.WorkMutation{}, executeErr
			}
			return bound.recoverAmbiguousUpdate(ctx, request, contentDigest, currentRecord, executeErr)
		}
		updated, err := bound.readIssue(ctx, request.ExternalRef.ExternalID)
		if err != nil {
			return tracker.WorkMutation{}, &tracker.Error{Kind: tracker.ErrorAmbiguous, RecoveryCode: "observe_then_reconcile", Message: "updated provider issue could not be verified", Cause: err}
		}
		if !issueMatchesUpdate(updated, request, contentDigest) {
			return tracker.WorkMutation{}, mappingConflict("provider update response does not match the requested values")
		}
		record, err := bound.issueRecord(updated)
		if err != nil {
			return tracker.WorkMutation{}, err
		}
		return tracker.WorkMutation{Record: record, Guarantee: tracker.UpdateUnguarded}, nil
	})
}

func (bound *BoundAdapter) withCatalogMutation(ctx context.Context, readiness Readiness, workID ids.WorkID, action func() (tracker.WorkMutation, error)) (tracker.WorkMutation, error) {
	if err := bound.validateReadiness(readiness); err != nil {
		return tracker.WorkMutation{}, err
	}
	if bound.binding.ConcurrencyMode == tracker.ConcurrencyServerMultiwriter {
		// Provider-native writers may proceed concurrently for distinct work, but
		// Beads has no uniqueness constraint over our namespaced stable mapping.
		// A process-shared mapping lock therefore protects only the list/create
		// identity decision for this WorkID; it is not provider CAS.
		mappingBinding := bound.binding
		digest := sha256.Sum256([]byte(bound.binding.WorkspaceIdentity + "\x00catalog-mapping\x00" + bound.binding.ProjectID.String() + "\x00" + workID.String()))
		mappingBinding.WorkspaceIdentity = fmt.Sprintf("mapping:%x", digest[:])
		lock, err := bound.locker.acquire(ctx, mappingBinding)
		if err != nil {
			return tracker.WorkMutation{}, err
		}
		defer lock.release()
		if err := bound.validateReadiness(readiness); err != nil {
			return tracker.WorkMutation{}, err
		}
		return action()
	}
	lock, err := bound.locker.acquire(ctx, bound.binding)
	if err != nil {
		return tracker.WorkMutation{}, err
	}
	defer lock.release()
	return action()
}

func (bound *BoundAdapter) catalogCapabilities() tracker.ProviderCapabilities {
	writers := tracker.ConcurrentWritersAdapterSerialized
	if bound.binding.ConcurrencyMode == tracker.ConcurrencyServerMultiwriter {
		writers = tracker.ConcurrentWritersProviderNative
	}
	return tracker.ProviderCapabilities{
		CatalogRead: tracker.SupportSupported, CatalogCreate: tracker.SupportSupported, CatalogUpdate: tracker.UpdateUnguarded,
		BlockingGraph: tracker.SupportUnsupported, Discussion: tracker.DiscussionUnsupported, Assignment: tracker.AssignmentUnsupported,
		ObservationRevision: tracker.ObservationRevisionContentDerived, SafeCompensation: tracker.CompensationUnsupported,
		ConcurrentWriters: writers, PortableExport: tracker.SupportUnsupported, RestorableBackup: tracker.SupportUnsupported,
	}
}

// Capabilities reports the guarantees of the ports actually implemented by
// this bound adapter. Provider health remains the Inspector's concern; this
// declaration never promotes an unhealthy binding into a usable one.
func (bound *BoundAdapter) Capabilities(context.Context) (tracker.ProviderCapabilities, error) {
	if bound == nil {
		return tracker.ProviderCapabilities{}, invalidCatalogRequest("catalog binding is missing", nil)
	}
	if err := validatePinnedBinding(bound.binding); err != nil {
		return tracker.ProviderCapabilities{}, err
	}
	capabilities := bound.catalogCapabilities()
	if err := capabilities.Validate(); err != nil {
		return tracker.ProviderCapabilities{}, invalidCatalogRequest("catalog capability declaration is invalid", err)
	}
	return capabilities, nil
}

func validateCreateRequest(bound *BoundAdapter, request tracker.CreateWorkRequest) error {
	if bound == nil || request.ProjectID != bound.binding.ProjectID {
		return invalidCatalogRequest("create project does not match the catalog binding", nil)
	}
	if _, err := ids.ParseProjectID(request.ProjectID.String()); err != nil {
		return invalidCatalogRequest("project identity is invalid", err)
	}
	if _, err := ids.ParseWorkID(request.WorkID.String()); err != nil {
		return invalidCatalogRequest("work identity is invalid", err)
	}
	if _, err := ids.ParseOperationID(request.OperationID.String()); err != nil {
		return invalidCatalogRequest("operation identity is invalid", err)
	}
	if request.MetadataSchema != metadataSchemaV1 || strings.TrimSpace(request.Title) == "" {
		return invalidCatalogRequest("schema 1 and a non-empty title are required", nil)
	}
	if err := validateCriteria(request.Criteria); err != nil {
		return invalidCatalogRequest("criteria are invalid", err)
	}
	if encoded := request.ProviderMetadata.Bytes(); len(encoded) != 0 {
		document, err := decodeMetadata(encoded)
		if err != nil {
			return invalidCatalogRequest("provider metadata is invalid", err)
		}
		if document.namespace != nil {
			return invalidCatalogRequest("caller metadata cannot own the adapter namespace", nil)
		}
	}
	return nil
}

func validateUpdateRequest(bound *BoundAdapter, request tracker.UpdateWorkRequest) error {
	if bound == nil {
		return invalidCatalogRequest("catalog binding is missing", nil)
	}
	if err := request.ExternalRef.Validate(); err != nil {
		return invalidCatalogRequest("external work reference is invalid", err)
	}
	if request.ExternalRef.ProjectID != bound.binding.ProjectID || request.ExternalRef.Provider != beadsProvider || request.ExternalRef.MetadataSchema != metadataSchemaV1 {
		return mappingConflict("external work reference does not belong to this binding")
	}
	if _, err := ids.ParseOperationID(request.OperationID.String()); err != nil {
		return invalidCatalogRequest("operation identity is invalid", err)
	}
	if request.ExpectedObservation == 0 || strings.TrimSpace(request.Title) == "" {
		return invalidCatalogRequest("expected observation and non-empty title are required", nil)
	}
	if err := validateCriteria(request.Criteria); err != nil {
		return invalidCatalogRequest("criteria are invalid", err)
	}
	return nil
}

func (bound *BoundAdapter) readIssue(ctx context.Context, id string) (providerIssue, error) {
	command, err := newShowCommand(bound.commandBinding(), id)
	if err != nil {
		return providerIssue{}, err
	}
	response, err := bound.adapter.execute(ctx, command)
	if err != nil {
		return providerIssue{}, err
	}
	issue, err := decodeProviderIssue(response.payload)
	if err != nil {
		return providerIssue{}, err
	}
	if issue.externalID != id {
		return providerIssue{}, mappingConflict("provider returned a different external issue")
	}
	return issue, nil
}

func (bound *BoundAdapter) observeMapping(ctx context.Context, projectID ids.ProjectID, workID ids.WorkID) ([]providerIssue, error) {
	command, err := newListCommand(bound.commandBinding())
	if err != nil {
		return nil, err
	}
	response, err := bound.adapter.execute(ctx, command)
	if err != nil {
		return nil, err
	}
	var records []json.RawMessage
	if err := json.Unmarshal(response.payload, &records); err != nil {
		return nil, metadataProtocolError("provider issue list is malformed", err)
	}
	var matches []providerIssue
	externalMappings := make(map[string]ids.WorkID)
	for _, raw := range records {
		issue, err := decodeProviderIssue(raw)
		if err != nil {
			return nil, err
		}
		if issue.document.namespace == nil {
			continue
		}
		kitty := issue.document.kitty
		if prior, exists := externalMappings[issue.externalID]; exists && prior != kitty.WorkID {
			return nil, mappingConflict("one external issue claims multiple work identities")
		}
		externalMappings[issue.externalID] = kitty.WorkID
		if kitty.ProjectID == projectID && kitty.WorkID == workID {
			matches = append(matches, issue)
		}
	}
	if len(matches) > 1 {
		return nil, mappingConflict("multiple provider issues claim one stable work identity")
	}
	return matches, nil
}

func decodeProviderIssue(payload []byte) (providerIssue, error) {
	var fields map[string]json.RawMessage
	if err := decodeJSONObject(payload, &fields); err != nil {
		return providerIssue{}, metadataProtocolError("provider issue must be a JSON object", err)
	}
	id, present, err := optionalString(fields, "id")
	if err != nil || !present || id == "" {
		return providerIssue{}, metadataProtocolError("provider issue id is missing or malformed", err)
	}
	issue := providerIssue{externalID: id}
	if issue.title, err = optionalStringPointer(fields, "title"); err != nil {
		return providerIssue{}, metadataProtocolError("provider title is malformed", err)
	}
	if issue.description, err = optionalStringPointer(fields, "description"); err != nil {
		return providerIssue{}, metadataProtocolError("provider description is malformed", err)
	}
	if issue.acceptance, err = optionalStringPointer(fields, "acceptance"); err != nil {
		return providerIssue{}, metadataProtocolError("provider acceptance is malformed", err)
	}
	metadata, err := metadataPayload(fields["metadata"])
	if err != nil {
		return providerIssue{}, metadataProtocolError("provider metadata is malformed", err)
	}
	issue.metadata = metadata
	issue.document, err = decodeMetadata(metadata)
	if err != nil {
		return providerIssue{}, err
	}
	if raw, ok := fields["labels"]; ok {
		if err := json.Unmarshal(raw, &issue.labels); err != nil {
			return providerIssue{}, metadataProtocolError("provider labels are malformed", err)
		}
	}
	if raw, ok := fields["dependencies"]; ok {
		if err := json.Unmarshal(raw, &issue.dependencies); err != nil {
			return providerIssue{}, metadataProtocolError("provider dependencies are malformed", err)
		}
	}
	if raw, ok := fields["comments"]; ok {
		if err := json.Unmarshal(raw, &issue.comments); err != nil {
			return providerIssue{}, metadataProtocolError("provider comments are malformed", err)
		}
	}
	if issue.assignee, err = optionalStringPointer(fields, "assignee"); err != nil {
		return providerIssue{}, metadataProtocolError("provider assignee is malformed", err)
	}
	if updated, present, err := optionalString(fields, "updated_at"); err != nil {
		return providerIssue{}, metadataProtocolError("provider timestamp is malformed", err)
	} else if present && updated != "" {
		parsed, err := time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return providerIssue{}, metadataProtocolError("provider timestamp is malformed", err)
		}
		issue.updatedAt = &parsed
	}
	if token, present, err := optionalString(fields, "revision"); err != nil {
		return providerIssue{}, metadataProtocolError("provider revision token is malformed", err)
	} else if present {
		issue.providerToken = token
	}
	return issue, nil
}

func metadataPayload(raw json.RawMessage) ([]byte, error) {
	if len(bytesTrimSpace(raw)) == 0 || string(bytesTrimSpace(raw)) == "null" {
		return []byte(`{}`), nil
	}
	trimmed := bytesTrimSpace(raw)
	if trimmed[0] == '"' {
		var encoded string
		if err := json.Unmarshal(trimmed, &encoded); err != nil {
			return nil, err
		}
		trimmed = []byte(encoded)
	}
	var object map[string]json.RawMessage
	if err := decodeJSONObject(trimmed, &object); err != nil {
		return nil, err
	}
	return json.Marshal(object)
}

func bytesTrimSpace(value []byte) []byte { return []byte(strings.TrimSpace(string(value))) }

func optionalString(fields map[string]json.RawMessage, key string) (string, bool, error) {
	raw, present := fields[key]
	if !present || string(bytesTrimSpace(raw)) == "null" {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, err
	}
	return value, true, nil
}

func optionalStringPointer(fields map[string]json.RawMessage, key string) (*string, error) {
	value, present, err := optionalString(fields, key)
	if err != nil || !present {
		return nil, err
	}
	return &value, nil
}

func validateIssueMapping(issue providerIssue, projectID ids.ProjectID, workID ids.WorkID, created ids.OperationID) error {
	if issue.document.namespace == nil {
		return mappingConflict("provider issue has no spec_kitty mapping")
	}
	kitty := issue.document.kitty
	if kitty.ProjectID != projectID || kitty.WorkID != workID || (created != "" && kitty.CreatedByOperation != created) {
		return mappingConflict("provider issue mapping conflicts with the stable work reference")
	}
	return nil
}

func (bound *BoundAdapter) issueRecord(issue providerIssue) (tracker.WorkRecord, error) {
	if issue.document.namespace == nil {
		return tracker.WorkRecord{}, mappingConflict("provider issue has no spec_kitty mapping")
	}
	kitty := issue.document.kitty
	canonicalMetadata, err := json.Marshal(issue.document.root)
	if err != nil {
		return tracker.WorkRecord{}, metadataProtocolError("provider metadata cannot be canonicalized", err)
	}
	design := kitty.DesignContext
	marker := kitty.AssignmentMarker.String()
	var markerObservation *string
	if _, present := issue.document.namespace["assignment_marker"]; present {
		markerObservation = &marker
	}
	observationInput := catalogObservation{
		ExternalID: issue.externalID, Title: issue.title, Description: issue.description, DesignContext: &design,
		Acceptance: issue.acceptance, Metadata: canonicalMetadata, Labels: issue.labels, Dependencies: issue.dependencies,
		Assignee: issue.assignee, AssignmentMarker: markerObservation, Comments: issue.comments, UpdatedAt: issue.updatedAt,
	}
	identity := deriveObservationIdentity(observationInput)
	observedAt := time.Now().UTC()
	if bound.now != nil {
		observedAt = bound.now().UTC()
	}
	observation, err := tracker.NewProviderObservation(issue.externalID, identity.Canonical, identity.Revision, digestString(identity.Digest), issue.providerToken, observedAt)
	if err != nil {
		return tracker.WorkRecord{}, metadataProtocolError("provider observation is invalid", err)
	}
	observation.ProviderUpdatedAtPresent = issue.updatedAt != nil
	if issue.updatedAt != nil {
		observation.ProviderUpdatedAt = issue.updatedAt.UTC()
	}
	if issue.assignee != nil {
		observation.Assignee = *issue.assignee
	}
	observation.OperationMarker = kitty.LastOperation.String()
	reference := tracker.ExternalWorkRef{
		ProjectID: kitty.ProjectID, WorkID: kitty.WorkID, Provider: beadsProvider, ExternalID: issue.externalID,
		MetadataSchema: kitty.SchemaVersion, CreatedByOperation: kitty.CreatedByOperation, LastObservation: identity.Revision,
	}
	title, description := "", ""
	if issue.title != nil {
		title = *issue.title
	}
	if issue.description != nil {
		description = *issue.description
	}
	assignee := ""
	if issue.assignee != nil {
		assignee = *issue.assignee
	}
	return tracker.NewWorkRecord(reference, title, description, kitty.DesignContext, kitty.Criteria, nil, assignee, marker, nil, observation)
}

func classifyCreateReplay(bound *BoundAdapter, request tracker.CreateWorkRequest, digest string, matches []providerIssue) (tracker.WorkMutation, error) {
	if len(matches) != 1 {
		return tracker.WorkMutation{}, mappingConflict("mapping observation is not unique")
	}
	issue := matches[0]
	if issue.document.kitty.CreatedByOperation != request.OperationID {
		return tracker.WorkMutation{}, mappingConflict("stable work identity is already mapped by another creation operation")
	}
	if issue.document.kitty.ContentDigest != digest {
		return tracker.WorkMutation{}, invalidCatalogRequest("operation replay has different semantic input", nil)
	}
	if !issueMatchesWorkInput(issue, request.Title, request.Description, request.DesignContext, request.Criteria, digest) ||
		!issueContainsProviderMetadata(issue, request.ProviderMetadata.Bytes()) {
		return tracker.WorkMutation{}, mappingConflict("provider work drifted after the recorded create operation")
	}
	record, err := bound.issueRecord(issue)
	if err != nil {
		return tracker.WorkMutation{}, err
	}
	return tracker.WorkMutation{Record: record, Guarantee: tracker.UpdateUnguarded, Replayed: true, Noop: true}, nil
}

func (bound *BoundAdapter) recoverAmbiguousCreate(ctx context.Context, request tracker.CreateWorkRequest, digest string, cause error) (tracker.WorkMutation, error) {
	matches, err := bound.observeMapping(ctx, request.ProjectID, request.WorkID)
	if err != nil {
		return tracker.WorkMutation{}, &tracker.Error{Kind: tracker.ErrorAmbiguous, RecoveryCode: "observe_then_reconcile", Message: "create outcome remains ambiguous because mapping observation failed", Cause: errors.Join(cause, err)}
	}
	if len(matches) == 1 {
		mutation, err := classifyCreateReplay(bound, request, digest, matches)
		if err != nil {
			return tracker.WorkMutation{}, err
		}
		mutation.Noop = false
		return mutation, nil
	}
	return tracker.WorkMutation{}, &tracker.Error{Kind: tracker.ErrorAmbiguous, RecoveryCode: "retry_create_after_complete_absence", Message: "create was not observed after a complete mapping scan", Cause: cause}
}

func (bound *BoundAdapter) recoverAmbiguousUpdate(ctx context.Context, request tracker.UpdateWorkRequest, digest string, before tracker.WorkRecord, cause error) (tracker.WorkMutation, error) {
	issue, err := bound.readIssue(ctx, request.ExternalRef.ExternalID)
	if err != nil {
		return tracker.WorkMutation{}, &tracker.Error{Kind: tracker.ErrorAmbiguous, RecoveryCode: "observe_then_reconcile", Message: "update outcome remains ambiguous because observation failed", Cause: errors.Join(cause, err)}
	}
	if err := validateIssueMapping(issue, request.ExternalRef.ProjectID, request.ExternalRef.WorkID, request.ExternalRef.CreatedByOperation); err != nil {
		return tracker.WorkMutation{}, err
	}
	if issueMatchesUpdate(issue, request, digest) {
		record, err := bound.issueRecord(issue)
		return tracker.WorkMutation{Record: record, Guarantee: tracker.UpdateUnguarded, Replayed: true}, err
	}
	record, recordErr := bound.issueRecord(issue)
	if recordErr == nil && record.Observation.Revision == before.Observation.Revision {
		return tracker.WorkMutation{}, &tracker.Error{Kind: tracker.ErrorAmbiguous, RecoveryCode: "retry_update_after_observed_unchanged", Message: "update was not observed after an ambiguous provider result", Cause: cause}
	}
	return tracker.WorkMutation{}, mappingConflict("provider values changed outside the ambiguous update target")
}

func issueMatchesUpdate(issue providerIssue, request tracker.UpdateWorkRequest, digest string) bool {
	if issue.document.namespace == nil || issue.document.kitty.LastOperation != request.OperationID || issue.document.kitty.ContentDigest != digest {
		return false
	}
	return issueMatchesWorkInput(issue, request.Title, request.Description, request.DesignContext, request.Criteria, digest)
}

func issueMatchesWorkInput(issue providerIssue, title, description, designContext string, criteria []tracker.Criterion, digest string) bool {
	if issue.document.namespace == nil || issue.document.kitty.ContentDigest != digest {
		return false
	}
	liveDigest := requestContentDigest(pointerValue(issue.title), pointerValue(issue.description), issue.document.kitty.DesignContext, issue.document.kitty.Criteria)
	return liveDigest == digest && pointerValue(issue.title) == title && pointerValue(issue.description) == description &&
		issue.document.kitty.DesignContext == designContext && pointerValue(issue.acceptance) == renderCriteria(criteria)
}

func issueContainsProviderMetadata(issue providerIssue, expected []byte) bool {
	if len(expected) == 0 {
		return true
	}
	var wanted map[string]json.RawMessage
	if decodeJSONObject(expected, &wanted) != nil {
		return false
	}
	for key, value := range wanted {
		observed, exists := issue.document.root[key]
		if !exists || !semanticJSONEqual(observed, value) {
			return false
		}
	}
	return true
}

func semanticJSONEqual(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	leftCanonical, leftErr := json.Marshal(leftValue)
	rightCanonical, rightErr := json.Marshal(rightValue)
	return leftErr == nil && rightErr == nil && string(leftCanonical) == string(rightCanonical)
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func decodeMutationExternalID(payload []byte) (string, error) {
	var fields map[string]json.RawMessage
	if err := decodeJSONObject(payload, &fields); err != nil {
		return "", metadataProtocolError("provider mutation response is malformed", err)
	}
	id, present, err := optionalString(fields, "id")
	if err != nil || !present || id == "" {
		return "", metadataProtocolError("provider mutation response has no external issue id", err)
	}
	return id, nil
}

func mutationCouldHaveApplied(response commandResponse) bool {
	return response.result.started && response.result.mutationOutcome != mutationNotStarted && response.result.mutationOutcome != mutationNotApplied
}

func requestContentDigest(title, description, design string, criteria []tracker.Criterion) string {
	var values []string
	values = append(values, title, description, design)
	for _, criterion := range criteria {
		values = append(values, criterion.ID, criterion.Text, criterion.Verifier.String(), fmt.Sprintf("%t", criterion.Required), criterion.EvidenceHint)
	}
	hash := sha256.New()
	for _, value := range values {
		var length [8]byte
		length[0] = byte(uint64(len(value)) >> 56)
		length[1] = byte(uint64(len(value)) >> 48)
		length[2] = byte(uint64(len(value)) >> 40)
		length[3] = byte(uint64(len(value)) >> 32)
		length[4] = byte(uint64(len(value)) >> 24)
		length[5] = byte(uint64(len(value)) >> 16)
		length[6] = byte(uint64(len(value)) >> 8)
		length[7] = byte(len(value))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func renderCriteria(criteria []tracker.Criterion) string {
	lines := make([]string, len(criteria))
	for index, criterion := range criteria {
		required := "optional"
		if criterion.Required {
			required = "required"
		}
		text := strings.Join(strings.Fields(criterion.Text), " ")
		line := fmt.Sprintf("%s — %s — %s — %s", criterion.ID, required, criterion.Verifier, text)
		if criterion.EvidenceHint != "" {
			line += " — evidence: " + strings.Join(strings.Fields(criterion.EvidenceHint), " ")
		}
		lines[index] = line
	}
	return strings.Join(lines, "\n")
}

var _ tracker.Catalog = (*BoundAdapter)(nil)
var _ tracker.CapabilityReporter = (*BoundAdapter)(nil)
