package mission_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

func TestCreationAuthorityBindsProcessAndCompleteGovernance(t *testing.T) {
	definition := mustDefinition(t)
	registry, err := mission.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	store := &authorityCreateStore{memoryStore: newMemoryStore()}
	service := mission.NewService(registry, store)
	at := fixtureTime()
	request := createRequest(t, definition, "authority")
	request.ProcessID = "process-authority"
	request.Governance = serviceGovernance(t, "governance-authority", at)
	first, err := service.Create(context.Background(), request)
	if err != nil || !first.AuthorityRecorded || first.ActorID != request.ActorID || first.ProcessID != request.ProcessID || first.Governance.Aggregate() != request.Governance.Aggregate() {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	replay, err := service.Create(context.Background(), request)
	if err != nil || !replay.Replayed || replay.ProcessID != request.ProcessID || replay.Governance.Aggregate() != request.Governance.Aggregate() {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	changed := request
	changed.ProcessID = "process-authority-other"
	if _, err = service.Create(context.Background(), changed); !mission.IsKind(err, mission.KindIdempotencyConflict) {
		t.Fatalf("changed authority err=%v", err)
	}
}

func TestCreationAuthorityRejectsPartialProvenanceBeforeStore(t *testing.T) {
	definition := mustDefinition(t)
	registry, _ := mission.NewRegistry(definition)
	for _, mutate := range []func(*mission.CreateRequest){
		func(request *mission.CreateRequest) { request.ProcessID = "process-partial" },
		func(request *mission.CreateRequest) {
			request.Governance = serviceGovernance(t, "governance-partial", fixtureTime())
		},
	} {
		store := &authorityCreateStore{memoryStore: newMemoryStore()}
		request := createRequest(t, definition, "partial")
		mutate(&request)
		if _, err := mission.NewService(registry, store).Create(context.Background(), request); !mission.IsKind(err, mission.KindInvalidRequest) || store.calls != 0 {
			t.Fatalf("partial provenance err=%v calls=%d", err, store.calls)
		}
	}
}

type authorityCreateStore struct {
	*memoryStore
	calls int
}

func (store *authorityCreateStore) CreateMission(ctx context.Context, commit mission.CreateCommit) (mission.CreateResult, error) {
	store.calls++
	result, err := store.memoryStore.CreateMission(ctx, commit)
	if err != nil {
		return mission.CreateResult{}, err
	}
	result.ActorID = commit.ActorID
	result.ProcessID = commit.ProcessID
	result.Governance = commit.Governance
	result.AuthorityRecorded = true
	store.memoryStore.mu.Lock()
	stored := store.memoryStore.createOps[commit.OperationID]
	stored.result = result
	store.memoryStore.createOps[commit.OperationID] = stored
	store.memoryStore.mu.Unlock()
	return result, nil
}

func serviceGovernance(t *testing.T, id string, at time.Time) governancecontext.Context {
	t.Helper()
	commitment, err := governancecontext.NewCommitment(governancecontext.AlgorithmSHA256, strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := governancecontext.NewEntry(governancecontext.KindProfile, "implementer-ivan", "1", commitment)
	if err != nil {
		t.Fatal(err)
	}
	value, err := governancecontext.NewContext(ids.GovernanceContextID(id), governancecontext.SchemaVersion, []governancecontext.Entry{entry}, at)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
