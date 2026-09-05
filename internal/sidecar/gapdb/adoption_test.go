package gapdb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProductionAdoptionRecordExactCoordinatesAdmit(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("..", "..", "..", "docs", "programs", "evidence", "m1-adoption.json"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := VerifyAdoptionRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if record.SourceCommit != AdoptedM1SourceCommit || record.SourceTree != AdoptedM1SourceTree || record.EvidenceManifestSHA256 != AdoptedEvidenceManifest || record.Decision != "adopt" {
		t.Fatalf("wrong adoption record: %+v", record)
	}
}

func TestProductionAdoptionRecordRejectsEveryAuthorityCoordinate(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("..", "..", "..", "docs", "programs", "evidence", "m1-adoption.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var baseline map[string]any
	if json.Unmarshal(raw, &baseline) != nil {
		t.Fatal("fixture is malformed")
	}
	tests := map[string]func(map[string]any){
		"schema":          func(v map[string]any) { v["schema_version"] = float64(2) },
		"program":         func(v map[string]any) { v["program_id"] = "other" },
		"mission":         func(v map[string]any) { v["mission_id"] = "M2" },
		"decision":        func(v map[string]any) { v["decision"] = "pending" },
		"manifest":        func(v map[string]any) { v["evidence_manifest_sha256"] = stringOf('0', 64) },
		"tuple":           func(v map[string]any) { v["admitted_tuple_sha256"] = stringOf('0', 64) },
		"commit":          func(v map[string]any) { v["source_commit"] = stringOf('0', 40) },
		"tree":            func(v map[string]any) { v["source_tree"] = stringOf('0', 40) },
		"sidecar":         func(v map[string]any) { v["sidecar_sha256"] = stringOf('0', 64) },
		"signer":          func(v map[string]any) { v["signer"] = "github:someone-else" },
		"rationale":       func(v map[string]any) { v["rationale"] = "different decision" },
		"signed_at":       func(v map[string]any) { v["signed_at"] = "2026-09-02T10:46:42Z" },
		"signature_kind":  func(v map[string]any) { v["signature"].(map[string]any)["kind"] = "detached" },
		"signature_value": func(v map[string]any) { v["signature"].(map[string]any)["value"] = "another-reference" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			var value map[string]any
			_ = json.Unmarshal(raw, &value)
			mutate(value)
			encoded, _ := json.Marshal(value)
			path := filepath.Join(t.TempDir(), "adoption.json")
			if err := os.WriteFile(path, encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyAdoptionRecord(path); !IsFailure(err, FailureAdmissionMismatch) {
				t.Fatalf("coordinate admitted: %v", err)
			}
		})
	}
	_ = baseline
}

func TestProductionAdoptionRecordRejectsSymlinkAndUnknownField(t *testing.T) {
	source, _ := filepath.Abs(filepath.Join("..", "..", "..", "docs", "programs", "evidence", "m1-adoption.json"))
	link := filepath.Join(t.TempDir(), "adoption.json")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAdoptionRecord(link); !IsFailure(err, FailureUnsafePath) {
		t.Fatalf("symlink admitted: %v", err)
	}
	raw, _ := os.ReadFile(source)
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	value["provider_transcript"] = "must not be admitted"
	encoded, _ := json.Marshal(value)
	path := filepath.Join(t.TempDir(), "unknown.json")
	_ = os.WriteFile(path, encoded, 0o600)
	if _, err := VerifyAdoptionRecord(path); !IsFailure(err, FailureAdmissionMismatch) {
		t.Fatalf("unknown field admitted: %v", err)
	}
}

func TestProductionExecutableRequiresAbsolutePath(t *testing.T) {
	admission, err := filepath.Abs(filepath.Join("..", "..", "..", "docs", "programs", "evidence", "m1-admission.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyExecutable(VerifyConfig{ExecutablePath: "gapdbd", AdmissionRecordPath: admission}); !IsFailure(err, FailureInvalidConfiguration) {
		t.Fatalf("relative executable did not fail configuration admission: %v", err)
	}
}

func stringOf(value byte, count int) string {
	result := make([]byte, count)
	for i := range result {
		result[i] = value
	}
	return string(result)
}
