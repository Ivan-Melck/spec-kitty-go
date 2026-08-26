package beads

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func TestMain(m *testing.M) {
	if os.Getenv("BEADS_DIR") != "" && os.Getenv("BD_JSON_ENVELOPE") == "1" {
		os.Exit(runCommandHelper())
	}
	os.Exit(m.Run())
}

func runCommandHelper() int {
	scenario := filepath.Base(os.Getenv("BEADS_DIR"))
	switch scenario {
	case "valid":
		fmt.Print(`{"schema_version":1,"data":{"id":"work-1"}}`)
	case "warning":
		fmt.Fprintln(os.Stderr, "warning text is deliberately non-semantic")
		fmt.Print(`{"schema_version":1,"data":{"id":"work-1"}}`)
	case "error":
		fmt.Fprintln(os.Stderr, `{"schema_version":1,"error":"gone","code":"not_found"}`)
		return 7
	case "prose-error":
		fmt.Fprintln(os.Stderr, "not found maybe")
		return 7
	case "signal-error":
		fmt.Fprintln(os.Stderr, `{"schema_version":1,"error":"gone","code":"not_found"}`)
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		time.Sleep(time.Second)
		return 7
	case "stdout-overflow":
		fmt.Print(strings.Repeat("x", 4096))
	case "stderr-overflow":
		fmt.Fprint(os.Stderr, strings.Repeat("x", 4096))
		return 7
	case "sleep":
		time.Sleep(10 * time.Second)
	case "applied-sleep":
		_ = os.WriteFile(filepath.Join(mustGetwd(), "applied"), []byte("yes"), 0o600)
		time.Sleep(10 * time.Second)
	case "environment":
		encoded, _ := json.Marshal(os.Environ())
		fmt.Printf(`{"schema_version":1,"data":%s}`, encoded)
	case "descendant":
		if os.Getenv("WP03_DESCENDANT") == "1" {
			time.Sleep(10 * time.Second)
			return 0
		}
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "WP03_DESCENDANT=1")
		if err := child.Start(); err != nil {
			return 9
		}
		_ = os.WriteFile(filepath.Join(mustGetwd(), "descendant.pid"), []byte(strconv.Itoa(child.Process.Pid)), 0o600)
		time.Sleep(10 * time.Second)
	default:
		return 8
	}
	return 0
}

func mustGetwd() string { value, _ := os.Getwd(); return value }

func TestCommandArgvVocabulary(t *testing.T) {
	binding := defaultBinding("/opt/bd", "/srv/beads", "/repo")
	binding.timeout = time.Second
	tests := []struct {
		name     string
		build    func() (command, error)
		want     []string
		shape    expectedShape
		mutating bool
	}{
		{"version", func() (command, error) { return newVersionCommand(binding) }, []string{"version", "--json"}, expectedObject, false},
		{"where", func() (command, error) { return newWhereCommand(binding) }, []string{"where", "--json"}, expectedObject, false},
		{"info", func() (command, error) { return newInfoCommand(binding) }, []string{"info", "--schema", "--json"}, expectedObject, false},
		{"doctor", func() (command, error) { return newDoctorCommand(binding) }, []string{"doctor", "--agent", "--json"}, expectedObject, false},
		{"show", func() (command, error) { return newShowCommand(binding, "x") }, []string{"show", "x", "--json"}, expectedObject, false},
		{"list", func() (command, error) { return newListCommand(binding) }, []string{"list", "--json"}, expectedArray, false},
		{"ready", func() (command, error) { return newReadyCommand(binding) }, []string{"ready", "--json"}, expectedArray, false},
		{"create", func() (command, error) {
			return newCreateCommand(binding, createValues{title: "t", description: "d", acceptance: "a", metadata: "{}"})
		}, []string{"create", "--title", "t", "--description", "d", "--acceptance", "a", "--metadata", "{}", "--json"}, expectedObject, true},
		{"update", func() (command, error) {
			return newUpdateCommand(binding, updateValues{id: "x", title: "t", description: "d", acceptance: "a", metadata: "{}", assignee: "ivan"})
		}, []string{"update", "x", "--title", "t", "--description", "d", "--acceptance", "a", "--metadata", "{}", "--assignee", "ivan", "--json"}, expectedObject, true},
		{"claim", func() (command, error) { return newAtomicClaimCommand(binding, "x") }, []string{"update", "x", "--claim", "--json"}, expectedObject, true},
		{"dep-add", func() (command, error) { return newDependencyAddCommand(binding, "blocked", "blocker") }, []string{"dep", "add", "blocked", "blocker", "--type", "blocks", "--json"}, expectedObject, true},
		{"dep-list", func() (command, error) { return newDependencyListCommand(binding, "x") }, []string{"dep", "list", "x", "--json"}, expectedArray, false},
		{"comment-add", func() (command, error) { return newCommentAppendCommand(binding, "x", "body") }, []string{"comments", "add", "x", "body", "--json"}, expectedObject, true},
		{"comment-list", func() (command, error) { return newCommentListCommand(binding, "x") }, []string{"comments", "x", "--json"}, expectedArray, false},
		{"export", func() (command, error) { return newExportCommand(binding) }, []string{"export", "--json"}, expectedJSONL, false},
		{"backup-init", func() (command, error) { return newBackupInitCommand(binding, "/backup") }, []string{"backup", "init", "/backup"}, expectedEmpty, true},
		{"backup-sync", func() (command, error) { return newBackupSyncCommand(binding) }, []string{"backup", "sync"}, expectedEmpty, true},
		{"backup-status", func() (command, error) { return newBackupStatusCommand(binding) }, []string{"backup", "status"}, expectedEmpty, false},
		{"backup-restore", func() (command, error) { return newBackupRestoreCommand(binding, "/backup") }, []string{"backup", "restore", "/backup"}, expectedEmpty, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.build()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.renderedArgv(), test.want) {
				t.Fatalf("argv=%q want=%q", got.renderedArgv(), test.want)
			}
			if got.shape != test.shape || got.mutating != test.mutating {
				t.Fatalf("metadata=%v/%t", got.shape, got.mutating)
			}
			if !reflect.DeepEqual(got.renderedArgv(), got.renderedArgv()) {
				t.Fatal("rendering changed")
			}
		})
	}
}

func TestCommandValidationBeforeSpawn(t *testing.T) {
	valid := defaultBinding("/opt/bd", "/srv/beads", "/repo")
	valid.timeout = time.Second
	tests := []struct {
		name  string
		build func() error
	}{
		{"relative executable", func() error { b := valid; b.executable = "bd"; _, err := newListCommand(b); return err }},
		{"relative workspace", func() error { b := valid; b.workspace = ".beads"; _, err := newListCommand(b); return err }},
		{"relative cwd", func() error { b := valid; b.workingDir = "."; _, err := newListCommand(b); return err }},
		{"negative timeout", func() error { b := valid; b.timeout = -1; _, err := newListCommand(b); return err }},
		{"too long timeout", func() error { b := valid; b.timeout = 31 * time.Second; _, err := newListCommand(b); return err }},
		{"explicit timeout absent", func() error { b := valid; b.timeout = 0; _, err := newDoctorCommand(b); return err }},
		{"empty id", func() error { _, err := newShowCommand(valid, ""); return err }},
		{"nul id", func() error { _, err := newShowCommand(valid, "x\x00y"); return err }},
		{"empty update", func() error { _, err := newUpdateCommand(valid, updateValues{id: "x"}); return err }},
		{"self dependency", func() error { _, err := newDependencyAddCommand(valid, "x", "x"); return err }},
		{"relative backup", func() error { _, err := newBackupInitCommand(valid, "backup"); return err }},
		{"credential", func() error {
			b := valid
			b.credentials = map[string]string{"SECRET": "sentinel"}
			_, err := newListCommand(b)
			return err
		}},
		{"negative output", func() error { b := valid; b.stdoutLimit = -1; _, err := newListCommand(b); return err }},
		{"nul optional argument", func() error {
			_, err := newCreateCommand(valid, createValues{title: "x", description: "bad\x00value"})
			return err
		}},
		{"unknown kind", func() error {
			_, err := buildCommand(CommandKind("other"), valid, []string{"other", "--json"}, expectedObject, false, false)
			return err
		}},
		{"wrong mutation metadata", func() error {
			_, err := buildCommand(CommandList, valid, []string{"list", "--json"}, expectedArray, true, false)
			return err
		}},
		{"invented json support", func() error {
			_, err := buildCommand(CommandBackupStatus, valid, []string{"backup", "status", "--json"}, expectedEmpty, false, true)
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.build() == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestCommandTimeoutDefaults(t *testing.T) {
	binding := defaultBinding("/opt/bd", "/srv/beads", "/repo")
	read, err := newShowCommand(binding, "x")
	if err != nil || read.binding.timeout != defaultReadTimeout {
		t.Fatalf("read timeout=%s err=%v", read.binding.timeout, err)
	}
	mutation, err := newCreateCommand(binding, createValues{title: "x"})
	if err != nil || mutation.binding.timeout != defaultMutationTimeout {
		t.Fatalf("mutation timeout=%s err=%v", mutation.binding.timeout, err)
	}
}

func helperCommand(t *testing.T, scenario string, mutating bool, timeout time.Duration, stdoutLimit, stderrLimit int64) command {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	workspace := filepath.Join(root, scenario)
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	binding := defaultBinding(executable, workspace, root)
	binding.timeout, binding.stdoutLimit, binding.stderrLimit = timeout, stdoutLimit, stderrLimit
	var cmd command
	if mutating {
		cmd, err = newCreateCommand(binding, createValues{title: "secret-title"})
	} else {
		cmd, err = newShowCommand(binding, "work-1")
	}
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestProcessRunnerSuccessWarningsAndDocumentedError(t *testing.T) {
	adapter := newAdapter(newProcessRunner())
	valid := helperCommand(t, "valid", false, 5*time.Second, 1024, 1024)
	response, err := adapter.execute(context.Background(), valid)
	if err != nil {
		t.Fatal(err)
	}
	if string(response.payload) != `{"id":"work-1"}` {
		t.Fatalf("payload=%s", response.payload)
	}
	warning := helperCommand(t, "warning", false, 5*time.Second, 1024, 1024)
	response, err = adapter.execute(context.Background(), warning)
	if err != nil {
		t.Fatal(err)
	}
	if !response.metadata.warningsPresent || response.metadata.warningCount < 1 {
		t.Fatalf("warnings=%+v", response.metadata)
	}
	rejected := helperCommand(t, "error", true, 5*time.Second, 1024, 1024)
	response, err = adapter.execute(context.Background(), rejected)
	if err == nil || response.result.mutationOutcome != mutationNotApplied {
		t.Fatalf("err=%v outcome=%s", err, response.result.mutationOutcome)
	}
}

func TestProcessRunnerBoundsCancellationAndAmbiguity(t *testing.T) {
	adapter := newAdapter(&processRunner{killGrace: 5 * time.Millisecond})
	for _, scenario := range []string{"stdout-overflow", "stderr-overflow"} {
		cmd := helperCommand(t, scenario, true, time.Second, 64, 64)
		response, err := adapter.execute(context.Background(), cmd)
		if err == nil || response.result.mutationOutcome != mutationAmbiguous {
			t.Fatalf("%s err=%v outcome=%s", scenario, err, response.result.mutationOutcome)
		}
	}
	cmd := helperCommand(t, "applied-sleep", true, 40*time.Millisecond, 1024, 1024)
	response, err := adapter.execute(context.Background(), cmd)
	if err == nil || response.result.mutationOutcome != mutationAmbiguous {
		t.Fatalf("err=%v outcome=%s", err, response.result.mutationOutcome)
	}
	if _, statErr := os.Stat(filepath.Join(cmd.binding.workingDir, "applied")); statErr != nil {
		t.Fatalf("mutation marker: %v", statErr)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	before := helperCommand(t, "applied-sleep", true, time.Second, 1024, 1024)
	result := newProcessRunner().run(canceled, before)
	if result.started || result.mutationOutcome != mutationNotStarted {
		t.Fatalf("result=%+v", result)
	}
	if _, statErr := os.Stat(filepath.Join(before.binding.workingDir, "applied")); !os.IsNotExist(statErr) {
		t.Fatalf("unexpected marker: %v", statErr)
	}
	read := helperCommand(t, "sleep", false, 30*time.Millisecond, 1024, 1024)
	_, err = adapter.execute(context.Background(), read)
	if !tracker.IsKind(err, tracker.ErrorTimeout) {
		t.Fatalf("read timeout classified as %v", err)
	}
	signaled := helperCommand(t, "signal-error", true, 5*time.Second, 1024, 1024)
	response, err = adapter.execute(context.Background(), signaled)
	if !tracker.IsKind(err, tracker.ErrorAmbiguous) || !response.result.signaled || response.result.mutationOutcome != mutationAmbiguous {
		t.Fatalf("signal err=%v result=%+v", err, response.result)
	}
}

func TestProcessRunnerSanitizesEnvironment(t *testing.T) {
	t.Setenv("WP03_SECRET_SENTINEL", "do-not-inherit")
	base := helperCommand(t, "environment", false, 5*time.Second, 1<<20, 1024)
	cmd, err := newListCommand(base.binding)
	if err != nil {
		t.Fatal(err)
	}
	cmd.binding.credentials = map[string]string{"DOLT_SERVER_HOST": "localhost"}
	response, err := newAdapter(newProcessRunner()).execute(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	var environment []string
	if err := json.Unmarshal(response.payload, &environment); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(environment, "\n")
	for _, required := range []string{"BEADS_DIR=", "BD_JSON_ENVELOPE=1", "BD_NON_INTERACTIVE=1", "DOLT_SERVER_HOST=localhost"} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing %s", required)
		}
	}
	if strings.Contains(joined, "WP03_SECRET_SENTINEL") || strings.Contains(joined, "do-not-inherit") {
		t.Fatal("secret inherited")
	}
	if !sortStrings(environment) {
		t.Fatal("environment is not sorted")
	}
}

func sortStrings(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i-1] > values[i] {
			return false
		}
	}
	return true
}

func TestProcessRunnerKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process groups")
	}
	cmd := helperCommand(t, "descendant", false, 80*time.Millisecond, 1024, 1024)
	_, err := newAdapter(&processRunner{killGrace: 5 * time.Millisecond}).execute(context.Background(), cmd)
	if err == nil {
		t.Fatal("expected timeout")
	}
	pidBytes, err := os.ReadFile(filepath.Join(cmd.binding.workingDir, "descendant.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(pidBytes))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		err = syscall.Kill(pid, 0)
		if errorsIsNoSuchProcess(err) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant %d still exists: %v", pid, err)
}

func errorsIsNoSuchProcess(err error) bool { return err == syscall.ESRCH }

func TestMissingExecutableIsNotStarted(t *testing.T) {
	binding := defaultBinding(filepath.Join(t.TempDir(), "missing"), t.TempDir(), t.TempDir())
	binding.timeout = time.Second
	cmd, err := newCreateCommand(binding, createValues{title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	result := newProcessRunner().run(context.Background(), cmd)
	if result.started || result.mutationOutcome != mutationNotStarted {
		t.Fatalf("result=%+v", result)
	}
}

func TestNoShellOrRawArgvProductionPath(t *testing.T) {
	for _, file := range []string{"command.go", "adapter.go"} {
		body, err := os.ReadFile(file)
		if err != nil {
			body, err = os.ReadFile(filepath.Join("internal/tracker/beads", file))
		}
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		for _, forbidden := range []string{"sh -c", "exec.CommandContext(\"sh\"", "github.com/steveyegge/beads", "github.com/gastownhall/beads", "database/sql"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s contains %q", file, forbidden)
			}
		}
	}
}
