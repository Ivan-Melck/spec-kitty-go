package scenario

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/helpercmd"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestIntegrationIG00ThroughIG09RecoversFromRealGitFacts(t *testing.T) {
	fixture, capsule, candidate := startReviewCandidate(t, "integration-faults")
	handoffResponse := runHandoffHelper(t, fixture, capsule, candidate, "handoff-integration-faults", "operation-handoff-integration-faults", "")
	if !handoffResponse.OK {
		t.Fatalf("handoff=%+v", handoffResponse)
	}
	reviewResponse := runReviewHelper(t, fixture, capsule, candidate, "handoff-integration-faults", "review-integration-faults", "operation-review-integration-faults", "actor-integration-reviewer", "process-integration-reviewer", review.Approved, "")
	if !reviewResponse.OK {
		t.Fatalf("review=%+v", reviewResponse)
	}
	scenarioGit(t, fixture.repository, "checkout", "--detach", "main")
	primaryBefore, err := fixture.primary.SnapshotPrimary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := sqlite.Open(context.Background(), fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkpoint.Close(); err != nil {
		t.Fatal(err)
	}
	templateDatabase := filepath.Join(t.TempDir(), "template.db")
	copyFile(t, fixture.database, templateDatabase)
	copyFile(t, fixture.database+".token-key", templateDatabase+".token-key")

	for _, boundary := range []string{"IG-00", "IG-01", "IG-02", "IG-03", "IG-04", "IG-05", "IG-06", "IG-07", "IG-08", "IG-09"} {
		boundary := boundary
		t.Run(boundary, func(t *testing.T) {
			resetIntegrationFenceForIsolatedScenario(t, fixture)
			scenarioGit(t, fixture.repository, "update-ref", "refs/heads/main", fixture.base)
			database := filepath.Join(t.TempDir(), "control.db")
			copyFile(t, templateDatabase, database)
			copyFile(t, templateDatabase+".token-key", database+".token-key")
			if err := os.WriteFile(filepath.Join(fixture.barrier, boundary+".action"), []byte("crash\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			operation := "operation-integrate-" + boundary
			args := integrationArgs(fixture, database, operation, "review-integration-faults", candidate, "actor-integrator", "process-integrator-"+boundary, time.Now().UTC())
			code, response := runIntegrationInProcess(t, args)
			if code != 86 || response.OK || response.Integration != nil || response.Error != nil {
				t.Fatalf("%s crash code=%d response=%+v", boundary, code, response)
			}
			assertIntegrationBoundaryState(t, database, fixture.repository, operation, boundary, fixture.base)
			if err := os.WriteFile(filepath.Join(fixture.barrier, boundary+".action"), []byte("continue\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			code, response = runIntegrationInProcess(t, args)
			if code != 0 || !response.OK || response.Integration == nil || response.Integration.Record.Status != integration.StatusDone || response.Integration.Record.ResultCommit == "" || response.Integration.Record.ObservedTarget == "" {
				t.Fatalf("%s recovery code=%d response=%+v", boundary, code, response)
			}
			assertIntegrationEnvelope(t, response.Integration, operation)
			original, _ := json.Marshal(response.Integration.Record)
			replayArgs := append([]string(nil), args...)
			setArgument(replayArgs, "--eligibility-mode", "complete")
			code, replay := runIntegrationInProcess(t, replayArgs)
			replayed, _ := json.Marshal(replay.Integration.Record)
			if code != 0 || !replay.OK || string(original) != string(replayed) {
				t.Fatalf("%s replay code=%d original=%s replay=%s response=%+v", boundary, code, original, replayed, replay)
			}
			assertOneIntegrationDone(t, database, "review-integration-faults")
		})
	}
	primaryAfter, err := fixture.primary.SnapshotPrimary(context.Background())
	if err != nil || primaryAfter != primaryBefore {
		t.Fatalf("integration crash matrix changed primary: before=%+v after=%+v err=%v", primaryBefore, primaryAfter, err)
	}
}

func resetIntegrationFenceForIsolatedScenario(t *testing.T, fixture workspaceScenario) {
	t.Helper()
	fence, err := fixture.primary.IntegrationFence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fence.Object != "" {
		scenarioGit(t, fixture.repository, "update-ref", "-d", fence.Ref, fence.Object)
	}
	for _, historyRef := range strings.Fields(scenarioGit(t, fixture.repository, "for-each-ref", "--format=%(refname)", "refs/spec-kitty/fence-history/")) {
		scenarioGit(t, fixture.repository, "update-ref", "-d", historyRef)
	}
}

func TestExpiredPublisherCannotCrossReplacementFenceAfterIG05Pause(t *testing.T) {
	fixture, capsule, candidate := startReviewCandidate(t, "integration-stale-publisher")
	handoff := runHandoffHelper(t, fixture, capsule, candidate, "handoff-integration-stale", "operation-handoff-integration-stale", "")
	if !handoff.OK {
		t.Fatalf("handoff=%+v", handoff)
	}
	judgment := runReviewHelper(t, fixture, capsule, candidate, "handoff-integration-stale", "review-integration-stale", "operation-review-integration-stale", "actor-integration-reviewer", "process-integration-reviewer", review.Approved, "")
	if !judgment.OK {
		t.Fatalf("review=%+v", judgment)
	}
	scenarioGit(t, fixture.repository, "checkout", "--detach", "main")
	primaryBefore, err := fixture.primary.SnapshotPrimary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.barrier, "IG-05.action"), []byte("block\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldArgs := integrationArgs(fixture, fixture.database, "operation-integrate-stale", "review-integration-stale", candidate, "actor-stale", "process-stale", time.Now().UTC())
	setArgument(oldArgs, "--ttl", "150ms")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	oldProcess := startIntegrationProcess(t, ctx, oldArgs)
	if err := waitForBoundaryReached(ctx, fixture.barrier, "IG-05", 1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(fixture.barrier, "IG-05.action"), []byte("continue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	replacementArgs := integrationArgs(fixture, fixture.database, "operation-integrate-replacement", "review-integration-stale", candidate, "actor-replacement", "process-replacement", time.Now().UTC())
	replacementProcess := startIntegrationProcess(t, ctx, replacementArgs)
	waitForIntegrationGeneration(t, ctx, fixture.database, 2, "active")
	if err := os.WriteFile(filepath.Join(fixture.barrier, "IG-05.release"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale, err := oldProcess.wait()
	if err != nil || stale.OK || stale.Error == nil || stale.Error.Kind != execution.ErrorKind(integration.KindAuthorityExpired) {
		t.Fatalf("stale publisher response=%+v err=%v", stale, err)
	}
	replacement, err := replacementProcess.wait()
	if err != nil || !replacement.OK || replacement.Integration == nil || replacement.Integration.Record.Status != integration.StatusDone {
		t.Fatalf("replacement response=%+v err=%v", replacement, err)
	}
	targetAfterReplacement := scenarioGit(t, fixture.repository, "rev-parse", "refs/heads/main")
	if targetAfterReplacement != replacement.Integration.Record.ResultCommit {
		t.Fatalf("replacement target=%s result=%s", targetAfterReplacement, replacement.Integration.Record.ResultCommit)
	}
	raw, err := sql.Open("sqlite", fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var staleResult, staleStatus, doneOperation string
	if err := raw.QueryRow(`SELECT result_commit,status FROM integrations WHERE operation_id='operation-integrate-stale'`).Scan(&staleResult, &staleStatus); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT operation_id FROM integration_done WHERE review_id='review-integration-stale'`).Scan(&doneOperation); err != nil {
		t.Fatal(err)
	}
	if staleResult != "" || staleStatus == string(integration.StatusDone) || doneOperation != "operation-integrate-replacement" {
		t.Fatalf("stale result=%q status=%s done operation=%s", staleResult, staleStatus, doneOperation)
	}
	primaryAfter, err := fixture.primary.SnapshotPrimary(context.Background())
	if err != nil || primaryAfter != primaryBefore {
		t.Fatalf("stale fencing changed primary: before=%+v after=%+v err=%v", primaryBefore, primaryAfter, err)
	}
}

func TestRepositoryFenceMovesBeforeReplacementSQLiteAuthorityAndStopsValidatedPublisher(t *testing.T) {
	fixture, candidate := approvedIntegrationScenario(t, "repository-first-stale-publisher")
	scenarioGit(t, fixture.repository, "checkout", "--detach", "main")
	wrapperRoot := integrationGitWrapper(t)
	oldBlock, replacementBlock := t.TempDir(), t.TempDir()
	oldArgs := integrationArgs(fixture, fixture.database, "operation-repository-first-old", "review-repository-first-stale-publisher", candidate, "actor-old-publisher", "process-old-publisher", time.Now().UTC())
	// Leave enough time for a loaded test host to start the helper and reach the
	// deliberately paused publication point; expiry is induced only afterward.
	setArgument(oldArgs, "--ttl", "1s")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	old := startIntegrationProcessEnv(t, ctx, oldArgs, []string{"PATH=" + wrapperRoot + string(os.PathListSeparator) + os.Getenv("PATH"), "SPK_GIT_BLOCK_MODE=target-before", "SPK_GIT_BLOCK_DIR=" + oldBlock})
	waitForFile(t, ctx, filepath.Join(oldBlock, "reached"))
	time.Sleep(1200 * time.Millisecond)
	replacementArgs := integrationArgs(fixture, fixture.database, "operation-repository-first-replacement", "review-repository-first-stale-publisher", candidate, "actor-replacement-publisher", "process-replacement-publisher", time.Now().UTC())
	replacement := startIntegrationProcessEnv(t, ctx, replacementArgs, []string{"PATH=" + wrapperRoot + string(os.PathListSeparator) + os.Getenv("PATH"), "SPK_GIT_BLOCK_MODE=fence-after", "SPK_GIT_BLOCK_DIR=" + replacementBlock})
	waitForFile(t, ctx, filepath.Join(replacementBlock, "reached"))
	fence, err := fixture.primary.IntegrationFence(context.Background())
	if err != nil || fence.Generation != 2 || fence.OperationID != "operation-repository-first-replacement" {
		t.Fatalf("replacement repository fence=%+v err=%v", fence, err)
	}
	raw, err := sql.Open("sqlite", fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var replacementRows int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM integration_mutation_leases WHERE operation_id='operation-repository-first-replacement'`).Scan(&replacementRows); err != nil || replacementRows != 0 {
		t.Fatalf("replacement became visible before fence installer returned: rows=%d err=%v", replacementRows, err)
	}
	if err := os.WriteFile(filepath.Join(oldBlock, "release"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale, err := old.wait()
	if err != nil || stale.OK || stale.Error == nil || stale.Error.Kind != execution.ErrorKind(integration.KindAuthorityExpired) {
		t.Fatalf("stale publisher response=%+v err=%v", stale, err)
	}
	if got := scenarioGit(t, fixture.repository, "rev-parse", "refs/heads/main"); got != fixture.base {
		t.Fatalf("stale publisher moved target=%s want=%s", got, fixture.base)
	}
	var staleResult, staleStatus, staleCleanup string
	if err := raw.QueryRow(`SELECT result_commit,status,cleanup_status FROM integrations WHERE operation_id='operation-repository-first-old'`).Scan(&staleResult, &staleStatus, &staleCleanup); err != nil {
		t.Fatal(err)
	}
	if staleResult != "" || staleStatus == string(integration.StatusDone) || staleCleanup != string(integration.CleanupPending) {
		t.Fatalf("stale result=%q status=%s cleanup=%s", staleResult, staleStatus, staleCleanup)
	}
	if err := os.WriteFile(filepath.Join(replacementBlock, "release"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	winner, err := replacement.wait()
	if err != nil || !winner.OK || winner.Integration == nil || winner.Integration.Record.Status != integration.StatusDone || winner.Integration.Record.MutationGeneration != 2 {
		t.Fatalf("replacement response=%+v err=%v", winner, err)
	}
}

func TestIntegrationSameOperationRecoversAfterProcessDeathBetweenGitFenceAndSQLite(t *testing.T) {
	fixture, candidate := approvedIntegrationScenario(t, "process-death-git-before-db")
	scenarioGit(t, fixture.repository, "checkout", "--detach", "main")
	wrapperRoot := integrationGitWrapper(t)
	args := integrationArgs(fixture, fixture.database, "operation-process-death-git-before-db", "review-process-death-git-before-db", candidate, "actor-process-death", "process-process-death", time.Now().UTC())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	process := startIntegrationProcessEnv(t, ctx, args, []string{"PATH=" + wrapperRoot + string(os.PathListSeparator) + os.Getenv("PATH"), "SPK_GIT_BLOCK_MODE=fence-kill-parent"})
	if err := process.command.Wait(); err == nil {
		t.Fatalf("helper survived injected process death stdout=%s stderr=%s", process.stdout.String(), process.stderr.String())
	}
	fenceBeforeReplay, err := fixture.primary.IntegrationFence(context.Background())
	if err != nil || fenceBeforeReplay.Generation != 1 || fenceBeforeReplay.OperationID != "operation-process-death-git-before-db" {
		t.Fatalf("orphan fence=%+v err=%v", fenceBeforeReplay, err)
	}
	raw, err := sql.Open("sqlite", fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var commands, leases int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM integration_commands WHERE operation_id=?`, "operation-process-death-git-before-db").Scan(&commands); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM integration_mutation_leases WHERE operation_id=?`, "operation-process-death-git-before-db").Scan(&leases); err != nil {
		t.Fatal(err)
	}
	if commands != 0 || leases != 0 {
		t.Fatalf("process death leaked commands=%d leases=%d", commands, leases)
	}
	replayed := runExternalIntegration(t, args)
	if !replayed.OK || replayed.Integration == nil || replayed.Integration.Record.Status != integration.StatusDone || replayed.Integration.Record.MutationGeneration != 1 || replayed.Integration.Record.FenceObject != fenceBeforeReplay.Object {
		t.Fatalf("same-operation replay=%+v", replayed)
	}
}

func TestIntegrationHelperPreservesGitAndPathDiagnosticKindsAcrossOSBoundary(t *testing.T) {
	t.Run("missing target", func(t *testing.T) {
		fixture, candidate := approvedIntegrationScenario(t, "diagnostic-missing-target")
		args := integrationArgs(fixture, fixture.database, "operation-diagnostic-missing-target", "review-diagnostic-missing-target", candidate, "actor-diagnostic", "process-diagnostic", time.Now().UTC())
		setArgument(args, "--target-ref", "refs/heads/missing")
		response := runExternalIntegration(t, args)
		if response.OK || response.Error == nil || response.Error.Kind != execution.KindGitFailure {
			t.Fatalf("missing target response=%+v", response)
		}
		assertIntegrationDiagnostic(t, response.Error, "operation-diagnostic-missing-target")
	})

	t.Run("target movement", func(t *testing.T) {
		fixture, candidate := approvedIntegrationScenario(t, "diagnostic-target-moved")
		scenarioGit(t, fixture.repository, "checkout", "-B", "external-advance", fixture.base)
		if err := os.WriteFile(filepath.Join(fixture.repository, "advance.txt"), []byte("advance\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		scenarioGit(t, fixture.repository, "add", "advance.txt")
		scenarioGit(t, fixture.repository, "commit", "-m", "external advance")
		advance := scenarioGit(t, fixture.repository, "rev-parse", "HEAD")
		scenarioGit(t, fixture.repository, "checkout", "--detach", fixture.base)
		barrier := t.TempDir()
		if err := os.WriteFile(filepath.Join(barrier, "IG-05.action"), []byte("block\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		args := integrationArgs(fixture, fixture.database, "operation-diagnostic-target-moved", "review-diagnostic-target-moved", candidate, "actor-diagnostic", "process-diagnostic", time.Now().UTC())
		setArgument(args, "--barrier", barrier)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		process := startIntegrationProcess(t, ctx, args)
		if err := waitForBoundaryReached(ctx, barrier, "IG-05", 1); err != nil {
			t.Fatal(err)
		}
		scenarioGit(t, fixture.repository, "update-ref", "refs/heads/main", advance, fixture.base)
		if err := os.WriteFile(filepath.Join(barrier, "IG-05.release"), []byte("release\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		response, err := process.wait()
		if err != nil || response.OK || response.Error == nil || response.Error.Kind != execution.KindTargetChanged {
			t.Fatalf("target movement response=%+v err=%v", response, err)
		}
		assertIntegrationDiagnostic(t, response.Error, "operation-diagnostic-target-moved")
	})

	t.Run("managed root changes after binding", func(t *testing.T) {
		fixture, candidate := approvedIntegrationScenario(t, "diagnostic-root-changed")
		scenarioGit(t, fixture.repository, "checkout", "--detach", "main")
		barrier := t.TempDir()
		managed := filepath.Join(t.TempDir(), "mutable-managed-root")
		if err := os.WriteFile(filepath.Join(barrier, "IG-02.action"), []byte("block\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		args := integrationArgs(fixture, fixture.database, "operation-diagnostic-root-changed", "review-diagnostic-root-changed", candidate, "actor-diagnostic", "process-diagnostic", time.Now().UTC())
		setArgument(args, "--barrier", barrier)
		setArgument(args, "--workspace-root", managed)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		process := startIntegrationProcess(t, ctx, args)
		if err := waitForBoundaryReached(ctx, barrier, "IG-02", 1); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(fixture.repository, managed); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(barrier, "IG-02.release"), []byte("release\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		response, err := process.wait()
		if err != nil || response.OK || response.Error == nil || response.Error.Kind != execution.KindPathUnsafe {
			t.Fatalf("changed managed root response=%+v err=%v", response, err)
		}
		assertIntegrationDiagnostic(t, response.Error, "operation-diagnostic-root-changed")
	})

	t.Run("managed root is unsafe at bind", func(t *testing.T) {
		fixture, candidate := approvedIntegrationScenario(t, "diagnostic-root-unsafe")
		args := integrationArgs(fixture, fixture.database, "operation-diagnostic-root-unsafe", "review-diagnostic-root-unsafe", candidate, "actor-diagnostic", "process-diagnostic", time.Now().UTC())
		setArgument(args, "--workspace-root", filepath.Join(fixture.repository, "unsafe-managed-root"))
		response := runExternalIntegration(t, args)
		if response.OK || response.Error == nil || response.Error.Kind != execution.KindPathUnsafe || response.Error.OperationID != "operation-diagnostic-root-unsafe" || response.Error.AggregateIDs["project_id"] != "project-1" {
			t.Fatalf("unsafe managed root response=%+v", response)
		}
	})
}

func TestIntegrationRecoversRepositoryFirstFenceAcquisitionWindows(t *testing.T) {
	for _, installFence := range []bool{false, true} {
		name := "after read-only authority preparation"
		if installFence {
			name = "after Git fence before SQLite activation"
		}
		t.Run(name, func(t *testing.T) {
			fixture, candidate := approvedIntegrationScenario(t, "pending-fence-"+fmt.Sprint(installFence))
			scenarioGit(t, fixture.repository, "checkout", "--detach", "main")
			now := time.Now().UTC()
			operation := "operation-pending-fence-" + fmt.Sprint(installFence)
			request := integration.Request{SchemaVersion: 1, CorrelationID: operation, OperationID: ids.OperationID(operation), ReviewID: ids.ReviewID("review-pending-fence-" + fmt.Sprint(installFence)), RepositoryID: "repository-1", TargetRef: "refs/heads/main", ActorID: "actor-pending", ProcessID: ids.ProcessID("process-pending-" + fmt.Sprint(installFence)), Candidate: candidate, LeaseTTL: 2 * time.Minute, StartedAt: now}
			store, err := sqlite.Open(context.Background(), fixture.database)
			if err != nil {
				t.Fatal(err)
			}
			adapter, err := gitrepo.Bind(context.Background(), gitrepo.Runner{}, "repository-1", "project-1", fixture.repository, fixture.managedRoot, "refs/heads/main", now)
			if err != nil {
				_ = store.Close()
				t.Fatal(err)
			}
			currentFence, err := adapter.IntegrationFence(context.Background())
			if err != nil {
				_ = store.Close()
				t.Fatal(err)
			}
			record, err := store.PrepareIntegrationAuthority(context.Background(), request, fixture.base, currentFence)
			if err != nil {
				_ = store.Close()
				t.Fatal(err)
			}
			if installFence {
				if _, err := adapter.InstallIntegrationFence(context.Background(), record, currentFence); err != nil {
					_ = store.Close()
					t.Fatal(err)
				}
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			args := integrationArgs(fixture, fixture.database, operation, request.ReviewID.String(), candidate, request.ActorID.String(), request.ProcessID.String(), now)
			response := runExternalIntegration(t, args)
			if !response.OK || response.Integration == nil || response.Integration.Record.Status != integration.StatusDone || response.Integration.Record.FenceRef == "" || response.Integration.Record.FenceObject == "" || response.Integration.Record.CleanupStatus != integration.CleanupComplete {
				t.Fatalf("pending fence recovery response=%+v", response)
			}
		})
	}
}

func TestIntegrationDifferentOperationSupersedesExpiredOrphanFence(t *testing.T) {
	fixture, candidate := approvedIntegrationScenario(t, "orphan-fence-supersession")
	scenarioGit(t, fixture.repository, "checkout", "--detach", "main")
	now := time.Now().UTC()
	store, err := sqlite.Open(context.Background(), fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := gitrepo.Bind(context.Background(), gitrepo.Runner{}, "repository-1", "project-1", fixture.repository, fixture.managedRoot, "refs/heads/main", now)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	current, err := adapter.IntegrationFence(context.Background())
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	orphanRequest := integration.Request{SchemaVersion: 1, CorrelationID: "operation-orphan-fence", OperationID: "operation-orphan-fence", ReviewID: "review-orphan-fence-supersession", RepositoryID: "repository-1", TargetRef: "refs/heads/main", ActorID: "actor-orphan", ProcessID: "process-orphan", Candidate: candidate, LeaseTTL: time.Second, StartedAt: now}
	orphan, err := store.PrepareIntegrationAuthority(context.Background(), orphanRequest, fixture.base, current)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	orphanFence, err := adapter.InstallIntegrationFence(context.Background(), orphan, current)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	winnerArgs := integrationArgs(fixture, fixture.database, "operation-orphan-successor", orphanRequest.ReviewID.String(), candidate, "actor-successor", "process-successor", time.Now().UTC())
	blocked := runExternalIntegration(t, winnerArgs)
	if blocked.OK || blocked.Error == nil || blocked.Error.Kind != execution.ErrorKind(integration.KindAuthorityBusy) {
		t.Fatalf("unexpired orphan successor response=%+v", blocked)
	}
	stillOrphan, err := adapter.IntegrationFence(context.Background())
	if err != nil || stillOrphan.Object != orphanFence.Object || stillOrphan.Generation != 1 {
		t.Fatalf("unexpired orphan moved: fence=%+v err=%v", stillOrphan, err)
	}
	time.Sleep(1200 * time.Millisecond)
	winner := runExternalIntegration(t, winnerArgs)
	if !winner.OK || winner.Integration == nil || winner.Integration.Record.Status != integration.StatusDone || winner.Integration.Record.MutationGeneration != 2 {
		t.Fatalf("expired orphan successor response=%+v", winner)
	}
	current, err = adapter.IntegrationFence(context.Background())
	if err != nil || current.OperationID != "operation-orphan-successor" || current.Generation != 2 || current.PredecessorObject != orphanFence.Object {
		t.Fatalf("successor fence=%+v orphan=%+v err=%v", current, orphanFence, err)
	}
	raw, err := sql.Open("sqlite", fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var orphanRows int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM integration_mutation_leases WHERE operation_id=?`, orphanRequest.OperationID).Scan(&orphanRows); err != nil || orphanRows != 0 {
		t.Fatalf("orphan SQLite rows=%d err=%v", orphanRows, err)
	}
	scenarioGit(t, fixture.repository, "reflog", "expire", "--expire=now", "--all")
	scenarioGit(t, fixture.repository, "gc", "--prune=now")
	replayArgs := integrationArgs(fixture, fixture.database, orphanRequest.OperationID.String(), orphanRequest.ReviewID.String(), candidate, orphanRequest.ActorID.String(), orphanRequest.ProcessID.String(), orphanRequest.StartedAt)
	setArgument(replayArgs, "--ttl", orphanRequest.LeaseTTL.String())
	replayed := runExternalIntegration(t, replayArgs)
	if replayed.OK || replayed.Error == nil || replayed.Error.Kind != execution.ErrorKind(integration.KindAuthorityExpired) {
		t.Fatalf("superseded orphan replay response=%+v", replayed)
	}
	afterReplay, err := adapter.IntegrationFence(context.Background())
	if err != nil || afterReplay.Object != current.Object || afterReplay.Generation != current.Generation || afterReplay.OperationID != current.OperationID {
		t.Fatalf("superseded orphan replay moved fence: before=%+v after=%+v err=%v", current, afterReplay, err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM integration_mutation_leases WHERE operation_id=?`, orphanRequest.OperationID).Scan(&orphanRows); err != nil || orphanRows != 0 {
		t.Fatalf("replayed orphan SQLite rows=%d err=%v", orphanRows, err)
	}
}

func TestIntegrationDelayedGenerationTwoIntentCannotRollBackActiveGenerationThree(t *testing.T) {
	fixture, candidate := approvedIntegrationScenario(t, "delayed-generation-intent")
	scenarioGit(t, fixture.repository, "checkout", "--detach", "main")
	now := time.Now().UTC()
	store, err := sqlite.Open(context.Background(), fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	adapter, err := gitrepo.Bind(context.Background(), gitrepo.Runner{}, "repository-1", "project-1", fixture.repository, fixture.managedRoot, "refs/heads/main", now)
	if err != nil {
		t.Fatal(err)
	}
	request := func(operation string, offset time.Duration) integration.Request {
		return integration.Request{SchemaVersion: 1, CorrelationID: operation, OperationID: ids.OperationID(operation), ReviewID: "review-delayed-generation-intent", RepositoryID: "repository-1", TargetRef: "refs/heads/main", ActorID: ids.ActorID("actor-" + operation), ProcessID: ids.ProcessID("process-" + operation), Candidate: candidate, LeaseTTL: 2 * time.Minute, StartedAt: now.Add(offset)}
	}
	establish := func(input integration.Request, current integration.FenceFacts) (integration.Record, integration.FenceFacts) {
		record, err := store.PrepareIntegrationAuthority(context.Background(), input, fixture.base, current)
		if err != nil {
			t.Fatal(err)
		}
		fence, err := adapter.InstallIntegrationFence(context.Background(), record, current)
		if err != nil {
			t.Fatal(err)
		}
		record, err = store.ActivateIntegrationAuthority(context.Background(), record, fence)
		if err != nil {
			t.Fatal(err)
		}
		return record, fence
	}
	initial, err := adapter.IntegrationFence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first, firstFence := establish(request("operation-generation-one", 0), initial)
	if _, err := store.RecordIntegrationRepair(context.Background(), first, integration.ConflictEvidence{Base: fixture.base}, integration.KindRepairRequired, "release generation one for monotonic installer test"); err != nil {
		t.Fatal(err)
	}
	staleSecond, err := store.PrepareIntegrationAuthority(context.Background(), request("operation-generation-two-stale", time.Millisecond), fixture.base, firstFence)
	if err != nil {
		t.Fatal(err)
	}
	winningSecond, secondFence := establish(request("operation-generation-two-winning", 2*time.Millisecond), firstFence)
	if _, err := store.RecordIntegrationRepair(context.Background(), winningSecond, integration.ConflictEvidence{Base: fixture.base}, integration.KindRepairRequired, "release generation two for monotonic installer test"); err != nil {
		t.Fatal(err)
	}
	third, thirdFence := establish(request("operation-generation-three", 3*time.Millisecond), secondFence)
	if third.MutationGeneration != 3 {
		t.Fatalf("third generation=%d", third.MutationGeneration)
	}
	if _, err := adapter.InstallIntegrationFence(context.Background(), staleSecond, firstFence); !integration.IsKind(err, integration.KindAuthorityBusy) {
		t.Fatalf("delayed generation-two installer error=%v", err)
	}
	current, err := adapter.IntegrationFence(context.Background())
	if err != nil || current.Object != thirdFence.Object || current.Generation != 3 || current.OperationID != third.OperationID {
		t.Fatalf("current=%+v third=%+v err=%v", current, thirdFence, err)
	}
	raw, err := sql.Open("sqlite", fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var staleRows int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM integration_mutation_leases WHERE operation_id=?`, staleSecond.OperationID).Scan(&staleRows); err != nil || staleRows != 0 {
		t.Fatalf("stale generation-two rows=%d err=%v", staleRows, err)
	}
}

func TestIntegrationSerializesTwoLanesFromTwoMissions(t *testing.T) {
	fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-mission-a", lane: "lane-mission-a", tracker: "TK-MISSION-A"}, {work: "work-mission-b", lane: "lane-mission-b", tracker: "TK-MISSION-B"}})
	control, err := sqlite.Open(context.Background(), fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	draft := workgraph.NewDraft("project-1", "mission-2")
	if err := draft.AddLane(workgraph.Lane{ID: "lane-mission-b"}); err != nil {
		t.Fatal(err)
	}
	if err := draft.AddWork(workgraph.WorkItem{ID: "work-mission-b", LaneID: "lane-mission-b", Criteria: []workgraph.Criterion{{ID: "criterion-work-mission-b", Text: "done"}}}); err != nil {
		t.Fatal(err)
	}
	revision, err := draft.Finalize(workgraph.FinalizeMetadata{ID: "revision-mission-2", Number: 1, ActorID: "actor-owner", FinalizedAt: time.Unix(2, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if err := finalizeScenarioRevision(context.Background(), control, revision, 0); err != nil {
		t.Fatal(err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}

	capsuleA, candidateA := startMissionCandidate(t, fixture, "mission-1", "revision-1", "work-mission-a", "lane-mission-a", "A")
	capsuleB, candidateB := startMissionCandidate(t, fixture, "mission-2", "revision-mission-2", "work-mission-b", "lane-mission-b", "B")
	approveMissionCandidate(t, fixture, capsuleA, "mission-1", candidateA, "a")
	approveMissionCandidate(t, fixture, capsuleB, "mission-2", candidateB, "b")
	scenarioGit(t, fixture.repository, "checkout", "--detach", "main")
	primaryBefore, err := fixture.primary.SnapshotPrimary(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(fixture.barrier, "IG-01.action"), []byte("block\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	argsA := integrationArgs(fixture, fixture.database, "operation-integrate-mission-a", "review-integration-a", candidateA, "actor-integrator-a", "process-integrator-a", time.Now().UTC())
	argsB := integrationArgs(fixture, fixture.database, "operation-integrate-mission-b", "review-integration-b", candidateB, "actor-integrator-b", "process-integrator-b", time.Now().UTC())
	type result struct {
		code     int
		response helpercmd.Response
	}
	first := make(chan result, 1)
	go func() {
		code, response := runIntegrationCommand(argsA)
		first <- result{code: code, response: response}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := waitForBoundaryReached(ctx, fixture.barrier, "IG-01", 1); err != nil {
		t.Fatal(err)
	}
	codeB, blocked := runIntegrationInProcess(t, argsB)
	if codeB != 0 || blocked.OK || blocked.Error == nil || blocked.Error.Kind != execution.ErrorKind(integration.KindAuthorityBusy) {
		t.Fatalf("cross-mission contender code=%d response=%+v", codeB, blocked)
	}
	assertIntegrationDiagnostic(t, blocked.Error, "operation-integrate-mission-b")
	if err := os.WriteFile(filepath.Join(fixture.barrier, "IG-01.release"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	completedA := <-first
	if completedA.code != 0 || !completedA.response.OK || completedA.response.Integration == nil || completedA.response.Integration.Record.MissionID != "mission-1" {
		t.Fatalf("mission A result=%+v", completedA)
	}
	assertIntegrationEnvelope(t, completedA.response.Integration, "operation-integrate-mission-a")
	if err := os.WriteFile(filepath.Join(fixture.barrier, "IG-01.action"), []byte("continue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	codeB, completedB := runIntegrationInProcess(t, argsB)
	if codeB != 0 || !completedB.OK || completedB.Integration == nil || completedB.Integration.Record.MissionID != "mission-2" || completedB.Integration.Record.PreflightTarget != completedA.response.Integration.Record.ResultCommit {
		t.Fatalf("mission B code=%d response=%+v first=%+v", codeB, completedB, completedA.response)
	}
	assertIntegrationEnvelope(t, completedB.Integration, "operation-integrate-mission-b")
	if completedA.response.Integration.Record.MutationGeneration != 1 || completedB.Integration.Record.MutationGeneration != 2 || completedA.response.Integration.Record.ResultCommit == completedB.Integration.Record.ResultCommit {
		t.Fatalf("non-serialized generations/results: first=%+v second=%+v", completedA.response.Integration.Record, completedB.Integration.Record)
	}
	for _, candidate := range []string{candidateA, candidateB} {
		base := scenarioGit(t, fixture.repository, "merge-base", candidate, completedB.Integration.Record.ObservedTarget)
		if base != candidate {
			t.Fatalf("final target %s does not contain candidate %s (merge-base=%s)", completedB.Integration.Record.ObservedTarget, candidate, base)
		}
	}
	primaryAfter, err := fixture.primary.SnapshotPrimary(context.Background())
	if err != nil || primaryAfter != primaryBefore {
		t.Fatalf("cross-mission integration changed primary: before=%+v after=%+v err=%v", primaryBefore, primaryAfter, err)
	}
	var done, active, generations int
	raw, err := sql.Open("sqlite", fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if err := raw.QueryRow(`SELECT COUNT(*) FROM integration_done`).Scan(&done); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM integration_mutation_leases WHERE status='active'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT COUNT(DISTINCT generation) FROM integration_mutation_leases`).Scan(&generations); err != nil {
		t.Fatal(err)
	}
	if done != 2 || active != 0 || generations != 2 {
		t.Fatalf("done=%d active=%d generations=%d", done, active, generations)
	}
}

func TestIntegrationConflictPersistsRepairAndRefusesCleanup(t *testing.T) {
	fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-integration-conflict", lane: "lane-integration-conflict", tracker: "TK-INTEGRATION-CONFLICT"}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	response := waitWorkspaceProcess(t, fixture.start(ctx, t, workspaceStart{
		operation: "operation-start-integration-conflict", work: "work-integration-conflict", lane: "lane-integration-conflict", actor: "actor-conflict", process: "process-conflict",
		workspace: "workspace-integration-conflict", branch: "runs/integration-conflict", trackerWork: "TK-INTEGRATION-CONFLICT", editFile: "base.txt", editContent: "candidate conflict line\n",
	}))
	if !response.OK || response.Capsule == nil || response.Work == nil {
		t.Fatalf("candidate response=%+v", response)
	}
	candidate := response.Work.Commit
	approveMissionCandidate(t, fixture, response.Capsule, "mission-1", candidate, "conflict")
	if err := os.WriteFile(filepath.Join(fixture.repository, "base.txt"), []byte("target conflict line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scenarioGit(t, fixture.repository, "add", "base.txt")
	scenarioGit(t, fixture.repository, "commit", "-m", "advance target incompatibly")
	target := scenarioGit(t, fixture.repository, "rev-parse", "HEAD")
	scenarioGit(t, fixture.repository, "checkout", "--detach", "HEAD")
	primaryBefore, err := fixture.primary.SnapshotPrimary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	operation := "operation-integrate-conflict"
	code, conflict := runIntegrationInProcess(t, integrationArgs(fixture, fixture.database, operation, "review-integration-conflict", candidate, "actor-integrator-conflict", "process-integrator-conflict", time.Now().UTC()))
	if code != 0 || conflict.OK || conflict.Error == nil || conflict.Error.Kind != execution.ErrorKind(integration.KindConflict) || conflict.Error.RetrySafety != execution.RetryDecisionRequired {
		t.Fatalf("conflict code=%d response=%+v", code, conflict)
	}
	assertIntegrationDiagnostic(t, conflict.Error, operation)
	if conflict.Error.AggregateIDs["mutation_generation"] == "" || conflict.Error.AggregateIDs["workspace_path"] == "" {
		t.Fatalf("conflict diagnostic omitted durable authority/workspace: %+v", conflict.Error)
	}
	var status, repairKind, action string
	var evidenceJSON []byte
	raw, err := sql.Open("sqlite", fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT status,repair_kind,repair_action,evidence_json FROM integrations WHERE operation_id=?`, operation).Scan(&status, &repairKind, &action, &evidenceJSON); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	var evidence integration.ConflictEvidence
	if err := json.Unmarshal(evidenceJSON, &evidence); err != nil || status != string(integration.StatusRepairRequired) || repairKind != string(integration.KindConflict) || action == "" || evidence.Target != target || evidence.Candidate != candidate || len(evidence.ConflictFiles) != 1 || evidence.ConflictFiles[0] != "base.txt" {
		t.Fatalf("status=%s kind=%s action=%q evidence=%+v decode=%v", status, repairKind, action, evidence, err)
	}
	adapter, err := gitrepo.Bind(context.Background(), gitrepo.Runner{}, "repository-1", "project-1", fixture.repository, fixture.managedRoot, "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cleanupRecord := integration.Record{OperationID: ids.OperationID(operation), Candidate: candidate, WorkspacePath: evidence.Workspace, Status: integration.StatusRepairRequired}
	if err := adapter.CleanupIntegrationWorkspace(context.Background(), cleanupRecord); !integration.IsKind(err, integration.KindRepairRequired) {
		t.Fatalf("conflict cleanup error=%v", err)
	}
	if got := scenarioGit(t, fixture.repository, "rev-parse", "refs/heads/main"); got != target {
		t.Fatalf("conflict moved target=%s want=%s", got, target)
	}
	if scenarioGit(t, fixture.repository, "cat-file", "-t", candidate) != "commit" {
		t.Fatal("candidate became unreachable")
	}
	primaryAfter, err := fixture.primary.SnapshotPrimary(context.Background())
	if err != nil || primaryAfter != primaryBefore {
		t.Fatalf("conflict changed primary: before=%+v after=%+v err=%v", primaryBefore, primaryAfter, err)
	}
}

func startMissionCandidate(t *testing.T, fixture workspaceScenario, mission, revision, work, lane, suffix string) (*execution.RunCapsule, string) {
	t.Helper()
	operation := "operation-start-mission-" + suffix
	args := []string{"start-run", "--db", fixture.database, "--tracker-store", fixture.tracker, "--repository", fixture.repository, "--workspace-root", fixture.managedRoot,
		"--schema-version", "1", "--correlation-id", operation, "--at", "2026-08-24T12:00:00Z", "--operation", operation, "--project", "project-1", "--repository-id", "repository-1", "--mission", mission,
		"--revision", revision, "--work", work, "--lane", lane, "--actor", "actor-mission-" + suffix, "--process", "process-mission-" + suffix, "--workspace", "workspace-mission-" + suffix,
		"--branch", "runs/mission-" + suffix, "--base", fixture.base, "--provider", "fake", "--tracker-work", "TK-MISSION-" + suffix, "--tracker-revision", "1", "--assignee", "actor-mission-" + suffix,
		"--ttl", "1m", "--edit-file", "candidate-" + suffix + ".txt", "--edit-content", "candidate " + suffix + "\n"}
	var stdout, stderr bytes.Buffer
	if code := helpercmd.Run(context.Background(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("start mission %s code=%d stdout=%s stderr=%s", mission, code, stdout.String(), stderr.String())
	}
	var response helpercmd.Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil || !response.OK || response.Capsule == nil || response.Work == nil {
		t.Fatalf("start mission %s response=%+v decode=%v raw=%s", mission, response, err, stdout.String())
	}
	return response.Capsule, response.Work.Commit
}

func approveMissionCandidate(t *testing.T, fixture workspaceScenario, capsule *execution.RunCapsule, mission, candidate, suffix string) {
	t.Helper()
	handoffID := "handoff-integration-" + suffix
	handoffOperation := "operation-handoff-integration-" + suffix
	args := handoffArgs(fixture, capsule, candidate, handoffID, handoffOperation)
	for index := range args {
		if args[index] == "--mission" && index+1 < len(args) {
			args[index+1] = mission
		}
	}
	handoffResponse := runJudgmentHelper(t, args)
	if !handoffResponse.OK {
		t.Fatalf("%s handoff=%+v", mission, handoffResponse)
	}
	reviewResponse := runReviewHelper(t, fixture, capsule, candidate, handoffID, "review-integration-"+suffix, "operation-review-integration-"+suffix, "actor-reviewer-"+suffix, "process-reviewer-"+suffix, review.Approved, "")
	if !reviewResponse.OK {
		t.Fatalf("%s review=%+v", mission, reviewResponse)
	}
}

func integrationArgs(fixture workspaceScenario, database, operation, reviewID, candidate, actor, process string, at time.Time) []string {
	return []string{"integrate", "--db", database, "--eligibility-mode", "legacy-v1", "--repository", fixture.repository, "--workspace-root", fixture.managedRoot, "--barrier", fixture.barrier,
		"--schema-version", "1", "--correlation-id", operation, "--at", at.Format(time.RFC3339Nano), "--operation", operation, "--review-id", reviewID,
		"--project", "project-1", "--repository-id", "repository-1", "--target-ref", "refs/heads/main", "--actor", actor, "--process", process, "--candidate", candidate, "--ttl", "2m"}
}

func runIntegrationInProcess(t *testing.T, args []string) (int, helpercmd.Response) {
	t.Helper()
	code, response := runIntegrationCommand(args)
	return code, response
}

func runIntegrationCommand(args []string) (int, helpercmd.Response) {
	var stdout, stderr bytes.Buffer
	code := helpercmd.Run(context.Background(), args, &stdout, &stderr)
	var response helpercmd.Response
	if stdout.Len() != 0 {
		_ = json.Unmarshal(stdout.Bytes(), &response)
	}
	return code, response
}

func startIntegrationProcess(t *testing.T, ctx context.Context, args []string) *claimProcess {
	return startIntegrationProcessEnv(t, ctx, args, nil)
}

func startIntegrationProcessEnv(t *testing.T, ctx context.Context, args, environment []string) *claimProcess {
	t.Helper()
	command := exec.CommandContext(ctx, helperBinary, args...)
	if len(environment) != 0 {
		command.Env = append(os.Environ(), environment...)
	}
	process := &claimProcess{command: command}
	command.Stdout, command.Stderr = &process.stdout, &process.stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	return process
}

func integrationGitWrapper(t *testing.T) string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
real_git=%q
case " ${*} " in
  *" update-ref --stdin "*)
	input_file=$(mktemp)
	trap 'rm -f "${input_file}"' EXIT
	cat > "${input_file}"
	if [ "${SPK_GIT_BLOCK_MODE}" = "target-before" ] && grep -q "update refs/heads/main" "${input_file}"; then
      : > "${SPK_GIT_BLOCK_DIR}/reached"
      while [ ! -f "${SPK_GIT_BLOCK_DIR}/release" ]; do sleep 0.01; done
    fi
	"${real_git}" "${@}" < "${input_file}"
	status=$?
	if [ $status -ne 0 ]; then exit $status; fi
	if grep -q "update refs/spec-kitty/fences/" "${input_file}"; then
	  if [ "${SPK_GIT_BLOCK_MODE}" = "fence-after" ]; then
      : > "${SPK_GIT_BLOCK_DIR}/reached"
      while [ ! -f "${SPK_GIT_BLOCK_DIR}/release" ]; do sleep 0.01; done
      exit 0
	  fi
	  if [ "${SPK_GIT_BLOCK_MODE}" = "fence-kill-parent" ]; then
      kill -KILL "${PPID}"
      exit 0
	  fi
	fi
	exit 0
    ;;
esac
exec "${real_git}" "${@}"
`, realGit)
	if err := os.WriteFile(filepath.Join(root, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func waitForFile(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %s: %v", path, ctx.Err())
		case <-ticker.C:
		}
	}
}

func runExternalIntegration(t *testing.T, args []string) helpercmd.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	response, err := startIntegrationProcess(t, ctx, args).wait()
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func approvedIntegrationScenario(t *testing.T, suffix string) (workspaceScenario, string) {
	t.Helper()
	fixture, capsule, candidate := startReviewCandidate(t, suffix)
	handoffID := "handoff-" + suffix
	if response := runHandoffHelper(t, fixture, capsule, candidate, handoffID, "operation-handoff-"+suffix, ""); !response.OK {
		t.Fatalf("handoff=%+v", response)
	}
	if response := runReviewHelper(t, fixture, capsule, candidate, handoffID, "review-"+suffix, "operation-review-"+suffix, "actor-reviewer-"+suffix, "process-reviewer-"+suffix, review.Approved, ""); !response.OK {
		t.Fatalf("review=%+v", response)
	}
	return fixture, candidate
}

func setArgument(args []string, name, value string) {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == name {
			args[index+1] = value
			return
		}
	}
}

func waitForIntegrationGeneration(t *testing.T, ctx context.Context, database string, generation int64, status string) {
	t.Helper()
	raw, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		if err := raw.QueryRow(`SELECT COUNT(*) FROM integration_mutation_leases WHERE generation=? AND status=?`, generation, status).Scan(&count); err == nil && count == 1 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for integration generation %d status %s: %v", generation, status, ctx.Err())
		case <-ticker.C:
		}
	}
}

func assertIntegrationBoundaryState(t *testing.T, database, repository, operation, boundary, base string) {
	t.Helper()
	target := scenarioGit(t, repository, "rev-parse", "refs/heads/main")
	raw, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var rows int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM integrations WHERE operation_id=?`, operation).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if boundary == "IG-00" {
		if rows != 0 || target != base {
			t.Fatalf("%s rows=%d target=%s base=%s", boundary, rows, target, base)
		}
		return
	}
	if rows != 1 {
		t.Fatalf("%s durable integration rows=%d", boundary, rows)
	}
	var workspacePath, resultCommit, status, cleanupStatus string
	if err := raw.QueryRow(`SELECT workspace_path,result_commit,status,cleanup_status FROM integrations WHERE operation_id=?`, operation).Scan(&workspacePath, &resultCommit, &status, &cleanupStatus); err != nil {
		t.Fatal(err)
	}
	var done int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM integration_done WHERE operation_id=?`, operation).Scan(&done); err != nil {
		t.Fatal(err)
	}
	switch boundary {
	case "IG-01", "IG-02":
		if workspacePath != "" || resultCommit != "" || target != base || done != 0 {
			t.Fatalf("%s workspace=%q result=%q target=%s done=%d", boundary, workspacePath, resultCommit, target, done)
		}
	case "IG-03":
		if workspacePath == "" || resultCommit != "" || target != base || done != 0 {
			t.Fatalf("%s workspace=%q result=%q target=%s done=%d", boundary, workspacePath, resultCommit, target, done)
		}
	case "IG-04", "IG-05":
		if workspacePath == "" || resultCommit != "" || target != base || done != 0 || scenarioGit(t, workspacePath, "rev-parse", "HEAD") == base {
			t.Fatalf("%s workspace=%q result=%q target=%s done=%d", boundary, workspacePath, resultCommit, target, done)
		}
	case "IG-06":
		if resultCommit != "" || target == base || done != 0 {
			t.Fatalf("%s result=%q target=%s done=%d", boundary, resultCommit, target, done)
		}
	case "IG-07":
		if resultCommit == "" || target != resultCommit || done != 0 || status != string(integration.StatusResultReady) {
			t.Fatalf("%s result=%q target=%s done=%d status=%s", boundary, resultCommit, target, done, status)
		}
	case "IG-08", "IG-09":
		if resultCommit == "" || target != resultCommit || done != 1 || status != string(integration.StatusDone) || cleanupStatus != string(integration.CleanupPending) {
			t.Fatalf("%s result=%q target=%s done=%d status=%s cleanup=%s", boundary, resultCommit, target, done, status, cleanupStatus)
		}
		if _, err := os.Stat(workspacePath); err != nil {
			t.Fatalf("%s cleanup evidence workspace=%q err=%v", boundary, workspacePath, err)
		}
	}
}

func assertOneIntegrationDone(t *testing.T, database, reviewID string) {
	t.Helper()
	raw, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var count int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM integration_done WHERE review_id=?`, reviewID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("done count=%d err=%v", count, err)
	}
}

func copyFile(t *testing.T, source, destination string) {
	t.Helper()
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertIntegrationEnvelope(t *testing.T, result *helpercmd.IntegrationResult, operation string) {
	t.Helper()
	if result == nil || result.SchemaVersion != execution.ResultSchemaVersion || result.Kind != "integration_done" || result.CorrelationID != operation || result.OperationID.String() != operation || result.RetrySafety != execution.RetrySafeSameOperation || len(result.ObservedState) == 0 || len(result.ExpectedState) == 0 || len(result.RecoveryActions) == 0 || result.OccurredAt.IsZero() {
		t.Fatalf("incomplete integration envelope=%+v", result)
	}
	for _, key := range []string{"operation_id", "review_id", "handoff_id", "mission_id", "work_revision_id", "work_id", "repository_id", "target_ref", "actor_id", "process_id", "candidate", "mutation_generation", "workspace_path", "result_commit"} {
		if result.AggregateIDs[key] == "" {
			t.Fatalf("integration envelope omitted %s: %+v", key, result)
		}
	}
}

func assertIntegrationDiagnostic(t *testing.T, diagnostic *execution.DiagnosticError, operation string) {
	t.Helper()
	if diagnostic == nil || diagnostic.SchemaVersion != execution.ResultSchemaVersion || diagnostic.CorrelationID != operation || diagnostic.OperationID.String() != operation || diagnostic.RetrySafety == "" || len(diagnostic.ObservedState) == 0 || len(diagnostic.ExpectedState) == 0 || len(diagnostic.RecoveryActions) == 0 || diagnostic.OccurredAt.IsZero() {
		t.Fatalf("incomplete integration diagnostic=%+v", diagnostic)
	}
	for _, key := range []string{"operation_id", "review_id", "handoff_id", "mission_id", "work_revision_id", "work_id", "repository_id", "target_ref", "actor_id", "process_id", "candidate"} {
		if diagnostic.AggregateIDs[key] == "" {
			t.Fatalf("integration diagnostic omitted %s: %+v", key, diagnostic)
		}
	}
}
