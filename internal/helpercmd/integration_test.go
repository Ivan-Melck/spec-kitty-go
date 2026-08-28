package helpercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/integration"
)

func TestLegacyV1IntegrationWrapperCannotExposeCompleteFacts(t *testing.T) {
	if _, ok := any(legacyV1IntegrationStore{}).(integration.EligibilityFactStore); ok {
		t.Fatal("legacy integration wrapper exposed complete eligibility")
	}
}

func TestIntegrateInvalidEligibilityModeRefusesBeforeRepositoryOrStoreAccess(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{
		"--db", "/definitely/not/a/control.db", "--repository", "/definitely/not/a/repository", "--workspace-root", "/definitely/not/workspaces",
		"--eligibility-mode", "auto", "--schema-version", "1", "--correlation-id", "correlation-integrate-mode", "--at", "2026-08-24T12:00:30Z",
		"--operation", "operation-integrate-mode", "--review-id", "review-integrate-mode", "--project", "project-mode", "--repository-id", "repository-mode",
		"--target-ref", "refs/heads/main", "--actor", "actor-mode", "--process", "process-mode", "--candidate", strings.Repeat("a", 40), "--ttl", "2m",
	}
	if code := runIntegrate(context.Background(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil || response.OK || response.Error == nil || response.Error.Kind != execution.KindInvalidCommand || !strings.Contains(response.Error.Message, "eligibility mode") {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}
