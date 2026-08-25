package scenario

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/helpercmd"
)

func TestWorkspaceMaintenanceHelperReportsLiveRefusalsWithoutMutation(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"active", "operation", "dirty", "quarantined", "unregistered", "branch", "head", "path", "unknown"} {
		variant := variant
		t.Run(variant, func(t *testing.T) {
			t.Parallel()
			fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-maintenance", lane: "lane-maintenance", tracker: "TK-MAINTENANCE"}})
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			input := workspaceStart{operation: "operation-maintenance-" + variant, work: "work-maintenance", lane: "lane-maintenance", actor: "actor-maintenance", process: "process-maintenance-" + variant, workspace: "workspace-maintenance-" + variant, branch: "runs/maintenance-" + variant, trackerWork: "TK-MAINTENANCE"}
			started := waitWorkspaceProcess(t, fixture.start(ctx, t, input))
			if !started.OK || started.Capsule == nil {
				t.Fatalf("start=%+v", started)
			}
			path := started.Capsule.WorkspacePath
			workspaceID := input.workspace
			workspaceRoot := fixture.managedRoot
			subcommand := "workspace-inspect"
			switch variant {
			case "active":
				subcommand = "workspace-cleanup"
			case "operation":
				workspaceID = ""
			case "dirty":
				if err := os.WriteFile(filepath.Join(path, "dirty.txt"), []byte("uncommitted evidence\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "quarantined":
				expireAuthorityProcess(t, ctx, fixture.database, *started.Capsule, input, started.Capsule.Deadline)
			case "unregistered":
				scenarioGit(t, fixture.repository, "worktree", "remove", "--force", path)
			case "branch":
				scenarioGit(t, path, "checkout", "-b", "runs/unexpected-branch")
			case "head":
				if err := os.WriteFile(filepath.Join(path, "committed.txt"), []byte("unexpected commit\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				scenarioGit(t, path, "add", "--", "committed.txt")
				scenarioGit(t, path, "commit", "-m", "unexpected maintenance head")
			case "path":
				workspaceRoot = filepath.Join(t.TempDir(), "wrong-managed-root")
				if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
					t.Fatal(err)
				}
			case "unknown":
				workspaceID = "workspace-definitely-absent"
			}

			response := runWorkspaceMaintenanceHelper(t, ctx, fixture, subcommand, input, workspaceID, workspaceRoot)
			if response.OK || response.Error == nil {
				t.Fatalf("%s response=%+v", variant, response)
			}
			diagnosticWorkspaceID := workspaceID
			if diagnosticWorkspaceID == "" {
				diagnosticWorkspaceID = input.workspace
			}
			assertCompleteMaintenanceDiagnostic(t, response.Error, input, diagnosticWorkspaceID)
			if variant == "unknown" {
				if response.Error.Kind != execution.KindNotFound {
					t.Fatalf("unknown kind=%s", response.Error.Kind)
				}
				return
			}
			wantKind := execution.KindCleanupRefused
			if variant == "path" {
				wantKind = execution.KindRepositoryConflict
			}
			if response.Error.Kind != wantKind {
				t.Fatalf("%s kind=%s want=%s diagnostic=%+v", variant, response.Error.Kind, wantKind, response.Error)
			}
			if response.Inspection == nil {
				t.Fatalf("%s omitted inspection evidence: %+v", variant, response)
			}
			if variant == "operation" && response.WorkspaceID.String() != input.workspace {
				t.Fatalf("operation resolution=%+v", response)
			}
			if variant == "active" {
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("refused cleanup removed active evidence: %v", err)
				}
			}
			if variant == "quarantined" && (response.Error.ObservedState["workspace_status"] != "quarantined" || response.Error.ObservedState["quarantined"] != "true" || !strings.Contains(response.Error.Message, "quarantined evidence")) {
				t.Fatalf("quarantine diagnostic=%+v", response.Error)
			}
			if variant == "dirty" && (response.Error.ObservedState["clean"] != "false" || !strings.Contains(response.Error.Message, "uncommitted changes")) {
				t.Fatalf("dirty diagnostic=%+v", response.Error)
			}
			if variant == "unregistered" && (response.Error.ObservedState["registered"] != "false" || !strings.Contains(response.Error.Message, "unknown or unregistered")) {
				t.Fatalf("registration diagnostic=%+v", response.Error)
			}
			if variant == "active" && !strings.Contains(response.Error.Message, "active authority") {
				t.Fatalf("active diagnostic=%+v", response.Error)
			}
			if variant == "path" && response.Error.ObservedState["discrepancies"] == "" {
				t.Fatalf("path diagnostic=%+v", response.Error)
			}
			if (variant == "branch" || variant == "head") && response.Error.ObservedState[variant] == response.Error.ExpectedState[variant] {
				t.Fatalf("%s discrepancy not rendered: %+v", variant, response.Error)
			}
		})
	}
}

func runWorkspaceMaintenanceHelper(t *testing.T, ctx context.Context, fixture workspaceScenario, subcommand string, input workspaceStart, workspaceID, workspaceRoot string) helpercmd.WorkspaceMaintenanceResponse {
	t.Helper()
	at := "2026-08-24T12:03:00Z"
	command := exec.CommandContext(ctx, helperBinary, subcommand,
		"--db", fixture.database, "--repository", fixture.repository, "--workspace-root", workspaceRoot,
		"--operation", input.operation, "--workspace", workspaceID, "--actor", input.actor, "--process", input.process,
		"--correlation-id", "maintenance-"+input.operation, "--at", at)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("%s helper: %v stderr=%s stdout=%s", subcommand, err, stderr.String(), stdout.String())
	}
	var response helpercmd.WorkspaceMaintenanceResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode %s response %q: %v", subcommand, stdout.String(), err)
	}
	return response
}

func assertCompleteMaintenanceDiagnostic(t *testing.T, diagnostic *execution.DiagnosticError, input workspaceStart, workspaceID string) {
	t.Helper()
	wantTime := time.Date(2026, 8, 24, 12, 3, 0, 0, time.UTC)
	if diagnostic.SchemaVersion != execution.ResultSchemaVersion || diagnostic.CorrelationID != "maintenance-"+input.operation || diagnostic.OperationID.String() != input.operation ||
		diagnostic.AggregateIDs["actor_id"] != input.actor || diagnostic.AggregateIDs["process_id"] != input.process || diagnostic.AggregateIDs["workspace_id"] != workspaceID ||
		diagnostic.OccurredAt != wantTime || diagnostic.RetrySafety != execution.RetryDecisionRequired || len(diagnostic.ObservedState) == 0 || len(diagnostic.ExpectedState) == 0 || len(diagnostic.RecoveryActions) == 0 {
		t.Fatalf("incomplete diagnostic=%+v", diagnostic)
	}
}
