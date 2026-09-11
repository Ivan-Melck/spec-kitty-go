package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/helpercmd"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/tracker/hubnot"
)

func TestNativeAndCompatibilityBinariesHaveOneClosedSurface(t *testing.T) {
	repository := repositoryRoot(t)
	for _, target := range []string{"./cmd/spec-kitty", "./cmd/spk-phase1-helper"} {
		binary := buildBinary(t, repository, target)
		output, err := exec.Command(binary, "--help").CombinedOutput()
		if err != nil {
			t.Fatalf("%s help: %v: %s", target, err, output)
		}
		help := string(output)
		for _, required := range []string{"status", "progress", "operation inspect", "operation resume", "drive", "--control-root", "--output"} {
			if !strings.Contains(help, required) {
				t.Fatalf("%s help omitted %q:\n%s", target, required, help)
			}
		}
		for _, forbidden := range []string{"--db", "--backend", "--eligibility", "--fallback", "--waiver", "--import", "--force"} {
			if strings.Contains(help, forbidden) {
				t.Fatalf("%s help exposed forbidden option %q", target, forbidden)
			}
		}
	}
}

func TestUnknownLegacyOptionsRefuseBeforeFilesystemMutation(t *testing.T) {
	repository := repositoryRoot(t)
	binary := buildBinary(t, repository, "./cmd/spec-kitty")
	parent := t.TempDir()
	root := filepath.Join(parent, "authority")
	for _, option := range []string{"--db", "--backend", "--eligibility", "--fallback", "--waiver", "--import", "--force"} {
		command := exec.Command(binary, option, "value", "--control-root", root, "status")
		output, err := command.Output()
		if err == nil {
			t.Fatalf("%s unexpectedly succeeded: %s", option, output)
		}
		var envelope helpercmd.Envelope
		if decodeErr := json.Unmarshal(output, &envelope); decodeErr != nil || envelope.Outcome != helpercmd.OutcomeInvalid {
			t.Fatalf("%s envelope=%+v decode=%v output=%s", option, envelope, decodeErr, output)
		}
		if _, statErr := os.Lstat(root); !os.IsNotExist(statErr) {
			t.Fatalf("%s mutated control root: %v", option, statErr)
		}
	}
}

func TestHistoricalAndUnsafeRootsRefuseBeforeInstallationOrMutation(t *testing.T) {
	repository := repositoryRoot(t)
	binary := buildBinary(t, repository, "./cmd/spec-kitty")
	parent := t.TempDir()
	header := append([]byte("SQLite format 3\x00"), make([]byte, 128)...)
	makeRoot := func(name string) string {
		root := filepath.Join(parent, name)
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		return root
	}
	historical := makeRoot("historical")
	writeFixture(t, filepath.Join(historical, "control.db"), header)
	renamed := makeRoot("renamed")
	writeFixture(t, filepath.Join(renamed, "copied-authority.bin"), header)
	mixed := makeRoot("mixed")
	writeFixture(t, filepath.Join(mixed, "control.db"), header)
	writeFixture(t, filepath.Join(mixed, "IDENTITY"), []byte("malformed-current-marker"))
	malformed := makeRoot("malformed")
	writeFixture(t, filepath.Join(malformed, "IDENTITY"), []byte("malformed-current-marker"))
	ambiguous := makeRoot("ambiguous")
	writeFixture(t, filepath.Join(ambiguous, "unknown-authority"), []byte("unknown"))
	symlink := filepath.Join(parent, "unsafe")
	if err := os.Symlink(historical, symlink); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{historical, renamed, mixed, malformed, ambiguous, symlink} {
		before := directoryFingerprint(t, parent)
		command := exec.Command(binary, "--control-root", root, "status")
		command.Env = []string{"PATH=" + os.Getenv("PATH")}
		output, err := command.Output()
		if err == nil {
			t.Fatalf("root=%s unexpectedly succeeded: %s", root, output)
		}
		var envelope helpercmd.Envelope
		if decodeErr := json.Unmarshal(output, &envelope); decodeErr != nil || envelope.Diagnostic == nil || envelope.Diagnostic.Kind != "OFFLINE_DISPOSITION_REQUIRED" {
			t.Fatalf("root=%s envelope=%+v decode=%v output=%s", root, envelope, decodeErr, output)
		}
		if after := directoryFingerprint(t, parent); after != before {
			t.Fatalf("root=%s mutated filesystem before refusal\nbefore=%s\nafter=%s", root, before, after)
		}
	}
}

func writeFixture(t *testing.T, path string, value []byte) {
	t.Helper()
	if err := os.WriteFile(path, value, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRealGapDBMutationInitializesAndQueriesRemainPureAcrossRestart(t *testing.T) {
	sidecar := os.Getenv("SPEC_KITTY_WP15_GAPDBD")
	if sidecar == "" {
		t.Skip("set SPEC_KITTY_WP15_GAPDBD to the exact admitted assertion-extension sidecar")
	}
	repository := repositoryRoot(t)
	binary := buildBinary(t, repository, "./cmd/spec-kitty")
	parent, err := os.MkdirTemp("/tmp", "spk-wp15-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "authority")
	runtimeRoot := filepath.Join(parent, "runtime")
	evidence := filepath.Join(repository, "docs", "programs", "evidence")
	environment := append(os.Environ(),
		"SPEC_KITTY_GAPDBD_PATH="+sidecar,
		"SPEC_KITTY_EVIDENCE_ROOT="+evidence,
		"SPEC_KITTY_RUNTIME_ROOT="+runtimeRoot,
	)
	initialize := exec.Command(binary, "--control-root", root, "initialize")
	initialize.Env = environment
	initializedBytes, initializeErr := initialize.Output()
	if initializeErr != nil {
		t.Fatalf("initialize: %v: %s", initializeErr, initializedBytes)
	}
	var initialized helpercmd.Envelope
	if err := json.Unmarshal(initializedBytes, &initialized); err != nil || initialized.Host == nil || initialized.RuntimeEpochID == "" {
		t.Fatalf("initialize envelope=%+v err=%v output=%s", initialized, err, initializedBytes)
	}
	var first helpercmd.Envelope
	for run := 0; run < 2; run++ {
		command := exec.Command(binary, "--control-root", root, "status")
		command.Env = environment
		output, commandErr := command.Output()
		if commandErr != nil {
			t.Fatalf("run=%d: %v: %s", run, commandErr, output)
		}
		var current helpercmd.Envelope
		if err := json.Unmarshal(output, &current); err != nil || current.Host == nil || current.Host.State != "ready" || current.DatabaseID == "" || current.RuntimeEpochID == "" {
			t.Fatalf("run=%d envelope=%+v err=%v output=%s", run, current, err, output)
		}
		if run == 0 {
			first = current
		} else if current.DatabaseID != first.DatabaseID || current.RuntimeEpochID != first.RuntimeEpochID || current.ProjectionRevision != first.ProjectionRevision || current.Host.ProjectionSHA256 != first.Host.ProjectionSHA256 {
			t.Fatalf("query mutated durable coordinate first=%+v second=%+v", first, current)
		}
	}
	if first.RuntimeEpochID != initialized.RuntimeEpochID || first.ProjectionRevision != initialized.ProjectionRevision {
		t.Fatalf("query changed initialization coordinate initialized=%+v queried=%+v", initialized, first)
	}
}

func TestRealGapDBLostAcknowledgementRestartResumeAndConflict(t *testing.T) {
	sidecar := os.Getenv("SPEC_KITTY_WP15_GAPDBD")
	if sidecar == "" {
		t.Skip("set SPEC_KITTY_WP15_GAPDBD to the exact admitted assertion-extension sidecar")
	}
	repository := repositoryRoot(t)
	binary := buildBinary(t, repository, "./cmd/spec-kitty")
	parent, err := os.MkdirTemp("/tmp", "spk-wp15-resume-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "authority")
	environment := append(os.Environ(),
		"SPEC_KITTY_GAPDBD_PATH="+sidecar,
		"SPEC_KITTY_EVIDENCE_ROOT="+filepath.Join(repository, "docs", "programs", "evidence"),
		"SPEC_KITTY_RUNTIME_ROOT="+filepath.Join(parent, "runtime"),
	)
	initialize := exec.Command(binary, "--control-root", root, "initialize")
	initialize.Env = environment
	initializedBytes, err := initialize.Output()
	if err != nil {
		t.Fatalf("initialize: %v: %s", err, initializedBytes)
	}
	var initialized helpercmd.Envelope
	if err := json.Unmarshal(initializedBytes, &initialized); err != nil || initialized.RuntimeEpochID == "" {
		t.Fatalf("initialize envelope=%+v err=%v output=%s", initialized, err, initializedBytes)
	}

	operationID := journalv2.OperationID("operation-live-lost-ack")
	definition := sha256.Sum256([]byte("live-lost-ack-definition"))
	digest, err := journalv2.ParseDigest("definition", fmt.Sprintf("%x", definition))
	if err != nil {
		t.Fatal(err)
	}
	state, err := mission.NewMissionAuthorityStateV2(mission.MissionAuthorityStateInputV2{
		ProjectID: "project-live", MissionID: "mission-live", DefinitionRevision: "definition-live", DefinitionDigest: digest,
		Version: 1, Status: mission.AuthorityMissionActiveV2, Step: "initialized", LastOperationID: operationID, ObservedAt: "2026-09-05T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	type missionWire struct {
		SchemaVersion    uint32                     `json:"schema_version"`
		OperationID      journalv2.OperationID      `json:"operation_id"`
		ProjectID        journalv2.ProjectID        `json:"project_id"`
		MissionID        journalv2.MissionID        `json:"mission_id"`
		ActorID          journalv2.ActorID          `json:"actor_id"`
		ProcessID        journalv2.ProcessID        `json:"process_id"`
		RuntimeEpochID   journalv2.RuntimeEpochID   `json:"runtime_epoch_id"`
		DoctrineRevision string                     `json:"doctrine_revision"`
		ProfileRevision  string                     `json:"profile_revision"`
		PolicyRevision   string                     `json:"policy_revision"`
		ObservedAt       string                     `json:"observed_at"`
		EventType        mission.MissionEventTypeV2 `json:"event_type"`
		Successor        json.RawMessage            `json:"successor"`
	}
	wire := missionWire{
		SchemaVersion: mission.MissionAuthoritySchemaVersionV2, OperationID: operationID, ProjectID: "project-live", MissionID: "mission-live",
		ActorID: "actor-live", ProcessID: "process-live", RuntimeEpochID: helpercmd.RuntimeEpochCurrent,
		DoctrineRevision: "doctrine-live", ProfileRevision: "profile-live", PolicyRevision: "policy-live",
		ObservedAt: "2026-09-05T12:00:00Z", EventType: mission.MissionEventCreatedV2, Successor: state.CanonicalBytes(),
	}
	requestFor := func(value missionWire) []byte {
		payload, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		request, marshalErr := json.Marshal(helpercmd.OperationRequest{SchemaVersion: 1, Action: string(mission.ActionMissionCreate), Scope: "mission:mission-live", Kind: helpercmd.OperationMission, Payload: payload})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return request
	}
	run := func(verb string, request []byte) ([]byte, error) {
		command := exec.Command(binary, "--control-root", root, "operation", verb, "--request", "-")
		command.Env = environment
		command.Stdin = bytes.NewReader(request)
		return command.Output()
	}
	wire.RuntimeEpochID = "epoch-forged-stale"
	staleBytes, staleErr := run("execute", requestFor(wire))
	var stale helpercmd.Envelope
	if staleErr == nil || json.Unmarshal(staleBytes, &stale) != nil || stale.Outcome != helpercmd.OutcomeUnavailable || stale.Diagnostic == nil || stale.Diagnostic.Kind != string(mission.MissionAuthorityStaleEpochV2) {
		t.Fatalf("absent stale epoch was normalized: err=%v envelope=%+v output=%s", staleErr, stale, staleBytes)
	}
	wire.RuntimeEpochID = helpercmd.RuntimeEpochCurrent

	// A response-drop proxy accepts the command's success bytes, then cuts the
	// transport before acknowledgement. Run therefore reports a transport
	// failure even though the native process durably accepted the operation.
	drop := &droppedAcknowledgementWriter{}
	lost := exec.Command(binary, "--control-root", root, "operation", "execute", "--request", "-")
	lost.Env = environment
	lost.Stdin = bytes.NewReader(requestFor(wire))
	lost.Stdout = drop
	var lostStderr bytes.Buffer
	lost.Stderr = &lostStderr
	lostErr := lost.Run()
	if lostErr == nil || !strings.Contains(lostErr.Error(), errAcknowledgementDropped.Error()) {
		t.Fatalf("response-drop proxy did not cut acknowledgement: err=%v stderr=%s accepted=%s", lostErr, lostStderr.String(), drop.accepted.Bytes())
	}
	lostAcknowledgement := drop.accepted.Bytes()
	var accepted helpercmd.Envelope
	if err := json.Unmarshal(lostAcknowledgement, &accepted); err != nil || accepted.Outcome != helpercmd.OutcomeSuccess || accepted.Result == nil {
		t.Fatalf("transport cut before durable acceptance evidence: envelope=%+v err=%v bytes=%s", accepted, err, lostAcknowledgement)
	}
	resumedBytes, err := run("resume", requestFor(wire))
	if err != nil {
		t.Fatalf("restart resume: %v: %s", err, resumedBytes)
	}
	var committed, resumed helpercmd.Envelope
	if err := json.Unmarshal(lostAcknowledgement, &committed); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(resumedBytes, &resumed); err != nil {
		t.Fatal(err)
	}
	if committed.Result == nil || resumed.Result == nil || committed.Result.Kind != helpercmd.ResultMission || resumed.Result.Kind != helpercmd.ResultMission || !bytes.Equal(committed.Result.Payload, resumed.Result.Payload) || committed.RequestSHA256 != resumed.RequestSHA256 {
		t.Fatalf("resume did not return exact committed result: committed=%+v resumed=%+v", committed, resumed)
	}

	// Supplying the concrete origin in place of the original symbolic selector
	// is still a different canonical request and must not collapse to a replay.
	wire.RuntimeEpochID = committed.RuntimeEpochID
	conflictBytes, conflictErr := run("resume", requestFor(wire))
	if conflictErr == nil {
		t.Fatalf("changed request unexpectedly succeeded: %s", conflictBytes)
	}
	var conflict helpercmd.Envelope
	if err := json.Unmarshal(conflictBytes, &conflict); err != nil || conflict.Outcome != helpercmd.OutcomeConflict || conflict.Diagnostic == nil || conflict.Diagnostic.Kind != string(mission.MissionAuthorityIdempotencyConflictV2) {
		t.Fatalf("changed request conflict envelope=%+v decode=%v output=%s", conflict, err, conflictBytes)
	}
}

var errAcknowledgementDropped = errors.New("test transport dropped acknowledgement")

type droppedAcknowledgementWriter struct {
	accepted bytes.Buffer
	dropped  bool
}

func (w *droppedAcknowledgementWriter) Write(value []byte) (int, error) {
	if w.dropped {
		return 0, errAcknowledgementDropped
	}
	w.dropped = true
	_, _ = w.accepted.Write(value)
	return len(value), errAcknowledgementDropped
}

func TestNativeBinaryDependencyGraphContainsNoSQLite(t *testing.T) {
	repository := repositoryRoot(t)
	command := exec.Command("go", "list", "-deps", "./cmd/spec-kitty")
	command.Dir = repository
	command.Env = append(os.Environ(), "GOTOOLCHAIN=go1.26.7", "CGO_ENABLED=0")
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bytes.ToLower(output), []byte("sqlite")) || bytes.Contains(output, []byte("database/sql")) {
		t.Fatalf("native binary dependency graph retained relational storage:\n%s", output)
	}
}

func buildBinary(t *testing.T, repository, target string) string {
	t.Helper()
	name := strings.TrimPrefix(filepath.Base(target), "./")
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	command := exec.Command("go", "build", "-trimpath", "-o", binary, target)
	command.Dir = repository
	command.Env = append(os.Environ(), "GOTOOLCHAIN=go1.26.7", "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v: %s", target, err, output)
	}
	return binary
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join(directory, "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func directoryFingerprint(t *testing.T, root string) string {
	t.Helper()
	var values []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		values = append(values, relative+":"+info.Mode().String()+":"+strconv.FormatInt(info.Size(), 10))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(values, "\n")
}

// TestCompiledTrackerPublicLifecycle exercises the product binary against only
// public hn commands in an isolated repository, with no mission installation.
func TestCompiledTrackerPublicLifecycle(t *testing.T) {
	hn := os.Getenv("GO_KITTY_TEST_HUBNOT_BINARY")
	if hn == "" {
		t.Skip("set GO_KITTY_TEST_HUBNOT_BINARY to the admitted executable")
	}
	binary := buildBinary(t, repositoryRoot(t), "./cmd/spec-kitty")
	workspace := t.TempDir()
	public := func(program string, args ...string) []byte {
		t.Helper()
		c := exec.Command(program, args...)
		c.Dir = workspace
		c.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8", "HOME=" + workspace}
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("public setup %v: %v %s", args, err, out)
		}
		return out
	}
	public("git", "init", "-q")
	public("git", "-c", "user.name=Consumer fixture", "-c", "user.email=consumer@test.invalid", "commit", "--allow-empty", "-q", "-m", "fixture")
	public(hn, "init", "--name", "Consumer fixture")
	var identity struct {
		Data struct {
			Actor string `json:"actor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(public(hn, "identity", "show", "--json"), &identity); err != nil {
		t.Fatal(err)
	}
	config := hubnot.BindingConfig{Schema: "go-kitty.hubnot-binding/1", Provider: "hubnot", ProjectID: "project", WorkspacePath: workspace, ExecutablePath: hn, ExpectedActor: identity.Data.Actor, Identity: hubnot.QualifiedProviderIdentity()}
	binding := filepath.Join(t.TempDir(), "binding.json")
	encoded, _ := json.Marshal(config)
	writeFixture(t, binding, encoded)
	control := filepath.Join(t.TempDir(), "must-not-exist")
	type resultEnvelope struct {
		Schema  string          `json:"schema"`
		Action  string          `json:"action"`
		Outcome string          `json:"outcome"`
		Result  json.RawMessage `json:"result"`
	}
	run := func(action string, data any, want int) map[string]any {
		t.Helper()
		args := []string{"--control-root", control, "tracker", action, "--binding", binding}
		var input []byte
		if data != nil {
			input, _ = json.Marshal(map[string]any{"schema": "go-kitty.tracker-request/1", "action": action, "data": data})
			args = append(args, "--input", "-")
		}
		c := exec.Command(binary, args...)
		c.Env = []string{"PATH=" + os.Getenv("PATH")}
		c.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		c.Stderr = &stderr
		out, err := c.Output()
		code := 0
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		if code != want {
			t.Fatalf("%s code=%d want=%d stderr=%s output=%s", action, code, want, stderr.Bytes(), out)
		}
		var env resultEnvelope
		if err = json.Unmarshal(out, &env); err != nil || env.Schema != "go-kitty.tracker-result/1" || env.Action != action {
			t.Fatalf("invalid envelope %s %v", out, err)
		}
		if _, err = os.Stat(control); !os.IsNotExist(err) {
			t.Fatal("tracker touched mission control root")
		}
		var result map[string]any
		if len(env.Result) > 0 {
			d := json.NewDecoder(bytes.NewReader(env.Result))
			d.UseNumber()
			if err = d.Decode(&result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	object := func(v any) map[string]any {
		t.Helper()
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("object missing: %T %v", v, v)
		}
		return m
	}
	refs := func() []byte { return public("git", "for-each-ref", "--format=%(refname) %(objectname)") }
	requireProof := func(v any, want string) {
		t.Helper()
		p := object(v)
		raw, err := base64.StdEncoding.DecodeString(p["canonical_fields_base64"].(string))
		sum := sha256.Sum256(raw)
		if err != nil || p["kind"] != want || p["digest"] != fmt.Sprintf("%x", sum) || p["provider_token"] == "" {
			t.Fatalf("proof incomplete: %v %v", p, err)
		}
	}
	before := refs()
	inspection := run("inspect", nil, 0)
	if object(inspection["capabilities"])["catalog_update"] != "unguarded" {
		t.Fatalf("capability overclaim: %v", inspection)
	}
	if !bytes.Equal(before, refs()) {
		t.Fatal("inspect wrote")
	}
	// Signal cancellation must terminate a command holding an incomplete real
	// stdin pipe; both reads and cleanup finish without provider dispatch.
	for _, signal := range []os.Signal{syscall.SIGINT, syscall.SIGTERM} {
		c := exec.Command(binary, "--control-root", control, "tracker", "create", "--binding", binding, "--input", "-")
		pipe, err := c.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		var output, diagnostics bytes.Buffer
		c.Stdout = &output
		c.Stderr = &diagnostics
		if err = c.Start(); err != nil {
			t.Fatal(err)
		}
		if _, err = pipe.Write([]byte(`{"schema":"go-kitty.tracker-request/1"`)); err != nil {
			_ = c.Process.Kill()
			_ = c.Wait()
			t.Fatal(err)
		}
		// The in-process pipe test deterministically witnesses entry into Read;
		// this compiled witness additionally checks installed signal handling.
		time.Sleep(100 * time.Millisecond)
		if err = c.Process.Signal(signal); err != nil {
			_ = c.Process.Kill()
			_ = c.Wait()
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- c.Wait() }()
		select {
		case err = <-done:
		case <-time.After(3 * time.Second):
			_ = c.Process.Kill()
			<-done
			_ = pipe.Close()
			t.Fatalf("%s left blocked input alive", signal)
		}
		_ = pipe.Close()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 130 || !bytes.Contains(output.Bytes(), []byte(`"outcome":"canceled"`)) || !bytes.Equal(before, refs()) {
			t.Fatalf("%s cancellation lost: %v %s %s", signal, err, output.Bytes(), diagnostics.Bytes())
		}
		if _, err = os.Stat(control); !os.IsNotExist(err) {
			t.Fatal("canceled tracker touched control root")
		}
	}
	// Named FIFOs are not file inputs: refuse before open/read can block.
	fifo := filepath.Join(t.TempDir(), "request-fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"tracker", "inspect", "--binding", fifo}, {"tracker", "create", "--binding", binding, "--input", fifo}} {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		c := exec.CommandContext(ctx, binary, args...)
		output, err := c.Output()
		cancel()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 || !bytes.Contains(output, []byte(`"outcome":"invalid"`)) || !bytes.Equal(before, refs()) {
			t.Fatalf("named FIFO was not bounded: %v %s", err, output)
		}
	}
	simple := map[string]any{"work_id": "simple", "operation_id": "create-simple", "title": "Simple", "provider_metadata_json": `{"number":9007199254740993}`}
	created := run("create", simple, 0)
	record := object(created["record"])
	ref := record["external_ref"]
	requireProof(record["proof"], "original-operation/1")
	before = refs()
	replayed := run("create", simple, 0)
	if replayed["replayed"] != true || !bytes.Equal(before, refs()) {
		t.Fatal("create replay wrote")
	}
	current := run("read", map[string]any{"external_ref": ref}, 0)
	requireProof(current["proof"], "known-work-observation/1")
	update := map[string]any{"external_ref": ref, "operation_id": "update-simple", "title": "Updated", "description": "New body", "design_context": "Context", "criteria": []any{}, "expected_observation": object(current["proof"])["revision"], "required_guard": "unguarded"}
	changed := run("update", update, 0)
	if object(changed["record"])["title"] != "Updated" {
		t.Fatal("update lost")
	}
	current = run("read", map[string]any{"external_ref": ref}, 0)
	assignment := map[string]any{"external_ref": ref, "operation_id": "assign-simple", "expected_observation": object(current["proof"])["revision"], "assignee": "claimed-person", "required_guarantee": "unguarded"}
	assigned := run("assign", assignment, 0)
	requireProof(object(assigned["observation"])["proof"], "original-operation/1")
	observedAssignment := run("assign", map[string]any{"external_ref": ref, "mode": "observe"}, 0)
	if object(observedAssignment["observation"])["operation_marker"] == "" {
		t.Fatal("assignment attribution missing")
	}
	for i := 0; i < 3; i++ {
		current = run("read", map[string]any{"external_ref": ref}, 0)
		body := fmt.Sprintf("Exact body %d\n雪", i) + strings.Repeat("x", 2048)
		comment := run("comment", map[string]any{"external_ref": ref, "operation_id": fmt.Sprintf("comment-%d", i), "expected_observation": object(current["proof"])["revision"], "author": "Accountable author", "body": body}, 0)
		entry := object(comment["entry"])
		if entry["signing_actor"] != identity.Data.Actor || entry["author"] != "Accountable author" || entry["body"] != body {
			t.Fatal("author/signer loss")
		}
		requireProof(entry["proof"], "original-operation/1")
	}
	discussion := run("discussion", map[string]any{"external_ref": ref}, 0)
	requireProof(discussion["common_proof"], "discussion-observation/1")
	requireProof(discussion["current_work_observation"], "known-work-observation/1")
	entries := discussion["entries"].([]any)
	if len(entries) != 3 {
		t.Fatal("discussion lost entries")
	}
	for _, entry := range entries {
		e := object(entry)
		if e["proof"] != nil || object(e["proof_ref"])["digest"] != object(discussion["common_proof"])["digest"] {
			t.Fatal("shared proof not resolved")
		}
	}
	// Hold real stdout backpressure after the first byte of a large current
	// observation. Cancellation must finish without writing a second envelope.
	for _, signal := range []os.Signal{syscall.SIGINT, syscall.SIGTERM} {
		outputReader, outputWriter, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := outputWriter.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var capacityErr syscall.Errno
		if err = raw.Control(func(fd uintptr) {
			_, _, capacityErr = syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_SETPIPE_SZ, 4096)
		}); err != nil || capacityErr != 0 {
			t.Fatalf("pipe capacity: %v %v", err, capacityErr)
		}
		request, _ := json.Marshal(map[string]any{"schema": "go-kitty.tracker-request/1", "action": "read", "data": map[string]any{"external_ref": ref}})
		c := exec.Command(binary, "--control-root", control, "tracker", "read", "--binding", binding, "--input", "-")
		c.Stdin = bytes.NewReader(request)
		c.Stdout = outputWriter
		var diagnostics bytes.Buffer
		c.Stderr = &diagnostics
		beforeOutput := refs()
		if err = c.Start(); err != nil {
			t.Fatal(err)
		}
		outputWriter.Close()
		_ = outputReader.SetReadDeadline(time.Now().Add(3 * time.Second))
		first := make([]byte, 1)
		if _, err = outputReader.Read(first); err != nil {
			_ = c.Process.Kill()
			_ = c.Wait()
			outputReader.Close()
			t.Fatalf("output did not begin: %v %s", err, diagnostics.Bytes())
		}
		if err = c.Process.Signal(signal); err != nil {
			_ = c.Process.Kill()
			_ = c.Wait()
			outputReader.Close()
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- c.Wait() }()
		select {
		case err = <-done:
		case <-time.After(3 * time.Second):
			_ = c.Process.Kill()
			<-done
			outputReader.Close()
			t.Fatalf("%s left stdout backpressure alive", signal)
		}
		rest, readErr := io.ReadAll(outputReader)
		outputReader.Close()
		var exit *exec.ExitError
		partial := append(first, rest...)
		if !errors.As(err, &exit) || exit.ExitCode() != 130 || readErr != nil || json.Valid(partial) || !bytes.Equal(beforeOutput, refs()) {
			t.Fatalf("stdout cancellation mismatch: %v bytes=%d read=%v", err, len(partial), readErr)
		}
		if _, err = os.Stat(control); !os.IsNotExist(err) {
			t.Fatal("output cancellation touched control root")
		}
	}
	define := func(work string) map[string]any {
		return map[string]any{"work_id": work, "operation_id": "define-" + work, "title": "Rich " + work, "description": "Full description", "design_context": "Context", "criteria": []any{map[string]any{"id": "z", "text": "Second lexical", "outcome": "Observable", "verifier": "test", "required": true}, map[string]any{"id": "a", "text": "First lexical", "outcome": "Observable", "verifier": "review", "required": false}}, "write_scopes": []any{map[string]any{"path": "internal/sample", "domain": "sample", "access": "write_exclusive"}}, "provenance": []any{map[string]any{"id": "FR001", "kind": "required_outcome"}}, "provider_metadata_json": `{"number":9007199254740993,"huge":1e400,"text":"雪"}`}
	}
	supplier := run("define", define("supplier"), 0)
	target := run("define", define("target"), 0)
	_ = supplier
	targetRef := object(target["observation"])["external_ref"]
	rich := run("definition", map[string]any{"external_ref": targetRef}, 0)
	definition := object(rich["definition"])
	if object(definition["criteria"].([]any)[0])["id"] != "z" || !strings.Contains(definition["provider_metadata_json"].(string), "9007199254740993") {
		t.Fatal("definition accessor fidelity lost")
	}
	relation := map[string]any{"operation_id": "link", "blocked_work_id": "target", "blocking_work_id": "supplier"}
	linked := run("link", relation, 0)
	edge := object(linked["dependency"])["external_edge_id"]
	if edge == "" {
		t.Fatal("edge identity missing")
	}
	deps := run("dependencies", map[string]any{"external_ref": targetRef}, 0)
	if deps["proof_scope"] != "portable_relations_only" || len(deps["items"].([]any)) != 1 {
		t.Fatal("dependency loss")
	}
	ready := run("ready", map[string]any{}, 0)
	if ready["authority"] != "diagnostic_only" {
		t.Fatal("ready overclaim")
	}
	relation["operation_id"] = "unlink"
	unlinked := run("unlink", relation, 0)
	if object(unlinked["dependency"])["external_edge_id"] != edge {
		t.Fatal("unstable edge identity")
	}
	// Complete inventory includes the two rich definitions; the simple catalog
	// record remains readable through its own port, without invented rich fields.
	before = refs()
	inventory := run("inventory", nil, 0)
	requireProof(inventory["common_proof"], "complete-current-inventory/1")
	if len(inventory["items"].([]any)) != 2 || !bytes.Equal(before, refs()) {
		t.Fatal("complete inventory lost values or wrote")
	}
	before = refs()
	bad := map[string]any{"external_ref": ref, "mode": "observe", "operation_id": "forbidden"}
	run("assign", bad, 2)
	if !bytes.Equal(before, refs()) {
		t.Fatal("invalid observe wrote")
	}
	current = run("read", map[string]any{"external_ref": ref}, 0)
	update["operation_id"] = "guarded"
	update["expected_observation"] = object(current["proof"])["revision"]
	update["required_guard"] = "guarded"
	run("update", update, 3)
	if !bytes.Equal(before, refs()) {
		t.Fatal("guard refusal wrote")
	}
	// Reject raw malformed Unicode/duplicates before any provider mutation.
	for _, input := range []string{
		`{"schema":"go-kitty.tracker-request/1","action":"create","data":{"work_id":"bad","operation_id":"bad","title":"\ud800"}}`,
		`{"schema":"go-kitty.tracker-request/1","action":"create","data":{"work_id":"bad","operation_id":"bad","title":"` + string([]byte{0xff}) + `"}}`,
		`{"schema":"go-kitty.tracker-request/1","action":"create","data":{"work_id":"bad","operation_id":"bad","title":"T","provider_metadata_json":"{\"x\":1,\"x\":2}"}}`,
	} {
		c := exec.Command(binary, "tracker", "create", "--binding", binding, "--input", "-")
		c.Stdin = strings.NewReader(input)
		out, err := c.Output()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 || !bytes.Contains(out, []byte(`"outcome":"invalid"`)) || !bytes.Equal(before, refs()) {
			t.Fatalf("malformed mutation was not refused: %v %s", err, out)
		}
	}
	config.ReadOnly = true
	encoded, _ = json.Marshal(config)
	writeFixture(t, binding, encoded)
	run("inspect", nil, 0)
	simple["work_id"] = "readonly"
	simple["operation_id"] = "readonly"
	run("create", simple, 3)
	if !bytes.Equal(before, refs()) {
		t.Fatal("read-only request wrote")
	}
	config.ReadOnly = false
	encoded, _ = json.Marshal(config)
	writeFixture(t, binding, encoded)
	helpOutput, err := exec.Command(binary, "tracker", "--help").Output()
	if err != nil {
		t.Fatal(err)
	}
	helpText := string(helpOutput)
	start := strings.Index(helpText, "  spec-kitty --output json tracker create --binding binding.json --input - > created.json")
	if start < 0 {
		t.Fatal("runnable example missing")
	}
	end := strings.Index(helpText[start:], "\n\nResults use")
	if end < 0 {
		t.Fatal("example boundary missing")
	}
	example := strings.ReplaceAll(helpText[start:start+end], "binding.json", binding)
	shell := exec.Command("bash", "-e", "-o", "pipefail", "-c", example)
	shell.Dir = workspace
	shell.Env = append(os.Environ(), "PATH="+filepath.Dir(binary)+":"+os.Getenv("PATH"))
	exampleOutput, err := shell.CombinedOutput()
	if err != nil {
		t.Fatalf("help example did not run: %v %s", err, exampleOutput)
	}
	if !bytes.Contains(exampleOutput, []byte(`"work_id":"example"`)) || !bytes.Contains(exampleOutput, []byte(`"kind":"known-work-observation/1"`)) {
		t.Fatalf("example did not read its returned reference: %s", exampleOutput)
	}
	// A native client can create a second signed opening with the same portable
	// mapping. The diagnostic ready port must preserve proof absence explicitly.
	var shown struct {
		Data struct {
			State json.RawMessage `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(public(hn, "issue", "show", object(targetRef)["external_id"].(string), "--json"), &shown); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(t.TempDir(), "duplicate-state.json")
	writeFixture(t, stateFile, shown.Data.State)
	public(hn, "issue", "open", "--json", "--actor", identity.Data.Actor, "--operation", "native-duplicate-mapping", "--input", stateFile)
	before = refs()
	unavailableReady := run("ready", map[string]any{}, 0)
	if unavailableReady["availability"] != "unavailable" || unavailableReady["proof"] != nil || len(unavailableReady["diagnostics"].([]any)) == 0 || !bytes.Equal(before, refs()) {
		t.Fatalf("unavailable graph proof fabricated: %v", unavailableReady)
	}
	config.Identity.SourceCommit = strings.Repeat("0", 40)
	encoded, _ = json.Marshal(config)
	writeFixture(t, binding, encoded)
	run("inspect", nil, 3)
	config.Identity = hubnot.QualifiedProviderIdentity()
	config.ExecutablePath = filepath.Join(workspace, "missing-hn")
	encoded, _ = json.Marshal(config)
	writeFixture(t, binding, encoded)
	run("inspect", nil, 2)
	if !bytes.Equal(before, refs()) {
		t.Fatal("binding refusal wrote")
	}
	for _, mode := range []string{"json", "human"} {
		c := exec.Command(binary, "--output="+mode, "tracker", "--help")
		help, err := c.CombinedOutput()
		if err != nil || !strings.Contains(string(help), "provider_metadata_json") || !strings.Contains(string(help), "original-operation") {
			t.Fatalf("help incomplete %v %s", err, help)
		}
	}
}
