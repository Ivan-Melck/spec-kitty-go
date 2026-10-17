//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Prepared prelaunch controls, not a Host/expiry execution receipt. The existing
// source-review opt-in authorizes running this test; it is not report capability
// or an input identity. No command, gate pair, lifecycle or daemon is created.
func TestExpiryAuthenticBundleImageAndArtifactRefusals(t *testing.T) {
	if !expiryLowerSHA256(os.Getenv("SPEC_KITTY_EXPIRY_HOST_SOURCE_REVIEW_SHA256")) {
		t.Skip("requires independent actual-source review; authentic controls NOT executed")
	}
	image := os.Getenv("SPEC_KITTY_EXPIRY_HOST_TEST_IMAGE")
	input := os.Getenv("SPEC_KITTY_EXPIRY_HOST_INPUT")
	parent := os.Getenv("SPEC_KITTY_EXPIRY_HOST_RUN_PARENT")
	if image == "" || input == "" || parent == "" {
		t.Fatal("reviewed invocation must supply existing TEST, INPUT and RUN_PARENT opt-ins")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(home, ".cache", "spec-kitty-parity-resume-followup")
	imageDirectory := filepath.Join(cache, "approved-observation-parent-TnMdOgsw", "expiry-image")
	if image != filepath.Join(imageDirectory, "gapdbd.expiry-evidence") || input != filepath.Join(cache, "expiry-host-bundle-h1lqg608", "input-record.json") {
		t.Fatal("opt-ins must name the authoritative TEST image and authentic input bundle")
	}

	// This must succeed FIRST, including all twenty detached artifacts and the
	// linked client check. Missing-bundle/dummy-image refusals cannot substitute.
	expiryAuthenticRequireVerifiedAndClosed(t, image, input)

	if err := validatePathChain(parent, false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("RUN_PARENT must be an existing private directory: %v", err)
	}
	root, err := os.MkdirTemp(parent, "expiry-authentic-prelaunch-")
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately not TempDir/Cleanup: all intact and mutated custody survives.
	t.Logf("preserved authentic prelaunch custody: %s; no daemon started", root)

	record, err := decodeExpiryInput([]byte(expiryInputCanonical + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]expiryBlobRef{
		"input-record.json": {Name: "input-record.json", Bytes: uint64(len(expiryInputCanonical) + 1), SHA256: expiryInputRecordSHA256},
	}
	for _, ref := range []expiryBlobRef{record.Tuple.Source.ZIP, record.Tuple.Source.BaseInventory, record.Tuple.Source.ModifiedInventory, record.Tuple.Build.Recipe, record.Tuple.Build.Manifest} {
		refs[ref.Name] = ref
	}
	for _, delta := range record.Tuple.Source.Delta {
		refs[delta.After.Name] = delta.After
	}
	for _, ref := range record.PriorScopedEvidence {
		refs[ref.Name] = ref
	}
	if len(refs) != 21 {
		t.Fatal("fixed bundle cardinality drift")
	}
	intact := expiryAuthenticCopyBundle(t, filepath.Dir(input), root, "intact", refs)
	// Authenticate the copied, complete bundle before any negative case.
	expiryAuthenticRequireVerifiedAndClosed(t, image, filepath.Join(intact, "input-record.json"))

	t.Run("actual_CONTROL", func(t *testing.T) {
		control := filepath.Join(imageDirectory, "gapdbd.control")
		if _, err := expiryAuthenticReadImage(control, 5426833, "59508c1885d1ee02b4218376d7a6a772771627f3f9530074f4124613b76cd8c9"); err != nil {
			t.Fatal(err)
		}
		// The real CONTROL has the wrong size, so existing metadata refusal is
		// UnsafePath, not a manufactured universal AdmissionMismatch.
		expiryAuthenticRequireRefusal(t, control, filepath.Join(intact, "input-record.json"), FailureUnsafePath, 21, 1)
	})
	t.Run("authenticated_original_M1", func(t *testing.T) {
		const old = "/home/user/.cache/spec-kitty-parity-resume-41c20b14/executables/original-m1/gapdbd"
		const digest = "62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e"
		if _, err := expiryAuthenticReadImage(old, 5300621, digest); err != nil {
			t.Fatalf("required authenticated original M1 image: %v", err)
		}
		// Authenticity comes from the exact OLD admission's size/digest pins,
		// not an unobservable Close in the ordinary admission reader. Existing
		// ordinary compatibility tests remain separate, not C3 cleanup evidence.
		expiryAuthenticRequireRefusal(t, old, filepath.Join(intact, "input-record.json"), FailureUnsafePath, 21, 1)
	})
	t.Run("authenticated_WP13_old_image", func(t *testing.T) {
		const old = "/home/user/.cache/spec-kitty-parity-resume-41c20b14/executables/wp13-extension/gapdbd"
		if _, err := expiryAuthenticReadImage(old, 5327126, "947bda324a550f4a7534e4efec2a75485e95b4c27907fd0298f72aba247dadad"); err != nil {
			t.Fatalf("required authenticated WP13 old image: %v", err)
		}
		expiryAuthenticRequireRefusal(t, old, filepath.Join(intact, "input-record.json"), FailureUnsafePath, 21, 1)
	})
	t.Run("same_size_single_byte_TEST_mutation", func(t *testing.T) {
		raw, err := expiryAuthenticReadImage(image, expiryExecutableBytes, expiryExecutableSHA256)
		if err != nil {
			t.Fatal(err)
		}
		raw[len(raw)/2] ^= 1
		if int64(len(raw)) != expiryExecutableBytes || expiryRawSHA256(raw) == expiryExecutableSHA256 {
			t.Fatal("single-byte image mutation did not preserve size/change digest")
		}
		mutated := filepath.Join(root, "TEST-single-byte-never-executed")
		if err := os.WriteFile(mutated, raw, 0o500); err != nil {
			t.Fatal(err)
		}
		expiryAuthenticRequireRefusal(t, mutated, filepath.Join(intact, "input-record.json"), FailureAdmissionMismatch, 21, 1)
	})

	// Fixed actual authentication targets, with the exact number of metadata
	// reads through each target. Each case starts with all 21 authenticated files,
	// changes exactly one byte in one file, and keeps the actual TEST intact.
	for _, tc := range []struct {
		name, target string
		reads        uint64
	}{
		{"input", "input-record.json", 1},
		{"base_zip", "base.zip", 2},
		{"base_inventory", "base.inventory.json", 3},
		{"modified_inventory", "modified.inventory.json", 4},
		{"build_recipe", "build.py", 5},
		{"build_manifest", "build.json", 6},
		{"expiry_delta", "delta/expiry.go", 7},
		{"gate_delta", "delta/gate.go", 8},
		{"disabled_gate_delta", "delta/gate_disabled.go", 9},
		{"gate_test_delta", "delta/gate_test.go", 10},
		{"prior_hook_review", "prior/hook-review.md", 11},
		{"prior_adapter_review", "prior/adapter-review.md", 12},
		{"prior_adapter_qualification", "prior/adapter-qualification.json", 13},
		{"prior_adapter_live", "prior/adapter-live.jsonl", 14},
		{"prior_adapter_live_race", "prior/adapter-live-race.jsonl", 15},
		{"prior_gate_unit", "prior/gate-unit.log", 16},
		{"prior_gate_race", "prior/gate-race.log", 17},
		{"prior_gate_vet", "prior/gate-vet.log", 18},
		{"prior_test_build", "prior/test-build.log", 19},
		{"prior_repeat_build", "prior/repeat-build.log", 20},
		{"prior_control_build", "prior/control-build.log", 21},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := expiryAuthenticCopyBundle(t, intact, root, tc.name, refs)
			ref := refs[tc.target]
			target := filepath.Join(directory, tc.target)
			raw := expiryAuthenticReadArtifact(t, target, ref)
			raw[len(raw)/2] ^= 1
			if uint64(len(raw)) != ref.Bytes || expiryRawSHA256(raw) == ref.SHA256 {
				t.Fatal("artifact mutation did not preserve size/change digest")
			}
			if err := os.WriteFile(target, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			expiryAuthenticRequireRefusal(t, image, filepath.Join(directory, "input-record.json"), FailureAdmissionMismatch, tc.reads, 0)
		})
	}
}

func expiryAuthenticRequireVerifiedAndClosed(t *testing.T, image, input string) {
	t.Helper()
	verified, verifyErr := verifyExpiryQualificationExecutable(VerifyConfig{ExecutablePath: image, AdmissionRecordPath: input})
	if verified == nil {
		t.Fatalf("authentic positive returned nil: %v", verifyErr)
	}
	report := verified.expiry.report
	provenance, executed, identity := verified.Provenance(), verified.ExecutedImage(), verified.expiry.identity
	noLaunch := verified.expiry.gate == nil && verified.expiry.child == nil && !verified.expiry.transferred
	// Exactly one actual owner Close, even for an unexpected nonnil/error pair.
	// Never defer/retry Close and accidentally replace its first error with nil.
	closeErr := verified.Close()
	if verifyErr != nil || closeErr != nil || report == nil || report.cleanupError() != nil {
		t.Fatalf("authentic positive: verify=%v first Close=%v report=%v missing-report=%t", verifyErr, closeErr, report.cleanupError(), report == nil)
	}
	if !noLaunch || verified.source != nil || verified.execution != nil {
		t.Fatal("prelaunch verifier created launch state or retained closed descriptors")
	}
	if provenance.ExecutablePath != image || provenance.SHA256 != expiryExecutableSHA256 || provenance.Size != expiryExecutableBytes || provenance.ContractRevision != expiryInputContractRevision ||
		provenance.SourceRevision != "" || provenance.SourceTree != "" || provenance.ModuleVersion != "" || provenance.AdmissionRecordSHA256 != "" ||
		executed.SHA256 != expiryExecutableSHA256 || executed.Size != expiryExecutableBytes || executed.Seals&requiredExecutionSeals != requiredExecutionSeals {
		t.Fatal("actual image facts differ or historical source/admission was invented")
	}
	if identity != (ExpiryTechnicalIdentity{
		InputRecordSHA256: expiryInputRecordSHA256, TechnicalTupleSHA256: expiryTechnicalTupleSHA256, ModifiedInventorySHA256: expiryModifiedInventorySHA256,
		ClientModule: expiryClientModule, ClientVersion: expiryClientVersion, ClientModuleH1: expiryClientModuleH1, ClientGoModH1: expiryClientGoModH1,
		ExecutableSHA256: expiryExecutableSHA256, ExecutableBytes: expiryExecutableBytes,
	}) {
		t.Fatal("fixed technical identity differs")
	}
	expiryAuthenticRequireClosedCounts(t, report, 21, 1, 1)
}

func expiryAuthenticRequireRefusal(t *testing.T, image, input string, kind FailureKind, metadata, sources uint64) {
	t.Helper()
	report := new(expiryCleanupReport)
	verified, verifyErr := verifyExpiryQualificationExecutableWithReport(VerifyConfig{ExecutablePath: image, AdmissionRecordPath: input}, report)
	if verified != nil {
		actualReport := verified.expiry.report
		closeErr := verified.Close() // close unexpected capability ONCE before failing
		t.Fatalf("negative returned capability: verify=%v first Close=%v report=%v actual-report=%v", verifyErr, closeErr, report.cleanupError(), actualReport.cleanupError())
	}
	if !IsFailure(verifyErr, kind) || report.cleanupError() != nil {
		t.Fatalf("negative: verify=%v want=%s cleanup=%v", verifyErr, kind, report.cleanupError())
	}
	expiryAuthenticRequireClosedCounts(t, report, metadata, sources, 0)
}

func expiryAuthenticRequireClosedCounts(t *testing.T, report *expiryCleanupReport, metadata, sources, images uint64) {
	t.Helper()
	for category := range report.counts {
		var want uint64
		switch category {
		case expiryMetadata:
			want = metadata
		case expirySource:
			want = sources
		case expiryImage:
			want = images
		}
		count := &report.counts[category]
		if count.required.Load() != want || count.recorded.Load() != want || count.completed.Load() != want {
			t.Fatalf("category %d first-close counts=%d/%d/%d want=%d; cleanup=%v", category, count.required.Load(), count.recorded.Load(), count.completed.Load(), want, report.cleanupError())
		}
	}
}

func expiryAuthenticReadArtifact(t *testing.T, filename string, ref expiryBlobRef) []byte {
	t.Helper()
	report := new(expiryCleanupReport)
	raw, readErr := readExpiryArtifact(filename, ref.Bytes, ref.SHA256, report)
	if readErr != nil || report.cleanupError() != nil {
		t.Fatalf("authenticate copy member %s: read=%v first-close report=%v", ref.Name, readErr, report.cleanupError())
	}
	expiryAuthenticRequireClosedCounts(t, report, 1, 0, 0)
	return raw
}

func expiryAuthenticCopyBundle(t *testing.T, source, root, label string, refs map[string]expiryBlobRef) string {
	t.Helper()
	directory, err := os.MkdirTemp(root, label+"-")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"delta", "prior"} {
		if err := os.Mkdir(filepath.Join(directory, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Closed copy allowlist; never walk/copy a cache tree or copy extra files.
	for _, name := range [21]string{
		"input-record.json", "base.zip", "base.inventory.json", "modified.inventory.json", "build.py", "build.json",
		"delta/expiry.go", "delta/gate.go", "delta/gate_disabled.go", "delta/gate_test.go",
		"prior/hook-review.md", "prior/adapter-review.md", "prior/adapter-qualification.json", "prior/adapter-live.jsonl", "prior/adapter-live-race.jsonl",
		"prior/gate-unit.log", "prior/gate-race.log", "prior/gate-vet.log", "prior/test-build.log", "prior/repeat-build.log", "prior/control-build.log",
	} {
		ref, ok := refs[name]
		if !ok || ref.Name != name {
			t.Fatalf("fixed copy member not authenticated: %s", name)
		}
		raw := expiryAuthenticReadArtifact(t, filepath.Join(source, name), ref)
		// WriteFile reports its actual first close error; no retry masks it.
		if err := os.WriteFile(filepath.Join(directory, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

// Authenticate actual image bytes without constructing a command or admission.
// This read-only helper also returns the bytes for the single-byte TEST copy.
func expiryAuthenticReadImage(filename string, size int64, digest string) (_ []byte, resultErr error) {
	if err := validatePathChain(filename, true); err != nil {
		return nil, err
	}
	fd, err := openNoSymlinks(filename)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "authentic-prelaunch-image")
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	before, err := identityOf(file)
	if err != nil {
		return nil, err
	}
	if err := validateExecutableIdentity(before, size); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(file, size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != size || expiryRawSHA256(raw) != digest {
		return nil, fmt.Errorf("actual image size/digest differs: %s", filename)
	}
	after, err := identityOf(file)
	if err != nil || !sameExpiryFile(before, after) {
		return nil, errors.Join(err, errors.New("actual image changed during authentication"))
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	atPath, err := identityFromInfo(info)
	if err != nil || !sameExpiryFile(before, atPath) {
		return nil, errors.Join(err, errors.New("actual image pathname changed"))
	}
	return raw, nil
}
