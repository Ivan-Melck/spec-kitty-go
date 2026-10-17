//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"go/ast"
	"go/format"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/controlroot"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// PREPARED entry controls, not execution/qualification receipts. Independent
// actual-source review must precede every opted-in invocation. These use only
// the four existing expiryNativeConfig opt-ins and preserve every new artifact.
//
// Actual Start does NOT sanitize these failures into ProgressError: it returns
// the sidecar Error unchanged. Moreover, the qualification input cannot pass
// VerifyAssertionExtensionAdoption. Test that refusal honestly, then separately
// reach ordinary VerifyExecutable with the genuine adopted admission and TEST
// image, and test the actual input directly at VerifyExecutable. No admission
// factory, record substitution race, or product-policy relaxation is introduced.
func expiryEntryConfig(t *testing.T) Config {
	t.Helper()
	config := expiryNativeConfig(t, &expiryActualACKObserver{})
	config.AssertionExtensionAdoptionRecordPath = filepath.Join(filepath.Dir(config.M1AdoptionRecordPath), "m1-assertion-extension-adoption.json")
	// Pin actual supplied artifacts before any entry call. A missing/wrong input
	// or executable is a fixture error, never the intended refusal witness.
	for _, artifact := range []struct {
		path, sha string
		bytes     int64
	}{
		{config.Sidecar.AdmissionRecordPath, expiryInputSHA, 4691},
		{config.Sidecar.ExecutablePath, expiryExecutableSHA, 5491681},
	} {
		info, err := os.Lstat(artifact.path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != artifact.bytes {
			t.Fatalf("fixed entry artifact metadata: %v", err)
		}
		file, err := os.Open(artifact.path)
		if err != nil {
			t.Fatal(err)
		}
		held, statErr := file.Stat()
		hash := sha256.New()
		n, readErr := io.Copy(hash, io.LimitReader(file, artifact.bytes+1))
		closeErr := file.Close()
		if statErr != nil || !os.SameFile(info, held) || readErr != nil || closeErr != nil || n != artifact.bytes || hex.EncodeToString(hash.Sum(nil)) != artifact.sha {
			t.Fatalf("fixed entry artifact identity/digest: stat=%v read=%v close=%v bytes=%d", statErr, readErr, closeErr, n)
		}
	}
	decision, err := preflightHostRoot(t.Context(), config)
	if err != nil || decision.Action != controlroot.Initialize {
		t.Fatalf("valid fresh entry preflight: action=%s error=%v", decision.Action, err)
	}
	if err := validateHostDependencies(config); err != nil {
		t.Fatal(err)
	}
	t.Log("read-only preflightHostRoot succeeded: initialize; non-nil capability dependencies validated")
	return config
}

// Check only our newly allocated paths, never any historical authority root.
func expiryEntryNoLaunchArtifacts(t *testing.T, config Config) {
	t.Helper()
	entries, err := os.ReadDir(config.Sidecar.RuntimeRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("entry refusal created runtime artifacts: entries=%d error=%v", len(entries), err)
	}
	lease := filepath.Join(filepath.Dir(config.ControlRoot), "."+filepath.Base(config.ControlRoot)+expiryLeaseSuffix)
	if _, err := os.Lstat(lease); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("entry refusal reached lease acquisition: %v", err)
	}
}

func TestExpiryHostEntryPublicStartRejectsActualQualificationRecord(t *testing.T) {
	config := expiryEntryConfig(t)
	ordinaryRecord := filepath.Join(filepath.Dir(config.M1AdoptionRecordPath), "m1-assertion-extension-admission.json")
	admission, err := sidecargapdb.VerifyAssertionExtensionAdoption(config.AssertionExtensionAdoptionRecordPath, ordinaryRecord)
	if err != nil {
		t.Fatal("genuine ordinary adoption prerequisite", err)
	}
	if admission == (sidecargapdb.ExecutableAdmission{}) {
		t.Fatal("ordinary positive pairing returned an empty capability")
	}
	t.Log("valid ordinary adoption/OLD admission pairing succeeded")
	newAdmission, pairErr := sidecargapdb.VerifyAssertionExtensionAdoption(config.AssertionExtensionAdoptionRecordPath, config.Sidecar.AdmissionRecordPath)
	pairRefusal, pairOK := pairErr.(*sidecargapdb.Error)
	if newAdmission != (sidecargapdb.ExecutableAdmission{}) || !pairOK || pairRefusal == nil || pairRefusal.Kind != sidecargapdb.FailureAdmissionMismatch || pairRefusal.Operation != "verify_extension_adoption" || pairRefusal.Reason != "extension admission record differs" || pairRefusal.Cause != nil || pairErr.Error() != "gapdb sidecar verify_extension_adoption: admission_mismatch reason=extension admission record differs" {
		t.Fatalf("actual new-pair refusal: %T %v", pairErr, pairErr)
	}
	// Caller capability is genuine/comparable, but cannot authorize a different
	// record. The source control proves the later unconditional overwrite.
	config.Sidecar.Admission = admission
	host, err := Start(t.Context(), config)
	if config.Sidecar.Admission != admission {
		t.Fatal("Start mutated the caller's scalar config")
	}
	refusal, ok := err.(*sidecargapdb.Error)
	if host != nil || !ok || refusal == nil || refusal.Kind != sidecargapdb.FailureAdmissionMismatch || refusal.Operation != "verify_extension_adoption" || refusal.Reason != "extension admission record differs" || refusal.Cause != nil || err.Error() != "gapdb sidecar verify_extension_adoption: admission_mismatch reason=extension admission record differs" {
		t.Fatalf("actual public record refusal (NOT executable-verifier evidence): host=%p error=%T %v", host, err, err)
	}
	if _, err := os.Lstat(config.ControlRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("public record refusal created authority: %v", err)
	}
	expiryEntryNoLaunchArtifacts(t, config)
	t.Logf("actual public qualification-record refusal: %T %v; ordinary VerifyExecutable NOT reached", err, err)
}

func TestExpiryHostEntryOrdinaryVerifierRejectsActualQualificationRecord(t *testing.T) {
	config := expiryEntryConfig(t)
	ordinaryRecord := filepath.Join(filepath.Dir(config.M1AdoptionRecordPath), "m1-assertion-extension-admission.json")
	admission, err := sidecargapdb.VerifyAssertionExtensionAdoption(config.AssertionExtensionAdoptionRecordPath, ordinaryRecord)
	if err != nil {
		t.Fatal("genuine ordinary adoption prerequisite", err)
	}
	verified, err := sidecargapdb.VerifyExecutable(sidecargapdb.VerifyConfig{
		ExecutablePath: config.Sidecar.ExecutablePath, AdmissionRecordPath: config.Sidecar.AdmissionRecordPath, Admission: admission,
	})
	if verified != nil {
		if closeErr := verified.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal("ordinary verifier accepted qualification input")
	}
	refusal, ok := err.(*sidecargapdb.Error)
	if !ok || refusal == nil || refusal.Kind != sidecargapdb.FailureAdmissionMismatch || refusal.Operation != "verify" || refusal.Reason != "admission record digest differs" || refusal.Cause != nil || err.Error() != "gapdb sidecar verify: admission_mismatch reason=admission record digest differs" {
		t.Fatalf("actual ordinary verifier input refusal: %T %v", err, err)
	}
	expiryEntryNoLaunchArtifacts(t, config)
	t.Logf("actual ordinary VerifyExecutable qualification-input refusal: %T %v", err, err)
}

func TestExpiryHostEntryPublicStartReachesOrdinaryExecutableRefusal(t *testing.T) {
	config := expiryEntryConfig(t)
	config.Sidecar.AdmissionRecordPath = filepath.Join(filepath.Dir(config.M1AdoptionRecordPath), "m1-assertion-extension-admission.json")
	admission, err := sidecargapdb.VerifyAssertionExtensionAdoption(config.AssertionExtensionAdoptionRecordPath, config.Sidecar.AdmissionRecordPath)
	if err != nil {
		t.Fatal("ordinary adoption must pass BEFORE public executable-refusal experiment", err)
	}
	config.Sidecar.Admission = admission
	host, err := Start(t.Context(), config)
	refusal, ok := err.(*sidecargapdb.Error)
	// Fixed TEST is 5,491,681 bytes; adopted assertion image is 5,327,126.
	// Its actual metadata refusal precedes hashing/exec, NOT record rejection.
	if host != nil || !ok || refusal == nil || refusal.Kind != sidecargapdb.FailureUnsafePath || refusal.Operation != "verify" || refusal.Reason != "executable metadata is not admitted" || refusal.Cause == nil || err.Error() != "gapdb sidecar verify: unsafe_path reason=executable metadata is not admitted" {
		t.Fatalf("ordinary VerifyExecutable was not the actual public refusal: host=%p error=%T %v", host, err, err)
	}
	if _, err := os.Lstat(config.ControlRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("public executable refusal created authority: %v", err)
	}
	expiryEntryNoLaunchArtifacts(t, config)
	t.Logf("actual public ordinary VerifyExecutable TEST-image refusal: %T %v", err, err)
}

func TestExpiryHostEntryExistingEmptyRootRefusesBeforeProvider(t *testing.T) {
	config := expiryEntryConfig(t)
	if err := os.Mkdir(config.ControlRoot, 0700); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(config.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	host, run, err := startExpiryHostQualification(t.Context(), config)
	refusal, ok := err.(*Error)
	if host != nil || run != nil || !ok || refusal == nil || *refusal != (Error{Kind: ErrorStartup, State: HostRecovering, Field: "expiry_root_exists"}) || err.Error() != "production runtime: kind=STARTUP_REFUSED state=recovering field=expiry_root_exists" {
		t.Fatalf("actual existing-empty entry refusal: host=%p run=%p error=%T %v", host, run, err, err)
	}
	after, statErr := os.Lstat(config.ControlRoot)
	entries, readErr := os.ReadDir(config.ControlRoot)
	if statErr != nil || !os.SameFile(before, after) || readErr != nil || len(entries) != 0 {
		t.Fatalf("new existing root changed: stat=%v read=%v entries=%d", statErr, readErr, len(entries))
	}
	expiryEntryNoLaunchArtifacts(t, config)
	t.Logf("actual tagged existing-empty entry refusal: %T %v; nil Host/run; root preserved", err, err)
}

func TestExpiryHostEntryControlRootSymlinkRefusesBeforeProvider(t *testing.T) {
	config := expiryEntryConfig(t)
	// Allocate a known NEW external target, not an existing/historical root.
	external, err := os.MkdirTemp(filepath.Dir(filepath.Dir(config.ControlRoot)), "expiry-entry-external-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("preserved new external symlink target: %s", external)
	marker := filepath.Join(external, "entry-control-marker")
	if err := os.WriteFile(marker, []byte("preserve-new-entry-target\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, config.ControlRoot); err != nil {
		t.Fatal(err)
	}
	host, run, err := startExpiryHostQualification(t.Context(), config)
	refusal, ok := err.(*controlroot.OfflineDispositionError)
	if host != nil || run != nil || !ok || refusal == nil || refusal.Code != controlroot.OfflineDispositionRequiredCode || refusal.Disposition.Classification != controlroot.Unsafe || refusal.Disposition.Action != controlroot.OfflineDispositionRequired || err.Error() != "OFFLINE_DISPOSITION_REQUIRED: UNSAFE_CONTROL_ROOT" {
		t.Fatalf("actual symlink entry refusal: host=%p run=%p error=%T %v", host, run, err, err)
	}
	rootSymlink := false
	for _, finding := range refusal.Disposition.Findings {
		rootSymlink = rootSymlink || finding.DetailCode == "ROOT_SYMLINK"
	}
	link, linkErr := os.Readlink(config.ControlRoot)
	after, statErr := os.Lstat(marker)
	value, readErr := os.ReadFile(marker) // only our new target, never historical data
	if !rootSymlink || linkErr != nil || link != external || statErr != nil || !os.SameFile(before, after) || readErr != nil || string(value) != "preserve-new-entry-target\n" {
		t.Fatal("symlink/new external target not preserved or refusal not ROOT_SYMLINK")
	}
	expiryEntryNoLaunchArtifacts(t, config)
	t.Logf("actual tagged symlink entry refusal: %T %v; nil Host/run; new target preserved", err, err)
}

// Provider-free source evidence only. In particular, a genuine caller Admission
// cannot distinguish overwrite from reuse dynamically: it has the same value as
// the adoption-selected admission. Check the actual unconditional assignment.
func TestExpiryHostEntrySourceAdmissionOverwriteAndVerifierReachability(t *testing.T) {
	public, source := expirySourceFunction(t, "composition.go", "Start")
	publicBody := source[int(public.Body.Pos())-1 : int(public.Body.End())-1]
	if strings.TrimSpace(publicBody) != "{\n\treturn start(ctx, config, false)\n}" {
		t.Fatal("public Start no longer delegates to ordinary root")
	}
	fn, source := expirySourceFunction(t, "composition.go", "start")
	body := source[int(fn.Body.Pos())-1 : int(fn.Body.End())-1]
	previous := -1
	for _, required := range []string{
		"preflightHostRoot(ctx, config)",
		"sidecargapdb.VerifyAssertionExtensionAdoption(config.AssertionExtensionAdoptionRecordPath, config.Sidecar.AdmissionRecordPath)",
		"config.Sidecar.Admission = extensionAdmission",
		"validateHostDependencies(config)",
		"sidecargapdb.Start(ctx, config.Sidecar)",
	} {
		position := strings.Index(body, required)
		if position <= previous {
			t.Fatal("ordinary entry ordering/reachability changed", required)
		}
		previous = position
	}
	overwrites := 0
	for _, statement := range fn.Body.List {
		assignment, ok := statement.(*ast.AssignStmt)
		if !ok || assignment.Tok != token.ASSIGN || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
			continue
		}
		var lhs, rhs bytes.Buffer
		if err := format.Node(&lhs, token.NewFileSet(), assignment.Lhs[0]); err != nil {
			t.Fatal(err)
		}
		if err := format.Node(&rhs, token.NewFileSet(), assignment.Rhs[0]); err != nil {
			t.Fatal(err)
		}
		if lhs.String() == "config.Sidecar.Admission" {
			overwrites++
			if rhs.String() != "extensionAdmission" {
				t.Fatal("caller Admission is not overwritten by verified adoption")
			}
		}
	}
	if overwrites != 1 || strings.Contains(body, "ProgressError") {
		t.Fatal("ordinary unconditional overwrite/error surface changed")
	}
	fn, source = expirySourceFunction(t, "../sidecar/gapdb/lifecycle.go", "Start")
	body = source[int(fn.Body.Pos())-1 : int(fn.Body.End())-1]
	previous = -1
	for _, required := range []string{"normalizeLifecycleConfig(&config)", "VerifyExecutable(VerifyConfig{", "Admission: config.Admission", "if err != nil {\n\t\treturn nil, err\n\t}", "return startVerified(ctx, config, verified)"} {
		// Search the suffix, not an earlier normalize error-return block.
		position := strings.Index(body[previous+1:], required)
		if position < 0 {
			t.Fatal("ordinary verifier-before-launch/error propagation changed", required)
		}
		previous += position + 1
	}
}

func TestExpiryHostEntrySourceFreshnessBeforeAnyProviderCall(t *testing.T) {
	fn, source := expirySourceFunction(t, "expiry_qualification.go", "startExpiryHostQualification")
	body := source[int(fn.Body.Pos())-1 : int(fn.Body.End())-1]
	previous := -1
	for _, required := range []string{
		"preflightHostRoot(ctx, config)", "validateHostDependencies(config)",
		"if decision.Action != controlroot.Initialize {\n\t\treturn nil, nil, expiryFailure(\"expiry_root_not_fresh\")\n\t}",
		"if _, err := os.Lstat(config.ControlRoot); !errors.Is(err, os.ErrNotExist) {\n\t\treturn nil, nil, expiryFailure(\"expiry_root_exists\")\n\t}",
		"reserveExpiryHostLease(ctx, config)", "return lease.startGeneration(lease.control, ctx, config.Sidecar.SupervisorRunID)",
	} {
		position := strings.Index(body, required)
		if position <= previous {
			t.Fatal("freshness must refuse with nil handles BEFORE lease/provider", required)
		}
		previous = position
	}
	var calls []string
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if _, closure := call.Fun.(*ast.FuncLit); closure {
			calls = append(calls, "rollback closure")
			return true // include the calls inside the deferred closure too
		}
		var name bytes.Buffer
		if err := format.Node(&name, token.NewFileSet(), call.Fun); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, name.String())
		return true
	})
	// Closed actual entry-call census: no hidden provider call before the
	// existing-root guards. Rollback is deferred; generation is last.
	want := []string{"context.Background", "preflightHostRoot", "validateHostDependencies", "expiryFailure", "os.Lstat", "errors.Is", "expiryFailure", "reserveExpiryHostLease", "rollback closure", "lease.rollbackControl", "preflightHostRoot", "expiryFailure", "lease.startGeneration"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("entry gained/moved calls: %v", calls)
	}
}
