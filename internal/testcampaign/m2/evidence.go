package m2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

const (
	ManifestName = "manifest.json"
	CensusName   = "census.json"
	maximumFile  = 64 << 20
	maximumFiles = 2048
	maximumTotal = 256 << 20

	AdmittedM1AdoptionSHA256            = "b6506abc327ed2b22c6b51b4069cb0b8edb0af5ac2ea26a1aa3a33de6bca30de"
	AdmittedM1EvidenceSHA256            = "397f93a9b0f1ea24715ba57b0cb58aeb489e13a7e27e8bc76259597f36f2f309"
	AdmittedM1TupleSHA256               = "4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d"
	AdmittedM1SidecarSHA256             = "947bda324a550f4a7534e4efec2a75485e95b4c27907fd0298f72aba247dadad"
	QualificationCandidateSidecarSHA256 = "366a893d36c7c60282e84f43e67674c5d5e9a63b6e092227fffcdf861014ce21"
)

type ArtifactInput struct {
	Path string
	Kind string
	Data []byte
}

type Publication struct {
	Directory             string `json:"directory"`
	ManifestPath          string `json:"manifest_path"`
	ManifestSHA256        string `json:"manifest_sha256"`
	ProducerArchivePath   string `json:"producer_archive_path"`
	ProducerArchiveSHA256 string `json:"producer_archive_sha256"`
	ProducerReceiptPath   string `json:"producer_receipt_path"`
}

type Verification struct {
	RunID          string `json:"run_id"`
	SourceCommit   string `json:"source_commit"`
	SourceTree     string `json:"source_tree"`
	ManifestSHA256 string `json:"manifest_sha256"`
	Verdict        string `json:"verdict"`
	ReviewEligible bool   `json:"review_eligible"`
	ReleaseReady   bool   `json:"release_ready"`
	Adopted        bool   `json:"adopted"`
}

type evidenceVerificationPolicy struct {
	qualificationSidecarSHA256 string
	verifyCandidateAdmission   func(string) error
}

func frozenEvidenceVerificationPolicy() evidenceVerificationPolicy {
	return evidenceVerificationPolicy{
		qualificationSidecarSHA256: QualificationCandidateSidecarSHA256,
		verifyCandidateAdmission: func(path string) error {
			_, err := sidecargapdb.VerifyM2QualificationCandidateAdmission(path)
			return err
		},
	}
}

type RawPerformance struct {
	SchemaVersion            int                     `json:"schema_version"`
	RunID                    string                  `json:"run_id"`
	SourceCommit             string                  `json:"source_commit"`
	SourceTree               string                  `json:"source_tree"`
	ReferenceProfileSHA256   string                  `json:"reference_profile_sha256"`
	Environment              PerformanceEnvironment  `json:"environment"`
	Dataset                  PerformanceDataset      `json:"dataset"`
	RecoveryOperations       int                     `json:"recovery_operations"`
	RecoveryMS               []float64               `json:"recovery_ms"`
	QueryMS                  []float64               `json:"query_ms"`
	MutationMS               []float64               `json:"mutation_ms"`
	Samples                  []PerformanceSample     `json:"samples"`
	Statistics               []PerformanceStatistics `json:"statistics"`
	WarmupExcluded           int                     `json:"warmup_excluded"`
	ReplayExcluded           int                     `json:"replay_excluded"`
	RuntimeBootstrapExcluded int                     `json:"runtime_bootstrap_excluded"`
	Failures                 int                     `json:"failures"`
	Method                   string                  `json:"method"`
}

// PerformanceReference is the source-bound admission predicate for one
// reproducible M2 timing host. Ranges are intentional only where ambient
// conditions cannot be bit-for-bit stable; everything else is exact.
type PerformanceReference struct {
	SchemaVersion       int      `json:"schema_version"`
	CPUModel            string   `json:"cpu_model"`
	LogicalCPUs         int      `json:"logical_cpus"`
	PhysicalCores       int      `json:"physical_cores"`
	MemoryMinBytes      uint64   `json:"memory_min_bytes"`
	MemoryMaxBytes      uint64   `json:"memory_max_bytes"`
	KernelSystem        string   `json:"kernel_system"`
	KernelRelease       string   `json:"kernel_release"`
	Architecture        string   `json:"architecture"`
	Filesystem          string   `json:"filesystem"`
	StorageDevicePrefix string   `json:"storage_device_prefix"`
	PowerGovernor       string   `json:"power_governor"`
	BoostEnabled        bool     `json:"boost_enabled"`
	Load1Maximum        float64  `json:"load1_maximum"`
	GoVersion           string   `json:"go_version"`
	CGOEnabled          string   `json:"cgo_enabled"`
	DatasetGenerator    string   `json:"dataset_generator"`
	FamilyIDs           []string `json:"family_ids"`
}

type PerformanceEnvironment struct {
	CPUModel        string  `json:"cpu_model"`
	LogicalCPUs     int     `json:"logical_cpus"`
	PhysicalCores   int     `json:"physical_cores"`
	MemoryBytes     uint64  `json:"memory_bytes"`
	KernelSystem    string  `json:"kernel_system"`
	KernelRelease   string  `json:"kernel_release"`
	Architecture    string  `json:"architecture"`
	Filesystem      string  `json:"filesystem"`
	StorageDevice   string  `json:"storage_device"`
	PowerGovernor   string  `json:"power_governor"`
	BoostEnabled    bool    `json:"boost_enabled"`
	Load1           float64 `json:"load1"`
	GoVersion       string  `json:"go_version"`
	CGOEnabled      string  `json:"cgo_enabled"`
	SidecarSHA256   string  `json:"sidecar_sha256"`
	QualifierSHA256 string  `json:"qualifier_sha256"`
	ProductSHA256   string  `json:"product_sha256"`
}

type PerformanceDataset struct {
	Generator             string         `json:"generator"`
	WarmupOperations      int            `json:"warmup_operations"`
	SuffixOperations      int            `json:"suffix_operations"`
	CheckpointRevision    uint64         `json:"checkpoint_revision"`
	FamilyOperationCounts map[string]int `json:"family_operation_counts"`
	FamilyRecoveryCounts  map[string]int `json:"family_recovery_counts"`
	ExpectedProjectionSHA string         `json:"expected_projection_sha256"`
	FinalRevision         uint64         `json:"final_revision"`
}

type PerformanceSample struct {
	Index              int      `json:"index"`
	Kind               string   `json:"kind"`
	Family             string   `json:"family,omitempty"`
	OperationID        string   `json:"operation_id,omitempty"`
	EmittedFamilies    []string `json:"emitted_families,omitempty"`
	DurationNS         int64    `json:"duration_ns"`
	Included           bool     `json:"included"`
	ExclusionReason    string   `json:"exclusion_reason,omitempty"`
	ProjectionSHA256   string   `json:"projection_sha256,omitempty"`
	ObservedOperations int      `json:"observed_operations,omitempty"`
	PublicationCount   int      `json:"publication_count,omitempty"`
	ProjectionRevision uint64   `json:"projection_revision,omitempty"`
	BeforeRevision     uint64   `json:"before_revision,omitempty"`
	AfterRevision      uint64   `json:"after_revision,omitempty"`
	MutationCount      uint64   `json:"mutation_count,omitempty"`
	PublicationIDs     []string `json:"publication_ids,omitempty"`
}

type PerformanceStatistics struct {
	Kind      string  `json:"kind"`
	Count     int     `json:"count"`
	MinimumMS float64 `json:"minimum_ms"`
	MedianMS  float64 `json:"median_ms"`
	P95MS     float64 `json:"p95_ms"`
	P99MS     float64 `json:"p99_ms"`
	MaximumMS float64 `json:"maximum_ms"`
	Excluded  int     `json:"excluded"`
}

func SummarizePerformance(raw RawPerformance) (Performance, error) {
	result := Performance{
		RecoveryOperations: raw.RecoveryOperations,
		RecoveryRuns:       len(raw.RecoveryMS), RecoveryP95MS: percentile(raw.RecoveryMS, 95),
		QueryObservations: len(raw.QueryMS), QueryP95MS: percentile(raw.QueryMS, 95),
		MutationObservations: len(raw.MutationMS), MutationP95MS: percentile(raw.MutationMS, 95),
	}
	if raw.SchemaVersion != 1 || raw.RunID == "" || !isHex(raw.SourceCommit, 40) || !isHex(raw.SourceTree, 40) || !validDigest(raw.ReferenceProfileSHA256) || raw.Method != "monotonic-nearest-rank-inclusive" || raw.Failures != 0 || raw.RecoveryOperations < 10000 || len(raw.RecoveryMS) < 30 || len(raw.QueryMS) < 10000 || len(raw.MutationMS) < 10000 {
		return result, fmt.Errorf("performance observation census or identity is incomplete")
	}
	if err := validatePerformanceSamples(raw); err != nil {
		return result, err
	}
	for _, observations := range [][]float64{raw.RecoveryMS, raw.QueryMS, raw.MutationMS} {
		for _, observation := range observations {
			if observation < 0 {
				return result, fmt.Errorf("performance observation is negative")
			}
		}
	}
	if result.RecoveryP95MS > 2000 || result.QueryP95MS > 2 || result.MutationP95MS > 8 {
		return result, fmt.Errorf("performance threshold exceeded: recovery_p95_ms=%.6f limit=2000 query_p95_ms=%.6f limit=2 mutation_p95_ms=%.6f limit=8", result.RecoveryP95MS, result.QueryP95MS, result.MutationP95MS)
	}
	return result, nil
}

func VerifyPerformanceReference(referenceBytes []byte, raw RawPerformance) error {
	if raw.ReferenceProfileSHA256 != SHA256(referenceBytes) {
		return fmt.Errorf("performance reference digest differs from raw evidence")
	}
	if err := VerifyPerformanceEnvironmentReference(referenceBytes, raw.Environment); err != nil {
		return err
	}
	var reference PerformanceReference
	if err := DecodeStrict(referenceBytes, &reference); err != nil {
		return fmt.Errorf("performance reference: %w", err)
	}
	wantFamilies := make([]string, len(authorityFamilies))
	for index, definition := range authorityFamilies {
		wantFamilies[index] = definition.ID
	}
	if raw.Dataset.Generator != reference.DatasetGenerator || raw.Dataset.WarmupOperations != raw.WarmupExcluded || raw.Dataset.SuffixOperations != len(raw.MutationMS) || len(raw.Dataset.FamilyOperationCounts)+len(raw.Dataset.FamilyRecoveryCounts) != len(wantFamilies) || !validDigest(raw.Dataset.ExpectedProjectionSHA) {
		return fmt.Errorf("performance dataset differs from the source-bound reference")
	}
	for _, family := range wantFamilies {
		operations, recoveries := raw.Dataset.FamilyOperationCounts[family], raw.Dataset.FamilyRecoveryCounts[family]
		if operations <= 0 && recoveries <= 0 || operations > 0 && recoveries > 0 {
			return fmt.Errorf("performance dataset omitted authority family %s", family)
		}
	}
	if len(raw.Dataset.FamilyRecoveryCounts) != 1 || raw.Dataset.FamilyRecoveryCounts["family/runtime_host"] != len(raw.RecoveryMS) || raw.Dataset.FamilyOperationCounts["family/runtime_host"] != 0 {
		return fmt.Errorf("runtime_host must be bound only to the closed restart/recovery census")
	}
	return nil
}

// VerifyPerformanceEnvironmentReference admits the immutable pre-run host
// observation before any warmup or timed work can influence its load control.
func VerifyPerformanceEnvironmentReference(referenceBytes []byte, environment PerformanceEnvironment) error {
	var reference PerformanceReference
	if err := DecodeStrict(referenceBytes, &reference); err != nil {
		return fmt.Errorf("performance reference: %w", err)
	}
	wantFamilies := make([]string, len(authorityFamilies))
	for index, definition := range authorityFamilies {
		wantFamilies[index] = definition.ID
	}
	if reference.SchemaVersion != 1 || reference.CPUModel == "" || reference.LogicalCPUs <= 0 || reference.PhysicalCores <= 0 || reference.MemoryMinBytes == 0 || reference.MemoryMaxBytes < reference.MemoryMinBytes || reference.KernelSystem == "" || reference.KernelRelease == "" || reference.Architecture == "" || reference.Filesystem == "" || reference.StorageDevicePrefix == "" || reference.PowerGovernor == "" || reference.Load1Maximum <= 0 || reference.GoVersion != "go1.26.7" || reference.CGOEnabled != "0" || reference.DatasetGenerator == "" || !slices.Equal(reference.FamilyIDs, wantFamilies) {
		return fmt.Errorf("performance reference profile is incomplete or differs from the frozen family registry")
	}
	if environment.CPUModel != reference.CPUModel || environment.LogicalCPUs != reference.LogicalCPUs || environment.PhysicalCores != reference.PhysicalCores || environment.MemoryBytes < reference.MemoryMinBytes || environment.MemoryBytes > reference.MemoryMaxBytes || environment.KernelSystem != reference.KernelSystem || environment.KernelRelease != reference.KernelRelease || environment.Architecture != reference.Architecture || environment.Filesystem != reference.Filesystem || !strings.HasPrefix(environment.StorageDevice, reference.StorageDevicePrefix) || environment.PowerGovernor != reference.PowerGovernor || environment.BoostEnabled != reference.BoostEnabled || environment.Load1 > reference.Load1Maximum || environment.GoVersion != reference.GoVersion || environment.CGOEnabled != reference.CGOEnabled || environment.SidecarSHA256 != QualificationCandidateSidecarSHA256 || !validDigest(environment.QualifierSHA256) || !validDigest(environment.ProductSHA256) {
		return fmt.Errorf("observed performance environment is outside the source-bound reference profile")
	}
	return nil
}

const PerformanceCampaignSelectorV1 = "TestM2AcceptedAuthorityPerformanceCampaign"

var performanceTerminalMembershipMutantIDsV1 = [...]string{
	"acceptance_membership_omitted",
	"effect_membership_omitted",
	"integration_member_unbound",
	"review_membership_duplicated",
}

// PerformanceTerminalMembershipMutantIDsV1 returns the closed child-test
// census shared by the executing campaign and its receipt verifier.
func PerformanceTerminalMembershipMutantIDsV1() []string {
	return append([]string(nil), performanceTerminalMembershipMutantIDsV1[:]...)
}

// VerifyPerformanceTranscript proves the gated real-authority performance
// scenario actually ran once. The raw machine stream, rather than a producer
// summary, is the authority for run/pass/skip/fail membership.
func VerifyPerformanceTranscript(raw []byte) error {
	const selector = PerformanceCampaignSelectorV1
	counts := map[string]int{}
	childCounts := map[string]map[string]int{}
	for _, id := range performanceTerminalMembershipMutantIDsV1 {
		childCounts[selector+"/terminal-membership-mutant/"+id] = map[string]int{}
	}
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var event struct {
			Action string `json:"Action"`
			Test   string `json:"Test"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			return fmt.Errorf("performance transcript contains non-JSON event: %w", err)
		}
		if event.Test == selector {
			counts[event.Action]++
			if event.Action == "skip" || event.Action == "fail" {
				return fmt.Errorf("performance selector action=%s", event.Action)
			}
		} else if event.Test != "" && strings.HasPrefix(event.Test, selector+"/") {
			actions, ok := childCounts[event.Test]
			if !ok {
				return fmt.Errorf("performance selector emitted unexpected subtest %q", event.Test)
			}
			actions[event.Action]++
			if event.Action == "skip" || event.Action == "fail" {
				return fmt.Errorf("performance subtest %q action=%s", event.Test, event.Action)
			}
		}
	}
	if counts["run"] != 1 || counts["pass"] != 1 || counts["skip"] != 0 || counts["fail"] != 0 {
		return fmt.Errorf("performance selector receipt run=%d pass=%d skip=%d fail=%d", counts["run"], counts["pass"], counts["skip"], counts["fail"])
	}
	for child, actions := range childCounts {
		if actions["run"] != 1 || actions["pass"] != 1 || actions["skip"] != 0 || actions["fail"] != 0 {
			return fmt.Errorf("performance subtest %q receipt run=%d pass=%d skip=%d fail=%d", child, actions["run"], actions["pass"], actions["skip"], actions["fail"])
		}
	}
	return nil
}

func validatePerformanceSamples(raw RawPerformance) error {
	wantTotal := raw.WarmupExcluded + raw.ReplayExcluded + raw.RuntimeBootstrapExcluded + len(raw.MutationMS) + len(raw.QueryMS) + len(raw.RecoveryMS)
	if len(raw.Samples) != wantTotal || raw.Dataset.SuffixOperations != len(raw.MutationMS) || raw.RecoveryOperations != raw.Dataset.SuffixOperations || raw.WarmupExcluded < 0 || raw.RuntimeBootstrapExcluded < 0 {
		return fmt.Errorf("performance raw sample census is incomplete")
	}
	observed := map[string][]float64{"mutation": {}, "query": {}, "recovery": {}}
	excluded := map[string]int{}
	familyCounts := map[string]int{}
	recoveryFamilyCounts := map[string]int{}
	operationIDs := map[string]struct{}{}
	publicationIDs := map[string]struct{}{}
	for index, sample := range raw.Samples {
		if sample.Index != index || sample.DurationNS < 0 {
			return fmt.Errorf("performance sample %d has invalid identity or duration", index)
		}
		if _, ok := observed[sample.Kind]; !ok {
			return fmt.Errorf("performance sample %d has unknown kind %q", index, sample.Kind)
		}
		if !sample.Included {
			if sample.Kind != "mutation" || sample.ExclusionReason != "warmup" && sample.ExclusionReason != "idempotent_replay" && sample.ExclusionReason != "runtime_bootstrap" {
				return fmt.Errorf("performance sample %d has unapproved exclusion", index)
			}
			if sample.ExclusionReason == "warmup" && (sample.Family != "" || len(sample.EmittedFamilies) != 0 || sample.BeforeRevision != 0 || sample.AfterRevision != 0 || sample.ProjectionRevision != 0 || sample.MutationCount != 0 || sample.PublicationCount != 0 || len(sample.PublicationIDs) != 0) {
				return fmt.Errorf("performance warmup sample %d claims post-checkpoint authority evidence", index)
			}
			if sample.ExclusionReason == "idempotent_replay" && (sample.AfterRevision != sample.BeforeRevision || sample.MutationCount != 0 || sample.PublicationCount != 0 || len(sample.PublicationIDs) != 0 || len(sample.EmittedFamilies) != 0) {
				return fmt.Errorf("performance replay sample %d fabricates a durable acknowledgement", index)
			}
			if sample.ExclusionReason == "runtime_bootstrap" && (sample.Family != "" || len(sample.EmittedFamilies) != 0 || sample.BeforeRevision != 0 || sample.AfterRevision != 0 || sample.ProjectionRevision != 0 || sample.MutationCount != 0 || sample.PublicationCount != 0 || len(sample.PublicationIDs) != 0) {
				return fmt.Errorf("performance runtime bootstrap sample %d fabricates journal authority evidence", index)
			}
			excluded[sample.Kind]++
			excluded[sample.ExclusionReason]++
			continue
		}
		if sample.Kind == "mutation" {
			if len(sample.PublicationIDs) == 0 || len(sample.EmittedFamilies) == 0 || sample.MutationCount == 0 || sample.AfterRevision <= sample.BeforeRevision || sample.ProjectionRevision != sample.AfterRevision {
				return fmt.Errorf("performance mutation sample %d lacks acknowledged publication evidence", index)
			}
			if _, duplicate := operationIDs[sample.OperationID]; duplicate || sample.OperationID == "" || sample.PublicationCount != len(sample.PublicationIDs) || !slices.IsSorted(sample.PublicationIDs) || !slices.IsSorted(sample.EmittedFamilies) || !slices.Contains(sample.EmittedFamilies, sample.Family) {
				return fmt.Errorf("performance mutation sample %d has duplicate or noncanonical authority identity", index)
			}
			operationIDs[sample.OperationID] = struct{}{}
			for _, publication := range sample.PublicationIDs {
				if publication == "" {
					return fmt.Errorf("performance mutation sample %d has empty publication identity", index)
				}
				if _, duplicate := publicationIDs[publication]; duplicate {
					return fmt.Errorf("performance publication %q is duplicated", publication)
				}
				publicationIDs[publication] = struct{}{}
			}
		}
		if sample.Kind == "recovery" && (sample.Family != "family/runtime_host" || !slices.Equal(sample.EmittedFamilies, []string{"family/runtime_host"}) || sample.ObservedOperations != raw.RecoveryOperations || !validDigest(sample.ProjectionSHA256)) {
			return fmt.Errorf("performance recovery sample %d lacks runtime_host restart evidence", index)
		}
		if sample.ExclusionReason != "" {
			return fmt.Errorf("included performance sample %d has an exclusion reason", index)
		}
		observed[sample.Kind] = append(observed[sample.Kind], float64(sample.DurationNS)/float64(time.Millisecond))
		if sample.Kind == "mutation" {
			for _, family := range sample.EmittedFamilies {
				familyCounts[family]++
			}
		} else if sample.Kind == "recovery" {
			recoveryFamilyCounts[sample.Family]++
		}
	}
	if excluded["warmup"] != raw.WarmupExcluded || excluded["idempotent_replay"] != raw.ReplayExcluded || excluded["runtime_bootstrap"] != raw.RuntimeBootstrapExcluded || !slices.Equal(observed["mutation"], raw.MutationMS) || !slices.Equal(observed["query"], raw.QueryMS) || !slices.Equal(observed["recovery"], raw.RecoveryMS) {
		return fmt.Errorf("performance arrays are not a projection of the retained raw samples")
	}
	if len(familyCounts) != len(raw.Dataset.FamilyOperationCounts) {
		return fmt.Errorf("performance family census differs from retained suffix samples")
	}
	for family, count := range raw.Dataset.FamilyOperationCounts {
		if familyCounts[family] != count {
			return fmt.Errorf("performance family %s count=%d want=%d", family, familyCounts[family], count)
		}
	}
	if len(recoveryFamilyCounts) != len(raw.Dataset.FamilyRecoveryCounts) {
		return fmt.Errorf("performance recovery family census differs from retained samples")
	}
	for family, count := range raw.Dataset.FamilyRecoveryCounts {
		if recoveryFamilyCounts[family] != count {
			return fmt.Errorf("performance recovery family %s count=%d want=%d", family, recoveryFamilyCounts[family], count)
		}
	}
	wantStatistics := []PerformanceStatistics{
		performanceStatistics("mutation", raw.MutationMS, excluded["mutation"]),
		performanceStatistics("query", raw.QueryMS, 0),
		performanceStatistics("recovery", raw.RecoveryMS, 0),
	}
	if !slices.Equal(raw.Statistics, wantStatistics) {
		return fmt.Errorf("performance statistics differ from retained raw samples")
	}
	return nil
}

func performanceStatistics(kind string, values []float64, excluded int) PerformanceStatistics {
	result := PerformanceStatistics{Kind: kind, Count: len(values), Excluded: excluded}
	if len(values) == 0 {
		return result
	}
	ordered := append([]float64(nil), values...)
	slices.Sort(ordered)
	result.MinimumMS = ordered[0]
	result.MedianMS = percentile(ordered, 50)
	result.P95MS = percentile(ordered, 95)
	result.P99MS = percentile(ordered, 99)
	result.MaximumMS = ordered[len(ordered)-1]
	return result
}

func PerformanceStatisticsFor(kind string, values []float64, excluded int) PerformanceStatistics {
	return performanceStatistics(kind, values, excluded)
}

func percentile(values []float64, percent int) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]float64(nil), values...)
	slices.Sort(ordered)
	index := (percent*len(ordered) + 99) / 100
	if index < 1 {
		index = 1
	}
	return ordered[index-1]
}

func Publish(outputRoot string, manifest Manifest, census Census, inputs []ArtifactInput) (Publication, error) {
	return publishWithPolicy(outputRoot, manifest, census, inputs, frozenEvidenceVerificationPolicy())
}

func publishWithPolicy(outputRoot string, manifest Manifest, census Census, inputs []ArtifactInput, policy evidenceVerificationPolicy) (Publication, error) {
	if !filepath.IsAbs(outputRoot) || manifest.RunID == "" || census.RunID != manifest.RunID || census.SourceCommit != manifest.Source.Commit || census.SourceTree != manifest.Source.Tree {
		return Publication{}, fmt.Errorf("absolute output root and one RunID/source identity are required")
	}
	if len(inputs)+1 > maximumFiles {
		return Publication{}, fmt.Errorf("artifact census exceeds %d", maximumFiles)
	}
	attempt, err := ReserveAttempt(outputRoot, manifest.RunID, manifest.Source)
	if err != nil {
		return Publication{}, err
	}
	return publishReservedWithPolicy(attempt, manifest, census, inputs, policy)
}

func PublishReserved(attempt *Attempt, manifest Manifest, census Census, inputs []ArtifactInput) (Publication, error) {
	return publishReservedWithPolicy(attempt, manifest, census, inputs, frozenEvidenceVerificationPolicy())
}

func publishReservedWithPolicy(attempt *Attempt, manifest Manifest, census Census, inputs []ArtifactInput, policy evidenceVerificationPolicy) (Publication, error) {
	if attempt == nil || attempt.RunID != manifest.RunID || attempt.Source != manifest.Source {
		return Publication{}, fmt.Errorf("reserved attempt differs from manifest")
	}
	target := attempt.Directory
	events, terminal, err := attempt.TerminalBytes("pass", nil)
	if err != nil {
		return Publication{}, err
	}
	inputs = append(inputs,
		ArtifactInput{Path: AttemptEventsName, Kind: "attempt-index", Data: events},
		ArtifactInput{Path: "terminal.json", Kind: "attempt-terminal", Data: terminal},
	)

	manifest.Artifacts = nil
	paths := make(map[string]struct{}, len(inputs)+1)
	totalBytes := 0
	for _, input := range inputs {
		totalBytes += len(input.Data)
		if !safeRelativePath(input.Path) || !validKind(input.Kind) || len(input.Data) > maximumFile || totalBytes > maximumTotal || input.Path == ManifestName || input.Path == CensusName {
			return Publication{}, fmt.Errorf("unsafe or oversized artifact %q", input.Path)
		}
		if _, duplicate := paths[input.Path]; duplicate {
			return Publication{}, fmt.Errorf("duplicate artifact path %q", input.Path)
		}
		paths[input.Path] = struct{}{}
		if err := writeCreateOnce(filepath.Join(target, filepath.FromSlash(input.Path)), input.Data); err != nil {
			return Publication{}, err
		}
		manifest.Artifacts = append(manifest.Artifacts, Artifact{Path: input.Path, Kind: input.Kind, SHA256: SHA256(input.Data)})
	}
	censusBytes, err := CanonicalJSON(census)
	if err != nil {
		return Publication{}, err
	}
	if err := writeCreateOnce(filepath.Join(target, CensusName), censusBytes); err != nil {
		return Publication{}, err
	}
	manifest.Artifacts = append(manifest.Artifacts, Artifact{Path: CensusName, Kind: "census", SHA256: SHA256(censusBytes)})
	slices.SortFunc(manifest.Artifacts, func(a, b Artifact) int { return strings.Compare(a.Path, b.Path) })
	manifestBytes, err := CanonicalJSON(manifest)
	if err != nil {
		return Publication{}, err
	}
	if err := writeCreateOnce(filepath.Join(target, ManifestName), manifestBytes); err != nil {
		return Publication{}, err
	}
	if _, _, err := verifyWithPolicy(context.Background(), target, filepath.Join(target, "contract-m2.json"), &manifest.Source, policy); err != nil {
		return Publication{}, fmt.Errorf("prepared pass evidence did not verify: %w", err)
	}
	archive, members, err := BuildDeterministicArchive(target)
	if err != nil {
		return Publication{}, err
	}
	receipt := ProducerArchiveReceipt{SchemaVersion: 1, RunID: manifest.RunID, SourceCommit: manifest.Source.Commit, SourceTree: manifest.Source.Tree, ManifestSHA256: SHA256(manifestBytes), ArchiveSHA256: SHA256(archive), Members: members}
	receiptBytes, err := CanonicalJSON(receipt)
	if err != nil {
		return Publication{}, err
	}
	stagedArchive := filepath.Join(attempt.BundleDirectory, "producer.tar")
	stagedReceipt := filepath.Join(attempt.BundleDirectory, "producer.sha256.json")
	if err := writeCreateOnce(stagedArchive, archive); err != nil {
		return Publication{}, err
	}
	if err := writeCreateOnce(stagedReceipt, receiptBytes); err != nil {
		return Publication{}, err
	}
	if err := attempt.PublishPass(); err != nil {
		return Publication{}, err
	}
	return Publication{Directory: attempt.FinalDirectory, ManifestPath: filepath.Join(attempt.FinalDirectory, ManifestName), ManifestSHA256: SHA256(manifestBytes), ProducerArchivePath: filepath.Join(attempt.FinalBundle, "producer.tar"), ProducerArchiveSHA256: SHA256(archive), ProducerReceiptPath: filepath.Join(attempt.FinalBundle, "producer.sha256.json")}, nil
}

func Verify(ctx context.Context, directory, schemaPath string, expectedSource *Source) (Verification, Manifest, error) {
	return verifyWithPolicy(ctx, directory, schemaPath, expectedSource, frozenEvidenceVerificationPolicy())
}

func verifyWithPolicy(ctx context.Context, directory, schemaPath string, expectedSource *Source, policy evidenceVerificationPolicy) (Verification, Manifest, error) {
	_ = ctx
	if !validDigest(policy.qualificationSidecarSHA256) || policy.verifyCandidateAdmission == nil {
		return Verification{}, Manifest{}, fmt.Errorf("evidence verification policy is incomplete")
	}
	if !filepath.IsAbs(directory) || !filepath.IsAbs(schemaPath) {
		return Verification{}, Manifest{}, fmt.Errorf("evidence and schema paths must be absolute")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return Verification{}, Manifest{}, err
	}
	defer root.Close()
	manifestBytes, err := readRegular(root, ManifestName, 8<<20)
	if err != nil {
		return Verification{}, Manifest{}, err
	}
	var manifest Manifest
	if err := DecodeStrict(manifestBytes, &manifest); err != nil {
		return Verification{}, manifest, fmt.Errorf("decode manifest: %w", err)
	}
	canonical, err := CanonicalJSON(manifest)
	if err != nil || !bytes.Equal(canonical, manifestBytes) {
		return Verification{}, manifest, fmt.Errorf("manifest is not canonical JSON")
	}
	if err := validateSchema(schemaPath, manifestBytes); err != nil {
		return Verification{}, manifest, err
	}
	directoryRunID := filepath.Base(directory)
	if directoryRunID == "evidence" {
		directoryRunID = filepath.Base(filepath.Dir(directory))
	}
	if directoryRunID != manifest.RunID || manifest.SchemaVersion != 1 || manifest.ProgramID != ProgramID || manifest.MissionID != MissionID {
		return Verification{}, manifest, fmt.Errorf("manifest identity is invalid")
	}
	if err := validateSource(manifest.Source); err != nil {
		return Verification{}, manifest, err
	}
	if expectedSource != nil && *expectedSource != manifest.Source {
		return Verification{}, manifest, fmt.Errorf("expected source differs from manifest")
	}
	if manifest.M1Admission != (M1Admission{AdmittedM1AdoptionSHA256, AdmittedM1EvidenceSHA256, AdmittedM1TupleSHA256, AdmittedM1SidecarSHA256}) {
		return Verification{}, manifest, fmt.Errorf("M1 admission chain differs from frozen adoption and assertion extension")
	}
	if len(manifest.Artifacts) == 0 || len(manifest.Artifacts) > maximumFiles {
		return Verification{}, manifest, fmt.Errorf("artifact census is empty or over budget")
	}
	paths, kinds := map[string]Artifact{}, map[string]int{}
	var totalBytes int64
	previous := ""
	for _, artifact := range manifest.Artifacts {
		if !safeRelativePath(artifact.Path) || !validKind(artifact.Kind) || !validDigest(artifact.SHA256) || artifact.Path <= previous {
			return Verification{}, manifest, fmt.Errorf("artifact census is unsafe, unordered, or duplicated at %q", artifact.Path)
		}
		previous = artifact.Path
		contents, readErr := readRegular(root, filepath.FromSlash(artifact.Path), maximumFile)
		totalBytes += int64(len(contents))
		if totalBytes > maximumTotal {
			return Verification{}, manifest, fmt.Errorf("aggregate evidence exceeds %d bytes", maximumTotal)
		}
		if readErr != nil || SHA256(contents) != artifact.SHA256 {
			return Verification{}, manifest, fmt.Errorf("artifact %q is absent or changed: %w", artifact.Path, readErr)
		}
		paths[artifact.Path] = artifact
		kinds[artifact.Kind]++
	}
	if kinds["census"] != 1 || kinds["performance"] != 1 || kinds["performance-raw"] != 1 || kinds["performance-reference"] != 1 || kinds["source"] != 1 || kinds["m1-admission"] != 1 || kinds["qualification-admission"] != 1 || kinds["sbom"] != 1 || kinds["sbom-raw"] != 1 || kinds["binary"] < 2 || kinds["campaign"] < 1 || kinds["campaign-composition"] != 1 || kinds["acceptance-matrix"] != 1 || kinds["successor-contract"] != 1 || kinds["attempt-index"] != 1 || kinds["attempt-terminal"] != 1 || kinds["producer-identity"] != 1 || kinds["producer-profile"] != 1 || kinds["sidecar"] != 1 || kinds["install"] != 1 || kinds["install-smoke"] != 1 || kinds["review-input"] != 1 || kinds["race"] != 1 || kinds["race-raw"] != 1 || kinds["absence-evidence"] < 1 || kinds["contract"] < 2 {
		return Verification{}, manifest, fmt.Errorf("mandatory artifact kind census is incomplete")
	}
	candidateArtifact, err := oneArtifactByKind(paths, "qualification-admission")
	if err != nil {
		return Verification{}, manifest, err
	}
	candidatePath := filepath.Join(directory, filepath.FromSlash(candidateArtifact.Path))
	if err := policy.verifyCandidateAdmission(candidatePath); err != nil {
		return Verification{}, manifest, fmt.Errorf("M2 qualification candidate admission differs: %w", err)
	}
	if err := verifyClosedDirectory(directory, paths); err != nil {
		return Verification{}, manifest, err
	}
	sourceArtifact, err := oneArtifactByKind(paths, "source")
	if err != nil {
		return Verification{}, manifest, err
	}
	sourceBytes, err := readRegular(root, filepath.FromSlash(sourceArtifact.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	var recordedSource Source
	if err := DecodeStrict(sourceBytes, &recordedSource); err != nil || recordedSource != manifest.Source {
		return Verification{}, manifest, fmt.Errorf("source artifact differs from manifest")
	}
	producerArtifact, err := oneArtifactByKind(paths, "producer-identity")
	if err != nil {
		return Verification{}, manifest, err
	}
	producerBytes, err := readRegular(root, filepath.FromSlash(producerArtifact.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	var producer ProducerIdentity
	if err := DecodeStrict(producerBytes, &producer); err != nil || producer.SchemaVersion != 1 || producer.ExecutorActorID == "" || producer.ProcessID == "" || len(producer.ImplementationContributors) == 0 || !slices.IsSorted(producer.ImplementationContributors) || len(slices.Compact(append([]string(nil), producer.ImplementationContributors...))) != len(producer.ImplementationContributors) || producer.ProfileID != "implementer-ivan" || !validDigest(producer.ProfileRevision) {
		return Verification{}, manifest, fmt.Errorf("producer identity is absent or incomplete")
	}
	profileArtifact, err := oneArtifactByKind(paths, "producer-profile")
	if err != nil {
		return Verification{}, manifest, err
	}
	profileBytes, err := readRegular(root, filepath.FromSlash(profileArtifact.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	var profile map[string]any
	if err := jsonUnmarshal(profileBytes, &profile); err != nil || profile["profile_id"] != "implementer-ivan" || SHA256(profileBytes) != producer.ProfileRevision {
		return Verification{}, manifest, fmt.Errorf("producer profile bytes do not bind profile revision")
	}
	terminalArtifact, err := oneArtifactByKind(paths, "attempt-terminal")
	if err != nil {
		return Verification{}, manifest, err
	}
	terminalBytes, err := readRegular(root, filepath.FromSlash(terminalArtifact.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	var terminal AttemptTerminal
	if err := DecodeStrict(terminalBytes, &terminal); err != nil || terminal.Verdict != "pass" || terminal.RunID != manifest.RunID || terminal.SourceCommit != manifest.Source.Commit || terminal.SourceTree != manifest.Source.Tree {
		return Verification{}, manifest, fmt.Errorf("attempt terminal differs from technical pass")
	}
	censusBytes, err := readRegular(root, CensusName, maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	var census Census
	if err := DecodeStrict(censusBytes, &census); err != nil {
		return Verification{}, manifest, fmt.Errorf("decode census: %w", err)
	}
	if census.SchemaVersion != 1 || census.RunID != manifest.RunID || census.SourceCommit != manifest.Source.Commit || census.SourceTree != manifest.Source.Tree {
		return Verification{}, manifest, fmt.Errorf("census identity differs from manifest")
	}
	definitions := FrozenDefinitions()
	if len(census.Entries) != len(definitions) {
		return Verification{}, manifest, fmt.Errorf("campaign census length differs from frozen definitions")
	}
	for _, entry := range census.Entries {
		artifact, ok := paths[entry.ArtifactPath]
		if !ok || artifact.SHA256 != entry.ArtifactSHA256 {
			return Verification{}, manifest, fmt.Errorf("census entry %q does not bind one exact artifact", entry.ID)
		}
	}
	for index, definition := range definitions {
		entry := census.Entries[index]
		receiptBytes, err := readRegular(root, filepath.FromSlash(entry.ArtifactPath), maximumFile)
		if err != nil {
			return Verification{}, manifest, err
		}
		var claimed GoTestReceipt
		if err := DecodeStrict(receiptBytes, &claimed); err != nil {
			return Verification{}, manifest, err
		}
		rawPath := "campaigns/" + safeID(definition.ID) + ".go-test.jsonl"
		rawArtifact, ok := paths[rawPath]
		if !ok || rawArtifact.Kind != "campaign-raw" {
			return Verification{}, manifest, fmt.Errorf("campaign %s raw transcript is absent", definition.ID)
		}
		raw, err := readRegular(root, filepath.FromSlash(rawPath), maximumFile)
		if err != nil {
			return Verification{}, manifest, err
		}
		recomputed, err := VerifyGoTestReceipt(raw, manifest.RunID, manifest.Source, definition, definition.ExpectedRepetitions/definition.TrialsPerRun, claimed.ProcessExit)
		if err != nil {
			return Verification{}, manifest, fmt.Errorf("campaign %s receipt did not recompute: %w", definition.ID, err)
		}
		recomputedBytes, _ := CanonicalJSON(recomputed)
		if !bytes.Equal(recomputedBytes, receiptBytes) {
			return Verification{}, manifest, fmt.Errorf("campaign %s receipt is forged or substituted", definition.ID)
		}
	}
	structuredArtifact, err := oneArtifactByKind(paths, "campaign-structure")
	if err != nil {
		return Verification{}, manifest, err
	}
	structuredBytes, err := readRegular(root, filepath.FromSlash(structuredArtifact.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	wp14RawPath := "campaigns/" + safeID("fault/adversarial-tracker") + ".go-test.jsonl"
	wp14Raw, err := readRegular(root, filepath.FromSlash(wp14RawPath), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	contractSourceArtifact, err := oneArtifactByKind(paths, "campaign-contract-source")
	if err != nil {
		return Verification{}, manifest, err
	}
	contractSource, err := readRegular(root, filepath.FromSlash(contractSourceArtifact.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	structured, err := VerifyWP14CampaignReceiptBytes(contractSource, wp14Raw)
	if err != nil {
		return Verification{}, manifest, err
	}
	wantStructured, _ := CanonicalJSON(structured)
	if !bytes.Equal(wantStructured, structuredBytes) {
		return Verification{}, manifest, fmt.Errorf("WP14 structured family/cut receipt is forged or substituted")
	}
	matrixArtifact, err := oneArtifactByKind(paths, "campaign-composition")
	if err != nil {
		return Verification{}, manifest, err
	}
	matrixBytes, err := readRegular(root, filepath.FromSlash(matrixArtifact.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	if err := VerifyFamilyCutMatrix(matrixBytes, manifest, census, paths); err != nil {
		return Verification{}, manifest, err
	}
	raceArtifact, err := oneArtifactByKind(paths, "race")
	if err != nil {
		return Verification{}, manifest, err
	}
	raceRawArtifact, err := oneArtifactByKind(paths, "race-raw")
	if err != nil {
		return Verification{}, manifest, err
	}
	raceBytes, err := readRegular(root, filepath.FromSlash(raceArtifact.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	raceRaw, err := readRegular(root, filepath.FromSlash(raceRawArtifact.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	var claimedRace CommandReceipt
	if err := DecodeStrict(raceBytes, &claimedRace); err != nil {
		return Verification{}, manifest, fmt.Errorf("decode race receipt: %w", err)
	}
	recomputedRace, err := VerifyRaceReceipt(raceRaw, claimedRace.ExitCode)
	if err != nil {
		return Verification{}, manifest, fmt.Errorf("race receipt did not recompute: %w", err)
	}
	recomputedRaceBytes, _ := CanonicalJSON(recomputedRace)
	if !bytes.Equal(recomputedRaceBytes, raceBytes) {
		return Verification{}, manifest, fmt.Errorf("race receipt is forged or substituted")
	}
	sbomArtifact, err := oneArtifactByKind(paths, "sbom")
	if err != nil {
		return Verification{}, manifest, err
	}
	sbomRawArtifact, err := oneArtifactByKind(paths, "sbom-raw")
	if err != nil {
		return Verification{}, manifest, err
	}
	sbomBytes, err := readRegular(root, filepath.FromSlash(sbomArtifact.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	sbomRaw, err := readRegular(root, filepath.FromSlash(sbomRawArtifact.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	if err := VerifySBOMEvidence(sbomRaw, sbomBytes); err != nil {
		return Verification{}, manifest, err
	}
	if err := verifyNestedAbsence(root, paths, manifest.Source, policy.qualificationSidecarSHA256); err != nil {
		return Verification{}, manifest, err
	}
	qualifier, ok := paths["bin/spk-m2-qualification"]
	if !ok || qualifier.Kind != "binary" {
		return Verification{}, manifest, fmt.Errorf("qualification binary is absent")
	}
	product, ok := paths["bin/spec-kitty"]
	if !ok || product.Kind != "binary" {
		return Verification{}, manifest, fmt.Errorf("product binary is absent")
	}
	installed, ok := paths["install/spec-kitty"]
	if !ok || installed.Kind != "install" {
		return Verification{}, manifest, fmt.Errorf("fresh-install product is absent")
	}
	qualifierBytes, err := readRegular(root, filepath.FromSlash(qualifier.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	productBytes, err := readRegular(root, filepath.FromSlash(product.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	installedBytes, err := readRegular(root, filepath.FromSlash(installed.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	if !bytes.Equal(productBytes, installedBytes) {
		return Verification{}, manifest, fmt.Errorf("fresh-install product differs from qualified product bytes")
	}
	if err := VerifyBuiltBinaryData(qualifierBytes, manifest.Source); err != nil {
		return Verification{}, manifest, fmt.Errorf("qualification binary: %w", err)
	}
	if err := VerifyBuiltBinaryData(productBytes, manifest.Source); err != nil {
		return Verification{}, manifest, fmt.Errorf("product binary: %w", err)
	}
	helpArtifact, err := oneArtifactByKind(paths, "install-smoke")
	if err != nil {
		return Verification{}, manifest, err
	}
	helpRawArtifact, err := oneArtifactByKind(paths, "install-smoke-raw")
	if err != nil {
		return Verification{}, manifest, err
	}
	helpReceiptBytes, err := readRegular(root, filepath.FromSlash(helpArtifact.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	helpRaw, err := readRegular(root, filepath.FromSlash(helpRawArtifact.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	var helpReceipt struct {
		SchemaVersion     int      `json:"schema_version"`
		InstalledPathKind string   `json:"installed_path_kind"`
		Argv              []string `json:"argv"`
		ExitCode          int      `json:"exit_code"`
		StdoutSHA256      string   `json:"stdout_sha256"`
		Verdict           string   `json:"verdict"`
	}
	if err := DecodeStrict(helpReceiptBytes, &helpReceipt); err != nil || helpReceipt.SchemaVersion != 1 || helpReceipt.InstalledPathKind != "fresh-private-root" || !slices.Equal(helpReceipt.Argv, []string{"--help"}) || helpReceipt.ExitCode != 0 || helpReceipt.StdoutSHA256 != SHA256(helpRaw) || helpReceipt.Verdict != "pass" {
		return Verification{}, manifest, fmt.Errorf("fresh-install help receipt is forged or incomplete")
	}
	summary, censusErr := Summarize(census.Entries, manifest.Census.SQLiteEdges)
	performanceArtifact, err := oneArtifactByKind(paths, "performance")
	if err != nil {
		return Verification{}, manifest, err
	}
	performanceBytes, err := readRegular(root, filepath.FromSlash(performanceArtifact.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	var raw RawPerformance
	if err := DecodeStrict(performanceBytes, &raw); err != nil {
		return Verification{}, manifest, err
	}
	transcriptArtifact, err := oneArtifactByKind(paths, "performance-raw")
	if err != nil {
		return Verification{}, manifest, err
	}
	transcript, err := readRegular(root, filepath.FromSlash(transcriptArtifact.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	if err := VerifyPerformanceTranscript(transcript); err != nil {
		return Verification{}, manifest, err
	}
	referenceArtifact, err := oneArtifactByKind(paths, "performance-reference")
	if err != nil {
		return Verification{}, manifest, err
	}
	referenceBytes, err := readRegular(root, filepath.FromSlash(referenceArtifact.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	if raw.RunID != manifest.RunID || raw.SourceCommit != manifest.Source.Commit || raw.SourceTree != manifest.Source.Tree {
		return Verification{}, manifest, fmt.Errorf("performance evidence identity differs from manifest")
	}
	if raw.Environment.QualifierSHA256 != SHA256(qualifierBytes) || raw.Environment.ProductSHA256 != SHA256(productBytes) {
		return Verification{}, manifest, fmt.Errorf("performance environment binary identities differ from qualified artifacts")
	}
	if err := VerifyPerformanceReference(referenceBytes, raw); err != nil {
		return Verification{}, manifest, err
	}
	performance, performanceErr := SummarizePerformance(raw)
	semantic := "pass"
	if censusErr != nil || performanceErr != nil || summary != manifest.Census || performance != manifest.Performance {
		semantic = "fail"
	}
	if manifest.Verdict != semantic {
		return Verification{}, manifest, fmt.Errorf("claimed verdict %q differs from recomputed %q", manifest.Verdict, semantic)
	}
	acceptanceArtifact, err := oneArtifactByKind(paths, "acceptance-matrix")
	if err != nil {
		return Verification{}, manifest, err
	}
	acceptanceBytes, err := readRegular(root, filepath.FromSlash(acceptanceArtifact.Path), maximumFile)
	if err != nil {
		return Verification{}, manifest, err
	}
	if err := VerifyAcceptanceMatrix(acceptanceBytes, manifest, census, paths); err != nil {
		return Verification{}, manifest, err
	}
	verification := Verification{RunID: manifest.RunID, SourceCommit: manifest.Source.Commit, SourceTree: manifest.Source.Tree, ManifestSHA256: SHA256(manifestBytes), Verdict: semantic, ReviewEligible: semantic == "pass", ReleaseReady: false, Adopted: false}
	return verification, manifest, nil
}

func verifyClosedDirectory(directory string, artifacts map[string]Artifact) error {
	want := make(map[string]struct{}, len(artifacts)+1)
	want[ManifestName] = struct{}{}
	for path := range artifacts {
		want[filepath.FromSlash(path)] = struct{}{}
	}
	seen := map[string]struct{}{}
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == directory {
			return nil
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("evidence contains symlink %q", relative)
		}
		if entry.IsDir() {
			return nil
		}
		if _, ok := want[relative]; !ok {
			return fmt.Errorf("unmanifested evidence member %q", relative)
		}
		seen[relative] = struct{}{}
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(want) {
		return fmt.Errorf("closed evidence census saw %d files, want %d", len(seen), len(want))
	}
	return nil
}

func ObserveCleanSource(repositoryRoot string) (Source, error) {
	commit, err := git(repositoryRoot, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return Source{}, err
	}
	tree, err := git(repositoryRoot, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return Source{}, err
	}
	status, err := git(repositoryRoot, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil || status != "" {
		return Source{}, fmt.Errorf("source tree is dirty or contains untracked bytes")
	}
	goMod, err := os.ReadFile(filepath.Join(repositoryRoot, "go.mod"))
	if err != nil {
		return Source{}, err
	}
	goSum, err := os.ReadFile(filepath.Join(repositoryRoot, "go.sum"))
	if err != nil {
		return Source{}, err
	}
	return Source{Commit: commit, Tree: tree, GoVersion: runtime.Version(), GoModSHA256: SHA256(goMod), GoSumSHA256: SHA256(goSum)}, nil
}

func git(repositoryRoot string, arguments ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", repositoryRoot}, arguments...)...)
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(arguments, " "), err)
	}
	return strings.TrimSpace(string(output)), nil
}

func validateSource(source Source) error {
	if !isHex(source.Commit, 40) || !isHex(source.Tree, 40) || source.GoVersion == "" || !validDigest(source.GoModSHA256) || !validDigest(source.GoSumSHA256) {
		return fmt.Errorf("source identity is incomplete")
	}
	return nil
}

func validateSchema(path string, document []byte) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var schema any
	if err := jsonUnmarshal(contents, &schema); err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource("m2.json", schema); err != nil {
		return err
	}
	compiled, err := compiler.Compile("m2.json")
	if err != nil {
		return err
	}
	var value any
	if err := jsonUnmarshal(document, &value); err != nil {
		return err
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Errorf("manifest violates M2 schema: %w", err)
	}
	return nil
}

func jsonUnmarshal(data []byte, destination any) error {
	return DecodeStrict(data, destination)
}

func oneArtifactByKind(paths map[string]Artifact, kind string) (Artifact, error) {
	var result Artifact
	count := 0
	for _, artifact := range paths {
		if artifact.Kind == kind {
			result, count = artifact, count+1
		}
	}
	if count != 1 {
		return Artifact{}, fmt.Errorf("artifact kind %q count=%d want=1", kind, count)
	}
	return result, nil
}

func safeRelativePath(path string) bool {
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != filepath.FromSlash(path) || strings.Contains(path, "\\") {
		return false
	}
	parts := strings.Split(path, "/")
	return !slices.Contains(parts, "") && !slices.Contains(parts, ".") && !slices.Contains(parts, "..")
}

func readRegular(root *os.Root, path string, maximum int64) ([]byte, error) {
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maximum {
		return nil, fmt.Errorf("evidence member is not a bounded regular file")
	}
	return io.ReadAll(io.LimitReader(file, maximum+1))
}

func writeCreateOnce(path string, data []byte) error {
	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, data) {
			return nil
		}
		return fmt.Errorf("immutable evidence member already exists: %s", filepath.Base(path))
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func isHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func validKind(kind string) bool {
	if len(kind) == 0 || len(kind) > 64 || kind[0] < 'a' || kind[0] > 'z' {
		return false
	}
	for _, character := range kind[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '.' && character != '-' {
			return false
		}
	}
	return true
}
