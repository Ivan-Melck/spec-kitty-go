package gitrepo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

func TestInspectionDiagnosesWithoutMutationAndCleanupRefusesDangerousWorktrees(t *testing.T) {
	repository, base := newRepository(t)
	adapter, err := Bind(context.Background(), Runner{}, "repository-1", "project-1", repository, filepath.Join(t.TempDir(), "workspaces"), "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.PrepareManagedRoot(); err != nil {
		t.Fatal(err)
	}
	record := workspaceRecord(t, adapter, "workspace-inspect", "runs/inspect", base)
	if err := adapter.Create(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	probe, err := adapter.Probe(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	record.DependencyHead, record.ObservedHead, record.GitDir, record.IndexPath = base, base, probe.GitDir, probe.IndexPath
	record.Stage, record.Status, record.Registered = workspace.StageReady, "ready", true

	before := runGit(t, record.CanonicalPath, "rev-parse", "HEAD")
	inspection, err := adapter.Inspect(context.Background(), record)
	if err != nil || !inspection.Known || !inspection.Registered || !inspection.Clean || len(inspection.Discrepancies) != 0 || len(inspection.PreservedCommits) != 1 || inspection.PreservedCommits[0] != base {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
	}
	if after := runGit(t, record.CanonicalPath, "rev-parse", "HEAD"); after != before {
		t.Fatalf("inspection mutated HEAD: %s -> %s", before, after)
	}
	if err := adapter.Cleanup(context.Background(), record, "active"); !workspace.IsKind(err, workspace.KindCleanupRefused) {
		t.Fatalf("active cleanup error=%v", err)
	}
	if err := os.WriteFile(filepath.Join(record.CanonicalPath, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inspection, err = adapter.Inspect(context.Background(), record)
	if err != nil || inspection.Clean || len(inspection.Discrepancies) == 0 {
		t.Fatalf("dirty inspection=%+v err=%v", inspection, err)
	}
	if err := adapter.Cleanup(context.Background(), record, "expired"); !workspace.IsKind(err, workspace.KindCleanupRefused) {
		t.Fatalf("dirty cleanup error=%v", err)
	}
	if _, err := os.Stat(record.CanonicalPath); err != nil {
		t.Fatalf("refused cleanup removed evidence: %v", err)
	}
	if err := os.Remove(filepath.Join(record.CanonicalPath, "dirty.txt")); err != nil {
		t.Fatal(err)
	}
	record.Status, record.QuarantineReason, record.QuarantinedAt = "quarantined", "expired", time.Now()
	if err := adapter.Cleanup(context.Background(), record, "expired"); !workspace.IsKind(err, workspace.KindCleanupRefused) || !cleanupMessageContains(err, "quarantined evidence") {
		t.Fatalf("quarantined cleanup error=%v", err)
	}
}

func cleanupMessageContains(err error, text string) bool {
	var typed *workspace.Error
	return errors.As(err, &typed) && strings.Contains(typed.Message, text)
}

func TestCleanupRequiresKnownRegistrationAndPreservesOwnedCommit(t *testing.T) {
	repository, base := newRepository(t)
	adapter, err := Bind(context.Background(), Runner{}, "repository-1", "project-1", repository, filepath.Join(t.TempDir(), "workspaces"), "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.PrepareManagedRoot(); err != nil {
		t.Fatal(err)
	}
	unknown := workspaceRecord(t, adapter, "workspace-unknown", "runs/unknown", base)
	if err := adapter.Cleanup(context.Background(), unknown, "expired"); !workspace.IsKind(err, workspace.KindCleanupRefused) {
		t.Fatalf("unknown cleanup error=%v", err)
	}
	record := workspaceRecord(t, adapter, "workspace-clean", "runs/clean", base)
	if err := adapter.Create(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	probe, err := adapter.Probe(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	record.DependencyHead, record.ObservedHead, record.GitDir, record.IndexPath = base, base, probe.GitDir, probe.IndexPath
	record.Stage, record.Status, record.Registered = workspace.StageReady, "ready", true
	if err := adapter.Cleanup(context.Background(), record, "expired"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(record.CanonicalPath); !os.IsNotExist(err) {
		t.Fatalf("eligible cleanup retained worktree: %v", err)
	}
	if got := runGit(t, repository, "rev-parse", record.OwnedBranch); got != base {
		t.Fatalf("cleanup lost exact branch commit: %s != %s", got, base)
	}
}

func TestValidateCleanupAggregatesEveryRefusalReason(t *testing.T) {
	inspection := Inspection{
		Known: false, Registered: false, Clean: false, RecordedBranch: "runs/expected", ObservedBranch: "runs/other",
		RecordedHead: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ObservedHead: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Discrepancies: []Discrepancy{{Fact: "branch", Observed: "runs/other", Expected: "runs/expected"}, {Fact: "head", Observed: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Expected: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
	}
	record := workspace.Record{Reservation: workspace.Reservation{WorkspaceID: "workspace-refusal"}, Status: "quarantined", Stage: workspace.StageReady, QuarantineReason: "expired", QuarantinedAt: time.Now()}
	err := ValidateCleanup(record, "active", inspection)
	var typed *workspace.Error
	if !errors.As(err, &typed) {
		t.Fatalf("error=%v", err)
	}
	for _, reason := range []string{"active authority", "quarantined evidence", "never reached verified readiness", "unknown or unregistered", "uncommitted changes", "Git facts differ"} {
		if !strings.Contains(typed.Message, reason) {
			t.Fatalf("message %q omitted %q", typed.Message, reason)
		}
	}
}
