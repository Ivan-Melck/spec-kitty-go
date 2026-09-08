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

func TestAssertionExtensionAdoptionChainsM1AndExactExecutable(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	adoptionPath := filepath.Join(root, "docs", "programs", "evidence", "m1-assertion-extension-adoption.json")
	admissionPath := filepath.Join(root, "docs", "programs", "evidence", "m1-assertion-extension-admission.json")
	admission, err := VerifyAssertionExtensionAdoption(adoptionPath, admissionPath)
	if err != nil {
		t.Fatal(err)
	}
	if admission.recordSHA256 != AssertionExtensionAdmissionSHA256 || admission.sidecarSHA256 == "" || admission.executableSize == 0 {
		t.Fatalf("incomplete extension admission: %+v", admission)
	}
	binary := os.Getenv("SPEC_KITTY_WP13_GAPDBD")
	if binary == "" {
		t.Skip("set SPEC_KITTY_WP13_GAPDBD to verify the exact assertion-extension executable")
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyExecutable(VerifyConfig{ExecutablePath: binary, AdmissionRecordPath: admissionPath, Admission: admission})
	if err != nil {
		t.Fatal(err)
	}
	defer verified.Close()
	provenance := verified.Provenance()
	if provenance.SHA256 != admission.sidecarSHA256 || provenance.SourceRevision != admission.sourceRevision || provenance.AdmissionRecordSHA256 != admission.recordSHA256 {
		t.Fatalf("extension executable provenance differs: %+v", provenance)
	}
}

func TestM2QualificationCandidateIsExactAndNotAnAdoption(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(root, "docs", "programs", "evidence", "m2-gapdb-qualification-candidate.json")
	admission, err := VerifyM2QualificationCandidateAdmission(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if admission.decision != "qualify" || admission.recordSHA256 != M2QualificationCandidateRecordSHA256 ||
		admission.sidecarSHA256 == "" || admission.executableSize == 0 {
		t.Fatalf("incomplete or over-promoted M2 candidate: %+v", admission)
	}
	if _, err := VerifyAssertionExtensionAdoption(recordPath, recordPath); !IsFailure(err, FailureAdmissionMismatch) {
		t.Fatalf("M2 candidate was accepted as the adopted M1 extension: %v", err)
	}

	binary := os.Getenv("SPEC_KITTY_M2_GAPDBD")
	if binary == "" {
		return
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyExecutable(VerifyConfig{ExecutablePath: binary, AdmissionRecordPath: recordPath, Admission: admission})
	if err != nil {
		t.Fatal(err)
	}
	defer verified.Close()
	provenance := verified.Provenance()
	if provenance.SHA256 != admission.sidecarSHA256 || provenance.SourceRevision != admission.sourceRevision || provenance.AdmissionRecordSHA256 != admission.recordSHA256 {
		t.Fatalf("M2 candidate executable provenance differs: %+v", provenance)
	}
}

func TestM2QualificationCandidateRejectsPromotionAndTupleMutation(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", "..", ".."))
	source := filepath.Join(root, "docs", "programs", "evidence", "m2-gapdb-qualification-candidate.json")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(map[string]any){
		"decision":      func(v map[string]any) { v["decision"] = "adopt" },
		"release_ready": func(v map[string]any) { v["release_ready"] = true },
		"adopted":       func(v map[string]any) { v["adopted"] = true },
		"source": func(v map[string]any) {
			v["gapdb_tuple"].(map[string]any)["source_revision"] = stringOf('0', 40)
		},
		"sidecar": func(v map[string]any) {
			v["gapdb_tuple"].(map[string]any)["sidecar"].(map[string]any)["sha256"] = stringOf('0', 64)
		},
		"unknown": func(v map[string]any) { v["operator_approval"] = true },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			mutate(value)
			encoded, _ := json.Marshal(value)
			path := filepath.Join(t.TempDir(), "candidate.json")
			if err := os.WriteFile(path, encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyM2QualificationCandidateAdmission(path); !IsFailure(err, FailureAdmissionMismatch) {
				t.Fatalf("mutated candidate admitted: %v", err)
			}
		})
	}
}

func TestAssertionExtensionAdoptionRejectsRecordMutation(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", "..", ".."))
	adoptionSource := filepath.Join(root, "docs", "programs", "evidence", "m1-assertion-extension-adoption.json")
	admissionSource := filepath.Join(root, "docs", "programs", "evidence", "m1-assertion-extension-admission.json")
	adoption, err := os.ReadFile(adoptionSource)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(adoption, &value); err != nil {
		t.Fatal(err)
	}
	value["sidecar_sha256"] = stringOf('0', 64)
	mutated, _ := json.Marshal(value)
	path := filepath.Join(t.TempDir(), "extension-adoption.json")
	if err := os.WriteFile(path, mutated, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAssertionExtensionAdoption(path, admissionSource); !IsFailure(err, FailureAdmissionMismatch) {
		t.Fatalf("mutated extension adoption admitted: %v", err)
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
