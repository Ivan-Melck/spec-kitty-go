package helpercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
)

func TestSyntheticStartGovernanceContextIsExplicitAndSemanticallyStable(t *testing.T) {
	first, err := syntheticStartGovernanceContext(time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	second, err := syntheticStartGovernanceContext(time.Date(2030, 1, 1, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID() != second.ID() || first.Aggregate() != second.Aggregate() || len(first.Entries()) != 1 || first.Entries()[0].Ref() != "phase1-helper-synthetic" {
		t.Fatalf("synthetic context is not explicit and stable: first=%+v second=%+v", first, second)
	}
}

func TestStartRunFlagFailuresAlwaysEmitCompleteDiagnosticJSON(t *testing.T) {
	for _, args := range [][]string{
		{"--operation", "operation-1", "--correlation-id", "correlation-1", "--actor", "actor-1", "--process", "process-1", "--at", "2026-08-24T12:00:00Z", "--unknown"},
		{"--operation", "operation-1", "--correlation-id", "correlation-1", "--actor", "actor-1", "--process", "process-1", "--at", "2026-08-24T12:00:00Z"},
	} {
		var stdout, stderr bytes.Buffer
		if code := runStartRun(context.Background(), args, &stdout, &stderr); code != 0 {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		var response Response
		if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		diagnostic := response.Error
		if response.OK || response.Message != "" || diagnostic == nil || diagnostic.SchemaVersion != execution.ResultSchemaVersion || diagnostic.Kind != execution.KindInvalidCommand || diagnostic.CorrelationID != "correlation-1" || diagnostic.OperationID != "operation-1" || diagnostic.AggregateIDs["actor_id"] != "actor-1" || diagnostic.AggregateIDs["process_id"] != "process-1" || diagnostic.ObservedState == nil || diagnostic.ExpectedState == nil || diagnostic.RetrySafety == "" || len(diagnostic.RecoveryActions) == 0 || diagnostic.OccurredAt.IsZero() {
			t.Fatalf("incomplete failure envelope: %+v", response)
		}
	}
}
