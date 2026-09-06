package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/helpercmd"
	"github.com/spec-kitty/go-kitty3/internal/mission"
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
		ActorID: "actor-live", ProcessID: "process-live", RuntimeEpochID: initialized.RuntimeEpochID,
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
		t.Fatalf("response-drop proxy did not cut acknowledgement: err=%v stderr=%s", lostErr, lostStderr.String())
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

	wire.ActorID = "actor-conflicting"
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
