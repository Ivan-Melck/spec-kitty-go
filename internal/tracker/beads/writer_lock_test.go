package beads

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func TestWriterLockCrossProcessContentionAndKilledHolderRecovery(t *testing.T) {
	if os.Getenv("GO_WANT_BEADS_LOCK_HELPER") == "1" {
		writerLockHelper()
		return
	}
	bound, _ := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
	for iteration := 0; iteration < 25; iteration++ {
		command := exec.Command(os.Args[0], "-test.run=TestWriterLockCrossProcessContentionAndKilledHolderRecovery")
		command.Env = append(os.Environ(), "GO_WANT_BEADS_LOCK_HELPER=1", "BEADS_LOCK_WORKSPACE="+bound.binding.WorkspacePath, "BEADS_LOCK_IDENTITY="+bound.binding.WorkspaceIdentity, "BEADS_LOCK_HOLD_MS=5000")
		stdout, err := command.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		command.Stderr = os.Stderr
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil || line != "locked\n" {
			_ = command.Process.Kill()
			t.Fatalf("helper: %q %v", line, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		_, err = filesystemWriterLocker{}.acquire(ctx, bound.binding)
		cancel()
		if !tracker.IsKind(err, tracker.ErrorLockContended) {
			_ = command.Process.Kill()
			t.Fatalf("iteration %d contention=%v", iteration, err)
		}
		if err := command.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = command.Wait()
		ctx, cancel = context.WithTimeout(context.Background(), time.Second)
		lock, err := filesystemWriterLocker{}.acquire(ctx, bound.binding)
		cancel()
		if err != nil {
			t.Fatalf("iteration %d recovery=%v", iteration, err)
		}
		lock.release()
	}
}

func TestWriterLockCanonicalAcrossLaneWorkingDirectories(t *testing.T) {
	if os.Getenv("GO_WANT_BEADS_LANE_LOCK_HELPER") == "1" {
		laneWriterLockHelper()
		return
	}
	bound, _ := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
	root := t.TempDir()
	record := filepath.Join(root, "critical-sections")
	goSignal := filepath.Join(root, "go")
	commands := make([]*exec.Cmd, 0, 2)
	for index, lane := range []string{"lane-a", "lane-b"} {
		working := filepath.Join(root, lane)
		mustMkdir(t, working)
		command := exec.Command(os.Args[0], "-test.run=TestWriterLockCanonicalAcrossLaneWorkingDirectories")
		command.Dir = working
		command.Env = append(os.Environ(), "GO_WANT_BEADS_LANE_LOCK_HELPER=1", "BEADS_LOCK_WORKSPACE="+bound.binding.WorkspacePath, "BEADS_LOCK_IDENTITY="+bound.binding.WorkspaceIdentity, "BEADS_LOCK_RECORD="+record, "BEADS_LOCK_GO="+goSignal, fmt.Sprintf("BEADS_LOCK_HELPER_ID=%d", index))
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, command)
	}
	waitForGlobCount(t, filepath.Join(root, "ready-*"), 2)
	if err := os.WriteFile(goSignal, []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	payload, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(payload))
	if len(lines) != 4 || lines[0] != "start" || lines[1] != "end" || lines[2] != "start" || lines[3] != "end" {
		t.Fatalf("canonical lock allowed overlap from divergent CWDs: %q", payload)
	}
}

func laneWriterLockHelper() {
	record, goSignal := os.Getenv("BEADS_LOCK_RECORD"), os.Getenv("BEADS_LOCK_GO")
	ready := filepath.Join(filepath.Dir(record), "ready-"+os.Getenv("BEADS_LOCK_HELPER_ID"))
	if err := os.WriteFile(ready, []byte("ready"), 0600); err != nil {
		os.Exit(2)
	}
	for {
		if _, err := os.Stat(goSignal); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	binding := tracker.TrackerBinding{WorkspacePath: os.Getenv("BEADS_LOCK_WORKSPACE"), WorkspaceIdentity: os.Getenv("BEADS_LOCK_IDENTITY")}
	lock, err := filesystemWriterLocker{}.acquire(context.Background(), binding)
	if err != nil {
		os.Exit(3)
	}
	defer lock.release()
	file, err := os.OpenFile(record, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(4)
	}
	_, _ = fmt.Fprintln(file, "start")
	_ = file.Close()
	time.Sleep(75 * time.Millisecond)
	file, err = os.OpenFile(record, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(5)
	}
	_, _ = fmt.Fprintln(file, "end")
	_ = file.Close()
}

func waitForGlobCount(t *testing.T, pattern string, wanted int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		matches, _ := filepath.Glob(pattern)
		if len(matches) >= wanted {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	matches, _ := filepath.Glob(pattern)
	t.Fatalf("glob %q count=%d want=%d", pattern, len(matches), wanted)
}

func writerLockHelper() {
	workspace := os.Getenv("BEADS_LOCK_WORKSPACE")
	identity := os.Getenv("BEADS_LOCK_IDENTITY")
	milliseconds, _ := strconv.Atoi(os.Getenv("BEADS_LOCK_HOLD_MS"))
	binding := tracker.TrackerBinding{WorkspacePath: workspace, WorkspaceIdentity: identity}
	lock, err := filesystemWriterLocker{}.acquire(context.Background(), binding)
	if err != nil {
		os.Exit(3)
	}
	defer lock.release()
	fmt.Println("locked")
	time.Sleep(time.Duration(milliseconds) * time.Millisecond)
}

func TestWriterLocksForDifferentWorkspacesDoNotContend(t *testing.T) {
	first, _ := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
	second, _ := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-2")
	lock1, err := filesystemWriterLocker{}.acquire(context.Background(), first.binding)
	if err != nil {
		t.Fatal(err)
	}
	defer lock1.release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lock2, err := filesystemWriterLocker{}.acquire(ctx, second.binding)
	if err != nil {
		t.Fatal(err)
	}
	lock2.release()
}

func TestWriterLockCancellationIsBoundedAndPermissionRestricted(t *testing.T) {
	bound, _ := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
	first, err := filesystemWriterLocker{}.acquire(context.Background(), bound.binding)
	if err != nil {
		t.Fatal(err)
	}
	defer first.release()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	started := time.Now()
	_, err = filesystemWriterLocker{}.acquire(ctx, bound.binding)
	cancel()
	if !tracker.IsKind(err, tracker.ErrorLockContended) || time.Since(started) > time.Second {
		t.Fatalf("error=%v duration=%s", err, time.Since(started))
	}
	info, err := os.Stat(writerLockPath(bound.binding))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}

func TestQualifiedServerMutationBypassesLocalWriterLock(t *testing.T) {
	bound, runner := testBoundAdapter(t, tracker.ConcurrencyServerMultiwriter, "db-1")
	readiness, err := bound.Preflight(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	locker := &countingLocker{err: fmt.Errorf("must not acquire")}
	bound.locker = locker
	request, err := newCreateCommand(bound.commandBinding(), createValues{title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	before := len(runner.calls)
	response, err := bound.executeMutation(context.Background(), readiness, request)
	if err != nil {
		t.Fatal(err)
	}
	if locker.calls != 0 || !response.result.started || len(runner.calls) != before+1 {
		t.Fatalf("lock=%d response=%#v calls=%v", locker.calls, response, runner.calls[before:])
	}
}

func TestQualifiedServerProcessesOverlap(t *testing.T) {
	if os.Getenv("GO_WANT_BEADS_SERVER_HELPER") == "1" {
		serverMutationHelper()
		return
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	mustMkdir(t, workspace)
	mustMkdir(t, filepath.Join(workspace, "embeddeddolt"))
	executable := testProtocolExecutable(t, root, tracker.ConcurrencyServerMultiwriter)
	record := filepath.Join(root, "server-writes")
	commands := make([]*exec.Cmd, 0, 2)
	for index := 0; index < 2; index++ {
		command := exec.Command(os.Args[0], "-test.run=TestQualifiedServerProcessesOverlap")
		command.Env = append(os.Environ(), "GO_WANT_BEADS_SERVER_HELPER=1", "BEADS_SERVER_EXECUTABLE="+executable, "BEADS_SERVER_WORKSPACE="+workspace, "BEADS_SERVER_RECORD="+record, fmt.Sprintf("BEADS_SERVER_PROJECT=project-%d", index))
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, command)
	}
	waitForGlobCount(t, record+".*.ready", 2)
	if err := os.WriteFile(record+".go", []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	waitForGlobCount(t, record+".*.started", 2)
	if err := os.WriteFile(record+".release", []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	payload, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(payload))
	if len(fields) != 12 || fields[0] != "start" || fields[3] != "start" || fields[6] != "end" || fields[9] != "end" {
		t.Fatalf("qualified server writes did not overlap: %q", payload)
	}
}

func serverMutationHelper() {
	workspace := os.Getenv("BEADS_SERVER_WORKSPACE")
	config := BindingConfig{ProjectID: trackerProjectID(os.Getenv("BEADS_SERVER_PROJECT")), ExecutablePath: os.Getenv("BEADS_SERVER_EXECUTABLE"), WorkspacePath: workspace, ProviderDatabaseIdentity: filepath.Join(workspace, "embeddeddolt"), ProtocolSchema: 1, ConcurrencyMode: tracker.ConcurrencyServerMultiwriter, ServerIdentity: "server-1", ServerEndpoint: "127.0.0.1:3306", Credentials: map[string]string{"DOLT_SERVER_HOST": "127.0.0.1", "DOLT_SERVER_PORT": "3306", "DOLT_CREDENTIALS_PATH": os.Getenv("BEADS_SERVER_RECORD")}}
	bound, err := Open(config)
	if err != nil {
		os.Exit(2)
	}
	readiness, err := bound.Preflight(context.Background())
	if err != nil {
		os.Exit(3)
	}
	request, err := newCreateCommand(bound.commandBinding(), createValues{title: "work"})
	if err != nil {
		os.Exit(4)
	}
	if _, err = bound.executeMutation(context.Background(), readiness, request); err != nil {
		os.Exit(5)
	}
}

func trackerProjectID(value string) ids.ProjectID { return ids.ProjectID(value) }

func TestUnqualifiedServerMutationRefusesBeforeLockOrCommand(t *testing.T) {
	bound, runner := testBoundAdapter(t, tracker.ConcurrencyServerMultiwriter, "db-1")
	info, err := os.Stat(bound.binding.ExecutablePath)
	if err != nil {
		t.Fatal(err)
	}
	readiness := Readiness{binding: bound.binding, executableSize: info.Size(), executableMTime: info.ModTime()}
	locker := &countingLocker{}
	bound.locker = locker
	request, _ := newCreateCommand(bound.commandBinding(), createValues{title: "work"})
	before := len(runner.calls)
	_, err = bound.executeMutation(context.Background(), readiness, request)
	if !tracker.IsKind(err, tracker.ErrorUnavailable) || locker.calls != 0 || len(runner.calls) != before {
		t.Fatalf("error=%v lock=%d calls=%v", err, locker.calls, runner.calls[before:])
	}
}
