package scenario

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/helpercmd"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

var helperBinary string

func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "spk-phase1-helper-")
	if err != nil {
		panic(err)
	}
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	helperBinary = filepath.Join(directory, "spk-phase1-helper")
	command := exec.Command("go", "build", "-race", "-o", helperBinary, "./cmd/spk-phase1-helper")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "build helper: %v\n%s", err, output)
		_ = os.RemoveAll(directory)
		os.Exit(2)
	}
	code := m.Run()
	_ = os.RemoveAll(directory)
	os.Exit(code)
}

func TestWP01ClaimRace(t *testing.T) {
	responses, snapshot := runRace(t, 2)
	assertRace(t, responses, snapshot, 2)
}

func TestWP01ContentionCeiling(t *testing.T) {
	responses, snapshot := runRace(t, 32)
	assertRace(t, responses, snapshot, 32)
}

func TestWP01LostResponseReplay(t *testing.T) {
	t.Parallel()
	database := seedDatabase(t)
	barrier := t.TempDir()
	if err := os.WriteFile(filepath.Join(barrier, "SR-02.action"), []byte("crash\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	input := claimInput{Database: database, Barrier: barrier, Operation: "operation-replay", Actor: "actor-1", Process: "process-1"}

	firstProcess, err := startClaim(ctx, helperBinary, input)
	if err != nil {
		t.Fatalf("start first claim: %v", err)
	}
	if err := waitForBoundaryReached(ctx, barrier, "SR-02", 1); err != nil {
		t.Fatal(err)
	}
	err = firstProcess.command.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 86 || firstProcess.stdout.Len() != 0 {
		t.Fatalf("post-commit crash exit=%v stdout=%q stderr=%q", err, firstProcess.stdout.String(), firstProcess.stderr.String())
	}
	if err := os.WriteFile(filepath.Join(barrier, "SR-02.action"), []byte("continue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	input.Process = "process-replacement"
	conflictProcess, err := startClaim(ctx, helperBinary, input)
	if err != nil {
		t.Fatalf("start changed-process replay: %v", err)
	}
	conflict, err := conflictProcess.wait()
	if err != nil || conflict.OK || conflict.Error == nil || conflict.Error.Kind != execution.KindIdempotencyConflict || conflict.Capsule != nil {
		t.Fatalf("changed-process replay = %+v, %v", conflict, err)
	}
	input.Process = "process-1"
	replayProcess, err := startClaim(ctx, helperBinary, input)
	if err != nil {
		t.Fatalf("start exact replay: %v", err)
	}
	replay, err := replayProcess.wait()
	if err != nil || !replay.OK {
		t.Fatalf("exact replay = %+v, %v", replay, err)
	}
	if !replay.Capsule.Replayed {
		t.Fatalf("post-crash operation was not replayed: %+v", replay.Capsule)
	}
	input.Process = "process-1"
	observer, err := startClaim(ctx, helperBinary, input)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := observer.wait()
	if err != nil || !observed.OK || observed.Capsule.Token != replay.Capsule.Token || observed.Capsule.RunID != replay.Capsule.RunID || observed.Capsule.LeaseID != replay.Capsule.LeaseID {
		t.Fatalf("repeated replay=%+v err=%v first replay=%+v", observed, err, replay)
	}
	snapshot, err := inspectAuthority(ctx, helperBinary, database)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ActiveLeases != 1 || snapshot.Runs != 1 || snapshot.Operations != 1 || snapshot.Events != 1 {
		t.Fatalf("lost-response replay duplicated authority: %+v", snapshot)
	}
}

func TestWP01ConcurrentFirstOpenConvergesAcrossProcesses(t *testing.T) {
	database := filepath.Join(t.TempDir(), "first-open.db")
	barrier := t.TempDir()
	if err := os.WriteFile(filepath.Join(barrier, "CS-00.action"), []byte("block\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const contenders = 32
	processes := make([]*claimProcess, contenders)
	for index := range contenders {
		process, err := startClaim(ctx, helperBinary, claimInput{Database: database, Barrier: barrier, Operation: fmt.Sprintf("operation-open-%02d", index), Actor: fmt.Sprintf("actor-open-%02d", index), Process: fmt.Sprintf("process-open-%02d", index)})
		if err != nil {
			t.Fatal(err)
		}
		processes[index] = process
	}
	if err := waitForBoundaryReached(ctx, barrier, "CS-00", contenders); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(barrier, "CS-00.release"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for index, process := range processes {
		response, err := process.wait()
		if err != nil || response.OK || response.Error == nil || response.Error.Kind != execution.KindNotFound {
			t.Fatalf("first-open claimant %d response=%+v err=%v", index, response, err)
		}
	}
	info, err := os.Stat(database + ".token-key")
	if err != nil || info.Mode().Perm() != 0o600 || info.Size() != 32 {
		t.Fatalf("token key info=%v err=%v", info, err)
	}
}

func TestWP01CrashAtClaimBoundaryLeavesNoAuthority(t *testing.T) {
	t.Parallel()
	database := seedDatabase(t)
	barrier := t.TempDir()
	if err := os.WriteFile(filepath.Join(barrier, "SR-00.action"), []byte("crash\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	process, err := startClaim(ctx, helperBinary, claimInput{Database: database, Barrier: barrier, Operation: "operation-crash", Actor: "actor-crash", Process: "process-crash"})
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForReached(ctx, barrier, 1); err != nil {
		t.Fatal(err)
	}
	err = process.command.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 86 {
		t.Fatalf("crash exit=%v stderr=%s stdout=%s", err, process.stderr.String(), process.stdout.String())
	}
	snapshot, err := inspectAuthority(ctx, helperBinary, database)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ActiveLeases != 0 || snapshot.Runs != 0 || snapshot.Operations != 0 || snapshot.Events != 0 {
		t.Fatalf("crash leaked authority: %+v", snapshot)
	}
}

func TestWP01HelperRejectsUnsafeIDBeforeFaultPath(t *testing.T) {
	t.Parallel()
	database := seedDatabase(t)
	barrier := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	process, err := startClaim(ctx, helperBinary, claimInput{Database: database, Barrier: barrier, Operation: "operation-invalid", Actor: "actor-invalid", Process: "../escape"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := process.wait()
	if err != nil || response.OK || response.Error == nil || response.Error.Kind != execution.KindInvalidCommand {
		t.Fatalf("unsafe ID response=%+v err=%v", response, err)
	}
	entries, err := os.ReadDir(barrier)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unsafe ID reached fault filesystem: %v", entries)
	}
}

func runRace(t *testing.T, contenders int) ([]helpercmd.Response, sqlite.AuthoritySnapshot) {
	t.Helper()
	database := seedDatabase(t)
	barrier := t.TempDir()
	if err := os.WriteFile(filepath.Join(barrier, "SR-00.action"), []byte("block\n"), 0o600); err != nil {
		t.Fatalf("write barrier action: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	processes := make([]*claimProcess, contenders)
	for index := range contenders {
		process, err := startClaim(ctx, helperBinary, claimInput{
			Database: database, Barrier: barrier, Operation: fmt.Sprintf("operation-%02d", index),
			Actor: fmt.Sprintf("actor-%02d", index), Process: fmt.Sprintf("process-%02d", index),
		})
		if err != nil {
			t.Fatalf("start claimant %d: %v", index, err)
		}
		processes[index] = process
	}
	if err := waitForReached(ctx, barrier, contenders); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(barrier, "SR-00.release"), []byte("release\n"), 0o600); err != nil {
		t.Fatalf("release claimants: %v", err)
	}
	responses := make([]helpercmd.Response, contenders)
	for index, process := range processes {
		response, err := process.wait()
		if err != nil {
			t.Fatalf("wait claimant %d: %v", index, err)
		}
		responses[index] = response
	}
	store, err := sqlite.Open(ctx, database)
	if err != nil {
		t.Fatalf("reopen control store: %v", err)
	}
	defer store.Close()
	snapshot, err := inspectAuthority(ctx, helperBinary, database)
	if err != nil {
		t.Fatalf("authority snapshot: %v", err)
	}
	var winner *execution.RunCapsule
	for index := range responses {
		if responses[index].OK {
			winner = responses[index].Capsule
			break
		}
	}
	if winner == nil {
		t.Fatal("race produced no winning capsule")
	}
	now := time.Now().UTC()
	service := execution.NewService(store, execution.RandomIdentities{}, execution.SystemClock{})
	heartbeat := execution.HeartbeatRequest{SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "race-heartbeat", OperationID: winner.OperationID, ActorID: ids.ActorID(winner.AggregateIDs["actor_id"]), ProcessID: ids.ProcessID(winner.AggregateIDs["process_id"]), LeaseID: winner.LeaseID, Generation: winner.Generation, Token: winner.Token, Now: now, LeaseTTL: time.Minute}
	if _, err := service.Heartbeat(ctx, heartbeat); err != nil {
		t.Fatalf("winning token did not authorize heartbeat: %v", err)
	}
	heartbeat.Token += "-wrong"
	heartbeat.Now = now.Add(time.Second)
	if _, err := service.Heartbeat(ctx, heartbeat); !execution.IsKind(err, execution.KindStaleAuthority) {
		t.Fatalf("wrong token heartbeat error = %v", err)
	}
	return responses, snapshot
}

func assertRace(t *testing.T, responses []helpercmd.Response, snapshot sqlite.AuthoritySnapshot, contenders int) {
	t.Helper()
	winners, conflicts := 0, 0
	for index, response := range responses {
		switch {
		case response.OK:
			winners++
			if response.Capsule.SchemaVersion != execution.ResultSchemaVersion || response.Capsule.Kind != "run_started" || response.Capsule.CorrelationID == "" || response.Capsule.OperationID == "" || len(response.Capsule.AggregateIDs) == 0 || len(response.Capsule.ObservedState) == 0 || len(response.Capsule.ExpectedState) == 0 || len(response.Capsule.RecoveryActions) == 0 || response.Capsule.Generation != 1 || response.Capsule.Token == "" {
				t.Errorf("claimant %d invalid capsule: %+v", index, response.Capsule)
			}
		case response.Error != nil && response.Error.Kind == execution.KindAuthorityConflict:
			conflicts++
			if response.Error.RetrySafety != execution.RetrySafeSameOperation || response.Error.SchemaVersion != execution.ResultSchemaVersion || response.Error.CorrelationID == "" || response.Error.OperationID == "" || len(response.Error.ObservedState) == 0 || len(response.Error.ExpectedState) == 0 || response.Error.AggregateIDs["work_id"] != "work-1" || len(response.Error.RecoveryActions) == 0 {
				t.Errorf("claimant %d incomplete diagnostic: %+v", index, response.Error)
			}
		default:
			t.Errorf("claimant %d unexpected response: %+v", index, response)
		}
	}
	if winners != 1 || conflicts != contenders-1 {
		t.Fatalf("winners=%d conflicts=%d responses=%+v", winners, conflicts, responses)
	}
	if snapshot.ActiveLeases != 1 || snapshot.Runs != 1 || snapshot.Operations != contenders || snapshot.Events != contenders {
		t.Fatalf("authority snapshot = %+v, contenders=%d", snapshot, contenders)
	}
}

func seedDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open control store: %v", err)
	}
	seed := sqlite.Seed{
		ProjectID: ids.ProjectID("project-1"), MissionID: ids.MissionID("mission-1"),
		WorkRevisionID: ids.WorkRevisionID("revision-1"), WorkID: ids.WorkID("work-1"),
	}
	if err := finalizeScenarioRevision(context.Background(), store, scenarioRevision(t, seed.WorkRevisionID, 1, []ids.WorkID{seed.WorkID}), 0); err != nil {
		_ = store.Close()
		t.Fatalf("save canonical authority fixture: %v", err)
	}
	if err := store.SeedAuthority(context.Background(), seed); err != nil {
		_ = store.Close()
		t.Fatalf("verify canonical authority fixture: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close seeded store: %v", err)
	}
	return path
}
