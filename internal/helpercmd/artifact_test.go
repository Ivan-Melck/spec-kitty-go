package helpercmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
)

func TestArtifactPublishBindsExactPriorIdentityAndReplays(t *testing.T) {
	database := filepath.Join(t.TempDir(), "control.db")
	at := time.Date(2026, 8, 28, 16, 0, 0, 0, time.UTC)
	first := artifactPublishRequest{
		Database: database, CorrelationID: "correlation-artifact-1", OperationID: "operation-artifact-1",
		ArtifactRevisionID: "artifact-plan-1", MissionID: "mission-artifact", Kind: artifact.KindPlan,
		SourceRef: "specs/plan.md", SourceRevision: "commit-plan-1", CreatedBy: "actor-artifact", CreatedAt: at,
		Content: []byte("plan revision one"), PresentConcerns: requiredConcerns(t, artifact.KindPlan), ValidatedAt: at,
	}
	created := invokeMethod(t, RunArtifact, []string{"publish", "--request-json", marshalMethod(t, first)})
	replayed := invokeMethod(t, RunArtifact, []string{"publish", "--request-json", marshalMethod(t, first)})
	if !created.OK || !replayed.OK || resultObject(t, replayed)["replayed"] != true {
		t.Fatalf("created=%+v replayed=%+v", created, replayed)
	}

	second := first
	second.CorrelationID, second.OperationID, second.ArtifactRevisionID = "correlation-artifact-2", "operation-artifact-2", "artifact-plan-2"
	second.SourceRevision, second.Content, second.ExpectedLatestRevisionID = "commit-plan-2", []byte("plan revision two"), "artifact-plan-1"
	advanced := invokeMethod(t, RunArtifact, []string{"publish", "--request-json", marshalMethod(t, second)})
	advancedResult := resultObject(t, advanced)
	revision, ok := advancedResult["revision"].(map[string]any)
	if !advanced.OK || !ok || revision["artifact_revision_id"] != "artifact-plan-2" || revision["revision_number"] != float64(2) {
		t.Fatalf("advanced=%+v result=%v", advanced, advancedResult)
	}

	stale := second
	stale.OperationID, stale.ArtifactRevisionID = "operation-artifact-stale", "artifact-plan-stale"
	stale.ExpectedLatestRevisionID = "artifact-plan-1"
	refused := invokeMethod(t, RunArtifact, []string{"publish", "--request-json", marshalMethod(t, stale)})
	if refused.OK || refused.Error == nil || refused.Error.Kind != "idempotency_conflict" {
		t.Fatalf("stale=%+v", refused)
	}
}

func TestArtifactStrictJSONRejectsNullBeforeStoreOpen(t *testing.T) {
	response := invokeMethodUsage(t, RunArtifact, []string{"publish", "--request-json", `{"database":"unused","correlation_id":"c","operation_id":"operation-artifact","artifact_revision_id":"artifact-1","mission_id":"mission-1","kind":"intent","source_ref":"intent.md","source_revision":"commit-1","created_by":"actor-1","created_at":"2026-08-28T00:00:00Z","content":null,"present_concerns":[],"validated_at":"2026-08-28T00:00:00Z","expected_latest_revision_id":""}`})
	if response.OK || response.Kind != "invalid_invocation" {
		t.Fatalf("response=%+v", response)
	}
}

func TestArtifactApproveValidatesBeforeOpeningStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "control.db")
	response := invokeMethod(t, func(ctx context.Context, _ []string, stdout, _ io.Writer) int {
		return runArtifactApprove(ctx, artifactApproveRequest{Database: path, CorrelationID: "correlation-approve-preopen", OperationID: "operation-approve-preopen", ApprovalID: "approval-preopen", ArtifactRevisionID: "artifact-preopen", Verdict: artifact.VerdictApproved, ActorID: "actor-preopen", EvidenceDigest: strings.Repeat("a", 64), GovernanceContextID: "governance-preopen", SubmittedAt: time.Time{}}, stdout)
	}, nil)
	if response.OK || response.Error == nil {
		t.Fatalf("response=%+v", response)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("store opened for malformed approval: %v", err)
	}
}

func requiredConcerns(t *testing.T, kind artifact.Kind) []artifact.Concern {
	t.Helper()
	policy, err := artifact.PolicyFor(kind)
	if err != nil {
		t.Fatal(err)
	}
	return policy.RequiredConcerns()
}
