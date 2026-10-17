package productionruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"syscall"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/projection"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// TEST ONLY. This method requires independent source/method review BEFORE any
// native execution. No product policy, provider, admission or selector changes.
const privateChainOriginalImageSHA256 = "947bda324a550f4a7534e4efec2a75485e95b4c27907fd0298f72aba247dadad"
const privateChainReportEnv = "SPEC_KITTY_OBSERVATION_PRIVATE_CHAIN_REPORT"
const privateChainSamples = 30

type privateChainCase struct {
	Name                string                   `json:"name"`
	Epochs              int                      `json:"epochs"`
	ExpectedOutcome     string                   `json:"expected_outcome"`
	ExpectedPrivateRows uint64                   `json:"expected_private_rows"`
	Samples             []privateChainSample     `json:"samples"`
	Statistics          retainedPerformanceStats `json:"statistics"`
}

func privateChainCases() []privateChainCase {
	return []privateChainCase{
		{Name: "private64_match", Epochs: 64, ExpectedOutcome: "matched", ExpectedPrivateRows: 195},
		{Name: "private65_refuse", Epochs: 65, ExpectedOutcome: "sanitized_refusal", ExpectedPrivateRows: 0},
	}
}

type privateChainSample struct {
	Index                int                      `json:"index"`
	Nanoseconds          int64                    `json:"nanoseconds"`
	ObservedClass        string                   `json:"observed_class"`
	Matched              bool                     `json:"matched"` // positive proof, NOT expected-case success
	ExpectedOutcomeMet   bool                     `json:"expected_outcome_met"`
	ErrorPresent         bool                     `json:"error_present"`
	ErrorType            string                   `json:"error_type"`
	ProofZero            bool                     `json:"proof_zero"`
	PublicationRows      uint64                   `json:"matched_publication_rows"`
	PrivateRows          uint64                   `json:"matched_private_rows"`
	TotalRows            uint64                   `json:"matched_total_rows"`
	ACKs                 int                      `json:"actual_acks"`
	Before               projection.AttributionV2 `json:"before"`
	After                projection.AttributionV2 `json:"after"`
	ProofAttribution     projection.AttributionV2 `json:"proof_attribution"`
	StateBefore          HostState                `json:"state_before"`
	StateAfter           HostState                `json:"state_after"`
	ProjectionObjectSame bool                     `json:"projection_object_same"`
	BaselineSame         bool                     `json:"baseline_same"`
	DatabaseStable       bool                     `json:"database_stable"`
	DataDirectoryStable  bool                     `json:"data_directory_stable"`
	EpochStable          bool                     `json:"epoch_stable"`
	FailureClass         string                   `json:"failure_class,omitempty"`
}

// Only the exact public sanitized refusal is expected. Cancellation, deadlines,
// wrapped/content-bearing errors and nonzero error proofs never earn this class.
func privateChainObservedClass(proof RetainedRecordsProof, err error) string {
	if err == nil {
		if proof == (RetainedRecordsProof{}) {
			return "zero_success"
		}
		return "matched"
	}
	if proof != (RetainedRecordsProof{}) {
		return "nonzero_error_proof"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if e, ok := err.(*ProgressError); ok && e != nil && e.Kind == ProgressIncomplete && e.Field == "retained_records" && e.Error() == "PROGRESS_INCOMPLETE: retained_records" {
		return "sanitized_refusal"
	}
	return "other_error"
}

func privateChainExpected(c privateChainCase, s privateChainSample) bool {
	if s.Nanoseconds <= 0 || s.Index < 0 || s.Index >= privateChainSamples || s.ACKs != 0 ||
		s.StateBefore != HostReady || s.StateAfter != HostReady || !s.ProjectionObjectSame ||
		!s.BaselineSame || !s.DatabaseStable || !s.DataDirectoryStable || !s.EpochStable || s.Before != s.After ||
		s.Before.DatabaseID == "" || s.Before.RuntimeEpochID == "" || s.Before.Revision == 0 ||
		s.Before.DurableThrough != s.Before.Revision || s.Before.CursorToken.IsZero() {
		return false
	}
	switch {
	case c.Epochs == 64 && c.ExpectedOutcome == "matched" && c.ExpectedPrivateRows == 195:
		return s.ObservedClass == "matched" && s.Matched && !s.ErrorPresent && !s.ProofZero &&
			s.ProofAttribution == s.Before && s.PublicationRows == 0 && s.PrivateRows == 195 && s.TotalRows == 195
	case c.Epochs == 65 && c.ExpectedOutcome == "sanitized_refusal" && c.ExpectedPrivateRows == 0:
		return s.ObservedClass == "sanitized_refusal" && !s.Matched && s.ErrorPresent && s.ProofZero &&
			s.ProofAttribution == (projection.AttributionV2{}) && s.PublicationRows == 0 && s.PrivateRows == 0 && s.TotalRows == 0
	default:
		return false
	}
}

type privateChainEpoch struct {
	Number              int                      `json:"number"`
	SupervisorRunID     string                   `json:"supervisor_run_id"`
	RuntimeEpochID      journalv2.RuntimeEpochID `json:"runtime_epoch_id"`
	DatabaseID          journalv2.DatabaseID     `json:"database_id"`
	DataDirectory       string                   `json:"data_directory"`
	RuntimeDirectory    string                   `json:"runtime_directory"`
	AdmissionSHA256     string                   `json:"admission_sha256"`
	Attribution         projection.AttributionV2 `json:"attribution"`
	FreshEpoch          bool                     `json:"fresh_epoch"`
	DatabaseStable      bool                     `json:"database_stable"`
	DataDirectoryStable bool                     `json:"data_directory_stable"`
	Ready               bool                     `json:"ready"`
	Closed              bool                     `json:"closed"`
	CloseError          string                   `json:"close_error,omitempty"`
}

type privateChainReport struct {
	Schema                  int                 `json:"schema_version"`
	Scope                   string              `json:"scope"`
	Stage                   string              `json:"stage"`
	GoVersion               string              `json:"go_version"`
	GOOS                    string              `json:"goos"`
	GOARCH                  string              `json:"goarch"`
	Profile                 map[string]string   `json:"profile"`
	BuildInfo               *debug.BuildInfo    `json:"build_info"`
	ProviderPath            string              `json:"provider_path"`
	ProviderSHA256          string              `json:"provider_sha256"`
	ProviderBytes           int                 `json:"provider_bytes"`
	ExpectedProviderSHA256  string              `json:"expected_provider_sha256"`
	TestExecutablePath      string              `json:"test_executable_path"`
	TestExecutableSHA256    string              `json:"test_executable_sha256"`
	TestExecutableBytes     int                 `json:"test_executable_bytes"`
	ReportDirectory         string              `json:"report_directory"`
	EvidenceRoot            string              `json:"evidence_root"`
	AuthorityRoot           string              `json:"authority_root"`
	RuntimeRoot             string              `json:"runtime_root"`
	Epochs                  []privateChainEpoch `json:"epochs"`
	Cases                   []privateChainCase  `json:"cases"`
	Failures                []string            `json:"failures"`
	Complete                bool                `json:"complete"`
	WholeLayerQualified     bool                `json:"whole_layer_qualified"`
	WholeMemoryQualified    bool                `json:"whole_memory_qualified"`
	GenuineExpiryQualified  bool                `json:"genuine_expiry_qualified"`
	PerformanceSLOQualified bool                `json:"performance_slo_qualified"`
	Disclosures             []string            `json:"disclosures"`
}

func privateChainConfiguration(binary, reportPath string, profile map[string]string, version, goos, goarch string) (bool, error) {
	if binary == "" && reportPath == "" {
		return false, nil
	}
	if binary == "" || reportPath == "" {
		return false, fmt.Errorf("both SPEC_KITTY_WP13_GAPDBD and %s are required", privateChainReportEnv)
	}
	if version != "go1.26.7" || goos != "linux" || goarch != "amd64" || profile["GOAMD64"] != "v1" || profile["CGO_ENABLED"] != "1" {
		return false, errors.New("requires Go1.26.7/linux/amd64/GOAMD64=v1/CGO_ENABLED=1")
	}
	for _, key := range []string{"GOFLAGS", "GOEXPERIMENT", "GODEBUG"} {
		if profile[key] != "" {
			return false, fmt.Errorf("nonordinary profile: %s", key)
		}
	}
	return true, nil
}

func privateChainReserve(path string) (*os.File, string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	parent := filepath.Dir(absolute)
	info, err := os.Lstat(parent)
	if err != nil {
		return nil, "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	real, realErr := filepath.EvalSymlinks(parent)
	if !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || !ok || stat.Uid != uint32(os.Geteuid()) || realErr != nil || real != parent {
		return nil, "", errors.New("report parent must be a real runner-owned0700 directory (no symlink ancestry)")
	}
	out, err := os.OpenFile(absolute, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, "", err
	}
	return out, parent, nil
}

// Recompute from every retained raw sample, including an unexpected failing
// prefix. Complete is stricter than successful statistics or positive Matched.
func privateChainFinalize(r *privateChainReport, testFailed, cleanupDone bool) {
	r.Complete = false
	r.WholeLayerQualified, r.WholeMemoryQualified, r.GenuineExpiryQualified, r.PerformanceSLOQualified = false, false, false, false
	valid := !testFailed && cleanupDone && len(r.Failures) == 0 && len(r.Cases) == 2 && len(r.Epochs) == 65
	seen := make(map[journalv2.RuntimeEpochID]bool)
	for i, e := range r.Epochs {
		if e.Number != i+1 || e.RuntimeEpochID == "" || seen[e.RuntimeEpochID] || !e.FreshEpoch || !e.DatabaseStable || !e.DataDirectoryStable || !e.Ready || !e.Closed || e.CloseError != "" {
			valid = false
		}
		seen[e.RuntimeEpochID] = true
	}
	want := privateChainCases()
	for i := range r.Cases {
		c := &r.Cases[i]
		durations := make([]time.Duration, 0, len(c.Samples))
		for j, s := range c.Samples {
			durations = append(durations, time.Duration(s.Nanoseconds))
			if s.Index != j || !s.ExpectedOutcomeMet || !privateChainExpected(*c, s) || s.FailureClass != "" {
				valid = false
			}
		}
		var ok bool
		c.Statistics, ok = retainedPerformanceStatistics(durations) // existing nearest-rank helper
		if !ok || len(c.Samples) != privateChainSamples || i >= len(want) {
			valid = false
			continue
		}
		w := want[i]
		if c.Name != w.Name || c.Epochs != w.Epochs || c.ExpectedOutcome != w.ExpectedOutcome || c.ExpectedPrivateRows != w.ExpectedPrivateRows {
			valid = false
		}
	}
	r.Complete = valid
}

func privateChainWrite(out *os.File, report privateChainReport) error {
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if _, err := out.WriteAt(body, 0); err != nil {
		return err
	}
	if err := out.Truncate(int64(len(body))); err != nil {
		return err
	}
	return out.Sync()
}

func TestRetainedPrivateChainOriginalHostNative(t *testing.T) {
	binary, reportPath := os.Getenv("SPEC_KITTY_WP13_GAPDBD"), os.Getenv(privateChainReportEnv)
	profile := make(map[string]string)
	for _, key := range []string{"GOAMD64", "CGO_ENABLED", "GOFLAGS", "GOEXPERIMENT", "GODEBUG", "GOTOOLCHAIN"} {
		profile[key] = os.Getenv(key)
	}
	enabled, err := privateChainConfiguration(binary, reportPath, profile, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Skip("independent review required before opting in with exact WP13 image and private-chain report")
	}
	out, parent, err := privateChainReserve(reportPath) // BEFORE any provider or evidence setup
	if err != nil {
		t.Fatal(err)
	}
	r := privateChainReport{Schema: 1, Scope: "original WP13 genuine Start-owned Host; no-ACK same-root private64 match/private65 public sanitized refusal ONLY", GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Profile: profile, ReportDirectory: parent, ExpectedProviderSHA256: privateChainOriginalImageSHA256, Cases: privateChainCases(), Disclosures: []string{
		"Independent method review BEFORE native execution; parent source/build/command/run/environment/image/ELF custody REQUIRED; self-report is not source qualification.",
		"30 sequential first-inclusive observations EACH at epochs64/65; time ONLY VerifyRetainedRecords; setup/restarts/assertions/report/close outside timer; no warmup/retries/discards/calibration/forcedGC/allocation instrumentation.",
		"Private64 expects actual matched195=3*64+3, publications0, ACKs0; private65 expects ZERO proof plus exact sanitized PROGRESS_INCOMPLETE retained_records; refusal is expected-case success, NOT a positive match or a timed performance failure.",
		"All raw observations retained; unexpected outcomes terminate with failing prefix; ordinary scheduler/GC/monitor/cache effects remain; existing deadlines are cooperative, not hard-wall guarantees.",
		"Durable authority/runtime evidence roots preserved even on failure. Lifecycle Close may remove its own ephemeral sockets/process artifacts; no evidentiary root RemoveAll.",
		"Only this fixed private-chain boundary workload; no full memory/alloc/RSS, SLO, all-layer, maximum-value/payload, concurrency, near-maximum-image/hash-work or genuine Host-expiry qualification.",
	}}
	var h *Host
	persistPrefix := func() {
		r.Stage = "pre_close_prefix"
		privateChainFinalize(&r, t.Failed(), false)
		if err := privateChainWrite(out, r); err != nil {
			r.Failures = append(r.Failures, "prefix report write: "+err.Error())
			t.Error(err)
		}
	}
	closeHost := func() {
		if h == nil {
			return
		}
		persistPrefix() // including epoch64's samples BEFORE its restart Close
		err := h.Close(context.Background())
		if len(r.Epochs) > 0 {
			e := &r.Epochs[len(r.Epochs)-1]
			e.Closed = err == nil && h.State() == HostClosed
			if err != nil {
				e.CloseError = err.Error()
			}
		}
		h = nil // Close is once-only; never retry a close failure into apparent success.
		if err != nil {
			r.Failures = append(r.Failures, "Close: "+err.Error())
			t.Error("Host Close failed; evidence preserved", err)
		}
	}
	defer func() {
		// Goexit from Fatal also reaches this. Persist prefix BEFORE Close can
		// block/fail, then preserve close diagnostics in the final report.
		if h == nil {
			persistPrefix()
		} else {
			closeHost()
		}
		r.Stage = "final"
		privateChainFinalize(&r, t.Failed(), true)
		if !r.Complete && !t.Failed() {
			r.Failures = append(r.Failures, "incomplete final report")
			t.Error("incomplete final report")
		}
		if err := privateChainWrite(out, r); err != nil {
			t.Error("final report write", err)
		}
		if err := out.Close(); err != nil {
			t.Error("report close", err)
		}
	}()
	fail := func(message string, err error) {
		if err != nil {
			message += ": " + err.Error()
		}
		r.Failures = append(r.Failures, message)
		t.Fatal(message)
	}
	r.BuildInfo, _ = debug.ReadBuildInfo()
	if r.BuildInfo != nil {
		for _, s := range r.BuildInfo.Settings {
			switch s.Key {
			case "-race", "-msan", "-asan", "-cover", "-tags", "-gcflags", "-asmflags", "-ldflags":
				if s.Value != "" && s.Value != "false" {
					fail("nonordinary executable setting "+s.Key, nil)
				}
			case "GOAMD64", "CGO_ENABLED":
				if s.Value != profile[s.Key] {
					fail("executable/profile mismatch "+s.Key, nil)
				}
			case "-buildmode":
				if s.Value != "exe" {
					fail("nonordinary executable buildmode", nil)
				}
			case "-compiler":
				if s.Value != "gc" {
					fail("nonordinary executable compiler", nil)
				}
			}
		}
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		fail("image path", err)
	}
	r.ProviderPath = binary
	image, err := os.ReadFile(binary)
	if err != nil {
		fail("read image", err)
	}
	digest := sha256.Sum256(image)
	r.ProviderSHA256, r.ProviderBytes = hex.EncodeToString(digest[:]), len(image)
	if r.ProviderSHA256 != privateChainOriginalImageSHA256 || r.ProviderBytes != 5327126 {
		fail("not the exact historical original WP13 image", nil)
	}
	r.TestExecutablePath, err = os.Executable()
	if err != nil {
		fail("executable path", err)
	}
	self, err := os.ReadFile(r.TestExecutablePath)
	if err != nil {
		fail("read executable", err)
	}
	digest = sha256.Sum256(self)
	r.TestExecutableSHA256, r.TestExecutableBytes = hex.EncodeToString(digest[:]), len(self)
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		fail("repository", err)
	}
	r.EvidenceRoot, err = os.MkdirTemp(parent, "chain-")
	if err != nil {
		fail("evidence root", err)
	}
	r.AuthorityRoot, r.RuntimeRoot = filepath.Join(r.EvidenceRoot, "authority"), filepath.Join(r.EvidenceRoot, "runtime")
	for _, path := range []string{r.AuthorityRoot, r.RuntimeRoot} {
		if err := os.Mkdir(path, 0700); err != nil {
			fail("private root", err)
		}
	}
	dataInfo, err := os.Stat(r.AuthorityRoot)
	if err != nil {
		fail("data root identity", err)
	}
	observer := &publicationProofFixture{} // ACK recorder only; NEVER a Host/provider substitute.
	config := Config{
		ControlRoot: r.AuthorityRoot, HostAuthorityID: "host-retained-private-chain", AuthorityBindingOperationID: "operation-retained-private-chain-binding",
		M1AdoptionRecordPath:                 filepath.Join(repository, "docs/programs/evidence/m1-adoption.json"),
		AssertionExtensionAdoptionRecordPath: filepath.Join(repository, "docs/programs/evidence/m1-assertion-extension-adoption.json"),
		Sidecar:                              sidecargapdb.LifecycleConfig{ExecutablePath: binary, AdmissionRecordPath: filepath.Join(repository, "docs/programs/evidence/m1-assertion-extension-admission.json"), RuntimeRoot: r.RuntimeRoot, DataDirectory: r.AuthorityRoot, ProductPolicySHA256: repeatByte('e', 64)},
		CapabilityReadiness:                  absentReadinessSource{}, DecompositionProvider: refusingDecompositionProvider{}, CandidateVerifier: refusingCandidateVerifier{}, IntegrationGit: refusingIntegrationGit{},
		PublicationBoundaryObserver: observer, MonitorInterval: time.Hour,
	}
	var database journalv2.DatabaseID
	seenEpochs := make(map[journalv2.RuntimeEpochID]bool)
	dataStable := func() bool {
		current, err := os.Stat(r.AuthorityRoot)
		return err == nil && os.SameFile(dataInfo, current) && h.owner.(*productionOwner).lifecycle.Provenance().DataDirectory == r.AuthorityRoot
	}
	for epochs := 1; epochs <= 65; epochs++ {
		if h != nil {
			closeHost()
			if t.Failed() {
				fail("restart Close failed", nil)
			}
		}
		config.Sidecar.SupervisorRunID = fmt.Sprintf("supervisor-retained-private-chain-%02d", epochs)
		h, err = Start(t.Context(), config) // ordinary admission, same durable data, fresh actual epoch
		if err != nil {
			fail(fmt.Sprintf("Start epoch%d", epochs), err)
		}
		if epochs == 1 {
			database = h.DatabaseID()
		}
		p := h.owner.(*productionOwner).lifecycle.Provenance()
		e := privateChainEpoch{Number: epochs, SupervisorRunID: p.SupervisorRunID, RuntimeEpochID: h.RuntimeEpochID(), DatabaseID: h.DatabaseID(), DataDirectory: p.DataDirectory, RuntimeDirectory: p.RuntimeDirectory, AdmissionSHA256: p.AdmissionRecordSHA256, Attribution: h.projection.Attribution(), FreshEpoch: h.RuntimeEpochID() != "" && !seenEpochs[h.RuntimeEpochID()], DatabaseStable: database != "" && h.DatabaseID() == database && p.DatabaseID == string(database), DataDirectoryStable: dataStable(), Ready: h.State() == HostReady}
		r.Epochs = append(r.Epochs, e)
		if !e.FreshEpoch || !e.DatabaseStable || !e.DataDirectoryStable || !e.Ready || e.SupervisorRunID != config.Sidecar.SupervisorRunID || len(observer.observerACKs) != 0 || e.Attribution.DatabaseID != database || e.Attribution.RuntimeEpochID != e.RuntimeEpochID {
			fail("restart identity/readiness/ACK invariant", nil)
		}
		seenEpochs[e.RuntimeEpochID] = true
		if epochs < 64 {
			continue // no VerifyRetainedRecords warmup, probe, or sample on earlier epochs
		}
		c := &r.Cases[epochs-64]
		baseline, projectionObject := h.projection.Attribution(), h.projection
		for i := 0; i < privateChainSamples; i++ {
			before, stateBefore := h.projection.Attribution(), h.State()
			ctx, acknowledgements := t.Context(), observer.observerACKs
			start := time.Now()
			proof, proofErr := h.VerifyRetainedRecords(ctx, acknowledgements)
			duration := time.Since(start)
			// EVERYTHING below is outside the measured call, including validation.
			after, stateAfter := h.projection.Attribution(), h.State()
			class := privateChainObservedClass(proof, proofErr)
			s := privateChainSample{Index: i, Nanoseconds: int64(duration), ObservedClass: class, Matched: class == "matched", ErrorPresent: proofErr != nil, ErrorType: fmt.Sprintf("%T", proofErr), ProofZero: proof == (RetainedRecordsProof{}), PublicationRows: proof.MatchedPublicationRecords(), PrivateRows: proof.MatchedPrivateRecords(), TotalRows: proof.MatchedRecords(), ACKs: len(observer.observerACKs), Before: before, After: after, ProofAttribution: proof.Attribution(), StateBefore: stateBefore, StateAfter: stateAfter, ProjectionObjectSame: h.projection == projectionObject, BaselineSame: before == baseline && after == baseline, DatabaseStable: h.DatabaseID() == database, DataDirectoryStable: dataStable(), EpochStable: h.RuntimeEpochID() == e.RuntimeEpochID}
			s.ExpectedOutcomeMet = privateChainExpected(*c, s)
			if !s.ExpectedOutcomeMet {
				s.FailureClass = "unexpected_outcome_or_invariant"
			}
			c.Samples = append(c.Samples, s) // append BEFORE any Fatal; retain failing duration/class/invariants
			if !s.ExpectedOutcomeMet {
				fail("sample failed: "+c.Name+"/"+fmt.Sprint(i)+"/"+class, nil)
			}
		}
	}
}

func TestPrivateChainWorkloadConfiguration(t *testing.T) {
	ordinary := map[string]string{"GOAMD64": "v1", "CGO_ENABLED": "1"}
	if enabled, err := privateChainConfiguration("", "", nil, "wrong", "wrong", "wrong"); enabled || err != nil {
		t.Fatal("ordinary suite must skip without both opt-ins")
	}
	for _, pair := range [][2]string{{"image", ""}, {"", "report"}} {
		if enabled, err := privateChainConfiguration(pair[0], pair[1], ordinary, "go1.26.7", "linux", "amd64"); enabled || err == nil {
			t.Fatal("partial configuration accepted")
		}
	}
	if enabled, err := privateChainConfiguration("image", "report", ordinary, "go1.26.7", "linux", "amd64"); !enabled || err != nil {
		t.Fatal("ordinary profile refused", err)
	}
	for key, value := range map[string]string{"GOAMD64": "v3", "CGO_ENABLED": "0", "GOFLAGS": "-race", "GOEXPERIMENT": "x", "GODEBUG": "gctrace=1"} {
		bad := map[string]string{"GOAMD64": "v1", "CGO_ENABLED": "1", key: value}
		if enabled, err := privateChainConfiguration("image", "report", bad, "go1.26.7", "linux", "amd64"); enabled || err == nil {
			t.Fatal("nonordinary profile accepted", key)
		}
	}
	for _, v := range [][3]string{{"go1.27.2", "linux", "amd64"}, {"go1.26.7", "darwin", "amd64"}, {"go1.26.7", "linux", "arm64"}} {
		if enabled, err := privateChainConfiguration("image", "report", ordinary, v[0], v[1], v[2]); enabled || err == nil {
			t.Fatal("unpinned platform accepted")
		}
	}
}

func TestPrivateChainWorkloadOutcomeAndFinalization(t *testing.T) {
	attr := projection.AttributionV2{DatabaseID: "db-control", RuntimeEpochID: "epoch-control", Revision: 1, DurableThrough: 1, CursorToken: journalv2.Digest{1}}
	proof := RetainedRecordsProof{attribution: attr, private: 195}
	for _, tc := range []struct {
		proof RetainedRecordsProof
		err   error
		class string
	}{
		{proof, nil, "matched"}, {RetainedRecordsProof{}, proofIncomplete("retained_records"), "sanitized_refusal"},
		{RetainedRecordsProof{}, nil, "zero_success"}, {proof, proofIncomplete("retained_records"), "nonzero_error_proof"},
		{RetainedRecordsProof{}, context.Canceled, "canceled"}, {RetainedRecordsProof{}, context.DeadlineExceeded, "deadline_exceeded"},
		{RetainedRecordsProof{}, proofIncomplete("retained_private_records"), "other_error"},
		{RetainedRecordsProof{}, fmt.Errorf("content: %w", proofIncomplete("retained_records")), "other_error"},
	} {
		if got := privateChainObservedClass(tc.proof, tc.err); got != tc.class {
			t.Fatalf("class=%s want=%s", got, tc.class)
		}
	}
	r := privateChainReport{Cases: privateChainCases()}
	for i := 1; i <= 65; i++ {
		r.Epochs = append(r.Epochs, privateChainEpoch{Number: i, RuntimeEpochID: journalv2.RuntimeEpochID(fmt.Sprint(i)), FreshEpoch: true, DatabaseStable: true, DataDirectoryStable: true, Ready: true, Closed: true})
	}
	for i := range r.Cases {
		c := &r.Cases[i]
		for j := 0; j < privateChainSamples; j++ {
			s := privateChainSample{Index: j, Nanoseconds: int64(j + 1), ObservedClass: c.ExpectedOutcome, Before: attr, After: attr, StateBefore: HostReady, StateAfter: HostReady, ProjectionObjectSame: true, BaselineSame: true, DatabaseStable: true, DataDirectoryStable: true, EpochStable: true}
			if i == 0 {
				s.Matched, s.PrivateRows, s.TotalRows, s.ProofAttribution = true, 195, 195, attr
			} else {
				s.ProofZero, s.ErrorPresent = true, true
			}
			s.ExpectedOutcomeMet = privateChainExpected(*c, s)
			if !s.ExpectedOutcomeMet {
				t.Fatal("expected outcome rejected")
			}
			for _, mutate := range []func(*privateChainSample){
				func(s *privateChainSample) { s.Nanoseconds = 0 }, func(s *privateChainSample) { s.ACKs = 1 },
				func(s *privateChainSample) { s.DataDirectoryStable = false }, func(s *privateChainSample) { s.DatabaseStable = false },
				func(s *privateChainSample) { s.StateAfter = HostDegraded }, func(s *privateChainSample) { s.After.Revision++ },
				func(s *privateChainSample) { s.PrivateRows++ }, func(s *privateChainSample) { s.ObservedClass = "deadline_exceeded" },
				func(s *privateChainSample) { s.Matched = !s.Matched }, func(s *privateChainSample) { s.ProofZero = !s.ProofZero },
			} {
				bad := s
				mutate(&bad)
				if privateChainExpected(*c, bad) {
					t.Fatal("bad observation accepted")
				}
			}
			c.Samples = append(c.Samples, s)
		}
	}
	privateChainFinalize(&r, false, true)
	if !r.Complete || r.Cases[1].Samples[0].Matched || r.Cases[0].Statistics.P95 != 29 || r.Cases[1].Statistics.Samples != 30 || r.WholeMemoryQualified || r.WholeLayerQualified || r.GenuineExpiryQualified || r.PerformanceSLOQualified {
		t.Fatal("finalizer confused refusal/match/completeness/qualification")
	}
	for _, mutate := range []func(*privateChainReport){
		func(r *privateChainReport) { r.Cases = r.Cases[:1] },
		func(r *privateChainReport) { r.Cases[1].Samples = r.Cases[1].Samples[:29] },
		func(r *privateChainReport) { r.Cases[1].Samples[0].Index = 1 },
		func(r *privateChainReport) { r.Cases[1].Samples[0].ObservedClass = "other_error" },
		func(r *privateChainReport) { r.Failures = []string{"close failed"} },
		func(r *privateChainReport) { r.Epochs[64].Closed = false },
		func(r *privateChainReport) { r.Epochs[64].RuntimeEpochID = r.Epochs[0].RuntimeEpochID },
	} {
		encoded, _ := json.Marshal(r)
		var bad privateChainReport
		if err := json.Unmarshal(encoded, &bad); err != nil {
			t.Fatal(err)
		}
		mutate(&bad)
		privateChainFinalize(&bad, false, true)
		if bad.Complete {
			t.Fatal("invalid report finalized complete")
		}
	}
	privateChainFinalize(&r, false, false)
	if r.Complete {
		t.Fatal("pre-close prefix cannot be complete")
	}
	privateChainFinalize(&r, true, true)
	if r.Complete {
		t.Fatal("failed test cannot be complete")
	}
}

func TestPrivateChainWorkloadExclusiveReport(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "report.json")
	out, gotParent, err := privateChainReserve(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	info, err := out.Stat()
	if err != nil || info.Mode().Perm() != 0600 || gotParent != parent {
		t.Fatal("wrong report permissions/parent", err)
	}
	if other, _, err := privateChainReserve(path); err == nil {
		other.Close()
		t.Fatal("overwrote reserved report")
	}
	r := privateChainReport{Stage: "pre_close_prefix", Cases: privateChainCases(), Failures: []string{"sample failure preserved"}}
	if err := privateChainWrite(out, r); err != nil {
		t.Fatal(err)
	}
	read := func() privateChainReport {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var got privateChainReport
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := read(); got.Complete || !reflect.DeepEqual(got.Failures, r.Failures) {
		t.Fatal("prefix evidence lost")
	}
	r.Stage = "final"
	r.Failures = append(r.Failures, "Close failure preserved")
	if err := privateChainWrite(out, r); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Complete || !reflect.DeepEqual(got.Failures, r.Failures) {
		t.Fatal("final close evidence lost")
	}
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	if f, _, err := privateChainReserve(filepath.Join(parent, "unsafe.json")); err == nil {
		f.Close()
		t.Fatal("nonprivate parent accepted")
	}
}
