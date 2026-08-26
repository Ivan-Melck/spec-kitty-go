package beads

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func TestRealBeadsWorktreeQualification(t *testing.T) {
	fixture := newRealBeadsFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.repository, "README.md"), []byte("qualification\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.run(t, "git", "add", "README.md"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.run(t, "git", "-c", "user.name=Spec Kitty Qualification", "-c", "user.email=qualification@invalid", "commit", "--quiet", "-m", "fixture"); err != nil {
		t.Fatal(err)
	}
	left := filepath.Join(fixture.worktrees, "left")
	right := filepath.Join(fixture.worktrees, "right")
	if _, _, err := fixture.run(t, "git", "worktree", "add", "--quiet", "-b", "qualification-left", left); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.run(t, "git", "worktree", "add", "--quiet", "-b", "qualification-right", right); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _, _ = fixture.run(t, "git", "worktree", "remove", "--force", left)
		_, _, _ = fixture.run(t, "git", "worktree", "remove", "--force", right)
	})

	t.Run("shared_identity", func(t *testing.T) {
		leftWhere := observeWhere(t, fixture, left)
		rightWhere := observeWhere(t, fixture, right)
		if leftWhere != rightWhere || leftWhere != fixture.workspace {
			t.Fatalf("linked worktrees diverged: left=%q right=%q", leftWhere, rightWhere)
		}
		leftAdapter := fixture.adapter(t)
		rightAdapter := fixture.adapter(t)
		leftReady, leftErr := leftAdapter.Preflight(context.Background())
		rightReady, rightErr := rightAdapter.Preflight(context.Background())
		if leftErr != nil || rightErr != nil || leftReady.binding.WorkspaceIdentity != rightReady.binding.WorkspaceIdentity {
			t.Fatalf("adapter worktree identity is not convergent: left=%v right=%v", leftErr, rightErr)
		}
	})

	t.Run("embedded_serialization", func(t *testing.T) {
		runCreateProcesses(t, fixture, tracker.ConcurrencyEmbeddedSerialized, 2, 5)
	})

	t.Run("server_multiwriter_contention", func(t *testing.T) {
		if os.Getenv("BEADS_SERVER_ID") == "" || os.Getenv("BEADS_SERVER_ENDPOINT") == "" || os.Getenv("DOLT_SERVER_HOST") == "" || os.Getenv("DOLT_SERVER_PORT") == "" {
			t.Skip("provider-native contention requires an explicitly qualified Beads server binding")
		}
		runCreateProcesses(t, fixture, tracker.ConcurrencyServerMultiwriter, 8, 125)
		stdout, _, err := fixture.runBD(t, "list", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Data []struct {
				ID       string         `json:"id"`
				Metadata map[string]any `json:"metadata"`
			} `json:"data"`
		}
		if decodeRealEnvelope(stdout, &envelope) != nil {
			t.Fatal("server contention catalog is malformed")
		}
		idsSeen := make(map[string]struct{}, 1000)
		worksSeen := make(map[string]struct{}, 1000)
		for _, issue := range envelope.Data {
			namespace, _ := issue.Metadata["spec_kitty"].(map[string]any)
			workID, _ := namespace["work_id"].(string)
			if workID == "" {
				continue
			}
			if _, duplicate := idsSeen[issue.ID]; duplicate {
				t.Fatalf("duplicate Beads identity %q", issue.ID)
			}
			if _, duplicate := worksSeen[workID]; duplicate {
				t.Fatalf("duplicate stable mapping %q", workID)
			}
			idsSeen[issue.ID], worksSeen[workID] = struct{}{}, struct{}{}
		}
		if len(idsSeen) != 1000 || len(worksSeen) != 1000 {
			t.Fatalf("server contention census ids=%d works=%d want=1000", len(idsSeen), len(worksSeen))
		}
	})
}

func observeWhere(t *testing.T, fixture *realBeadsFixture, directory string) string {
	t.Helper()
	stdout, _, err := fixture.runIn(t, directory, fixture.binary, "where", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Data struct {
			Path string `json:"path"`
		} `json:"data"`
	}
	if decodeRealEnvelope(stdout, &envelope) != nil {
		t.Fatal("where response is malformed")
	}
	return envelope.Data.Path
}

func (fixture *realBeadsFixture) runIn(t *testing.T, directory, executable string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = directory
	command.Env = realBeadsEnvironment(fixture.root, fixture.workspace)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func runCreateProcesses(t *testing.T, fixture *realBeadsFixture, mode tracker.ConcurrencyMode, processes, perProcess int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	commands := make([]*exec.Cmd, processes)
	var stderr = make([]bytes.Buffer, processes)
	for process := 0; process < processes; process++ {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRealBeadsProcessCreate$", "-test.v=false")
		command.Dir = fixture.repository
		command.Env = append(realBeadsEnvironment(fixture.root, fixture.workspace),
			"WP09_PROCESS_HELPER=1", "BEADS_BIN="+fixture.binary, "BEADS_DIGEST_SHA256="+fixture.digest,
			"WP09_PROJECT_ID="+fixture.projectID.String(), "WP09_CONCURRENCY_MODE="+string(mode),
			"WP09_PROCESS_INDEX="+strconv.Itoa(process), "WP09_CREATE_COUNT="+strconv.Itoa(perProcess),
			"BEADS_SERVER_ID="+os.Getenv("BEADS_SERVER_ID"), "BEADS_SERVER_ENDPOINT="+os.Getenv("BEADS_SERVER_ENDPOINT"),
			"DOLT_SERVER_HOST="+os.Getenv("DOLT_SERVER_HOST"), "DOLT_SERVER_PORT="+os.Getenv("DOLT_SERVER_PORT"),
		)
		command.Stdout = &bytes.Buffer{}
		command.Stderr = &stderr[process]
		commands[process] = command
	}
	for _, command := range commands {
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
	}
	var wait sync.WaitGroup
	errorsByProcess := make([]error, len(commands))
	for index, command := range commands {
		wait.Add(1)
		go func(index int, command *exec.Cmd) {
			defer wait.Done()
			errorsByProcess[index] = command.Wait()
		}(index, command)
	}
	wait.Wait()
	for index, err := range errorsByProcess {
		if err != nil {
			t.Fatalf("provider writer process %d failed: %v", index, err)
		}
	}
}

func TestRealBeadsProcessCreate(t *testing.T) {
	if os.Getenv("WP09_PROCESS_HELPER") != "1" {
		return
	}
	count, err := strconv.Atoi(os.Getenv("WP09_CREATE_COUNT"))
	if err != nil || count <= 0 || count > 1000 {
		t.Fatal("invalid helper create count")
	}
	process, err := strconv.Atoi(os.Getenv("WP09_PROCESS_INDEX"))
	if err != nil || process < 0 {
		t.Fatal("invalid helper process index")
	}
	mode := tracker.ConcurrencyMode(os.Getenv("WP09_CONCURRENCY_MODE"))
	config := BindingConfig{
		ProjectID: ids.ProjectID(os.Getenv("WP09_PROJECT_ID")), ExecutablePath: os.Getenv("BEADS_BIN"), WorkspacePath: os.Getenv("BEADS_DIR"),
		ProtocolSchema: 1, ConcurrencyMode: mode, ExpectedExecutableDigest: os.Getenv("BEADS_DIGEST_SHA256"),
	}
	if mode == tracker.ConcurrencyServerMultiwriter {
		config.ServerIdentity = os.Getenv("BEADS_SERVER_ID")
		config.ServerEndpoint = os.Getenv("BEADS_SERVER_ENDPOINT")
		config.Credentials = map[string]string{"DOLT_SERVER_HOST": os.Getenv("DOLT_SERVER_HOST"), "DOLT_SERVER_PORT": os.Getenv("DOLT_SERVER_PORT")}
	}
	bound, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	for offset := 0; offset < count; offset++ {
		index := process*count + offset
		_, err := bound.CreateWork(context.Background(), tracker.CreateWorkRequest{
			ProjectID: config.ProjectID, WorkID: ids.WorkID(fmt.Sprintf("concurrent-work-%04d", index)),
			OperationID: ids.OperationID(fmt.Sprintf("concurrent-op-%04d", index)), Title: fmt.Sprintf("concurrent work %04d", index), MetadataSchema: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
