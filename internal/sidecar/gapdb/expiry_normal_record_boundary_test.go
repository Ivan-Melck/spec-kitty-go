//go:build linux && amd64

package gapdb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// This is the SHA of the actual newline-terminated R record, not an admission
// capability. Keep it independent of the E-selected constants/helpers.
const normalRecordBoundaryRSHA = "53fa2099c5798c6ac7d05ca782deba422412605fc9a9a9696271823652138b57"

func normalRecordBoundarySpecimen(t *testing.T, directory string) []byte {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, "expiry_qualification_input.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, spec := range group.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != "expiryInputCanonical" {
				continue
			}
			if raw != nil || len(value.Values) != 1 {
				t.Fatal("ambiguous R source specimen")
			}
			literal, ok := value.Values[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				t.Fatal("R specimen is not an exact string constant")
			}
			text, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			raw = []byte(text + "\n")
		}
	}
	digest := sha256.Sum256(raw)
	if len(raw) != 4691 || hex.EncodeToString(digest[:]) != normalRecordBoundaryRSHA || !json.Valid(raw) {
		t.Fatalf("source fixture is not exact R: bytes=%d sha256=%x", len(raw), digest)
	}
	return raw
}

// Provider-free entry-decoding evidence only: the dummy is NOT TEST or a native
// executable. Even a deliberately R-matching admission must hit the OLD closed
// schema before opening an image. This test is selected both with and without E.
func TestExpiryNormalRecordBoundary(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate fixture source")
	}
	directory := filepath.Dir(source)
	raw := normalRecordBoundarySpecimen(t, directory)
	historical := historicalM1ExecutableAdmission()
	old, err := os.ReadFile(filepath.Join(directory, "../../../docs/programs/evidence/m1-admission.json"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(old)
	var oldRecord admissionRecord
	if hex.EncodeToString(digest[:]) != historical.recordSHA256 || !singleStrictJSONValue(old, &oldRecord) || validateAdmission(oldRecord, historical) != nil {
		t.Fatal("historical constructor/record positive prerequisite differs")
	}
	var decoded admissionRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err == nil || err.Error() != `json: unknown field "host_qualification_complete"` || singleStrictJSONValue(raw, new(admissionRecord)) {
		t.Fatalf("R did not reach the closed OLD schema: %v", err)
	}
	matching := historical // Copy the legitimate local admission; change ONLY R's digest.
	matching.recordSHA256 = normalRecordBoundaryRSHA
	if matching == historical || matching == (ExecutableAdmission{}) || historical.recordSHA256 == normalRecordBoundaryRSHA {
		t.Fatal("R-matching admission control is vacuous")
	}
	for _, test := range []struct {
		name      string
		admission ExecutableAdmission
		reason    string
	}{
		{"zero_admission", ExecutableAdmission{}, "admission record digest differs"},
		{"historical_copy_with_exact_R_digest", matching, "admission record is malformed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			record := filepath.Join(root, "R.json")
			dummy := filepath.Join(root, "dummy-image-NOT-TEST")
			for path, content := range map[string][]byte{record: raw, dummy: []byte("not an executable\n")} {
				if err := os.WriteFile(path, content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			verified, err := VerifyExecutable(VerifyConfig{ExecutablePath: dummy, AdmissionRecordPath: record, Admission: test.admission})
			if verified != nil {
				closeErr := verified.Close()
				t.Fatalf("ordinary verifier returned a capability for R; first Close=%v", closeErr)
			}
			refusal, ok := err.(*Error)
			if !ok || refusal == nil || refusal.Kind != FailureAdmissionMismatch || refusal.Operation != "verify" || refusal.Reason != test.reason || refusal.Cause != nil || err.Error() != "gapdb sidecar verify: admission_mismatch reason="+test.reason {
				t.Fatalf("wrong OLD verifier boundary: %T %v", err, err)
			}
			t.Logf("R sha256=%s; nil VerifiedExecutable; %v; nil cause", normalRecordBoundaryRSHA, err)
		})
	}
}
