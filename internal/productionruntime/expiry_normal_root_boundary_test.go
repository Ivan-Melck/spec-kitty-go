//go:build linux && amd64

package productionruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/controlroot"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// Actual newline-terminated R identity, independently pinned, not a capability.
const normalRootBoundaryRSHA = "53fa2099c5798c6ac7d05ca782deba422412605fc9a9a9696271823652138b57"

func normalRootBoundarySpecimen(t *testing.T, repository string) []byte {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(repository, "internal/sidecar/gapdb/expiry_qualification_input.go"), nil, 0)
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
	if len(raw) != 4691 || hex.EncodeToString(digest[:]) != normalRootBoundaryRSHA {
		t.Fatalf("source fixture is not exact R: bytes=%d sha256=%x", len(raw), digest)
	}
	return raw
}

// Pin the entire pre-provider prefix, including both fixed selections, the
// error return, unconditional admission overwrite, and dependency guard. Run
// BEFORE invoking roots. This is source evidence, not process/native evidence.
func normalRootBoundaryPinEntry(t *testing.T, directory string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(directory, "composition.go"))
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "composition.go", raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Start":                       "{\n\treturn start(ctx, config, false)\n}",
		"StartQualificationCandidate": "{\n\treturn start(ctx, config, true)\n}",
		"start": `{
	if ctx == nil {
		ctx = context.Background()
	}
	decision, err := preflightHostRoot(ctx, config)
	if err != nil {
		return nil, err
	}
	var extensionAdmission sidecargapdb.ExecutableAdmission
	if qualificationCandidate {
		extensionAdmission, err = sidecargapdb.VerifyM2QualificationCandidateAdmission(config.Sidecar.AdmissionRecordPath)
	} else {
		extensionAdmission, err = sidecargapdb.VerifyAssertionExtensionAdoption(config.AssertionExtensionAdoptionRecordPath, config.Sidecar.AdmissionRecordPath)
	}
	if err != nil {
		return nil, err
	}
	config.Sidecar.Admission = extensionAdmission
	if err := validateHostDependencies(config); err != nil {
		return nil, err
	}`,
	}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		expected, exists := want[fn.Name.Name]
		if !exists {
			continue
		}
		end := fn.Body.End()
		if fn.Name.Name == "start" {
			if len(fn.Body.List) < 9 {
				t.Fatal("ordinary start prefix disappeared")
			}
			end = fn.Body.List[7].End()
		}
		if string(raw[int(fn.Body.Pos())-1:int(end)-1]) != expected {
			t.Fatalf("%s pre-provider entry/order changed; refusing to run roots", fn.Name.Name)
		}
		delete(want, fn.Name.Name)
	}
	if len(want) != 0 {
		t.Fatal("missing ordinary root source", want)
	}
}

func normalRootBoundaryRefusal(t *testing.T, err error, operation, reason string) {
	t.Helper()
	refusal, ok := err.(*sidecargapdb.Error)
	if !ok || refusal == nil || refusal.Kind != sidecargapdb.FailureAdmissionMismatch || refusal.Operation != operation || refusal.Reason != reason || refusal.Cause != nil || err.Error() != "gapdb sidecar "+operation+": admission_mismatch reason="+reason {
		t.Fatalf("wrong fixed admission boundary: %T %v", err, err)
	}
}

// Pure entry-boundary fixtures: R is exact; dummy-image-NOT-TEST is NOT the
// authentic TEST image. No image/native/provider claim is made by these cases.
// No E-linked helper is used, so the same cases run in both build profiles.
func TestExpiryNormalRootBoundary(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate root source")
	}
	directory := filepath.Dir(source)
	repository := filepath.Clean(filepath.Join(directory, "../.."))
	normalRootBoundaryPinEntry(t, directory)
	raw := normalRootBoundarySpecimen(t, repository)
	for _, test := range []struct {
		name, operation, reason string
		candidate               bool
		start                   func(context.Context, Config) (*Host, error)
	}{
		{"Start", "verify_extension_adoption", "extension admission record differs", false, Start},
		{"StartQualificationCandidate", "verify_m2_candidate", "candidate record differs", true, StartQualificationCandidate},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			copyRecord := func(name string) string {
				t.Helper()
				data, err := os.ReadFile(filepath.Join(repository, "docs/programs/evidence", name))
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, name)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				return path
			}
			m1 := copyRecord("m1-adoption.json")
			extensionAdoption := copyRecord("m1-assertion-extension-adoption.json")
			oldExtension := copyRecord("m1-assertion-extension-admission.json")
			oldM2 := copyRecord("m2-gapdb-qualification-candidate.json")
			record := filepath.Join(root, "R.json")
			dummy := filepath.Join(root, "dummy-image-NOT-TEST")
			for path, data := range map[string][]byte{record: raw, dummy: []byte("not an executable\n")} {
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			admission, err := sidecargapdb.VerifyAssertionExtensionAdoption(extensionAdoption, oldExtension)
			if test.candidate {
				admission, err = sidecargapdb.VerifyM2QualificationCandidateAdmission(oldM2)
			}
			if err != nil || admission == (sidecargapdb.ExecutableAdmission{}) {
				t.Fatalf("legitimate nonzero caller admission prerequisite: %v", err)
			}
			config := Config{
				ControlRoot: filepath.Join(root, "authority"), HostAuthorityID: "host-entry-boundary", AuthorityBindingOperationID: "operation-entry-boundary",
				M1AdoptionRecordPath: m1, AssertionExtensionAdoptionRecordPath: extensionAdoption,
				Sidecar: sidecargapdb.LifecycleConfig{ExecutablePath: dummy, AdmissionRecordPath: record, Admission: admission,
					RuntimeRoot: filepath.Join(root, "runtime"), DataDirectory: filepath.Join(root, "authority"),
					SupervisorRunID: "supervisor-entry-boundary", ProductPolicySHA256: strings.Repeat("e", 64)},
			}
			// Intentionally no capability dependencies: an additional fail-safe
			// against launch, NOT the expected refusal. The pinned prefix places
			// their validation after the exact fixed-verifier error return.
			if _, err := sidecargapdb.VerifyAdoptionRecord(m1); err != nil {
				t.Fatal("positive M1 prerequisite", err)
			}
			decision, err := preflightHostRoot(t.Context(), config)
			if err != nil || decision.Action != controlroot.Initialize {
				t.Fatalf("positive fresh-root preflight: action=%s error=%v", decision.Action, err)
			}
			// Reject unexpected positive admission BEFORE calling either root.
			var rejected sidecargapdb.ExecutableAdmission
			if test.candidate {
				rejected, err = sidecargapdb.VerifyM2QualificationCandidateAdmission(record)
			} else {
				rejected, err = sidecargapdb.VerifyAssertionExtensionAdoption(extensionAdoption, record)
			}
			if rejected != (sidecargapdb.ExecutableAdmission{}) {
				t.Fatal("fixed verifier unexpectedly returned a capability; root NOT called")
			}
			normalRootBoundaryRefusal(t, err, test.operation, test.reason)
			before, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			host, err := test.start(t.Context(), config)
			if host != nil {
				t.Fatal("root returned a Host for R")
			}
			normalRootBoundaryRefusal(t, err, test.operation, test.reason)
			if config.Sidecar.Admission != admission {
				t.Fatal("root mutated caller admission")
			}
			for _, path := range []string{config.ControlRoot, config.Sidecar.RuntimeRoot} {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("refusal created root/runtime artifacts at %s: %v", path, err)
				}
			}
			after, readErr := os.ReadDir(root)
			if readErr != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("entry created artifacts (including sibling lease): %v", readErr)
			}
			t.Logf("R sha256=%s; positive M1/preflight; nonzero caller admission; nil Host; %v; nil cause; no root/runtime artifacts", normalRootBoundaryRSHA, err)
		})
	}
}
