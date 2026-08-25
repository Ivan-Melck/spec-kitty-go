package helpercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/execution"
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
