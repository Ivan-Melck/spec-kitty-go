//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"syscall"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"golang.org/x/sys/unix"
)

// These coordinates are technical input identities, never executable admissions
// or a Git/module identity for the modified provider source.
const (
	expiryInputRecordSHA256       = "53fa2099c5798c6ac7d05ca782deba422412605fc9a9a9696271823652138b57"
	expiryTechnicalTupleSHA256    = "9d65f8bd363b4f416688a2038714e531e4bfe7a7a81976decb0c8eaae0f834e7"
	expiryModifiedInventorySHA256 = "1f80dde08a349ccc16e3ffef825cabfe8a8c7c2db50444ee15c9598c68ecffbf"
	expiryExecutableSHA256        = "83d5db39db928f62c9725253c53fdb8d1264f1b6e317c94db33aa11944f5905b"
	expiryExecutableBytes         = int64(5491681)
	expiryInputContractRevision   = "gapdb-sidecar-lifecycle/expiry-qualification-v1"
	expiryClientModule            = "github.com/spec-kitty/gapdb"
	expiryClientVersion           = "v0.0.0-20260907114832-006e9d469d40"
	expiryClientModuleH1          = "h1:vTBY6qolbiwbtAX/CFGAgNlmoQ9VpxGPAce+84BjSbM="
	expiryClientGoModH1           = "h1:8xNZ3k+SQIqcxLkS8bjbooOAv9xzZcbg5arjKaV6fcQ="
	expiryInputMaximum            = 65536
	expiryArtifactChunk           = 32 << 10
	expiryZIPUncompressedMaximum  = uint64(32 << 20)
)

type expiryBlobRef struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Bytes  uint64 `json:"bytes"`
}

type expiryDeltaRef struct {
	Path         string        `json:"path"`
	Operation    string        `json:"operation"`
	BeforeSHA256 string        `json:"before_sha256"`
	After        expiryBlobRef `json:"after"`
}

type expiryModuleCoordinates struct {
	Module   string `json:"module"`
	Version  string `json:"version"`
	ModuleH1 string `json:"module_h1"`
	GoModH1  string `json:"gomod_h1"`
}

type expiryTechnicalTuple struct {
	SchemaVersion uint32 `json:"schema_version"`
	Source        struct {
		Kind              string           `json:"kind"`
		Module            string           `json:"module"`
		Version           string           `json:"version"`
		ModuleH1          string           `json:"module_h1"`
		GoModH1           string           `json:"gomod_h1"`
		ZIP               expiryBlobRef    `json:"zip"`
		BaseInventory     expiryBlobRef    `json:"base_inventory"`
		BaseFiles         uint64           `json:"base_files"`
		ModifiedInventory expiryBlobRef    `json:"modified_inventory"`
		ModifiedFiles     uint64           `json:"modified_files"`
		Delta             []expiryDeltaRef `json:"delta"`
	} `json:"source"`
	Client     expiryModuleCoordinates `json:"client"`
	Executable struct {
		SHA256 string `json:"sha256"`
		Bytes  uint64 `json:"bytes"`
	} `json:"executable"`
	Control struct {
		SHA256 string `json:"sha256"`
		Bytes  uint64 `json:"bytes"`
	} `json:"control"`
	Build struct {
		Go                  string        `json:"go"`
		GoToolchain         string        `json:"gotoolchain"`
		GoFlags             string        `json:"goflags"`
		CGOEnabled          string        `json:"cgo_enabled"`
		GOOS                string        `json:"goos"`
		GOARCH              string        `json:"goarch"`
		Tags                string        `json:"tags"`
		Trimpath            bool          `json:"trimpath"`
		BuildVCS            bool          `json:"buildvcs"`
		IdenticalTestBuilds uint64        `json:"identical_test_builds"`
		Recipe              expiryBlobRef `json:"recipe"`
		Manifest            expiryBlobRef `json:"build_manifest"`
	} `json:"build"`
	Protocol struct {
		Wire                   uint64 `json:"wire"`
		Storage                uint64 `json:"storage"`
		WireReferenceSHA256    string `json:"wire_reference_sha256"`
		StorageReferenceSHA256 string `json:"storage_reference_sha256"`
	} `json:"protocol"`
	Launch struct {
		Contract       string   `json:"contract"`
		GateFD         uint64   `json:"gate_fd"`
		ExecutableFD   uint64   `json:"executable_fd"`
		ExecutablePath string   `json:"executable_path"`
		Environment    []string `json:"environment"`
	} `json:"launch"`
	RecoveryContract string `json:"recovery_contract"`
}

type expiryInputRecord struct {
	SchemaVersion             uint32               `json:"schema_version"`
	Kind                      string               `json:"kind"`
	Scope                     string               `json:"scope"`
	Adopted                   bool                 `json:"adopted"`
	ReleaseReady              bool                 `json:"release_ready"`
	ProductionAdmission       bool                 `json:"production_admission"`
	HostQualificationComplete bool                 `json:"host_qualification_complete"`
	TechnicalTupleSHA256      string               `json:"technical_tuple_sha256"`
	Tuple                     expiryTechnicalTuple `json:"tuple"`
	PriorScopedEvidence       []expiryBlobRef      `json:"prior_scoped_evidence"`
}

// The complete compiled specimen closes EVERY name, value, required member and
// ordered array. Comparing canonical bytes to this value is deliberate: merely
// decoding Go zero values would accept missing false/zero members and case aliases.
const expiryInputCanonical = `{"adopted":false,"host_qualification_complete":false,"kind":"expiry-host-qualification-input","prior_scoped_evidence":[{"bytes":7102,"name":"prior/hook-review.md","sha256":"4849d767b1cfc7664b4262dfec467101a30b2e40fad24e91af3e9ece756d1946"},{"bytes":6020,"name":"prior/adapter-review.md","sha256":"20f5a17aa9f99236a35999eb924312a70e4821a88bada433b7027b68669ffc82"},{"bytes":4457,"name":"prior/adapter-qualification.json","sha256":"c890dbf7fd378e8c629f64af4c179e38540c947a40684e18e99b525d5bed9abe"},{"bytes":5663,"name":"prior/adapter-live.jsonl","sha256":"59634c06347f6d52ad9e7bdbf4e6d3a00baf65b97ebfa5a1e9863cad13163a10"},{"bytes":5673,"name":"prior/adapter-live-race.jsonl","sha256":"daedceba488a47333bf990dac0f090708b227683ae252fd6268ecedac06de555"},{"bytes":878,"name":"prior/gate-unit.log","sha256":"b02f5d6bbaaae40ff606b88f713fbe0762c4ed0045ff88307e8bd85125091dd5"},{"bytes":887,"name":"prior/gate-race.log","sha256":"8a897afcfaa9142914e43ab9b7fa983fe580ebe7b61ae488f47e753ec0287136"},{"bytes":97,"name":"prior/gate-vet.log","sha256":"326784834f836c609346c5c608a3a9dc752c89f554c15ab891ddc7167e2d59e6"},{"bytes":260,"name":"prior/test-build.log","sha256":"dace8cd9b02d7d20578f7a1b7cff48274e9ab8f23f31171d0e2f237baf0afacf"},{"bytes":267,"name":"prior/repeat-build.log","sha256":"2bd9d2fba0daa20743967534023377635a0ef5b7947c2bccbd6b8036dfcf7449"},{"bytes":233,"name":"prior/control-build.log","sha256":"e609265c6b7c2ac8eb8eaf8359210252c2d964ede7a63abc024549ce6c0c52d1"}],"production_admission":false,"release_ready":false,"schema_version":1,"scope":"genuine-host-expiry-only","technical_tuple_sha256":"9d65f8bd363b4f416688a2038714e531e4bfe7a7a81976decb0c8eaae0f834e7","tuple":{"build":{"build_manifest":{"bytes":102339,"name":"build.json","sha256":"7227e3a716b35e026464aba98e2eaf28a6293406c027a0de960471608ad3e9aa"},"buildvcs":false,"cgo_enabled":"0","go":"go version go1.26.7 linux/amd64","goarch":"amd64","goflags":"","goos":"linux","gotoolchain":"go1.26.7","identical_test_builds":2,"recipe":{"bytes":8344,"name":"build.py","sha256":"d948de2a663041b56f02984ef4dbb5c36c39a7fa32a948f83c174f9c09a27ad2"},"tags":"spk_expiry_evidence","trimpath":true},"client":{"gomod_h1":"h1:8xNZ3k+SQIqcxLkS8bjbooOAv9xzZcbg5arjKaV6fcQ=","module":"github.com/spec-kitty/gapdb","module_h1":"h1:vTBY6qolbiwbtAX/CFGAgNlmoQ9VpxGPAce+84BjSbM=","version":"v0.0.0-20260907114832-006e9d469d40"},"control":{"bytes":5426833,"sha256":"59508c1885d1ee02b4218376d7a6a772771627f3f9530074f4124613b76cd8c9"},"executable":{"bytes":5491681,"sha256":"83d5db39db928f62c9725253c53fdb8d1264f1b6e317c94db33aa11944f5905b"},"launch":{"contract":"expiry-host-fixed-fd/v1","environment":["LANG=C","TZ=UTC","SPEC_KITTY_EXPIRY_GATE_FD=3"],"executable_fd":4,"executable_path":"/proc/self/fd/4","gate_fd":3},"protocol":{"storage":1,"storage_reference_sha256":"6d4cca9611e9e272e02d6e566dccc887dacf738c1da8c5d580237f731018295c","wire":1,"wire_reference_sha256":"cd08e19252628a1cbfe5bd43018caae86569e041dc01f9d4472dc2eae72389c2"},"recovery_contract":"reader-v2-only/no-optional-accelerators","schema_version":1,"source":{"base_files":400,"base_inventory":{"bytes":47518,"name":"base.inventory.json","sha256":"ea82a41611f31152ac18b18f95720330a8daa710f55f59cc4f55b63e0dbae064"},"delta":[{"after":{"bytes":6032,"name":"delta/expiry.go","sha256":"c795c284eb8e51a8bd7071c4b74646fbb825f801664b4d1459383f9cde5feb76"},"before_sha256":"da3f45409507ed69118a172ef9e5e80aa842a03087031fe746a2823713795a41","operation":"replace","path":"internal/engine/expiry.go"},{"after":{"bytes":2343,"name":"delta/gate.go","sha256":"4d3d919a73be77884b65fe44b82194d310c43b004e5ab20e0e46129385a82830"},"before_sha256":"","operation":"add","path":"internal/engine/spk_expiry_gate.go"},{"after":{"bytes":156,"name":"delta/gate_disabled.go","sha256":"0fbbdaa967efb9ca4230fce9e37787f1b9a3ddbc173ccd9894e43ea2792b40d9"},"before_sha256":"","operation":"add","path":"internal/engine/spk_expiry_gate_disabled.go"},{"after":{"bytes":2564,"name":"delta/gate_test.go","sha256":"d4f0167b986a582807c91bd4cd761591028ecdd3c713c9e90efbfe4bea52ae7f"},"before_sha256":"","operation":"add","path":"internal/engine/spk_expiry_gate_test.go"}],"gomod_h1":"h1:8xNZ3k+SQIqcxLkS8bjbooOAv9xzZcbg5arjKaV6fcQ=","kind":"authenticated-module-plus-exact-delta/v1","modified_files":403,"modified_inventory":{"bytes":47844,"name":"modified.inventory.json","sha256":"1f80dde08a349ccc16e3ffef825cabfe8a8c7c2db50444ee15c9598c68ecffbf"},"module":"github.com/spec-kitty/gapdb","module_h1":"h1:vTBY6qolbiwbtAX/CFGAgNlmoQ9VpxGPAce+84BjSbM=","version":"v0.0.0-20260907114832-006e9d469d40","zip":{"bytes":1672066,"name":"base.zip","sha256":"b40e783ab48fb800ed2390c72aedbe985121d5aac13dfab9b0e5317ffe8eb67d"}}}}`

func expiryRawSHA256(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func expiryCanonical(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(raw)
}

func decodeExpiryInput(raw []byte) (expiryInputRecord, error) {
	var record expiryInputRecord
	if len(raw) == 0 || len(raw) > expiryInputMaximum || raw[len(raw)-1] != '\n' {
		return record, errors.New("qualification input bound or terminator differs")
	}
	canonical, err := journalv2.CanonicalObject(raw[:len(raw)-1])
	if err != nil || !bytes.Equal(canonical, raw[:len(raw)-1]) {
		return record, errors.New("qualification input is not canonical unique JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return expiryInputRecord{}, errors.New("qualification input shape differs")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return expiryInputRecord{}, errors.New("qualification input has a trailing value")
	}
	if string(canonical) != expiryInputCanonical || expiryRawSHA256(raw) != expiryInputRecordSHA256 {
		return expiryInputRecord{}, errors.New("qualification input differs from the complete fixed specimen")
	}
	// Keep the framed tuple hash distinct from raw record and inventory hashes.
	tuple, err := expiryCanonical(record.Tuple)
	if err != nil {
		return expiryInputRecord{}, err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("spec-kitty.expiry-host.tuple.v1\x00"))
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(tuple)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write(tuple)
	if hex.EncodeToString(hash.Sum(nil)) != expiryTechnicalTupleSHA256 || record.TechnicalTupleSHA256 != expiryTechnicalTupleSHA256 {
		return expiryInputRecord{}, errors.New("qualification tuple identity differs")
	}
	return record, nil
}

// verifyExpiryQualificationExecutable has no exported verifier or admission
// result. It ignores caller Admission. No executable is opened until ALL fixed
// detached artifacts, the exact source relation and linked client are checked.
func verifyExpiryQualificationExecutable(config VerifyConfig) (*VerifiedExecutable, error) {
	return verifyExpiryQualificationExecutableWithReport(config, new(expiryCleanupReport))
}

func verifyExpiryQualificationExecutableWithReport(config VerifyConfig, report *expiryCleanupReport) (*VerifiedExecutable, error) {
	return verifyExpiryQualificationExecutableWithClose(config, report, (*os.File).Close)
}
// Private call-only close seam. Production always passes the literal method
// expression above; no configuration, stored closer or alternate file factory.
func verifyExpiryQualificationExecutableWithClose(config VerifyConfig, report *expiryCleanupReport, closeCall func(*os.File) error) (_ *VerifiedExecutable, resultErr error) {
	defer func() {
		if resultErr != nil {
			resultErr = withStartupCustody(resultErr, report.gateTeardownError())
		}
	}()
	for _, candidate := range []string{config.ExecutablePath, config.AdmissionRecordPath} {
		if err := validateAbsolutePath(candidate); err != nil {
			return nil, failure(FailureInvalidConfiguration, "verify_expiry_input", "qualification path is not absolute and normalized", err)
		}
		if err := validatePathChain(candidate, true); err != nil {
			return nil, failure(FailureUnsafePath, "verify_expiry_input", "qualification path ownership or mode is unsafe", err)
		}
	}
	input, err := readExpiryArtifactWithClose(config.AdmissionRecordPath, uint64(len(expiryInputCanonical)+1), expiryInputRecordSHA256, report, closeCall)
	if err != nil {
		return nil, failure(FailureAdmissionMismatch, "verify_expiry_input", "qualification input cannot be authenticated", err)
	}
	record, err := decodeExpiryInput(input)
	if err != nil {
		return nil, failure(FailureAdmissionMismatch, "verify_expiry_input", "qualification input differs", err)
	}
	if err := authenticateExpiryArtifactsWithClose(filepath.Dir(config.AdmissionRecordPath), record, report, closeCall); err != nil {
		return nil, failure(FailureAdmissionMismatch, "verify_expiry_input", "qualification artifacts differ", err)
	}
	build, ok := debug.ReadBuildInfo()
	if !ok || verifyExpiryClient(build) != nil {
		return nil, failure(FailureAdmissionMismatch, "verify_expiry_input", "linked client module differs or cannot be proved", nil)
	}
	verified, err := verifyExecutableImage(config.ExecutablePath, expiryExecutableSHA256, expiryExecutableBytes,
		ExecutableProvenance{ContractRevision: expiryInputContractRevision}, report)
	if err != nil {
		return nil, err
	}
	verified.expiry.identity = ExpiryTechnicalIdentity{
		InputRecordSHA256: expiryInputRecordSHA256, TechnicalTupleSHA256: expiryTechnicalTupleSHA256,
		ModifiedInventorySHA256: expiryModifiedInventorySHA256,
		ClientModule:            expiryClientModule, ClientVersion: expiryClientVersion,
		ClientModuleH1: expiryClientModuleH1, ClientGoModH1: expiryClientGoModH1,
		ExecutableSHA256: expiryExecutableSHA256, ExecutableBytes: expiryExecutableBytes,
	}
	verified.expiry.report = report
	return verified, nil
}

func verifyExpiryClient(build *debug.BuildInfo) error {
	if build == nil {
		return errors.New("linked client metadata absent")
	}
	found := false
	for _, dependency := range build.Deps {
		if dependency == nil || dependency.Path != expiryClientModule {
			continue
		}
		if found || dependency.Version != expiryClientVersion || dependency.Sum != expiryClientModuleH1 || dependency.Replace != nil {
			return errors.New("linked client is not the unmodified pinned module")
		}
		found = true
	}
	if !found {
		return errors.New("linked client dependency absent")
	}
	// Go's build metadata does not contain a separate go.mod sum. The checked
	// module h1 is reproduced from the ZIP below, whose actual go.mod separately
	// reproduces expiryClientGoModH1. No replacement dependency is accepted.
	return nil
}

func expirySafeRelative(name string) bool {
	if name == "" || path.IsAbs(name) || strings.ContainsAny(name, "\\\x00\r\n") || path.Clean(name) != name {
		return false
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

func expiryLowerSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

// Ancestor snapshots supplement openat2's held no-follow descriptor. Observed
// replacement/metadata drift refuses; this is not immunity to same-UID races.
func expiryPathAncestors(filename string) ([]fileIdentity, error) {
	if err := validatePathChain(filename, true); err != nil {
		return nil, err
	}
	components := strings.Split(strings.TrimPrefix(filepath.Dir(filename), "/"), "/")
	current := "/"
	identities := make([]fileIdentity, 0, len(components)+1)
	for index := -1; index < len(components); index++ {
		if index >= 0 {
			if components[index] == "" {
				continue
			}
			current = filepath.Join(current, components[index])
		}
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		identity, err := identityFromInfo(info)
		if err != nil || !info.IsDir() {
			return nil, errors.New("qualification artifact ancestor differs")
		}
		identities = append(identities, identity)
	}
	return identities, nil
}

func sameExpiryFile(first, second fileIdentity) bool {
	return sameExecutableIdentity(first, second) && first.ctimeNsec == second.ctimeNsec
}

// All directly owned artifact closes are synchronous and primary-preserving;
// no repeated-nil Close can repair a failed input read or return usable bytes.
func readExpiryArtifact(filename string, expectedBytes uint64, expectedSHA string, report *expiryCleanupReport) ([]byte, error) {
	return readExpiryArtifactWithClose(filename, expectedBytes, expectedSHA, report, (*os.File).Close)
}
func readExpiryArtifactWithClose(filename string, expectedBytes uint64, expectedSHA string, report *expiryCleanupReport, closeCall func(*os.File) error) (encoded []byte, resultErr error) {
	if expectedBytes == 0 || expectedBytes > 2<<20 || !expiryLowerSHA256(expectedSHA) {
		return nil, errors.New("qualification artifact bound differs")
	}
	ancestors, err := expiryPathAncestors(filename)
	if err != nil {
		return nil, err
	}
	// NONBLOCK prevents a raced FIFO/device substitution from blocking open;
	// the held descriptor must still be a regular file before any read.
	fd, err := unix.Openat2(unix.AT_FDCWD, filename, &unix.OpenHow{
		Flags:   uint64(unix.O_RDONLY | unix.O_NONBLOCK | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: uint64(unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS),
	})
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "expiry-qualification-artifact")
	report.metadataOpened()
	defer func() {
		err := closeCall(file)
		report.metadataClosed(err)
		resultErr = withStartupCustody(resultErr, err)
		if resultErr != nil { encoded = nil }
	}()
	before, err := identityOf(file)
	if err != nil {
		return nil, err
	}
	if before.size != int64(expectedBytes) || before.nlink != 1 || before.mode&syscall.S_IFMT != syscall.S_IFREG || before.mode&0o022 != 0 ||
		(before.uid != 0 && before.uid != uint32(os.Geteuid())) {
		return nil, errors.New("qualification artifact metadata differs")
	}
	var raw bytes.Buffer
	hash := sha256.New()
	read, err := io.CopyBuffer(io.MultiWriter(&raw, hash), io.LimitReader(file, int64(expectedBytes)+1), make([]byte, expiryArtifactChunk))
	if err != nil || read != int64(expectedBytes) || hex.EncodeToString(hash.Sum(nil)) != expectedSHA {
		return nil, errors.New("qualification artifact bytes differ")
	}
	after, err := identityOf(file)
	if err != nil || !sameExpiryFile(before, after) {
		return nil, errors.New("qualification artifact changed during read")
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	atPath, err := identityFromInfo(info)
	if err != nil || !sameExpiryFile(before, atPath) {
		return nil, errors.New("qualification artifact pathname changed")
	}
	finalAncestors, err := expiryPathAncestors(filename)
	if err != nil || len(ancestors) != len(finalAncestors) {
		return nil, errors.New("qualification artifact ancestry changed")
	}
	for index := range ancestors {
		if !sameExpiryFile(ancestors[index], finalAncestors[index]) {
			return nil, errors.New("qualification artifact ancestor changed")
		}
	}
	return raw.Bytes(), nil
}

func authenticateExpiryArtifacts(directory string, record expiryInputRecord, report *expiryCleanupReport) error {
	return authenticateExpiryArtifactsWithClose(directory, record, report, (*os.File).Close)
}
func authenticateExpiryArtifactsWithClose(directory string, record expiryInputRecord, report *expiryCleanupReport, closeCall func(*os.File) error) error {
	source := record.Tuple.Source
	refs := []expiryBlobRef{source.ZIP, source.BaseInventory, source.ModifiedInventory, record.Tuple.Build.Recipe, record.Tuple.Build.Manifest}
	for _, delta := range source.Delta {
		refs = append(refs, delta.After)
	}
	refs = append(refs, record.PriorScopedEvidence...)
	if len(refs) != 20 {
		return errors.New("qualification artifact set differs")
	}
	artifacts := make(map[string][]byte, len(refs))
	for _, ref := range refs {
		if !expirySafeRelative(ref.Name) {
			return errors.New("qualification artifact name is unsafe")
		}
		if _, duplicate := artifacts[ref.Name]; duplicate {
			return errors.New("qualification artifact name repeated")
		}
		raw, err := readExpiryArtifactWithClose(filepath.Join(directory, filepath.FromSlash(ref.Name)), ref.Bytes, ref.SHA256, report, closeCall)
		if err != nil {
			return err
		}
		artifacts[ref.Name] = raw
	}
	base, err := decodeExpiryInventory(artifacts[source.BaseInventory.Name], 400)
	if err != nil {
		return err
	}
	modified, err := decodeExpiryInventory(artifacts[source.ModifiedInventory.Name], 403)
	if err != nil {
		return err
	}
	if expiryRawSHA256(artifacts[source.ModifiedInventory.Name]) != expiryModifiedInventorySHA256 {
		return errors.New("modified inventory identity differs")
	}
	zipInventory, err := authenticateExpiryZIP(artifacts[source.ZIP.Name], source.Module, source.Version, source.ModuleH1, source.GoModH1)
	if err != nil {
		return err
	}
	if !equalExpiryInventory(base, zipInventory) {
		return errors.New("base inventory differs from authenticated module ZIP")
	}
	if err := authenticateExpiryDelta(base, modified, artifacts, source.Delta); err != nil {
		return err
	}
	// Frozen historical manifest bytes are authenticated above; its inventory
	// relationship is checked, not promoted to a signed/hermetic attestation.
	var manifest struct {
		OriginalInventory   map[string]string `json:"original_inventory"`
		TestSourceInventory map[string]string `json:"test_source_inventory"`
	}
	if err := json.Unmarshal(artifacts[record.Tuple.Build.Manifest.Name], &manifest); err != nil ||
		!equalExpiryInventory(base, manifest.OriginalInventory) || !equalExpiryInventory(modified, manifest.TestSourceInventory) {
		return errors.New("historical build inventories differ")
	}
	return nil
}

func decodeExpiryInventory(raw []byte, expectedEntries int) (map[string]string, error) {
	if len(raw) == 0 || len(raw) > expiryInputMaximum || raw[len(raw)-1] != '\n' {
		return nil, errors.New("inventory bound or terminator differs")
	}
	canonical, err := journalv2.CanonicalObject(raw[:len(raw)-1])
	if err != nil || !bytes.Equal(canonical, raw[:len(raw)-1]) {
		return nil, errors.New("inventory is not canonical unique JSON")
	}
	var inventory map[string]string
	if json.Unmarshal(canonical, &inventory) != nil || len(inventory) != expectedEntries {
		return nil, errors.New("inventory shape differs")
	}
	for name, digest := range inventory {
		if !expirySafeRelative(name) || !expiryLowerSHA256(digest) {
			return nil, errors.New("inventory path or digest differs")
		}
	}
	return inventory, nil
}

func equalExpiryInventory(first, second map[string]string) bool {
	if len(first) != len(second) {
		return false
	}
	for name, digest := range first {
		if second[name] != digest {
			return false
		}
	}
	return true
}

// This is the module dirhash HashZip algorithm over independently bounded,
// validated ZIP members. No module library/dependency or disk extraction is used.
func authenticateExpiryZIP(raw []byte, module, version, expectedH1, expectedGoModH1 string) (map[string]string, error) {
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	if len(archive.File) != 400 || len(archive.File) > 403 {
		return nil, errors.New("module ZIP member bound differs")
	}
	prefix := module + "@" + version + "/"
	members := append([]*zip.File(nil), archive.File...)
	sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })
	inventory := make(map[string]string, len(members))
	hash := sha256.New()
	var total uint64
	for _, member := range members {
		if !strings.HasPrefix(member.Name, prefix) || !member.Mode().IsRegular() || member.UncompressedSize64 > expiryZIPUncompressedMaximum-total {
			return nil, errors.New("module ZIP member type, prefix or size differs")
		}
		relative := strings.TrimPrefix(member.Name, prefix)
		if !expirySafeRelative(relative) {
			return nil, errors.New("module ZIP member path is unsafe")
		}
		if _, exists := inventory[relative]; exists {
			return nil, errors.New("module ZIP member repeated")
		}
		total += member.UncompressedSize64
		reader, err := member.Open()
		if err != nil {
			return nil, err
		}
		fileHash := sha256.New()
		n, readErr := io.CopyBuffer(fileHash, io.LimitReader(reader, int64(member.UncompressedSize64)+1), make([]byte, expiryArtifactChunk))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || uint64(n) != member.UncompressedSize64 {
			return nil, errors.Join(readErr, closeErr, errors.New("module ZIP member length or checksum differs"))
		}
		digest := hex.EncodeToString(fileHash.Sum(nil))
		inventory[relative] = digest
		_, _ = fmt.Fprintf(hash, "%s  %s\n", digest, member.Name)
	}
	if "h1:"+base64.StdEncoding.EncodeToString(hash.Sum(nil)) != expectedH1 {
		return nil, errors.New("module HashZip h1 differs")
	}
	goModDigest, ok := inventory["go.mod"]
	if !ok {
		return nil, errors.New("module go.mod absent")
	}
	goModHash := sha256.Sum256([]byte(goModDigest + "  go.mod\n"))
	if "h1:"+base64.StdEncoding.EncodeToString(goModHash[:]) != expectedGoModH1 {
		return nil, errors.New("module go.mod h1 differs")
	}
	return inventory, nil
}

func authenticateExpiryDelta(base, modified map[string]string, artifacts map[string][]byte, deltas []expiryDeltaRef) error {
	if len(base) != 400 || len(modified) != 403 || len(deltas) != 4 {
		return errors.New("source delta cardinality differs")
	}
	changes := make(map[string]expiryDeltaRef, 4)
	for index, delta := range deltas {
		if !expirySafeRelative(delta.Path) || !expirySafeRelative(delta.After.Name) {
			return errors.New("source delta path differs")
		}
		if _, exists := changes[delta.Path]; exists {
			return errors.New("source delta path repeated")
		}
		raw, exists := artifacts[delta.After.Name]
		if !exists || uint64(len(raw)) != delta.After.Bytes || expiryRawSHA256(raw) != delta.After.SHA256 || modified[delta.Path] != delta.After.SHA256 {
			return errors.New("source delta blob differs")
		}
		if index == 0 {
			if delta.Path != "internal/engine/expiry.go" || delta.Operation != "replace" || base[delta.Path] != delta.BeforeSHA256 {
				return errors.New("source replacement differs")
			}
			insertion := []byte("\tspkExpiryEvidenceHold() // isolated qualification only\n")
			if bytes.Count(raw, insertion) != 1 || expiryRawSHA256(bytes.Replace(raw, insertion, nil, 1)) != delta.BeforeSHA256 {
				return errors.New("source replacement does not reverse to base")
			}
			if !bytes.Contains(raw, append(append([]byte(nil), insertion...), []byte("\tsort.Slice(due,")...)) {
				return errors.New("expiry hold insertion is not before sort")
			}
		} else if delta.Operation != "add" || delta.BeforeSHA256 != "" || base[delta.Path] != "" {
			return errors.New("source addition differs")
		}
		changes[delta.Path] = delta
	}
	for name, digest := range base {
		if _, changed := changes[name]; !changed && modified[name] != digest {
			return errors.New("unchanged source entry differs or was deleted")
		}
	}
	for name := range modified {
		if base[name] == "" {
			if _, addition := changes[name]; !addition {
				return errors.New("unlisted source addition")
			}
		}
	}
	return nil
}
