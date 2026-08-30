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
	"github.com/spec-kitty/go-kitty3/internal/controljournal/checkpoint"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
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
	thirdApplier := &recordingApplier{}
	if err := service.ReplaceRuntime(t.Context(), "restart-two", "epoch-three", thirdApplier, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := service.Projection().Runtime; got.RuntimeEpochID != "epoch-three" || got.PredecessorEpoch != "epoch-two" || got.LogicalVersion != 2 {
		t.Fatalf("second restart projection=%+v", got)
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

type emptyRestoreHeads struct{}

func (emptyRestoreHeads) Get(context.Context, string) (storegapdb.Record, error) {
	return storegapdb.Record{}, &storegapdb.Error{Kind: storegapdb.OutcomeNotFound}
}
func (emptyRestoreHeads) Scan(context.Context, storegapdb.ScanRequest) (storegapdb.ScanPage, error) {
	return storegapdb.ScanPage{}, nil
}

func TestRestoreRejectsCallerBuiltCheckpointResult(t *testing.T) {
	invented := checkpoint.RestoreResultV1{Ready: true, Pristine: true}
	if _, err := restoreValidated(t.Context(), testConfig(t), &recordingApplier{}, newFakeGit(t), emptyRestoreHeads{}, invented); !IsOutcome(err, OutcomeInvalidCommand) {
		t.Fatalf("caller-built restore result granted authority: %v", err)
	}
}
