//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"testing"

	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// PREPARED ONLY. ActualSourceBeforeNative is CLOSED. The intentional clean and
// unclean positive controls remain in TestExpiryHostNativeRequiredInitialCleanUncleanCrossProduct.
func TestExpiryHostNativePriorRevocationRefusesBothRestartModes(t *testing.T) {
	for _, revocation := range []string{"gate_close", "failed_live_owner_cleanup_stale"} {
		for _, unclean := range []bool{false, true} {
			mode := "clean"
			if unclean {
				mode = "unclean"
			}
			t.Run(revocation+"/"+mode, func(t *testing.T) {
				config := expiryNativeConfig(t, &expiryActualACKObserver{})
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
				if run.owner.lifecycle.InspectOwnership().State != sidecargapdb.OwnershipOwnedLive {
					t.Fatal("positive live predecessor missing")
				}
				if revocation == "gate_close" {
					if err := run.gate.Close(); err != nil {
						t.Fatal("prior actual gate close", err)
					}
				} else {
					if err := run.owner.lifecycle.CleanupStale(); err == nil {
						t.Fatal("live-owner CleanupStale unexpectedly succeeded")
					}
				}
				select {
				case <-run.gate.Done():
				default:
					t.Fatal("prior revocation not completed")
				}
				if run.owner.lifecycle.InspectOwnership().State != sidecargapdb.OwnershipOwnedLive {
					t.Fatal("prior revocation did not retain original live-process trace")
				}
				var nextHost *Host
				var nextRun *expiryHostRun
				if unclean {
					nextHost, nextRun, err = run.restartUnclean(t.Context(), "supervisor-expiry-negative-unclean")
				} else {
					nextHost, nextRun, err = run.restartClean(t.Context(), "supervisor-expiry-negative-clean")
				}
				if err == nil || nextHost != nil || nextRun != nil {
					t.Fatal("prior revocation laundered into successor")
				}
				run.lease.mu.Lock()
				same := run.lease.current == run && run.lease.control == nil && run.lease.failed
				run.lease.mu.Unlock()
				if !same {
					t.Fatal("loser altered ownership or acquired controller")
				}
				finalErr := run.close(context.Background())
				cleaned = true
				t.Logf("FIRST final close: expected scenario rejection; actual error=%v", finalErr)
				if finalErr == nil {
					t.Fatal("failed final cleanup repaired prior revocation")
				}
				// A rejected scenario cannot excuse a real cleanup failure. These
				// are fresh read-only final reports, never another close or repair.
				cleanupErr := run.cleanupOutcomes()
				rootErr := run.lease.cleanup.outcome(true)
				t.Logf("POST-RUNDOWN actual full owner/Host/gate error=%v; full root error=%v", cleanupErr, rootErr)

				l := run.lease
				l.mu.Lock()
				c := l.control // The final controller did not exist before close.
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
