package provingground

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/application"
)

type recordingApplier struct {
	revision atomic.Uint64
	calls    atomic.Int64
	mu       sync.Mutex
	commands []string
	arrived  chan struct{}
	release  <-chan struct{}
}

func (a *recordingApplier) Apply(ctx context.Context, batch application.ValidatedBatchV1) (application.ResultV1, error) {
	a.calls.Add(1)
	if product, err := batch.ProductBatch(); err == nil {
		if operation, err := controljournal.DecodeOperationRecordV1(product.Operation.Value); err == nil {
			a.mu.Lock()
			a.commands = append(a.commands, operation.CommandType)
			a.mu.Unlock()
		}
	}
	if a.arrived != nil {
		a.arrived <- struct{}{}
	}
	if a.release != nil {
		select {
		case <-a.release:
		case <-ctx.Done():
			return application.ResultV1{}, ctx.Err()
		}
	}
	return application.ResultV1{Kind: application.OutcomeCommitted, CommittedRevision: a.revision.Add(1)}, nil
}

func (a *recordingApplier) commandTypes() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.commands...)
}

func testConfig(t *testing.T) Config {
	t.Helper()
	actors, err := controljournal.NewTrialImplementationActorsV1("actor-a", "actor-b", "process-a", "process-b", "process-c")
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		ProjectID: "project-m1", MissionID: "mission-m1", WorkRevisionID: "revision-one", RunID: "run-m1",
		RuntimeEpochID: "epoch-one", Actors: actors, WorkIDs: []controljournal.WorkID{"work-a", "work-b"}, LeaseTTL: time.Minute,
	}
}

func claimRequest(work, actor, process, suffix string, now time.Time) ClaimRequest {
	return ClaimRequest{OperationID: controljournal.OperationID("claim-" + suffix), WorkRevisionID: "revision-one", RuntimeEpochID: "epoch-one", WorkID: controljournal.WorkID(work), ActorID: controljournal.ActorID(actor), ProcessID: controljournal.ProcessID(process), LeaseID: controljournal.LeaseID("lease-" + suffix), Now: now}
}

func mustService(t *testing.T, applier batchApplier, git GitWorkspace) *Service {
	t.Helper()
	service, err := New(testConfig(t), applier, git)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestAuthorityConcurrentDistinctClaimsOverlapAndBothSucceed(t *testing.T) {
	release := make(chan struct{})
	applier := &recordingApplier{arrived: make(chan struct{}, 2), release: release}
	service := mustService(t, applier, nil)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	requests := []ClaimRequest{
		claimRequest("work-a", "actor-a", "process-a", "a", now),
		claimRequest("work-b", "actor-b", "process-b", "b", now),
	}
	results := make(chan error, 2)
	for _, request := range requests {
		request := request
		go func() {
			_, err := service.Claim(t.Context(), request)
			results <- err
		}()
	}
	for range requests {
		select {
		case <-applier.arrived:
		case <-time.After(time.Second):
			t.Fatal("different work aggregates were globally serialized")
		}
	}
	close(release)
	for range requests {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	assertAuthorityInvariants(t, service)
}

func TestAuthorityHighContentionSameItemHasOneWinner(t *testing.T) {
	service := mustService(t, &recordingApplier{}, nil)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	const contenders = 64
	start := make(chan struct{})
	var successes atomic.Int64
	var failures atomic.Int64
	var wait sync.WaitGroup
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, err := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", fmt.Sprintf("same-%d", index), now))
			if err == nil {
				successes.Add(1)
			} else if IsOutcome(err, OutcomeAuthorityConflict) {
				failures.Add(1)
			} else {
				t.Errorf("untyped terminal outcome: %v", err)
			}
		}(index)
	}
	close(start)
	wait.Wait()
	if successes.Load() != 1 || failures.Load() != contenders-1 {
		t.Fatalf("successes=%d failures=%d", successes.Load(), failures.Load())
	}
	assertAuthorityInvariants(t, service)
}

func TestAuthorityTypedCoordinateRefusals(t *testing.T) {
	service := mustService(t, &recordingApplier{}, nil)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	authority, err := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "typed", now))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		kind   OutcomeKind
		mutate func(*Authority)
	}{
		{"revision", OutcomeStaleWorkRevision, func(a *Authority) { a.WorkRevisionID = "revision-old" }},
		{"lease", OutcomeStaleLease, func(a *Authority) { a.LeaseID = "lease-old" }},
		{"fence", OutcomeStaleFence, func(a *Authority) { a.FenceToken++ }},
		{"epoch", OutcomeStaleEpoch, func(a *Authority) { a.RuntimeEpochID = "epoch-old" }},
		{"process", OutcomeActorProcessMismatch, func(a *Authority) { a.ProcessID = "process-b" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutant := authority
			test.mutate(&mutant)
			if err := service.Authorize(mutant, "handoff", now.Add(time.Second)); !IsOutcome(err, test.kind) {
				t.Fatalf("got %v want %s", err, test.kind)
			}
		})
	}
	assertAuthorityInvariants(t, service)
}

func TestAuthorityClaimRefusesStaleRevisionAndEpochBeforePublication(t *testing.T) {
	applier := &recordingApplier{}
	service := mustService(t, applier, nil)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	staleRevision := claimRequest("work-a", "actor-a", "process-a", "stale-revision", now)
	staleRevision.WorkRevisionID = "revision-old"
	if _, err := service.Claim(t.Context(), staleRevision); !IsOutcome(err, OutcomeStaleWorkRevision) {
		t.Fatalf("stale revision=%v", err)
	}
	staleEpoch := claimRequest("work-a", "actor-a", "process-a", "stale-epoch", now)
	staleEpoch.RuntimeEpochID = "epoch-old"
	if _, err := service.Claim(t.Context(), staleEpoch); !IsOutcome(err, OutcomeStaleEpoch) {
		t.Fatalf("stale epoch=%v", err)
	}
	if applier.calls.Load() != 0 {
		t.Fatalf("refusal published %d batches", applier.calls.Load())
	}
}

func TestAuthorityExpireValidatesFenceAndProcessCoordinates(t *testing.T) {
	service := mustService(t, &recordingApplier{}, nil)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	authority, err := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "expire-coordinate", now))
	if err != nil {
		t.Fatal(err)
	}
	staleFence := authority
	staleFence.FenceToken++
	if err := service.Expire(t.Context(), "expire-stale-fence", staleFence, authority.Deadline); !IsOutcome(err, OutcomeStaleFence) {
		t.Fatalf("stale fence expired authority: %v", err)
	}
	wrongProcess := authority
	wrongProcess.ProcessID = "process-b"
	if err := service.Expire(t.Context(), "expire-wrong-process", wrongProcess, authority.Deadline); !IsOutcome(err, OutcomeActorProcessMismatch) {
		t.Fatalf("wrong process expired authority: %v", err)
	}
	if _, ok := service.Current("work-a"); !ok {
		t.Fatal("refusal removed current authority")
	}
}

func TestAuthorityHeartbeatRefreshesCapsuleAndStalesPriorHead(t *testing.T) {
	service := mustService(t, &recordingApplier{}, nil)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	first, err := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "heartbeat", now))
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := service.Heartbeat(t.Context(), "heartbeat-one", first, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AuthorityHeadRecordRevision == first.AuthorityHeadRecordRevision || !refreshed.Deadline.After(first.Deadline) {
		t.Fatalf("heartbeat did not refresh: first=%+v refreshed=%+v", first, refreshed)
	}
	if err := service.Authorize(first, "handoff", now.Add(2*time.Second)); !IsOutcome(err, OutcomeStaleLease) {
		t.Fatalf("prior capsule survived heartbeat: %v", err)
	}
}

func TestAuthorityConcurrentClaimFocusedCampaign(t *testing.T) {
	err := RunFocusedCampaign(t.Context(), 100, func(ctx context.Context, index int) error {
		service := mustService(t, &recordingApplier{}, nil)
		now := time.Date(2026, 8, 30, 12, 0, index, 0, time.UTC)
		var wait sync.WaitGroup
		errors := make(chan error, 2)
		for _, request := range []ClaimRequest{
			claimRequest("work-a", "actor-a", "process-a", fmt.Sprintf("campaign-a-%d", index), now),
			claimRequest("work-b", "actor-b", "process-b", fmt.Sprintf("campaign-b-%d", index), now),
		} {
			request := request
			wait.Add(1)
			go func() { defer wait.Done(); _, err := service.Claim(ctx, request); errors <- err }()
		}
		wait.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				return err
			}
		}
		assertAuthorityInvariants(t, service)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertAuthorityInvariants(t *testing.T, service *Service) {
	t.Helper()
	for workID, state := range service.works {
		state.mu.Lock()
		if state.current != nil {
			if state.current.WorkID != workID || state.current.Status != "active" || state.current.Generation == 0 || state.current.FenceToken == 0 || state.current.AuthorityHeadRecordRevision == 0 {
				state.mu.Unlock()
				t.Fatalf("invalid current authority for %s: %+v", workID, state.current)
			}
		}
		state.mu.Unlock()
	}
}
