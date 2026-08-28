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

func TestArtifactPublicationHundredCallerTwoConnectionExactPriorRace(t *testing.T) {
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
	conflicts := 0
	for e := range failures {
		if !artifact.IsKind(e, artifact.ErrorIdempotencyConflict) {
			t.Errorf("publication race: %v", e)
		} else {
			conflicts++
		}
	}
	winners := 0
	for result := range results {
		winners++
		if result.Revision.RevisionNumber() != 1 {
			t.Fatalf("winner revision=%d want=1", result.Revision.RevisionNumber())
		}
	}
	if winners != 1 || conflicts != callers-1 {
		t.Fatalf("winners=%d conflicts=%d", winners, conflicts)
	}
	var revisions, operations, events int
	if err = left.db.QueryRow(`SELECT (SELECT COUNT(*) FROM phase3_artifact_revisions),(SELECT COUNT(*) FROM phase3_operations WHERE operation_kind='artifact_publish'),(SELECT COUNT(*) FROM phase3_events WHERE event_kind='artifact_published')`).Scan(&revisions, &operations, &events); err != nil || revisions != 1 || operations != 1 || events != 1 {
		t.Fatalf("partial race revisions=%d operations=%d events=%d err=%v", revisions, operations, events, err)
	}
}

func TestArtifactPublicationExactPriorReplayPrecedesCurrentCAS(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	policy, _ := artifact.PolicyFor(artifact.KindPlan)
	validation, _ := artifact.NewValidationResult(artifact.ValidationResultParams{ArtifactRevisionID: "artifact-replay", Verdict: artifact.ValidationPass, Policy: policy, ValidatedAt: time.Date(2026, 8, 28, 7, 0, 0, 0, time.UTC)})
	request := artifact.AtomicPublishRequest{OperationID: "operation-replay", RequestDigest: digest64("8"), ArtifactRevisionID: "artifact-replay", MissionID: "mission-replay", Kind: artifact.KindPlan, ContentDigest: digest64("9"), SourceRef: "refs/heads/main", SourceRevision: "revision-replay", CreatedBy: "actor-replay", CreatedAt: time.Date(2026, 8, 28, 7, 0, 1, 0, time.UTC), Validation: validation}
	first, err := store.PublishAtomic(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`UPDATE phase3_artifact_revisions SET revision_number=2 WHERE artifact_revision_id=?`, first.Revision.ID()); err == nil {
		t.Fatal("immutable revision mutation unexpectedly succeeded")
	}
	replay, err := store.PublishAtomic(ctx, request)
	if err != nil || !replay.Replayed || replay.Revision.ID() != first.Revision.ID() {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	stale := request
	stale.OperationID = "operation-stale"
	stale.RequestDigest = digest64("a")
	stale.ArtifactRevisionID = "artifact-stale"
	stale.Validation, _ = artifact.NewValidationResult(artifact.ValidationResultParams{ArtifactRevisionID: stale.ArtifactRevisionID, Verdict: artifact.ValidationPass, Policy: policy, ValidatedAt: request.CreatedAt})
	if _, err = store.PublishAtomic(ctx, stale); !artifact.IsKind(err, artifact.ErrorIdempotencyConflict) {
		t.Fatalf("stale exact-prior error=%v", err)
	}
	var revisions, operations int
	if err = store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM phase3_artifact_revisions),(SELECT COUNT(*) FROM phase3_operations WHERE operation_kind='artifact_publish')`).Scan(&revisions, &operations); err != nil || revisions != 1 || operations != 1 {
		t.Fatalf("stale write revisions=%d operations=%d err=%v", revisions, operations, err)
	}
}

func TestArtifactPublicationRejectsLegacyOperationBeforeAggregateWrites(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.db.Exec(`INSERT INTO operations(operation_id,operation_kind,idempotency_key,correlation_id,request_digest,status,created_at) VALUES('operation-artifact-legacy','claim','legacy-key','correlation','digest','completed','2026-08-28T07:30:00Z')`); err != nil {
		t.Fatal(err)
	}
	policy, _ := artifact.PolicyFor(artifact.KindPlan)
	validation, _ := artifact.NewValidationResult(artifact.ValidationResultParams{ArtifactRevisionID: "artifact-legacy-op", Verdict: artifact.ValidationPass, Policy: policy, ValidatedAt: time.Date(2026, 8, 28, 7, 30, 0, 0, time.UTC)})
	request := artifact.AtomicPublishRequest{OperationID: "operation-artifact-legacy", RequestDigest: digest64("b"), ArtifactRevisionID: "artifact-legacy-op", MissionID: "mission-legacy-op", Kind: artifact.KindPlan, ContentDigest: digest64("c"), SourceRef: "refs/heads/main", SourceRevision: "revision-legacy-op", CreatedBy: "actor-legacy-op", CreatedAt: time.Date(2026, 8, 28, 7, 30, 1, 0, time.UTC), Validation: validation}
	if _, err = store.PublishAtomic(ctx, request); !artifact.IsKind(err, artifact.ErrorIdempotencyConflict) {
		t.Fatalf("legacy operation conflict=%v", err)
	}
	var revisions, validations, phase3Operations, events int
	if err = store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM phase3_artifact_revisions),(SELECT COUNT(*) FROM phase3_artifact_validations),(SELECT COUNT(*) FROM phase3_operations),(SELECT COUNT(*) FROM phase3_events)`).Scan(&revisions, &validations, &phase3Operations, &events); err != nil || revisions+validations+phase3Operations+events != 0 {
		t.Fatalf("partial aggregate revisions=%d validations=%d operations=%d events=%d err=%v", revisions, validations, phase3Operations, events, err)
	}
}

func TestArtifactPublicationBoundaryFaultRollsBackAndReopensCleanly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := artifact.PolicyFor(artifact.KindIntent)
	if err != nil {
		t.Fatal(err)
	}
	validation, err := artifact.NewValidationResult(artifact.ValidationResultParams{ArtifactRevisionID: "artifact-fault", Verdict: artifact.ValidationPass, Policy: policy, ValidatedAt: time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`CREATE TRIGGER fault_artifact_validation BEFORE INSERT ON phase3_artifact_validations BEGIN SELECT RAISE(ABORT,'seeded boundary fault'); END`); err != nil {
		t.Fatal(err)
	}
	request := artifact.AtomicPublishRequest{OperationID: "operation-artifact-fault", RequestDigest: digest64("2"), ArtifactRevisionID: "artifact-fault", MissionID: "mission-fault", Kind: artifact.KindIntent, ContentDigest: digest64("3"), SourceRef: "refs/heads/main", SourceRevision: "revision-fault", CreatedBy: "actor-fault", CreatedAt: time.Date(2026, 8, 28, 8, 0, 1, 0, time.UTC), Validation: validation}
	if _, err = store.PublishAtomic(ctx, request); err == nil {
		t.Fatal("boundary fault was hidden")
	}
	var revisions, operations int
	if err = store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM phase3_artifact_revisions),(SELECT COUNT(*) FROM phase3_operations)`).Scan(&revisions, &operations); err != nil || revisions != 0 || operations != 0 {
		t.Fatalf("partial publication revisions=%d operations=%d err=%v", revisions, operations, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM phase3_artifact_revisions),(SELECT COUNT(*) FROM phase3_operations)`).Scan(&revisions, &operations); err != nil || revisions != 0 || operations != 0 {
		t.Fatalf("reopened partial publication revisions=%d operations=%d err=%v", revisions, operations, err)
	}
}
