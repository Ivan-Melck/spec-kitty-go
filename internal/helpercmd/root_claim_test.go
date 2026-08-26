package helpercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/execution"
)

func TestClaimSuppliesSyntheticGovernanceContextBeforeAdmission(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runClaim(context.Background(), []string{
		"--db", filepath.Join(t.TempDir(), "control.db"),
		"--operation", "operation-claim-context",
		"--project", "project-claim-context",
		"--mission", "mission-claim-context",
		"--revision", "revision-claim-context",
		"--work", "work-claim-context",
		"--actor", "actor-claim-context",
		"--process", "process-claim-context",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Error == nil {
		t.Fatalf("expected repository admission diagnostic, got %+v", response)
	}
	if response.Error.Kind != execution.KindNotFound || response.Error.Message != "work generation was not seeded" {
		t.Fatalf("claim did not reach repository admission: %+v", response.Error)
	}
}
