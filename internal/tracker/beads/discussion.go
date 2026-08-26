package beads

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

const (
	discussionMarkerPrefix = "<!-- spec-kitty:discussion:v1;op="
	discussionMarkerSuffix = " -->\n"
	maxDiscussionBodyBytes = 1 << 20
)

// Beads v1.2.2 has no separate queryable comment-metadata field. The marker
// is therefore stored as one canonical first line in the same atomic append as
// the semantic body. Only this exact namespaced line is interpreted; the rest
// of the provider string is returned byte-for-byte as the semantic body.
func renderDiscussionBody(operationID ids.OperationID, body string) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(operationID.String()))
	return discussionMarkerPrefix + encoded + discussionMarkerSuffix + body
}

func parseDiscussionBody(providerBody string) (semanticBody, marker string, marked bool) {
	lineEnd := strings.IndexByte(providerBody, '\n')
	if lineEnd < 0 {
		return providerBody, "", false
	}
	line := providerBody[:lineEnd+1]
	if !strings.HasPrefix(line, discussionMarkerPrefix) || !strings.HasSuffix(line, discussionMarkerSuffix) {
		return providerBody, "", false
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(line, discussionMarkerPrefix), discussionMarkerSuffix)
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return providerBody, "", false
	}
	operationID, err := ids.ParseOperationID(string(decoded))
	if err != nil || renderDiscussionBody(operationID, "") != line {
		return providerBody, "", false
	}
	return providerBody[lineEnd+1:], operationID.String(), true
}

func validateAppendDiscussionRequest(bound *BoundAdapter, request tracker.AppendDiscussionRequest) error {
	if err := validateGraphReference(bound, request.ExternalRef); err != nil {
		return err
	}
	if _, err := ids.ParseOperationID(request.OperationID.String()); err != nil {
		return invalidCatalogRequest("discussion operation identity is invalid", err)
	}
	if strings.TrimSpace(request.Author) == "" || strings.IndexByte(request.Author, 0) >= 0 {
		return invalidCatalogRequest("discussion author mapping is required", nil)
	}
	if request.ExpectedObservation == 0 {
		return invalidCatalogRequest("discussion expected observation is required", nil)
	}
	if !utf8.ValidString(request.Body) || strings.IndexByte(request.Body, 0) >= 0 || len(request.Body) > maxDiscussionBodyBytes {
		return invalidCatalogRequest("discussion body must be bounded valid UTF-8", nil)
	}
	return nil
}

func (bound *BoundAdapter) ListDiscussion(ctx context.Context, reference tracker.ExternalWorkRef) ([]tracker.DiscussionEntry, error) {
	if err := validateGraphReference(bound, reference); err != nil {
		return nil, err
	}
	entries, _, err := bound.observeDiscussion(ctx, reference)
	return entries, err
}

func (bound *BoundAdapter) AppendDiscussion(ctx context.Context, request tracker.AppendDiscussionRequest) (tracker.DiscussionMutation, error) {
	if err := validateAppendDiscussionRequest(bound, request); err != nil {
		return tracker.DiscussionMutation{}, err
	}
	release, err := bound.beginProviderMutation(ctx)
	if err != nil {
		return tracker.DiscussionMutation{}, err
	}
	defer release()

	entries, before, err := bound.observeDiscussion(ctx, request.ExternalRef)
	if err != nil {
		return tracker.DiscussionMutation{}, err
	}
	if err := bound.ensureDiscussionOperationScoped(ctx, request.ExternalRef, request.OperationID); err != nil {
		return tracker.DiscussionMutation{}, err
	}
	if entry, state := classifyDiscussionMarker(entries, request.OperationID, request.Author, request.Body); state != markerAbsent {
		if state == markerExact {
			return tracker.DiscussionMutation{Entry: entry, Observation: before, Replayed: true, Noop: true}, nil
		}
		return tracker.DiscussionMutation{}, mappingConflict("discussion operation marker conflicts with provider history")
	}
	if before.Revision != request.ExpectedObservation {
		return tracker.DiscussionMutation{}, &tracker.Error{Kind: tracker.ErrorRevisionConflict, RecoveryCode: "reobserve_discussion", Message: "discussion observation changed before append"}
	}
	command, err := newCommentAppendCommand(bound.commandBinding(), request.ExternalRef.ExternalID, renderDiscussionBody(request.OperationID, request.Body))
	if err != nil {
		return tracker.DiscussionMutation{}, err
	}
	response, executeErr := bound.adapter.execute(ctx, command)
	if executeErr != nil && !mutationCouldHaveApplied(response) {
		return tracker.DiscussionMutation{}, executeErr
	}
	observedEntries, observed, observeErr := bound.observeDiscussion(ctx, request.ExternalRef)
	if observeErr != nil {
		return tracker.DiscussionMutation{}, &tracker.Error{Kind: tracker.ErrorAmbiguous, RecoveryCode: "observe_then_reconcile", Message: "discussion append could not be verified", Cause: errors.Join(executeErr, observeErr)}
	}
	entry, state := classifyDiscussionMarker(observedEntries, request.OperationID, request.Author, request.Body)
	switch state {
	case markerExact:
		return tracker.DiscussionMutation{Entry: entry, Observation: observed, Replayed: executeErr != nil}, nil
	case markerConflict:
		return tracker.DiscussionMutation{}, mappingConflict("discussion operation marker conflicts with provider history")
	default:
		recovery := "observe_then_reconcile"
		if executeErr != nil {
			recovery = "retry_append_after_observed_absence"
		}
		return tracker.DiscussionMutation{}, &tracker.Error{Kind: tracker.ErrorAmbiguous, RecoveryCode: recovery, Message: "discussion append was not proven by provider observation", Cause: executeErr}
	}
}

func (bound *BoundAdapter) ensureDiscussionOperationScoped(ctx context.Context, current tracker.ExternalWorkRef, operationID ids.OperationID) error {
	index, err := bound.loadMappingIndex(ctx)
	if err != nil {
		return err
	}
	mapped, err := index.resolve(current.ProjectID, current.WorkID)
	if err != nil || mapped.ExternalID != current.ExternalID {
		return mappingConflict("discussion work mapping is not unique in the provider catalog")
	}
	references := make([]tracker.ExternalWorkRef, 0, len(index.byExternal))
	for _, reference := range index.byExternal {
		if reference.ProjectID == current.ProjectID && reference.ExternalID != current.ExternalID {
			references = append(references, reference)
		}
	}
	sort.Slice(references, func(i, j int) bool { return references[i].ExternalID < references[j].ExternalID })
	for _, reference := range references {
		entries, _, err := bound.observeDiscussion(ctx, reference)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.OperationMarker == operationID.String() {
				return mappingConflict("discussion operation marker belongs to another work mapping")
			}
		}
	}
	return nil
}

type markerState uint8

const (
	markerAbsent markerState = iota
	markerExact
	markerConflict
)

func classifyDiscussionMarker(entries []tracker.DiscussionEntry, operationID ids.OperationID, author, body string) (tracker.DiscussionEntry, markerState) {
	var found tracker.DiscussionEntry
	count := 0
	for _, entry := range entries {
		if entry.OperationMarker != operationID.String() {
			continue
		}
		count++
		found = entry
		if entry.Body != body || entry.Author != author {
			return tracker.DiscussionEntry{}, markerConflict
		}
	}
	if count == 1 {
		return found, markerExact
	}
	if count > 1 {
		return tracker.DiscussionEntry{}, markerConflict
	}
	return tracker.DiscussionEntry{}, markerAbsent
}

func (bound *BoundAdapter) observeDiscussion(ctx context.Context, reference tracker.ExternalWorkRef) ([]tracker.DiscussionEntry, tracker.ProviderObservation, error) {
	issue, err := bound.readIssue(ctx, reference.ExternalID)
	if err != nil {
		return nil, tracker.ProviderObservation{}, err
	}
	if err := validateIssueMapping(issue, reference.ProjectID, reference.WorkID, reference.CreatedByOperation); err != nil {
		return nil, tracker.ProviderObservation{}, err
	}
	command, err := newCommentListCommand(bound.commandBinding(), reference.ExternalID)
	if err != nil {
		return nil, tracker.ProviderObservation{}, err
	}
	response, err := bound.adapter.execute(ctx, command)
	if err != nil {
		return nil, tracker.ProviderObservation{}, err
	}
	var encoded []json.RawMessage
	if err := json.Unmarshal(response.payload, &encoded); err != nil {
		return nil, tracker.ProviderObservation{}, metadataProtocolError("provider comment list is malformed", err)
	}
	entries := make([]tracker.DiscussionEntry, 0, len(encoded))
	providerComments := make([]observationComment, 0, len(encoded))
	for _, raw := range encoded {
		entry, providerComment, err := decodeProviderComment(raw)
		if err != nil {
			return nil, tracker.ProviderObservation{}, err
		}
		entries = append(entries, entry)
		providerComments = append(providerComments, providerComment)
	}
	issue.comments = providerComments
	record, err := bound.issueRecord(issue)
	if err != nil {
		return nil, tracker.ProviderObservation{}, err
	}
	for index := range entries {
		entries[index].Observation = record.Observation.Revision
	}
	return entries, record.Observation, nil
}

func decodeProviderComment(payload []byte) (tracker.DiscussionEntry, observationComment, error) {
	var fields map[string]json.RawMessage
	if err := decodeJSONObject(payload, &fields); err != nil {
		return tracker.DiscussionEntry{}, observationComment{}, metadataProtocolError("provider comment must be a JSON object", err)
	}
	id, err := firstOptionalString(fields, "id", "comment_id")
	if err != nil {
		return tracker.DiscussionEntry{}, observationComment{}, metadataProtocolError("provider comment identity is malformed", err)
	}
	author, err := firstOptionalString(fields, "author", "created_by")
	if err != nil {
		return tracker.DiscussionEntry{}, observationComment{}, metadataProtocolError("provider comment author is malformed", err)
	}
	body, present, err := optionalString(fields, "text")
	if err != nil || !present {
		return tracker.DiscussionEntry{}, observationComment{}, metadataProtocolError("provider comment text is missing or malformed", err)
	}
	createdAtText, err := firstOptionalString(fields, "created_at", "created")
	if err != nil {
		return tracker.DiscussionEntry{}, observationComment{}, metadataProtocolError("provider comment timestamp is malformed", err)
	}
	var createdAt time.Time
	if createdAtText != "" {
		createdAt, err = time.Parse(time.RFC3339Nano, createdAtText)
		if err != nil {
			return tracker.DiscussionEntry{}, observationComment{}, metadataProtocolError("provider comment timestamp is malformed", err)
		}
		createdAt = createdAt.UTC()
	}
	semanticBody, marker, _ := parseDiscussionBody(body)
	entry := tracker.DiscussionEntry{ExternalCommentID: id, Author: author, Body: semanticBody, OperationMarker: marker, CreatedAt: createdAt}
	providerComment := observationComment{ID: id, Author: author, Body: body, Marker: marker, CreatedAt: createdAtText}
	return entry, providerComment, nil
}

var _ tracker.Discussion = (*BoundAdapter)(nil)
