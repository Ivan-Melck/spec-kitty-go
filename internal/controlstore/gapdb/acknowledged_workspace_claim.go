package gapdb

import (
	"bytes"
	"context"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// ObserveAcknowledgedWorkspacePathClaimV2 observes only the permanent claim
// created by this actual opaque coordinator ACK. It is not a recovery address,
// arbitrary lookup, workspace admission, or whole-trace certification. The
// returned domain value owns its bytes; revision is the original creation R.
func (a *Adapter) ObserveAcknowledgedWorkspacePathClaimV2(ctx context.Context, captured journalv2.StoreRevisionV2, ack journalv2.AcknowledgedPublicationV2) (journalv2.WorkspacePathClaimV2, uint64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fail := func(kind journalv2.ErrorKind, field string) (journalv2.WorkspacePathClaimV2, uint64, error) {
		return journalv2.WorkspacePathClaimV2{}, 0, recoveryError(kind, field)
	}
	if err := ctx.Err(); err != nil {
		return journalv2.WorkspacePathClaimV2{}, 0, err
	}
	plan := ack.Plan()
	state := plan.OperationState()
	if a == nil || a.client == nil || ack.ProviderRevision() == 0 || ack.DurableThroughRevision() < ack.ProviderRevision() ||
		ack.PublicationID() != plan.PublicationID() || ack.PlanDigest().IsZero() || ack.PlanDigest() != plan.Digest() ||
		ack.RecordSetDigest().IsZero() || ack.RecordSetDigest() != plan.RecordSetDigest() || ack.MutationCount() != plan.MutationCount() ||
		ack.AssertionCount() != plan.AssertionCount() || plan.MutationCount() == 0 || state.LastPublicationID() != ack.PublicationID() || state.LogicalVersion() == 0 {
		return fail(journalv2.ErrorInvalidCanonicalData, "acknowledged_publication")
	}
	if _, err := journalv2.NewStoreRevisionV2(captured.DatabaseID(), captured.CurrentRevision(), captured.DurableThroughRevision()); err != nil ||
		ack.ProviderRevision() > captured.CurrentRevision() || ack.DurableThroughRevision() > captured.DurableThroughRevision() {
		return fail(journalv2.ErrorInvalidVersion, "publication_coordinate")
	}
	claim := plan.WorkspacePathClaim()
	if claim == nil {
		return fail(journalv2.ErrorInvalidCanonicalData, "workspace_path_claim")
	}
	key, err := v2WorkspacePathClaimKey(claim.ClaimDigest())
	if err != nil {
		return fail(journalv2.ErrorInvalidCanonicalData, "workspace_path_claim")
	}
	expected := claim.CanonicalValue()
	owned, err := journalv2.NewWorkspacePathClaimV2(claim.ClaimDigest(), expected)
	if err != nil || len(expected) == 0 || len(expected) > v2ValueMaximum(key.Kind()) {
		return fail(journalv2.ErrorMalformed, "workspace_path_claim")
	}
	count := 0
	for _, sealed := range plan.Records() {
		if sealed.Role() != journalv2.RecordWorkspacePathClaim {
			continue
		}
		count++
		derived, err := v2KeyForSealedRecord(sealed)
		if err != nil || derived != key || !bytes.Equal(sealed.CanonicalValue(), expected) {
			return fail(journalv2.ErrorMalformed, "workspace_path_claim_record")
		}
	}
	if count != 1 {
		return fail(journalv2.ErrorMalformed, "workspace_path_claim_record")
	}
	// Public standalone observation must independently check database identity
	// and close its own capture; the enclosing proof verifies again after all reads.
	if err := a.VerifyRevisionV2(ctx, captured); err != nil {
		return journalv2.WorkspacePathClaimV2{}, 0, err
	}
	requestCtx, cancel := a.requestContext(ctx)
	defer cancel()
	record, err := a.client.Get(requestCtx, key.String())
	if err != nil {
		if ctx.Err() != nil {
			return journalv2.WorkspacePathClaimV2{}, 0, ctx.Err()
		}
		return journalv2.WorkspacePathClaimV2{}, 0, translateRecoveryError("workspace_path_claim", err)
	}
	parsed, parseErr := parseV2Key(record.Key)
	if record.Key != key.String() || parseErr != nil || parsed.Kind() != v2KeyWorkspacePathClaim || parsed.WorkspacePathClaimDigest() != claim.ClaimDigest() ||
		uint64(record.Revision) != ack.ProviderRevision() || record.ExpiresAt != nil || len(record.Value) == 0 || len(record.Value) > v2ValueMaximum(key.Kind()) || !bytes.Equal(record.Value, expected) {
		return fail(journalv2.ErrorMalformed, "workspace_path_claim_record")
	}
	if err := a.VerifyRevisionV2(ctx, captured); err != nil {
		return journalv2.WorkspacePathClaimV2{}, 0, err
	}
	if err := ctx.Err(); err != nil {
		return journalv2.WorkspacePathClaimV2{}, 0, err
	}
	return owned, uint64(record.Revision), nil
}
