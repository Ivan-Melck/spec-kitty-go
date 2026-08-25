package helpercmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

func TestCycle3RepositoryDiagnosticUsesStructuredHelperJSON(t *testing.T) {
	now := time.Date(2026, 8, 24, 15, 0, 0, 0, time.UTC)
	diagnostic := execution.NewDiagnostic(execution.KindControlStoreBusy, "control repository save failed", execution.RetrySafeSameOperation,
		"correlation-helper", ids.OperationID("operation-helper"),
		map[string]string{"actor_id": "actor-helper", "process_id": "process-helper", "expected_provider_revision": "2"},
		map[string]string{"control_repository": "save_failed"}, map[string]string{"control_repository": "durable and available"}, now)
	diagnostic.RecoveryActions = []string{"inspect the same operation", "retry after recovery"}
	diagnostic.Cause = errors.New("disk unavailable")
	var output bytes.Buffer
	if code := encodeTrackerFailure(&output, diagnostic); code != 0 {
		t.Fatalf("structured diagnostic exit code = %d", code)
	}
	var response TrackerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Error == nil || response.Message != "" || response.Error.SchemaVersion != execution.ResultSchemaVersion || response.Error.CorrelationID != "correlation-helper" || response.Error.OperationID != "operation-helper" || response.Error.AggregateIDs["actor_id"] != "actor-helper" || response.Error.AggregateIDs["process_id"] != "process-helper" || response.Error.OccurredAt != now || len(response.Error.RecoveryActions) != 2 {
		t.Fatalf("incomplete helper response: %+v", response)
	}
}
