package hubnot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"strings"
	"time"
)

var _ tracker.Discussion = (*BoundAdapter)(nil)
var _ tracker.AttributedDiscussionPort = (*BoundAdapter)(nil)

const discussionKeyPrefix = "gk-discussion-v1-"

type discussionIntent struct {
	Reference  tracker.ExternalWorkRef `json:"reference"`
	Author     string                  `json:"author"`
	BodyDigest string                  `json:"body_digest"`
	BodyBytes  int                     `json:"body_bytes"`
}

func discussionAttributionKey(commentKey string) string {
	digest := sha256.Sum256([]byte("go-kitty.discussion-attribution/1\x00" + commentKey))
	return "gk-attribution-v1-" + hex.EncodeToString(digest[:])
}
func discussionActions(projectRef tracker.ExternalWorkRef, r tracker.AppendDiscussionRequest) (operationAction, operationAction, error) {
	if err := validateOperationReference(projectRef.ProjectID, r.ExternalRef); err != nil {
		return operationAction{}, operationAction{}, err
	}
	if !validClaim(r.Author) {
		return operationAction{}, operationAction{}, invalid("bounded valid accountable author required", nil)
	}
	digest := sha256.Sum256([]byte(r.Body))
	meaning := discussionIntent{immutableReference(r.ExternalRef), r.Author, hex.EncodeToString(digest[:]), len(r.Body)}
	comment, err := newOperationAction(projectRef.ProjectID, r.OperationID, "discussion.append", meaning)
	if err != nil {
		return operationAction{}, comment, err
	}
	comment.key = discussionKeyPrefix + strings.TrimPrefix(comment.key, "gk-")
	attribution := comment
	attribution.key = discussionAttributionKey(comment.key)
	attribution.verify = func(event issueOperation) error {
		if event.Kind != "issue.revise" || event.Intent != "revise" {
			return operationConflict("discussion attribution is not a revision", nil)
		}
		if _, err := verifyEventMapping(event, r.ExternalRef.ProjectID, r.ExternalRef.WorkID, r.ExternalRef.CreatedByOperation, r.ExternalRef.ExternalID); err != nil {
			return err
		}
		stamp, err := stateOperationStamp(event)
		if err != nil {
			return err
		}
		if stamp.Target != nil || stamp.Noop {
			return operationConflict("unexpected discussion attribution fields", nil)
		}
		return nil
	}
	comment.verify = func(event issueOperation) error {
		if event.Kind != "issue.comment" || event.Intent != "comment" || event.State != nil || event.Body == nil || event.IssueID != r.ExternalRef.ExternalID || *event.Body != r.Body {
			return operationConflict("original comment differs from requested exact body or issue", nil)
		}
		return nil
	}
	return attribution, comment, nil
}
func (b *BoundAdapter) AppendAttributedDiscussion(ctx context.Context, r tracker.AppendDiscussionRequest) (tracker.AttributedDiscussionMutation, error) {
	e, err := b.operations(ctx)
	if err != nil {
		return tracker.AttributedDiscussionMutation{}, err
	}
	defer e.budget.close()
	return e.appendDiscussion(ctx, r, b.identity.Name)
}
func (b *BoundAdapter) AppendDiscussion(ctx context.Context, r tracker.AppendDiscussionRequest) (tracker.DiscussionMutation, error) {
	result, err := b.AppendAttributedDiscussion(ctx, r)
	return tracker.DiscussionMutation{Entry: result.Entry.Entry(), Observation: result.Entry.Proof(), Replayed: result.Replayed, Noop: result.Noop}, err
}
func (e *operationEngine) appendDiscussion(ctx context.Context, r tracker.AppendDiscussionRequest, actorName string) (tracker.AttributedDiscussionMutation, error) {
	var result tracker.AttributedDiscussionMutation
	if err := validateOperationReference(e.project, r.ExternalRef); err != nil {
		return result, err
	}
	attribution, comment, err := discussionActions(r.ExternalRef, r)
	if err != nil {
		return result, err
	}
	request := mutationRequest{intent: "comment", id: r.ExternalRef.ExternalID, key: comment.key, body: r.Body}
	if _, err := request.command(e.actor, actorName); err != nil {
		return result, err
	}
	// The comment's original stamp is the independently signed sibling, never a
	// body wrapper or current-state guess. Every recovery validates both children.
	var signedAttribution operationEffect
	comment.readStamp = func(event issueOperation) (operationStamp, error) {
		original, found, err := e.recover(ctx, attribution)
		if err != nil {
			return operationStamp{}, err
		}
		if !found {
			return operationStamp{}, operationConflict("comment has no original attribution", nil)
		}
		if event.Actor != original.Event.Actor || event.IssueID != original.Event.IssueID {
			return operationStamp{}, operationConflict("signed discussion children disagree", nil)
		}
		signedAttribution = original
		return original.Stamp, nil
	}
	attr, attrFound, err := e.recover(ctx, attribution)
	if err != nil {
		result.PriorMutation = attr.PriorMutation
		return result, err
	}
	child, childFound, err := e.recover(ctx, comment)
	prior := attrFound || childFound
	applied := false
	ambiguous := false
	fail := func(cause error) (tracker.AttributedDiscussionMutation, error) {
		var oe *OperationError
		if errors.As(cause, &oe) {
			applied = applied || oe.AppliedThisCall
			ambiguous = ambiguous || oe.Outcome == OutcomeAmbiguous
		}
		outcome := OutcomeUnstarted
		if attrFound || childFound || prior || applied {
			outcome = OutcomeApplied
		}
		if ambiguous {
			outcome = OutcomeAmbiguous
		}
		result.PriorMutation = prior
		return result, &OperationError{Outcome: outcome, PriorMutation: prior, AppliedThisCall: applied, Cause: cause}
	}
	if err != nil {
		return fail(err)
	}
	if childFound {
		attr = signedAttribution
		attrFound = true
	}
	if !attrFound {
		attr, err = e.run(ctx, attribution, func() (mutationRequest, error) {
			o, err := e.store.observe(ctx, e.budget, r.ExternalRef)
			if err != nil {
				return mutationRequest{}, err
			}
			if r.ExpectedObservation != 0 && o.Provider.Revision != r.ExpectedObservation {
				return mutationRequest{}, operationConflict("discussion observation changed", nil)
			}
			state, err := stampState(o.State, attribution, false)
			if err != nil {
				return mutationRequest{}, err
			}
			revision := mutationRequest{intent: "revise", id: r.ExternalRef.ExternalID, key: attribution.key, parents: headIDs(o.Heads), state: &state}
			// Validate both complete commands before the first potentially applied fact.
			if _, err := revision.command(e.actor, actorName); err != nil {
				return mutationRequest{}, err
			}
			if _, err := request.command(e.actor, actorName); err != nil {
				return mutationRequest{}, err
			}
			return revision, nil
		})
		applied = applied || attr.AppliedThisCall
		if err != nil {
			return fail(err)
		}
		attrFound = true
	}
	if !childFound {
		child, err = e.run(ctx, comment, func() (mutationRequest, error) { return request, nil })
		applied = applied || child.AppliedThisCall
		if err != nil {
			return fail(err)
		}
		childFound = true
	}
	// No mutable post-read: the full original pair is the successful result even
	// when a human later changes the issue, or a concurrent actor won both keys.
	proof, err := originalProof(child.Event, []issueOperation{attr.Event})
	if err != nil {
		return fail(err)
	}
	timestamp, err := time.Parse(time.RFC3339Nano, child.Event.Timestamp)
	if err != nil {
		return fail(protocolFailure(err))
	}
	entry := tracker.DiscussionEntry{ExternalCommentID: child.Event.EventID, Author: r.Author, Body: *child.Event.Body, OperationMarker: r.OperationID.String(), CreatedAt: timestamp, Observation: proof.Revision}
	result.Entry, err = tracker.NewAttributedDiscussionEntry(entry, child.Event.Actor, proof)
	if err != nil {
		return fail(err)
	}
	result.PriorMutation = prior
	result.Replayed = attr.Replayed && child.Replayed && !applied
	result.Noop = result.Replayed
	return result, nil
}
func (b *BoundAdapter) ListDiscussion(ctx context.Context, ref tracker.ExternalWorkRef) ([]tracker.DiscussionEntry, error) {
	observed, err := b.ObserveDiscussion(ctx, ref)
	if err != nil {
		return nil, err
	}
	entries := observed.Entries()
	result := make([]tracker.DiscussionEntry, len(entries))
	for i, entry := range entries {
		result[i] = entry.Entry()
	}
	return result, nil
}
func (b *BoundAdapter) ObserveDiscussion(ctx context.Context, ref tracker.ExternalWorkRef) (tracker.DiscussionObservation, error) {
	e, err := b.operations(ctx)
	if err != nil {
		return tracker.DiscussionObservation{}, err
	}
	defer e.budget.close()
	if err := validateOperationReference(e.project, ref); err != nil {
		return tracker.DiscussionObservation{}, err
	}
	return observeDiscussionWithReader(ctx, e.budget, ref, b)
}

type discussionReader interface {
	observe(context.Context, *observationBudget, tracker.ExternalWorkRef) (workObservation, error)
	lookupActorOperation(context.Context, *observationBudget, string, string) (issueOperation, envelope, error)
}

func observeDiscussionWithReader(ctx context.Context, budget *observationBudget, ref tracker.ExternalWorkRef, reader discussionReader) (tracker.DiscussionObservation, error) {
	for {
		result, err := discussionAttempt(ctx, budget, ref, reader)
		if !staleObservation(err) {
			return result, err
		}
		if err := budget.restart(); err != nil {
			return tracker.DiscussionObservation{}, operationConflict("discussion snapshot restart budget exhausted", err)
		}
	}
}
func discussionAttempt(ctx context.Context, budget *observationBudget, ref tracker.ExternalWorkRef, b discussionReader) (tracker.DiscussionObservation, error) {
	o, err := b.observe(ctx, budget, ref)
	if err != nil {
		return tracker.DiscussionObservation{}, err
	}
	entries := make([]tracker.DiscussionEntry, len(o.Comments))
	actors := make([]string, len(o.Comments))
	children := []issueOperation{}
	for i, row := range o.Comments {
		timestamp, err := time.Parse(time.RFC3339Nano, row.Timestamp)
		if err != nil {
			return tracker.DiscussionObservation{}, protocolFailure(err)
		}
		entry := tracker.DiscussionEntry{ExternalCommentID: row.ID, Author: row.Actor, Body: row.Body, OperationMarker: row.Operation, CreatedAt: timestamp}
		if strings.HasPrefix(row.Operation, discussionKeyPrefix) {
			comment, env, err := b.lookupActorOperation(ctx, budget, row.Actor, row.Operation)
			if err != nil {
				return tracker.DiscussionObservation{}, err
			}
			if env.Snapshot != o.Snapshot {
				return tracker.DiscussionObservation{}, errSnapshotChanged
			}
			if comment.IssueID != ref.ExternalID || !sameHistoryOperation(row, comment) {
				return tracker.DiscussionObservation{}, operationConflict("original comment differs from observed history", nil)
			}
			attribution, env, err := b.lookupActorOperation(ctx, budget, row.Actor, discussionAttributionKey(row.Operation))
			if err != nil {
				return tracker.DiscussionObservation{}, err
			}
			if env.Snapshot != o.Snapshot {
				return tracker.DiscussionObservation{}, errSnapshotChanged
			}
			intent, stamp, err := verifyDiscussionPair(ref, comment, attribution)
			if err != nil {
				return tracker.DiscussionObservation{}, err
			}
			entry.Author = intent.Author
			entry.OperationMarker = stamp.Operation.String()
			children = append(children, comment, attribution)
		}
		entries[i] = entry
		actors[i] = row.Actor
	}
	proof, err := observationProof(ref.ExternalID, o.Snapshot, struct {
		Kind       string                    `json:"kind"`
		WorkDigest string                    `json:"work_digest"`
		Children   []issueOperation          `json:"children"`
		Entries    []tracker.DiscussionEntry `json:"entries"`
	}{"discussion-observation/1", o.Provider.Digest, children, entries})
	if err != nil {
		return tracker.DiscussionObservation{}, err
	}
	attributed := make([]tracker.AttributedDiscussionEntry, len(entries))
	for i, entry := range entries {
		entry.Observation = proof.Revision
		attributed[i], err = tracker.NewAttributedDiscussionEntry(entry, actors[i], proof)
		if err != nil {
			return tracker.DiscussionObservation{}, err
		}
	}
	return tracker.NewDiscussionObservation(attributed, proof, o.Provider)
}
func verifyDiscussionPair(ref tracker.ExternalWorkRef, comment, attribution issueOperation) (discussionIntent, operationStamp, error) {
	var intent discussionIntent
	stamp, err := stateOperationStamp(attribution)
	if err != nil {
		return intent, stamp, err
	}
	if err := decodeStrict([]byte(stamp.Intent), &intent); err != nil {
		return intent, stamp, operationConflict("invalid original discussion intent", err)
	}
	if immutableReference(ref) != intent.Reference || comment.Body == nil {
		return intent, stamp, operationConflict("discussion attribution mapping differs", nil)
	}
	request := tracker.AppendDiscussionRequest{ExternalRef: ref, OperationID: stamp.Operation, Author: intent.Author, Body: *comment.Body}
	attrAction, commentAction, err := discussionActions(ref, request)
	if err != nil {
		return intent, stamp, err
	}
	if comment.Operation != commentAction.key || attribution.Operation != attrAction.key || comment.Actor != attribution.Actor || comment.IssueID != attribution.IssueID || stamp.Action != commentAction.action || stamp.Intent != commentAction.intent || stamp.SemanticDigest != commentAction.digest || stamp.Target != nil || stamp.Noop {
		return intent, stamp, operationConflict("signed discussion pair attribution differs", nil)
	}
	if err := attrAction.verify(attribution); err != nil {
		return intent, stamp, err
	}
	if err := commentAction.verify(comment); err != nil {
		return intent, stamp, err
	}
	return intent, stamp, nil
}
