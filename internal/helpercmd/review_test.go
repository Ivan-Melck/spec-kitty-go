package helpercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestReviewFlagFailureEmitsTypedEnvelope(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runReview(context.Background(), []string{"--operation", "operation-review", "--correlation-id", "correlation-review", "--actor", "actor-reviewer", "--process", "process-reviewer", "--at", "2026-08-24T12:00:00Z", "--criteria-json", "not-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Error == nil || response.Error.Kind != execution.KindInvalidCommand || response.Error.OperationID != "operation-review" || response.Error.AggregateIDs["actor_id"] != "actor-reviewer" {
		t.Fatalf("response=%+v", response)
	}
}

func TestEligibilityModeIsExplicitAndLegacyWrappersCannotExposeCompleteFacts(t *testing.T) {
	for _, value := range []string{"complete", "legacy-v1"} {
		if mode, err := parseEligibilityMode(value); err != nil || string(mode) != value {
			t.Fatalf("mode=%q parsed=%q err=%v", value, mode, err)
		}
	}
	for _, value := range []string{"", "legacy", "v1", "auto", "COMPLETE"} {
		if _, err := parseEligibilityMode(value); err == nil {
			t.Fatalf("unsupported mode %q was accepted", value)
		}
	}
	if _, ok := any(legacyV1HandoffStore{}).(handoff.EligibilityFactStore); ok {
		t.Fatal("legacy handoff wrapper exposed complete eligibility")
	}
	if _, ok := any(legacyV1ReviewStore{}).(review.EligibilityFactStore); ok {
		t.Fatal("legacy review wrapper exposed complete eligibility")
	}
}

func TestLegacyV1ModeProvesNamespaceWithoutDowngradingV2(t *testing.T) {
	revision := legacyRevisionFixture(t, "revision-legacy-mode")
	notV2 := &mission.FinalizationError{Kind: mission.FinalizationCurrentConflict, Field: "work_revision_id", Message: "not found"}
	tests := []struct {
		name  string
		store revisionNamespaceStore
		ok    bool
	}{
		{name: "exact legacy only", store: revisionNamespaceFixture{v2Err: notV2, legacy: revision}, ok: true},
		{name: "v2 exists", store: revisionNamespaceFixture{legacy: revision}},
		{name: "v2 failure is not typed absence", store: revisionNamespaceFixture{v2Err: errors.New("storage unavailable"), legacy: revision}},
		{name: "wrong v2 conflict field", store: revisionNamespaceFixture{v2Err: &mission.FinalizationError{Kind: mission.FinalizationCurrentConflict, Field: "generation"}, legacy: revision}},
		{name: "legacy absent", store: revisionNamespaceFixture{v2Err: notV2, legacyErr: errors.New("missing")}},
		{name: "legacy substituted", store: revisionNamespaceFixture{v2Err: notV2, legacy: legacyRevisionFixture(t, "revision-other")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := requireLegacyV1Revision(context.Background(), test.store, revision.ID())
			if (err == nil) != test.ok {
				t.Fatalf("ok=%t err=%v", test.ok, err)
			}
		})
	}
}

func TestInvalidEligibilityModeRefusesBeforeOpeningControlStore(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{
		"--db", "/definitely/not/a/control.db", "--eligibility-mode", "auto", "--schema-version", "1", "--correlation-id", "correlation-mode",
		"--at", "2026-08-24T12:00:30Z", "--operation", "operation-mode", "--handoff-id", "handoff-mode", "--project", "project-mode", "--mission", "mission-mode",
		"--revision", "revision-mode", "--work", "work-mode", "--run", "run-mode", "--workspace", "workspace-mode", "--lease", "lease-mode", "--generation", "1",
		"--token", "token-mode", "--actor", "actor-mode", "--process", "process-mode", "--candidate", strings.Repeat("a", 40), "--contributors", "actor-mode", "--evidence-digest", strings.Repeat("b", 64),
	}
	if code := runHandoff(context.Background(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil || response.OK || response.Error == nil || response.Error.Kind != execution.KindInvalidCommand || !strings.Contains(response.Error.Message, "eligibility mode") {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

type revisionNamespaceFixture struct {
	v2        workgraph.RevisionV2
	v2Err     error
	legacy    workgraph.Revision
	legacyErr error
}

func (f revisionNamespaceFixture) WorkRevisionV2(context.Context, ids.WorkRevisionID) (workgraph.RevisionV2, error) {
	return f.v2, f.v2Err
}
func (f revisionNamespaceFixture) WorkRevision(context.Context, ids.WorkRevisionID) (workgraph.Revision, error) {
	return f.legacy, f.legacyErr
}

func legacyRevisionFixture(t *testing.T, revisionID ids.WorkRevisionID) workgraph.Revision {
	t.Helper()
	draft := workgraph.NewDraft("project-mode", "mission-mode")
	if err := draft.AddLane(workgraph.Lane{ID: "lane-mode"}); err != nil {
		t.Fatal(err)
	}
	if err := draft.AddWork(workgraph.WorkItem{ID: "work-mode", LaneID: "lane-mode", Criteria: []workgraph.Criterion{{ID: "criterion-mode", Text: "verified"}}}); err != nil {
		t.Fatal(err)
	}
	revision, err := draft.Finalize(workgraph.FinalizeMetadata{ID: revisionID, Number: 1, ActorID: "actor-mode", FinalizedAt: time.Unix(1, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
