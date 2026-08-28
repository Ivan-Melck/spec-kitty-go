package sqlite

import (
	"context"
	"path/filepath"
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
