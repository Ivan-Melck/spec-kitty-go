package provingground

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
)

func TestZombieMayWriteW1ButHasZeroGovernedAuthority(t *testing.T) {
	git := newFakeGit(t)
	service := mustService(t, &recordingApplier{}, git)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	a, _ := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "zombie-a", now))
	w1, err := service.EnsureWorkspace(t.Context(), workspaceRequest(a, "zombie-w1", now.Add(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Expire(t.Context(), "expire-zombie", a, a.Deadline); err != nil {
		t.Fatal(err)
	}
	// This is the adversarial fact: POSIX does not know about a fence token.
	zombieFile := filepath.Join(w1.CanonicalPath, "zombie-output.txt")
	if err := os.WriteFile(zombieFile, []byte("zombie resumed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if contents, err := os.ReadFile(zombieFile); err != nil || string(contents) != "zombie resumed" {
		t.Fatalf("zombie write did not occur: %q %v", contents, err)
	}
	for _, action := range []string{"heartbeat", "handoff", "review", "integrate", "publish-effect"} {
		if err := service.Authorize(a, action, a.Deadline.Add(time.Second)); !IsOutcome(err, OutcomeStaleLease) {
			t.Fatalf("zombie action %s survived: %v", action, err)
		}
	}
	cRequest := claimRequest("work-a", "actor-a", "process-c", "zombie-c", a.Deadline.Add(time.Second))
	c, err := service.Claim(t.Context(), cRequest)
	if err != nil {
		t.Fatal(err)
	}
	w3, err := service.EnsureWorkspace(t.Context(), workspaceRequest(c, "zombie-w3", cRequest.Now.Add(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if w1.WorkspaceID == w3.WorkspaceID || w1.CanonicalPath == w3.CanonicalPath {
		t.Fatal("replacement inherited zombie workspace")
	}
}

func TestZombiePredecessorEpochRefusesIndependentlyOfLease(t *testing.T) {
	firstApplier := &recordingApplier{}
	secondApplier := &recordingApplier{}
	service := mustService(t, firstApplier, newFakeGit(t))
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	a, _ := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "epoch-a", now))
	if err := service.ReplaceRuntime(t.Context(), "restart-one", "epoch-two", secondApplier, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(secondApplier.commandTypes(), "runtime.owner_restart") || slices.Contains(secondApplier.commandTypes(), "authority.expire") {
		t.Fatalf("restart and lease expiry were conflated: %v", secondApplier.commandTypes())
	}
	if err := service.Authorize(a, "heartbeat", now.Add(2*time.Second)); !IsOutcome(err, OutcomeStaleEpoch) {
		t.Fatalf("predecessor epoch survived: %v", err)
	}
	request := claimRequest("work-a", "actor-a", "process-c", "epoch-c", now.Add(2*time.Second))
	request.RuntimeEpochID = "epoch-two"
	c, err := service.Claim(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if c.RuntimeEpochID != "epoch-two" || c.Generation != a.Generation+1 {
		t.Fatalf("restart/replacement coordinates conflated: a=%+v c=%+v", a, c)
	}
	projection := service.Projection()
	restoredConfig := testConfig(t)
	restoredConfig.RuntimeEpochID = "epoch-two"
	restored, err := Restore(restoredConfig, secondApplier, nil, projection)
	if err != nil {
		t.Fatal(err)
	}
	thirdApplier := &recordingApplier{}
	if err := restored.ReplaceRuntime(t.Context(), "restart-two", "epoch-three", thirdApplier, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := restored.Projection().Runtime; got.RuntimeEpochID != "epoch-three" || got.PredecessorEpoch != "epoch-two" || got.LogicalVersion != projection.Runtime.LogicalVersion+1 {
		t.Fatalf("restored restart projection=%+v prior=%+v", got, projection.Runtime)
	}
}

func TestZombieIsolationFocusedCampaign(t *testing.T) {
	err := RunFocusedCampaign(t.Context(), 100, func(ctx context.Context, index int) error {
		service := mustService(t, &recordingApplier{}, nil)
		now := time.Date(2026, 8, 30, 12, 0, index, 0, time.UTC)
		a, err := service.Claim(ctx, claimRequest("work-a", "actor-a", "process-a", fmt.Sprintf("zombie-campaign-a-%d", index), now))
		if err != nil {
			return err
		}
		if err := service.Expire(ctx, controljournal.OperationID(fmt.Sprintf("zombie-expire-%d", index)), a, a.Deadline); err != nil {
			return err
		}
		for _, action := range []string{"heartbeat", "handoff", "review", "integrate"} {
			if err := service.Authorize(a, action, a.Deadline.Add(time.Second)); !IsOutcome(err, OutcomeStaleLease) {
				return fmt.Errorf("stale action %s survived: %w", action, err)
			}
		}
		request := claimRequest("work-a", "actor-a", "process-c", fmt.Sprintf("zombie-campaign-c-%d", index), a.Deadline.Add(time.Second))
		if _, err := service.Claim(ctx, request); err != nil {
			return err
		}
		assertAuthorityInvariants(t, service)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRestoreRejectsMissingWorkAndDuplicateWorkspaceOperationMutants(t *testing.T) {
	git := newFakeGit(t)
	service := mustService(t, &recordingApplier{}, git)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	a, err := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "restore-mutant", now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.EnsureWorkspace(t.Context(), workspaceRequest(a, "restore-mutant-w1", now.Add(time.Second))); err != nil {
		t.Fatal(err)
	}
	projection := service.Projection()
	missing := projection
	missing.Works = append([]WorkProjection(nil), projection.Works[:1]...)
	if _, err := Restore(testConfig(t), &recordingApplier{}, git, missing); !IsOutcome(err, OutcomeInvalidCommand) {
		t.Fatalf("missing work census restored: %v", err)
	}
	duplicateOperation := projection
	copyBinding := projection.Workspaces[0]
	copyBinding.WorkspaceID = "workspace-duplicate-op"
	copyBinding.CanonicalPath += "-duplicate"
	copyBinding.GitDir += "-duplicate"
	copyBinding.IndexPath += "-duplicate"
	copyBinding.Status = "quarantined"
	copyBinding.QuarantineReason = "test stale copy"
	duplicateOperation.Workspaces = append(append([]WorkspaceBinding(nil), projection.Workspaces...), copyBinding)
	if _, err := Restore(testConfig(t), &recordingApplier{}, git, duplicateOperation); !IsOutcome(err, OutcomeInvalidCommand) {
		t.Fatalf("duplicate workspace operation restored: %v", err)
	}
}
