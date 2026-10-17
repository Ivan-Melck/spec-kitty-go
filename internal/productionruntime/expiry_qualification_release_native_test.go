//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

// PREPARED ONLY: source review and fresh native evidence are still required.
// Neither passive EOF nor the daemon deadline earns an explicit R/A witness.
// Both controls require scenario rejection AND independently successful cleanup.
func TestExpiryHostNativeEOFAndDaemonTimeoutControls(t *testing.T) {
	for _, mode := range []string{"EOF", "daemon_timeout"} {
		for _, actual := range []bool{false, true} {
			name := mode + "/private_only"
			if actual {
				name = mode + "/actual_ACK"
			}
			t.Run(name, func(t *testing.T) {
				observer := &expiryActualACKObserver{}
				config := expiryNativeConfig(t, observer)
				_, run, err := startExpiryHostQualification(t.Context(), config)
				if err != nil {
					t.Fatal(err)
				}
				cleaned := false
				t.Cleanup(func() {
					if !cleaned {
						t.Logf("failed-run final cleanup/residual (not PASS): %v", run.close(context.Background()))
					}
				})
				var first journalv2.AcknowledgedPublicationV2
				if actual {
					// genuine restored coordinator, initial & continuation
					coordinator := expiryNativeCoordinator(t, run.host, observer)
					first = publishLiveProofOperation(t, run.host, coordinator, nil)
					_ = publishLiveProofOperation(t, run.host, coordinator, &first)
					if len(observer.acks) != 2 {
						t.Fatal("real initial+continuation ACKs not captured")
					}
				}
				// baseline private6 + publication0 or7
				privateRows := uint64(6)
				publicationRows := uint64(0)
				if actual {
					publicationRows = expiryFixtureContinuationPublicationRows
				}
				baseline, err := run.host.VerifyRetainedRecords(t.Context(), observer.acks)
				if err != nil || baseline.MatchedPrivateRecords() != privateRows || baseline.MatchedPublicationRecords() != publicationRows || baseline.MatchedRecords() != privateRows+publicationRows {
					t.Fatal("exact baseline closure differs", err)
				}

				// exact hidden TTL extra with adapter ConditionalMutate
				owner := run.owner
				expires := time.Now().UTC().Add(2 * time.Second)
				inserted, err := owner.adapter.ConditionalMutate(t.Context(), "expiry-host-extra-insert",
					storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: "spk/expiry-evidence/extra", Value: []byte("expiry-host-extra-v1"),
						Condition: storegapdb.Condition{Kind: storegapdb.ConditionAbsent}, ExpiresAt: &expires},
					storegapdb.AckDurable)
				if err != nil || inserted.Revision == 0 || inserted.Ack != storegapdb.AckDurable || inserted.DurableThrough < inserted.Revision || inserted.MutationCount != 1 {
					t.Fatalf("actual future TTL insertion: %v %+v", err, inserted)
				}
				if err := run.host.refreshProjection(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := run.gate.WaitHeld(t.Context()); err != nil {
					t.Fatal("actual H", err)
				}
				// authenticate held S1 negative closure
				if err := run.observeS1Refusal(t.Context(), observer.acks); err != nil {
					t.Fatal("complete private S1/S2/terminal/Consume/outer closure", err)
				}

				// Capture the held revision BEFORE closing EOF: expiry can commit
				// immediately after Close returns. A post-Close baseline is racy.
				ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
				defer cancel()
				inspectStart, err := owner.adapter.Inspect(ctx)
				if err != nil {
					t.Fatal(err)
				}
				provenance := owner.lifecycle.Provenance()
				if !inspectStart.Healthy || inspectStart.Lifecycle != "ready" || inspectStart.DatabaseID != string(run.composition.database) || inspectStart.OwnerPID != provenance.Process.PID || inspectStart.OwnerStartedAt != provenance.Observation.Status.OwnerStartedAt || inspectStart.CurrentRevision != inserted.Revision || inspectStart.DurableThrough != inserted.Revision || ctx.Err() != nil {
					t.Fatal("original held coordinate differs before passive release")
				}
				parentClosedBeforeAdvance := false
				if mode == "EOF" {
					// call ORIGINAL run.gate.Close ONCE to close parent; no r.invalidate/fail/manual product flags
					if err := run.gate.Close(); err != nil {
						t.Fatal("prior actual gate close", err)
					}
					parentClosedBeforeAdvance = true
				} else {
					// daemon_timeout: DO NOT call gate/checkHeld/close or cancel any watched context before real durable revision advances
					// leave parent endpoint open and send noR
				}

				// Passive original adapter.Inspect loop with same owner/db/start and durable revision checks
				if ctx.Err() != nil {
					t.Fatal("caller cancellation cannot cause timeout control release")
				}
				tick := time.NewTicker(time.Millisecond)
				defer tick.Stop()
				for {
					select {
					case <-ctx.Done():
						t.Fatal("no real durable expiry revision", ctx.Err())
					case <-tick.C:
					}
					// Passive loop: no gate operations at all in daemon_timeout branch before advance
					status, err := owner.adapter.Inspect(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if !status.Healthy || status.Lifecycle != "ready" || status.DatabaseID != string(run.composition.database) ||
						status.OwnerPID != provenance.Process.PID || status.OwnerStartedAt != provenance.Observation.Status.OwnerStartedAt {
						t.Fatal("original owner lost before expiry")
					}
					if status.CurrentRevision > inserted.Revision && status.DurableThrough == status.CurrentRevision {
						// passive durable expiry advance observed
						if _, err := owner.adapter.Get(ctx, "spk/expiry-evidence/extra"); !storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
							t.Fatal("expired extra still present", err)
						}
						if ctx.Err() != nil {
							t.Fatal("caller cancellation cannot cause timeout control release")
						}
						if err := run.host.refreshProjection(ctx); err != nil {
							t.Fatal(err)
						}
						// normal h.VerifyRetainedRecords exact6+0/7 matched; this does NOT make a valid R/A qualification
						proof, err := run.host.VerifyRetainedRecords(ctx, observer.acks)
						if err != nil {
							t.Fatal("refreshed exact closure counts", err)
						}
						if proof.MatchedPrivateRecords() != privateRows || proof.MatchedPublicationRecords() != publicationRows ||
							proof.MatchedRecords() != privateRows+publicationRows {
							t.Fatalf("refreshed closure mismatch private=%d pub=%d expected %d+%d",
								proof.MatchedPrivateRecords(), proof.MatchedPublicationRecords(), privateRows, publicationRows)
						}
						beforeRev := inspectStart.CurrentRevision
						afterRev := status.CurrentRevision
						t.Logf("PASSIVE expiry advance: mode=%s; before=%d; after=%d; publication=%d; private=%d; parent_closed_before_advance=%t; explicitRA=false",
							mode, beforeRev, afterRev, publicationRows, privateRows, parentClosedBeforeAdvance)
						break
					}
				}

				// Require gate.CheckHeld(ctx) refuses (EOF canceled; timeout local12s exceeded) and released/matchedAfterExpiry flags false underlease.mu
				if err := run.gate.CheckHeld(ctx); err == nil {
					t.Fatal("gate.CheckHeld did NOT refuse after passive advance")
				}
				run.lease.mu.Lock()
				released := run.released
				matchedAfterExpiry := run.matchedAfterExpiry
				run.lease.mu.Unlock()
				if released || matchedAfterExpiry {
					t.Fatal("released or matchedAfterExpiry true before final close")
				}
				// don't call run.release because its fail would intentionally cancel generation prematurely

				// Final run.close(context.Background()) exactlyONCE must return scenario rejection
				finalErr := run.close(context.Background())
				cleaned = true
				t.Logf("FIRST final close: expected scenario rejection; actual error=%v", finalErr)
				if finalErr == nil {
					t.Fatal("failed final cleanup repaired passive expiry control")
				}
				cleanupErr := run.cleanupOutcomes()
				rootErr := run.lease.cleanup.outcome(true)
				t.Logf("POST-RUNDOWN actual full owner/Host/gate error=%v; full root error=%v", cleanupErr, rootErr)

				l := run.lease
				l.mu.Lock()
				c := l.control
				canonicalClosed := c != nil && c.lease == l && c.kind == expiryControlFinal &&
					c.predecessor == run && c.candidate == nil && l.current == run &&
					c.stage == expiryControlTerminalClosed && l.phase == expiryLeaseClosed &&
					c.metadata == nil && run.window != nil && l.quiescentLocked(run)
				ownerRecorded, hostRecorded := run.ownerCloseRecorded, run.hostCloseRecorded
				ownerErr, hostErr := run.firstOwnerCloseErr, run.firstHostCloseErr
				gateRecorded := run.gateCloseRecorded.Load()
				var gateErr error
				if first := run.firstGateCloseErr.Load(); first != nil {
					gateErr = first.err
				}
				var rootCounts [3][3]uint64
				for i, kind := range []expiryRootDescriptorKind{expiryRootParent, expiryRootData, expiryRootLock} {
					category := &l.cleanup.categories[kind]
					rootCounts[i] = [3]uint64{category.acquired.Load(), category.attempted.Load(), category.completed.Load()}
				}
				l.mu.Unlock()

				t.Logf("POST-RUNDOWN canonical final closed/quiescent=%t; first owner recorded=%t error=%v; first Host recorded=%t error=%v; first gate recorded=%t error=%v",
					canonicalClosed, ownerRecorded, ownerErr, hostRecorded, hostErr, gateRecorded, gateErr)
				for i, name := range []string{"parent", "data", "lock"} {
					t.Logf("POST-RUNDOWN required root %s acquired/attempted/completed=%v", name, rootCounts[i])
				}
				if cleanupErr != nil || rootErr != nil || !canonicalClosed || !ownerRecorded || !hostRecorded || !gateRecorded ||
					ownerErr != nil || hostErr != nil || gateErr != nil {
					t.Fatal("expected scenario rejection masked actual first-error/incomplete cleanup")
				}
				for i, name := range []string{"parent", "data", "lock"} {
					if rootCounts[i] != [3]uint64{1, 1, 1} {
						t.Fatalf("required root %s first closes not complete: %v", name, rootCounts[i])
					}
				}
			})
		}
	}
}
