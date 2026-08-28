package sqlite

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
)

func TestArtifactPublicationAtomicReplayAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := artifact.PolicyFor(artifact.KindSpecification)
	if err != nil {
		t.Fatal(err)
	}
	validation, err := artifact.NewValidationResult(artifact.ValidationResultParams{ArtifactRevisionID: "artifact-01", Verdict: artifact.ValidationPass, Policy: policy, ValidatedAt: time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	request := artifact.AtomicPublishRequest{OperationID: "operation-publish-01", RequestDigest: digest64("a"), ArtifactRevisionID: "artifact-01", MissionID: "mission-01", Kind: artifact.KindSpecification, ContentDigest: digest64("b"), SourceRef: "refs/heads/main", SourceRevision: "revision-01", CreatedBy: "actor-01", CreatedAt: time.Date(2026, 8, 28, 1, 0, 1, 0, time.UTC), Validation: validation}
	first, err := store.PublishAtomic(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.PublishAtomic(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision.RevisionNumber() != 1 || !again.Replayed {
		t.Fatalf("results %#v %#v", first, again)
	}
	changed := request
	changed.RequestDigest = digest64("c")
	if _, err = store.PublishAtomic(ctx, changed); !artifact.IsKind(err, artifact.ErrorIdempotencyConflict) {
		t.Fatalf("changed replay=%v", err)
	}
	_ = store.Close()
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	restored, err := store.Revision(ctx, "artifact-01")
	if err != nil || restored.ContentDigest() != request.ContentDigest {
		t.Fatalf("restored=%#v err=%v", restored, err)
	}
}
func digest64(seed string) string {
	out := ""
	for len(out) < 64 {
		out += seed
	}
	return out[:64]
}

func TestArtifactPublicationHundredCallerTwoConnectionRace(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	left, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	right, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()
	policy, err := artifact.PolicyFor(artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	const callers = 100
	var wg sync.WaitGroup
	results := make(chan artifact.PublishResult, callers)
	failures := make(chan error, callers)
	for i := 0; i < callers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("artifact-race-%03d", i)
			validation, e := artifact.NewValidationResult(artifact.ValidationResultParams{ArtifactRevisionID: id, Verdict: artifact.ValidationPass, Policy: policy, ValidatedAt: time.Date(2026, 8, 28, 6, 0, i, 0, time.UTC)})
			if e != nil {
				failures <- e
				return
			}
			sum := sha256.Sum256([]byte(id))
			request := artifact.AtomicPublishRequest{OperationID: fmt.Sprintf("operation-race-%03d", i), RequestDigest: fmt.Sprintf("%x", sum), ArtifactRevisionID: id, MissionID: "mission-race", Kind: artifact.KindPlan, ContentDigest: fmt.Sprintf("%x", sum), SourceRef: "refs/heads/main", SourceRevision: fmt.Sprintf("revision-%03d", i), CreatedBy: "actor-race", CreatedAt: time.Date(2026, 8, 28, 6, 1, i, 0, time.UTC), Validation: validation}
			store := left
			if i%2 == 1 {
				store = right
			}
			result, e := store.PublishAtomic(ctx, request)
			if e != nil {
				failures <- e
				return
			}
			results <- result
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		t.Errorf("publication race: %v", e)
	}
	seen := make(map[uint64]bool, callers)
	for result := range results {
		seen[result.Revision.RevisionNumber()] = true
	}
	if len(seen) != callers {
		t.Fatalf("unique revisions=%d want=%d", len(seen), callers)
	}
	for i := 1; i <= callers; i++ {
		if !seen[uint64(i)] {
			t.Fatalf("missing revision number %d", i)
		}
	}
}
