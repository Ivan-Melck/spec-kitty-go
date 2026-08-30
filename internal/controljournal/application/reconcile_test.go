package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

func TestReconcileAbsentPartialCorruptAndWatchIndependent(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		port := newMemoryPort()
		service, _ := admittedService(t, port, "reconcile-absent")
		result, err := service.Reconcile(t.Context(), batchFixture(t, "reconcile-absent", 1, 1, 0), ReconcileConfigV1{Attempts: 2, Deadline: time.Second})
		if err != nil || result.Kind != OutcomeProvenAbsent || result.CommittedRevision != 0 {
			t.Fatalf("absent result=%+v err=%v", result, err)
		}
	})

	for _, mutation := range []struct {
		name string
		run  func(*memoryPort, ValidatedBatchV1)
	}{
		{"partial", func(port *memoryPort, batch ValidatedBatchV1) { delete(port.records, batch.product.Events[0].Key) }},
		{"corrupt", func(port *memoryPort, batch ValidatedBatchV1) {
			record := port.records[batch.product.Events[0].Key]
			record.Value = []byte(`{"corrupt":true}`)
			port.records[batch.product.Events[0].Key] = record
		}},
		{"mixed revision", func(port *memoryPort, batch ValidatedBatchV1) {
			record := port.records[batch.product.AggregateHeads[0].Key]
			record.Revision++
			port.records[batch.product.AggregateHeads[0].Key] = record
		}},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			suffix := "reconcile-" + strings.ReplaceAll(mutation.name, " ", "-")
			port := newMemoryPort()
			service, _ := admittedService(t, port, suffix)
			batch := batchFixture(t, suffix, 2, 1, 1)
			if result, err := service.Apply(t.Context(), batch); err != nil || result.Kind != OutcomeCommitted {
				t.Fatalf("apply result=%+v err=%v", result, err)
			}
			port.mu.Lock()
			mutation.run(port, batch)
			port.mu.Unlock()
			result, err := service.Reconcile(t.Context(), batch, ReconcileConfigV1{Attempts: 2, Deadline: time.Second})
			if err != nil || result.Kind != OutcomeIndeterminate || result.CommittedRevision != 0 || result.Recovery != RecoveryOperator {
				t.Fatalf("closed reconciliation result=%+v err=%v", result, err)
			}
		})
	}

	// memoryPort deliberately implements no Watch method. Compilation and this
	// successful proof establish that watch delivery cannot participate.
	port := newMemoryPort()
	service, _ := admittedService(t, port, "reconcile-no-watch")
	batch := batchFixture(t, "reconcile-no-watch", 1, 1, 0)
	if result, err := service.Apply(t.Context(), batch); err != nil || result.Kind != OutcomeCommitted {
		t.Fatalf("apply result=%+v err=%v", result, err)
	}
	if result, err := service.Reconcile(t.Context(), batch, ReconcileConfigV1{Attempts: 1, Deadline: time.Second}); err != nil || result.Kind != OutcomeAlreadyCommitted {
		t.Fatalf("watch-independent result=%+v err=%v", result, err)
	}
}

func TestReconcileBoundsAndUnavailableReads(t *testing.T) {
	port := newMemoryPort()
	service, _ := admittedService(t, port, "reconcile-bounds")
	batch := batchFixture(t, "reconcile-bounds", 1, 1, 0)
	for _, config := range []ReconcileConfigV1{{}, {Attempts: maxReconcileAttemptsV1 + 1, Deadline: time.Second}, {Attempts: 1, Deadline: -time.Second}} {
		if _, err := service.Reconcile(t.Context(), batch, config); outcomeOf(err) != OutcomeRefused {
			t.Fatalf("invalid bound survived: %+v err=%v", config, err)
		}
	}
	port.beforeBatch = nil
	// A read-only port wrapper makes every ordinary read unavailable and proves
	// the attempt bound terminates without mutation or watch fallback.
	unavailable := &unavailableReadPort{memoryPort: port}
	unavailableService, err := NewService(unavailable, service.admission)
	if err != nil {
		t.Fatal(err)
	}
	beforeCalls := port.batchCalls
	result, err := unavailableService.Reconcile(context.Background(), batch, ReconcileConfigV1{Attempts: 3, Deadline: time.Second})
	if err != nil || result.Kind != OutcomeIndeterminate || result.Recovery != RecoveryOperator || port.batchCalls != beforeCalls || unavailable.reads != 3 {
		t.Fatalf("bounded unavailable result=%+v reads=%d batches=%d err=%v", result, unavailable.reads, port.batchCalls-beforeCalls, err)
	}
}

type unavailableReadPort struct {
	*memoryPort
	reads int
}

func (p *unavailableReadPort) Get(context.Context, string) (storegapdb.Record, error) {
	p.reads++
	return storegapdb.Record{}, &storegapdb.Error{Kind: storegapdb.OutcomeUnavailable, Operation: "get", Recovery: storegapdb.RecoveryRead}
}

func TestReconcileSameIDDifferentDigestRefuses(t *testing.T) {
	port := newMemoryPort()
	service, _ := admittedService(t, port, "reconcile-collision")
	original := batchFixture(t, "reconcile-collision", 1, 1, 0)
	if result, err := service.Apply(t.Context(), original); err != nil || result.Kind != OutcomeCommitted {
		t.Fatalf("apply result=%+v err=%v", result, err)
	}
	changedInput := batchInputFixture(t, "reconcile-collision", 1, 1, 0)
	changed, err := ValidateCommandEnvelopeV1(CommandEnvelopeInputV1{
		SchemaVersion: 1, ProjectID: "project", MissionID: "mission", OperationID: "operation-reconcile-collision",
		CommandType: "mission.advance", RuntimeEpochID: "epoch-reconcile-collision", Payload: []byte(`{"different":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	changedInput.Command = changed
	changedBatch, err := NewValidatedBatchV1(changedInput)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Reconcile(t.Context(), changedBatch, ReconcileConfigV1{Attempts: 1, Deadline: time.Second})
	if err != nil || result.Kind != OutcomeOperationIdentityCollision || result.CommittedRevision != 0 {
		t.Fatalf("collision result=%+v err=%v", result, err)
	}
}

// TestReconcileRealGJ02ResponseDrop is the admitted, wire-level fault path. It
// never fabricates a timeout: WP04's proxy first consumes a complete durable
// daemon response, records GJ-02, and closes the product connection before
// forwarding any response byte.
func TestReconcileRealGJ02ResponseDrop(t *testing.T) {
	binary := admittedApplicationBinary(t)
	runtimeRoot := privateApplicationRoot(t)
	dataRoot := privateApplicationRoot(t)
	lifecycle, err := sidecargapdb.Start(t.Context(), sidecargapdb.LifecycleConfig{
		ExecutablePath: binary, AdmissionRecordPath: applicationAdmissionRecord(t),
		RuntimeRoot: runtimeRoot, DataDirectory: filepath.Join(dataRoot, "database"),
		SupervisorRunID: "run-application-gj02", ProductPolicySHA256: strings.Repeat("a", 64),
		StartTimeout: 5 * time.Second, StopTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lifecycle.Stop(context.Background()); err != nil {
			t.Errorf("stop admitted lifecycle: %v", err)
		}
	}()
	provenance := lifecycle.Provenance()
	directAdapter, directConnection, err := storegapdb.Dial(provenance.ConfiguredSocket, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer directConnection.Close()
	registry, err := controljournal.NewEventRegistryV1([]controljournal.EventVersionV1{{
		EventType: "mission.advance", EventSchemaVersion: 1, ReducerVersion: "mission-v1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := controljournal.ValidateJournalV1(registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.MarkProductRecovered(restored); err != nil {
		t.Fatal(err)
	}
	startupProof, err := lifecycle.BindOwnerStartupEpochV1("epoch-real-gj02")
	if err != nil {
		t.Fatal(err)
	}
	admission, result, err := AdmitRuntimeEpochV1(t.Context(), directAdapter, EpochAdmissionInputV1{
		ProjectID: "project", MissionID: "mission", OperationID: "admit-real-gj02", EventID: "epoch-event-real-gj02",
		StartupProof: startupProof,
	})
	if err != nil || result.Kind != OutcomeCommitted {
		t.Fatalf("real admission result=%+v err=%v", result, err)
	}

	proxyRoot := privateApplicationRoot(t)
	productSocket := filepath.Join(proxyRoot, "p.sock")
	proxy, err := sidecargapdb.StartResponseDropProxy(sidecargapdb.ProxyConfig{
		ProductSocket: productSocket, DaemonSocket: provenance.ConfiguredSocket,
		ExpectedDaemon: provenance.Process, ExpectedDatabaseID: provenance.DatabaseID,
		SelectedRequestID: "operation-real-gj02", SourceCommit: strings.Repeat("a", 40), ReadTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := proxy.Close(); err != nil {
			t.Errorf("close response-drop proxy: %v", err)
		}
	}()
	productAdapter, productConnection, err := storegapdb.Dial(productSocket, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer productConnection.Close()
	service, err := NewService(productAdapter, admission)
	if err != nil {
		t.Fatal(err)
	}
	batch := batchFixture(t, "real-gj02", 2, 1, 1)
	result, err = service.Apply(t.Context(), batch)
	if err != nil || result.Kind != OutcomeAmbiguousResponse || result.CommittedRevision != 0 {
		t.Fatalf("real lost response result=%+v err=%v", result, err)
	}
	wait, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	cut, err := proxy.WaitForCut(wait)
	if err != nil || cut.FaultID != "GJ-02" || cut.RequestID != "operation-real-gj02" || cut.Revision == 0 {
		t.Fatalf("real cut=%+v err=%v", cut, err)
	}
	result, err = service.Reconcile(t.Context(), batch, ReconcileConfigV1{Attempts: 4, Deadline: 2 * time.Second})
	if err != nil || result.Kind != OutcomeAlreadyCommitted || result.CommittedRevision != cut.Revision {
		t.Fatalf("real reconciliation result=%+v cut=%+v err=%v", result, cut, err)
	}
	assertUniversalApplicationInvariants(t, directAdapter, batch, true, cut.Revision)
	page, err := directAdapter.Scan(t.Context(), storegapdb.ScanRequest{Prefix: "spk/v1/project/mission/operations/", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	commandRecords := 0
	for _, record := range page.Records {
		if record.Key == batch.product.Operation.Key {
			commandRecords++
		}
	}
	if commandRecords != 1 || cut.MutationCount != len(productMutations(batch.product)) {
		t.Fatalf("GJ-02 publication count=%d cut mutations=%d want=%d", commandRecords, cut.MutationCount, len(productMutations(batch.product)))
	}
}

func admittedApplicationBinary(t *testing.T) string {
	t.Helper()
	source := os.Getenv("SPEC_KITTY_M1_GAPDBD")
	if source == "" {
		t.Skip("exact admitted gapdbd not supplied with SPEC_KITTY_M1_GAPDBD")
	}
	if !filepath.IsAbs(source) {
		t.Fatalf("admitted gapdbd path is not absolute: %q", source)
	}
	encoded, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	if int64(len(encoded)) != 5_300_621 || hex.EncodeToString(digest[:]) != storegapdb.AdmittedSidecarSHA256 {
		t.Fatal("supplied gapdbd is not the admitted artifact")
	}
	directory := privateApplicationRoot(t)
	target := filepath.Join(directory, "gapdbd-62219cf")
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	return target
}

func privateApplicationRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "spk-app-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func applicationAdmissionRecord(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "docs", "programs", "evidence", "m1-admission.json"))
}
