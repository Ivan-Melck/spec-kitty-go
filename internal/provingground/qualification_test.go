package provingground

import (
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/faults"
)

func TestQualificationSeamUsesProductionHookAndReturnsDefensiveSnapshot(t *testing.T) {
	core := mustService(t, &recordingApplier{}, newFakeGit(t))
	runtime := &Runtime{core: core}
	hook := &cutHook{id: "unused", remaining: 1}
	if runtime.WithFaults(hook) != runtime || core.faults != hook {
		t.Fatal("qualification hook did not reach the composed runtime")
	}
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	authority, err := core.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "qualification", now))
	if err != nil {
		t.Fatal(err)
	}
	first := runtime.QualificationSnapshot()
	if first.RuntimeEpochID != core.config.RuntimeEpochID || len(first.Authorities) != 1 || first.Authorities[0].LeaseID != authority.LeaseID || len(first.Workspaces) != 0 {
		t.Fatalf("qualification snapshot = %+v", first)
	}
	first.Authorities[0].LeaseID = "mutated-copy"
	second := runtime.QualificationSnapshot()
	if second.Authorities[0].LeaseID != authority.LeaseID {
		t.Fatalf("snapshot mutation escaped into runtime: %+v", second)
	}
	runtime.WithFaults(nil)
	if _, ok := core.faults.(faults.Noop); !ok {
		t.Fatalf("nil hook did not restore no-op: %T", core.faults)
	}
}
