package productionruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// Reporting helpers and this opt-in fixture are TEST ONLY. Latency observations
// neither replace causal allocation accounting nor establish a universal SLO.
type retainedPerformanceStats struct {
	Samples int    `json:"samples"`
	Min     uint64 `json:"min_ns"`
	Median  uint64 `json:"median_nearest_rank_ns"`
	P95     uint64 `json:"p95_nearest_rank_ns"`
	Max     uint64 `json:"max_ns"`
	Total   uint64 `json:"total_ns"`
}

func retainedPerformanceStatistics(input []time.Duration) (retainedPerformanceStats, bool) {
	if len(input) == 0 {
		return retainedPerformanceStats{}, false
	}
	values := append([]time.Duration(nil), input...)
	var total uint64
	for _, d := range values {
		if d <= 0 || uint64(d) > math.MaxUint64-total {
			return retainedPerformanceStats{}, false
		}
		total += uint64(d)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	// Integer nearest rank without n*95 or n+1 overflow. n is slice length.
	n := len(values)
	medianIndex := n/2 + n%2 - 1
	p95Index := n - n/20 - 1 // ceil(19*n/20)-1
	return retainedPerformanceStats{n, uint64(values[0]), uint64(values[medianIndex]), uint64(values[p95Index]), uint64(values[n-1]), total}, true
}

func TestRetainedPerformanceStatistics(t *testing.T) {
	for _, invalid := range [][]time.Duration{nil, {0}, {-1}, {1, 0}, {time.Duration(math.MaxInt64), time.Duration(math.MaxInt64), 2}} {
		if got, ok := retainedPerformanceStatistics(invalid); ok || got != (retainedPerformanceStats{}) {
			t.Fatal("invalid duration/overflow produced statistics")
		}
	}
	for _, n := range []int{1, 2, 19, 20, 21, 30, 100} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			values := make([]time.Duration, n)
			for i := range values {
				values[i] = time.Duration(n - i)
			}
			before := append([]time.Duration(nil), values...)
			got, ok := retainedPerformanceStatistics(values)
			if !ok || !reflect.DeepEqual(values, before) || got.Samples != n || got.Min != 1 || got.Max != uint64(n) || got.Total != uint64(n*(n+1)/2) || got.Median != uint64((n+1)/2) || got.P95 != uint64((95*n+99)/100) {
				t.Fatal("nearest-rank/ownership error", got)
			}
		})
	}
}

type retainedPerformanceSample struct {
	Index        int    `json:"index"`
	Nanoseconds  int64  `json:"nanoseconds"`
	Matched      bool   `json:"matched"`
	FailureClass string `json:"failure_class,omitempty"`
}

type retainedPerformanceCase struct {
	Name            string                      `json:"name"`
	Epochs          int                         `json:"epochs"`
	ACKs            int                         `json:"actual_acks"`
	PublicationRows uint64                      `json:"matched_publication_rows"`
	PrivateRows     uint64                      `json:"matched_private_rows"`
	Samples         []retainedPerformanceSample `json:"samples"`
	Statistics      retainedPerformanceStats    `json:"statistics"`
}

type retainedPerformanceReport struct {
	Schema                  int                       `json:"schema_version"`
	Scope                   string                    `json:"scope"`
	GoVersion               string                    `json:"go_version"`
	GOOS                    string                    `json:"goos"`
	GOARCH                  string                    `json:"goarch"`
	ProviderSHA256          string                    `json:"provider_sha256"`
	ProviderBytes           int                       `json:"provider_bytes"`
	TestExecutableSHA256    string                    `json:"test_executable_sha256"`
	Cases                   []retainedPerformanceCase `json:"cases"`
	Complete                bool                      `json:"complete"`
	WholeLayerQualified     bool                      `json:"whole_layer_qualified"`
	WholeMemoryQualified    bool                      `json:"whole_memory_qualified"`
	GenuineExpiryQualified  bool                      `json:"genuine_expiry_qualified"`
	PerformanceSLOQualified bool                      `json:"performance_slo_qualified"`
	Disclosures             []string                  `json:"disclosures"`
}

// Exact historical WP13 admission through ordinary Start. New work measures the
// ACTUAL Host method, not the pure builder or separate-image adapter. Thirty
// observations/case, first INCLUDED, no discarded failures, retries, warmup,
// forced GC, allocation instrumentation, timers/cancellation injected by caller,
// mutation/concurrency during sampling or benchmark calibration. Restarts and
// publication setup are outside the measured method. This does not cover large
// payload/maximum work/private-chain workloads or define an unstated latency SLO.
func TestRetainedPerformanceOriginalHostNative(t *testing.T) {
	binary := os.Getenv("SPEC_KITTY_WP13_GAPDBD")
	reportPath := os.Getenv("SPEC_KITTY_OBSERVATION_PERFORMANCE_REPORT")
	if binary == "" && reportPath == "" {
		t.Skip("opt in with exact WP13 image and exclusive performance report path")
	}
	if binary == "" || reportPath == "" {
		t.Fatal("both exact image and report path are required")
	}
	if runtime.Version() != "go1.26.7" || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("performance profile requires Go1.26.7/linux/amd64")
	}
	for _, name := range []string{"GOFLAGS", "GOEXPERIMENT", "GODEBUG"} {
		if os.Getenv(name) != "" {
			t.Fatal("nonordinary build/runtime profile", name)
		}
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(image)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	selfBytes, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	selfDigest := sha256.Sum256(selfBytes)
	report := retainedPerformanceReport{Schema: 1, Scope: "original WP13 genuine Host; four small initial/restarted workloads ONLY", GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, ProviderSHA256: hex.EncodeToString(digest[:]), ProviderBytes: len(image), TestExecutableSHA256: hex.EncodeToString(selfDigest[:]), Disclosures: []string{
		"Timing covers VerifyRetainedRecords call only, not setup/restart/publication/teardown/assertions/reporting; first call included; no samples discarded.",
		"Thirty sequential non-race observations per workload; no intentional background load; ordinary GC/scheduler/Host monitor effects not subtracted.",
		"Parent command/source/toolchain custody is REQUIRED; this self-reported executable/image hash alone is not source qualification.",
		"No memory/alloc/RSS measurement; no maximum-work/large-value/private64-chain, concurrent-load, expiry-image or universal latency-SLO qualification.",
		"Existing cooperative deadlines remain; no hard-wall guarantee follows from the observed durations.",
	}}
	// Reserve one new report pathname BEFORE starting any provider. Save all
	// observations (including a failing prefix) before test cleanup via defer.
	out, err := os.OpenFile(reportPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := json.NewEncoder(out).Encode(report); err != nil {
			t.Error("write performance report", err)
		}
		if err := out.Close(); err != nil {
			t.Error("close performance report", err)
		}
	}()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, publications := range []bool{false, true} {
		kind := "private_only"
		if publications {
			kind = "actual_ACK_overwrite"
		}
		t.Run(kind, func(t *testing.T) {
			root, err := os.MkdirTemp("", "spk-rperf-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			runtimeRoot := filepath.Join(root, "runtime")
			if err := os.Mkdir(runtimeRoot, 0700); err != nil {
				t.Fatal(err)
			}
			observer := &publicationProofFixture{}
			config := Config{ControlRoot: filepath.Join(root, "authority"), HostAuthorityID: "host-retained-perf", AuthorityBindingOperationID: "operation-retained-perf-binding", M1AdoptionRecordPath: filepath.Join(repository, "docs/programs/evidence/m1-adoption.json"), AssertionExtensionAdoptionRecordPath: filepath.Join(repository, "docs/programs/evidence/m1-assertion-extension-adoption.json"), Sidecar: sidecargapdb.LifecycleConfig{ExecutablePath: binary, AdmissionRecordPath: filepath.Join(repository, "docs/programs/evidence/m1-assertion-extension-admission.json"), RuntimeRoot: runtimeRoot, DataDirectory: filepath.Join(root, "authority"), SupervisorRunID: "supervisor-retained-perf-initial", ProductPolicySHA256: repeatByte('e', 64)}, CapabilityReadiness: absentReadinessSource{}, DecompositionProvider: refusingDecompositionProvider{}, CandidateVerifier: refusingCandidateVerifier{}, IntegrationGit: refusingIntegrationGit{}, PublicationBoundaryObserver: observer, MonitorInterval: time.Hour}
			if publications {
				config.Reconciler = &missionCreatingReconciler{}
			}
			h, err := Start(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = h.Close(context.Background()) })
			if publications {
				registry := journalv2.DefaultEventRegistryV2()
				reducers, err := BuildReducerSetV2(registry)
				if err != nil {
					t.Fatal(err)
				}
				tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
				if err != nil {
					t.Fatal(err)
				}
				coordinator, err := journalv2.NewPublicationCoordinatorWithObserverAndSinkV2(h.owner.(*productionOwner).adapter, tracker, observer, &liveProofSink{host: h})
				if err != nil {
					t.Fatal(err)
				}
				first := publishLiveProofOperation(t, h, coordinator, nil)
				_ = publishLiveProofOperation(t, h, coordinator, &first)
				if len(observer.observerACKs) < 3 {
					t.Fatal("actual ACK setup incomplete")
				}
			}
			for epochs := 1; epochs <= 2; epochs++ {
				if epochs == 2 {
					oldEpoch := h.RuntimeEpochID()
					if err := h.Close(t.Context()); err != nil {
						t.Fatal(err)
					}
					config.Reconciler = nil
					config.Sidecar.SupervisorRunID = "supervisor-retained-perf-restarted"
					h, err = Start(t.Context(), config)
					if err != nil {
						t.Fatal(err)
					}
					if h.RuntimeEpochID() == oldEpoch {
						t.Fatal("restart did not rotate actual epoch")
					}
				}
				report.Cases = append(report.Cases, retainedPerformanceCase{Name: fmt.Sprintf("%s/epochs_%d", kind, epochs), Epochs: epochs, ACKs: len(observer.observerACKs)})
				c := &report.Cases[len(report.Cases)-1]
				before := h.projection.Attribution()
				durations := make([]time.Duration, 0, 30)
				for i := 0; i < 30; i++ {
					start := time.Now()
					proof, err := h.VerifyRetainedRecords(t.Context(), observer.observerACKs)
					d := time.Since(start)
					matched := err == nil && proof.Attribution() == before && proof.MatchedPrivateRecords() == uint64(3*epochs+3) && proof.MatchedRecords() == proof.MatchedPublicationRecords()+proof.MatchedPrivateRecords() && h.projection.Attribution() == before && h.State() == HostReady && ((publications && proof.MatchedPublicationRecords() > 0) || (!publications && proof.MatchedPublicationRecords() == 0))
					if i > 0 && (proof.MatchedPublicationRecords() != c.PublicationRows || proof.MatchedPrivateRecords() != c.PrivateRows) {
						matched = false
					}
					sample := retainedPerformanceSample{Index: i, Nanoseconds: int64(d), Matched: matched}
					if !matched {
						sample.FailureClass = "proof_error_or_invariant_mismatch"
					}
					c.Samples = append(c.Samples, sample)
					if !matched {
						t.Fatal("performance sample refused or changed attribution; prefix preserved")
					}
					c.PublicationRows, c.PrivateRows = proof.MatchedPublicationRecords(), proof.MatchedPrivateRecords()
					durations = append(durations, d)
				}
				var ok bool
				c.Statistics, ok = retainedPerformanceStatistics(durations)
				if !ok {
					t.Fatal("invalid measurement statistics")
				}
			}
			if err := h.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
	report.Complete = !t.Failed() && len(report.Cases) == 4
}
