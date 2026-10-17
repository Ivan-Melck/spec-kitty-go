package productionruntime

import (
	"bytes"
	"context"
	"errors"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"reflect"
	"testing"
)

func TestAcknowledgedPublicationProofMissingClaimReader(t *testing.T) {
	h, f := newPublicationProofFixture(t, false, journalv2.Scope{Kind: journalv2.ScopeHost}, "operation-claim", "claim")
	if f.observerACKs[0].Plan().WorkspacePathClaim() == nil {
		t.Fatal("fixture missing sealed claim")
	}
	assertProofRefused(t, h, f.observerACKs[0], ProgressIncomplete)
	if f.commits != 1 {
		t.Fatal("proof wrote")
	}
}

type claimProofReader struct {
	*publicationProofFixture
	claim    journalv2.WorkspacePathClaimV2
	revision uint64
	reads    int
	err      error
	cancel   context.CancelFunc
}

func (f *claimProofReader) ObserveAcknowledgedWorkspacePathClaimV2(ctx context.Context, captured journalv2.StoreRevisionV2, ack journalv2.AcknowledgedPublicationV2) (journalv2.WorkspacePathClaimV2, uint64, error) {
	f.reads++
	if f.cancel != nil {
		f.cancel()
	}
	if err := ctx.Err(); err != nil {
		return journalv2.WorkspacePathClaimV2{}, 0, err
	}
	if captured != f.captured {
		return journalv2.WorkspacePathClaimV2{}, 0, errors.New("crosscapture")
	}
	return f.claim, f.revision, f.err
}
func (f *claimProofReader) verifyAcknowledgedPublication(ctx context.Context, c journalv2.StoreRevisionV2, ack journalv2.AcknowledgedPublicationV2, origin ...journalv2.AcknowledgedPublicationV2) (AcknowledgedPublicationProof, error) {
	return verifyAcknowledgedPublication(ctx, f, c, ack, origin...)
}
func TestAcknowledgedPublicationProofClaimControls(t *testing.T) {
	for _, fault := range []string{"valid", "missing", "wrong_bytes", "wrong_digest", "wrong_revision", "crosscapture", "canceled", "read_canceled", "moved", "absent_foreign_claim", "zero_ack"} {
		t.Run(fault, func(t *testing.T) {
			ids := []string{"operation-claim", "claim"}
			if fault == "absent_foreign_claim" {
				ids = nil
			}
			h, f := newPublicationProofFixture(t, false, journalv2.Scope{Kind: journalv2.ScopeHost}, ids...)
			ack := f.observerACKs[0]
			expected := ack.Plan().WorkspacePathClaim()
			if expected == nil {
				_, other := newPublicationProofFixture(t, false, journalv2.Scope{Kind: journalv2.ScopeHost}, "operation-foreign", "claim")
				expected = other.observerACKs[0].Plan().WorkspacePathClaim()
			}
			r := &claimProofReader{publicationProofFixture: f, claim: *expected, revision: ack.ProviderRevision()}
			h.owner = r
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch fault {
			case "missing":
				r.err = &journalv2.Error{Kind: journalv2.ErrorNotFound}
			case "wrong_bytes":
				r.claim, _ = journalv2.NewWorkspacePathClaimV2(expected.ClaimDigest(), []byte(`{"workspace_id":"wrong"}`))
			case "wrong_digest":
				d, _ := journalv2.ReducerStateDigestV2([]byte(`{"foreign":true}`))
				r.claim, _ = journalv2.NewWorkspacePathClaimV2(d, expected.CanonicalValue())
			case "wrong_revision":
				r.revision++
			case "crosscapture":
				r.captured, _ = journalv2.NewStoreRevisionV2("other-db", 1, 1)
			case "canceled":
				cancel()
			case "read_canceled":
				r.cancel = cancel
			case "moved":
				f.verifyErr = errors.New("moved")
			case "zero_ack":
				ack = journalv2.AcknowledgedPublicationV2{}
			}
			proof, err := h.VerifyAcknowledgedPublication(ctx, ack)
			if fault == "valid" {
				if err != nil || proof.WorkspacePathClaim == nil || proof.ClaimRecordRevision != ack.ProviderRevision() || !bytes.Equal(proof.WorkspacePathClaim.CanonicalValue(), expected.CanonicalValue()) {
					t.Fatalf("missing permanent evidence: %v", err)
				}
				clone := proof.WorkspacePathClaim.CanonicalValue()
				clone[0] = '!'
				*proof.WorkspacePathClaim = journalv2.WorkspacePathClaimV2{}
				again, err := h.VerifyAcknowledgedPublication(ctx, ack)
				if err != nil || !bytes.Equal(again.WorkspacePathClaim.CanonicalValue(), expected.CanonicalValue()) {
					t.Fatal("aliased proof")
				}
			} else if fault == "absent_foreign_claim" {
				if err != nil || proof.WorkspacePathClaim != nil || proof.ClaimRecordRevision != 0 || r.reads != 0 {
					t.Fatal("foreign claim disclosure")
				}
			} else {
				if err == nil || !reflect.ValueOf(proof).IsZero() {
					t.Fatalf("partial proof: %v", err)
				}
				if fault == "canceled" || fault == "read_canceled" {
					if !errors.Is(err, context.Canceled) {
						t.Fatal("lost cancellation")
					}
				} else if fault == "moved" {
					if !IsProgressKind(err, ProgressResyncRequired) {
						t.Fatal(err)
					}
				} else if !IsProgressKind(err, ProgressIncomplete) {
					t.Fatal(err)
				}
				if (fault == "canceled" || fault == "zero_ack") && r.reads != 0 {
					t.Fatal("invalid ACK read claim")
				}
			}
			if f.commits != 1 || h.state != HostReady {
				t.Fatal("observation changed admission")
			}
		})
	}
}
