package external

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

func TestScenarioIDsAreExactOrderedAndImmutable(t *testing.T) {
	want := []string{
		"data/get-put",
		"conditional/cas-delete",
		"batch/atomic",
		"scan/sorted-prefix",
		"watch/registered-order",
		"expiry/release-authority",
		"ack/memory-visible",
		"ack/durable-restart",
		"inspect/authority",
		"authority/lease",
		"authority/review",
		"authority/workflow",
		"authority/integration",
		"response-loss/reconcile",
	}
	got := ScenarioIDs()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scenario order=%v", got)
	}
	got[0] = "changed"
	if ScenarioIDs()[0] != want[0] {
		t.Fatal("ScenarioIDs exposed mutable catalog storage")
	}
}

func TestRunWithRuntimeBindsOrderedTerminalArtifactsToUmbrellaRun(t *testing.T) {
	lifecycle := &sidecargapdb.Lifecycle{}
	fake := &fakeRuntime{revision: 100, lifecycle: lifecycle}
	result, err := runWithRuntime(t.Context(), "run-m1-external", fake)
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != "run-m1-external" || result.Lifecycle != lifecycle {
		t.Fatalf("result binding=%+v", result)
	}
	ids := ScenarioIDs()
	if len(result.Records) != len(ids) || len(result.Artifacts) != len(ids) || len(fake.calls) != len(ids) {
		t.Fatalf("census records=%d artifacts=%d calls=%d", len(result.Records), len(result.Artifacts), len(fake.calls))
	}
	for index, id := range ids {
		record := result.Records[index]
		if fake.calls[index] != (scenarioCall{runID: result.RunID, scenarioID: id, sequence: index + 1}) {
			t.Fatalf("call[%d]=%+v", index, fake.calls[index])
		}
		if record.RunID != result.RunID || record.ScenarioID != id || record.Sequence != index+1 || !record.Started || !record.Terminal || record.Outcome != OutcomePass {
			t.Fatalf("record[%d]=%+v", index, record)
		}
		if record.BeforeRevision != uint64(100+index) || record.AfterRevision != uint64(101+index) {
			t.Fatalf("record[%d] revision interval=%d..%d", index, record.BeforeRevision, record.AfterRevision)
		}
		digest := sha256.Sum256(result.Artifacts[index])
		if record.ArtifactSHA256 != hex.EncodeToString(digest[:]) {
			t.Fatalf("record[%d] artifact digest=%q", index, record.ArtifactSHA256)
		}
		var artifact scenarioArtifactV1
		if err := json.Unmarshal(result.Artifacts[index], &artifact); err != nil {
			t.Fatalf("artifact[%d]: %v", index, err)
		}
		if artifact.RunID != result.RunID || artifact.ScenarioID != id || artifact.Sequence != index+1 || artifact.BeforeRevision != record.BeforeRevision || artifact.AfterRevision != record.AfterRevision || artifact.Outcome != OutcomePass {
			t.Fatalf("artifact[%d]=%+v", index, artifact)
		}
	}
}

func TestRunWithRuntimeRejectsChildRunMismatch(t *testing.T) {
	fake := &fakeRuntime{revision: 7, lifecycle: &sidecargapdb.Lifecycle{}, foreignAt: 3}
	result, err := runWithRuntime(t.Context(), "run-umbrella", fake)
	if err == nil || !strings.Contains(err.Error(), "child run ID") {
		t.Fatalf("error=%v", err)
	}
	if len(result.Records) != 3 || len(result.Artifacts) != 3 {
		t.Fatalf("terminal prefix records=%d artifacts=%d", len(result.Records), len(result.Artifacts))
	}
	failed := result.Records[2]
	if failed.RunID != "run-umbrella" || failed.ScenarioID != ScenarioIDs()[2] || failed.Sequence != 3 || !failed.Started || !failed.Terminal || failed.Outcome != OutcomeFail {
		t.Fatalf("failed record=%+v", failed)
	}
	if result.Records[0].RunID != failed.RunID || result.Records[1].RunID != failed.RunID {
		t.Fatalf("records escaped umbrella binding: %+v", result.Records)
	}
}

func TestRunRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		runID     string
		lifecycle *sidecargapdb.Lifecycle
	}{
		{name: "nil-context", runID: "run-valid"},
		{name: "empty-run", ctx: t.Context()},
		{name: "invalid-run", ctx: t.Context(), runID: "run invalid"},
		{name: "absent-lifecycle", ctx: t.Context(), runID: "run-valid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Run(test.ctx, test.runID, test.lifecycle)
			if err == nil {
				t.Fatalf("result=%+v", result)
			}
			if len(result.Records) != 0 || len(result.Artifacts) != 0 {
				t.Fatalf("invalid input emitted child evidence: %+v", result)
			}
		})
	}
}

func TestRunAdmittedExternalLifecycle(t *testing.T) {
	binary := os.Getenv("SPEC_KITTY_M1_GAPDBD")
	dataRoot := os.Getenv("SPEC_KITTY_M1_DATA_ROOT")
	if binary == "" || dataRoot == "" {
		t.Skip("SPEC_KITTY_M1_GAPDBD and SPEC_KITTY_M1_DATA_ROOT are required")
	}
	root, err := os.MkdirTemp(dataRoot, "m1-external-runner-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(root, "runtime")
	dataDirectory := filepath.Join(root, "data", "database")
	for _, directory := range []string{runtimeRoot, filepath.Dir(dataDirectory)} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	lifecycle, err := sidecargapdb.Start(t.Context(), sidecargapdb.LifecycleConfig{
		ExecutablePath: binary, AdmissionRecordPath: externalAdmissionRecord(t),
		RuntimeRoot: runtimeRoot, DataDirectory: dataDirectory,
		SupervisorRunID: "run-external-integration-owner", ProductPolicySHA256: strings.Repeat("a", 64),
		StartTimeout: 5 * time.Second, StopTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	current := lifecycle
	t.Cleanup(func() {
		if current != nil {
			if err := current.Stop(context.Background()); err != nil {
				t.Errorf("stop external lifecycle: %v", err)
			}
		}
	})
	result, err := Run(t.Context(), "run-external-integration", lifecycle)
	current = result.Lifecycle
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Records) != 14 || result.Lifecycle == nil || result.Lifecycle == lifecycle {
		t.Fatalf("result census/lifecycle records=%d initial=%p current=%p", len(result.Records), lifecycle, result.Lifecycle)
	}
	for _, record := range result.Records {
		if record.Outcome != OutcomePass || !record.Started || !record.Terminal {
			t.Fatalf("non-pass external record=%+v", record)
		}
	}
}

type scenarioCall struct {
	runID      string
	scenarioID string
	sequence   int
}

type fakeRuntime struct {
	revision  uint64
	lifecycle *sidecargapdb.Lifecycle
	foreignAt int
	calls     []scenarioCall
}

func (f *fakeRuntime) currentRevision(context.Context) (uint64, error) { return f.revision, nil }

func (f *fakeRuntime) execute(_ context.Context, runID, scenarioID string, sequence int) (scenarioExecution, error) {
	f.calls = append(f.calls, scenarioCall{runID: runID, scenarioID: scenarioID, sequence: sequence})
	f.revision++
	if sequence == f.foreignAt {
		return scenarioExecution{RunID: "run-foreign"}, nil
	}
	return scenarioExecution{RunID: runID}, nil
}

func (f *fakeRuntime) currentLifecycle() *sidecargapdb.Lifecycle { return f.lifecycle }

func externalAdmissionRecord(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	path, err := filepath.Abs(filepath.Join(filepath.Dir(source), "../../../..", "docs/programs/evidence/m1-admission.json"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}
