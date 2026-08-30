package gapdb

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
)

func TestAdmissionPinnedPublicModule(t *testing.T) {
	root := repositoryRoot(t)
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(module)
	want := AdmittedModulePath + " " + AdmittedModuleVersion
	if !strings.Contains(text, want) {
		t.Fatalf("go.mod does not pin %q", want)
	}
	if strings.Contains(text, "replace ") || strings.Contains(text, "replace(") {
		t.Fatal("go.mod contains a replace directive")
	}
	sums, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		AdmittedModulePath + " " + AdmittedModuleVersion + " h1:kmYoO7Q3PlsNM6M0dSYtuJJSQSlbflC2wz3d/Bek2L0=",
		AdmittedModulePath + " " + AdmittedModuleVersion + "/go.mod h1:8xNZ3k+SQIqcxLkS8bjbooOAv9xzZcbg5arjKaV6fcQ=",
	} {
		if !strings.Contains(string(sums), line) {
			t.Fatalf("go.sum does not contain admitted line %q", line)
		}
	}
	// Referencing an exported public constant makes this test binary type-check
	// the admitted package without importing any implementation package.
	if gapdbapi.DefaultMaxBatchOperations < MaxMutationsPerBatch {
		t.Fatal("resolved public API no longer satisfies the admitted batch budget")
	}
}

func TestAdmissionTupleAndBudgetContract(t *testing.T) {
	contractPath := filepath.Join(repositoryRoot(t), "kitty-specs", "gapdb-control-journal-proving-ground-01M18EVR", "contracts", "m1-product-budgets-v1.json")
	encoded, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		AdmittedTupleSHA256 string `json:"admitted_tuple_sha256"`
		AdmittedGapDB       struct {
			SourceRevision string `json:"source_revision"`
			SourceTree     string `json:"source_tree"`
			ModuleVersion  string `json:"module_version"`
			SidecarSHA256  string `json:"sidecar_sha256"`
			WireVersion    int    `json:"wire_version"`
			StorageVersion int    `json:"storage_version"`
		} `json:"admitted_gapdb"`
		Limits struct {
			KeyBytes             int `json:"key_bytes"`
			OrdinaryValueBytes   int `json:"ordinary_value_bytes"`
			CheckpointValueBytes int `json:"checkpoint_value_bytes"`
			EventsPerCommand     int `json:"events_per_command"`
			MutationsPerBatch    int `json:"mutations_per_batch"`
			RawBatchBytes        int `json:"raw_batch_bytes"`
			WireFrameBytes       int `json:"wire_frame_bytes"`
			ScanRecordsPerPage   int `json:"scan_records_per_page"`
			ScanBytesPerPage     int `json:"scan_bytes_per_page"`
		} `json:"limits"`
	}
	if err := json.Unmarshal(encoded, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.AdmittedTupleSHA256 != AdmittedTupleSHA256 ||
		contract.AdmittedGapDB.SourceRevision != AdmittedSourceRevision ||
		contract.AdmittedGapDB.SourceTree != AdmittedSourceTree ||
		contract.AdmittedGapDB.ModuleVersion != AdmittedModuleVersion ||
		contract.AdmittedGapDB.SidecarSHA256 != AdmittedSidecarSHA256 ||
		contract.AdmittedGapDB.WireVersion != AdmittedWireVersion ||
		contract.AdmittedGapDB.StorageVersion != AdmittedStorageVersion {
		t.Fatalf("admitted tuple drifted: %+v", contract.AdmittedGapDB)
	}
	limits := contract.Limits
	if limits.KeyBytes != MaxKeyBytes || limits.OrdinaryValueBytes != MaxOrdinaryValueBytes ||
		limits.CheckpointValueBytes != MaxCheckpointValueBytes || limits.EventsPerCommand != MaxEventsPerCommand ||
		limits.MutationsPerBatch != MaxMutationsPerBatch || limits.RawBatchBytes != MaxRawBatchBytes ||
		limits.WireFrameBytes != MaxWireFrameBytes || limits.ScanRecordsPerPage != MaxScanRecords ||
		limits.ScanBytesPerPage != MaxScanBytes {
		t.Fatalf("product budget drifted: %+v", limits)
	}
	provider := gapdbapi.DefaultOptions().Limits
	if provider.MaxKeyBytes < MaxKeyBytes || provider.MaxValueBytes < MaxCheckpointValueBytes ||
		provider.MaxBatchBytes < MaxRawBatchBytes || provider.MaxFrameBytes < MaxWireFrameBytes ||
		provider.MaxBatchOperations < MaxMutationsPerBatch || provider.MaxScanRecords < MaxScanRecords ||
		provider.MaxScanBytes < MaxScanBytes {
		t.Fatal("admitted provider defaults no longer satisfy product budgets")
	}
}

func TestAdmissionProductionSourceHasNoForbiddenProviderPath(t *testing.T) {
	directory := filepath.Join(repositoryRoot(t), "internal", "controlstore", "gapdb")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		encoded, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		source := string(encoded)
		for _, forbidden := range []string{
			AdmittedModulePath + "/internal/" + "server",
			"internal/controlstore/" + "sqlite",
			"exec." + "LookPath",
			"Current" + "BatchRevision",
		} {
			if strings.Contains(source, forbidden) {
				t.Fatalf("%s contains forbidden production surface %q", entry.Name(), forbidden)
			}
		}
	}
}

func TestAdmissionExternalFlagsAreStable(t *testing.T) {
	for _, name := range []string{"gapdbd", "data-root", "qualification"} {
		if flag.Lookup(name) == nil {
			t.Fatalf("documented external test flag -%s is absent", name)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
}
