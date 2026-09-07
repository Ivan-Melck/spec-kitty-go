// Package external owns M2's direct use of the admitted provider adapter.
// It is qualification tooling only and is never reachable from production.
package external

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	restorev2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	"github.com/spec-kitty/go-kitty3/internal/controlstore"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	m2 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m2"
)

const PerformanceGenerator = "m2-authority-families-checkpoint-suffix-v1"

type PerformanceConfig struct {
	RunID                string
	Repository           string
	Executable           string
	QualifierExecutable  string
	ProductExecutable    string
	Reference            string
	RuntimeRoot          string
	DataRoot             string
	Source               m2.Source
	Warmup               int
	Observations         int
	RecoveryRuns         int
	CheckpointRevision   uint64
	CheckpointOperations int
	StartTimeout         time.Duration
	StopTimeout          time.Duration
	TestTimeout          time.Duration
}

type CheckpointResult struct {
	Revision         uint64
	ProjectionSHA256 string
	OperationCount   int
}

type RecoveryObservation struct {
	DurationNS         int64
	ProjectionSHA256   string
	ObservedOperations int
	Operations         map[string]RecoveredOperation
	RuntimeOperations  []string
}

type RecoveredOperation struct {
	Families       []string
	PublicationIDs []string
}

// PublishPerformanceOutboxConnected executes one real direct-provider outbox
// authority mutation against the already-running admitted Host database.
// Outbox deliberately has no Host route; the caller must restart the Host
// after this append so its in-memory projection cannot remain stale.
func PublishPerformanceOutboxConnected(ctx context.Context, socketPath string, source journalv2.SealedCommitPlanV2, sourceRecordRevision uint64, operationID string, hostAuthorityID journalv2.HostAuthorityID) (m2.PerformanceSample, error) {
	adapter, closer, err := storegapdb.Dial(socketPath, 5*time.Second)
	if err != nil {
		return m2.PerformanceSample{}, err
	}
	defer closer.Close()
	registry := journalv2.DefaultEventRegistryV2()
	reducers, err := productionruntime.BuildReducerSetV2(registry)
	if err != nil {
		return m2.PerformanceSample{}, err
	}
	recovered, err := restorev2.RestoreV2(ctx, restorev2.InputV2{Reader: adapter, EventRegistry: registry, Reducers: reducers, HostAuthorityID: hostAuthorityID, PriorRuntimeEpochID: "epoch-performance-outbox-observer", RecoveryAttemptID: "performance-outbox-source-restore", Budgets: restorev2.DefaultBudgetsV2()})
	if err != nil {
		return m2.PerformanceSample{}, err
	}
	tracker, err := restoredPerformanceTracker(reducers, recovered)
	if err != nil {
		return m2.PerformanceSample{}, err
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, tracker)
	if err != nil {
		return m2.PerformanceSample{}, err
	}
	if len(source.EffectIntents()) != 1 || source.BindingCreate() == nil || sourceRecordRevision == 0 || operationID == "" {
		return m2.PerformanceSample{}, fmt.Errorf("performance outbox source is incomplete")
	}
	var intent execution.EffectIntentV2
	if err := json.Unmarshal(source.EffectIntents()[0].Canonical, &intent); err != nil || len(intent.CanonicalBytesV2()) == 0 {
		return m2.PerformanceSample{}, fmt.Errorf("performance outbox source intent: %w", err)
	}
	revision, err := adapter.CaptureRevisionV2(ctx)
	if err != nil {
		return m2.PerformanceSample{}, err
	}
	durable, err := controlstore.BindDurableOutboxIntentV2(source, sourceRecordRevision, controlstore.OutboxIntentCoordinatesV2{
		EffectID: intent.EffectID, SourceOperationID: intent.OperationID, CanonicalIntent: intent.CanonicalBytesV2(),
		IntendedMutationDigest: intent.CanonicalRequestDigest, IntendedValue: intent.IntendedValue,
		ExpectedProviderRevision: uint64(intent.ExpectedProviderRevision), ExpectedValue: intent.ExpectedValue,
		ExpectedOperationMarker: intent.ExpectedOperationMarker, AttributionMarker: string(intent.OperationID),
		ActorID: journalv2.ActorID(intent.ActorID.String()), Capability: source.BindingCreate().CommandKind(),
	})
	if err != nil {
		return m2.PerformanceSample{}, err
	}
	command := controlstore.OutboxCommandV2{Action: controlstore.OutboxStartAttemptV2, Intent: durable, AttemptID: "attempt-" + operationID, RuntimeEpochID: "epoch-performance-outbox", OperationEventID: journalv2.EventID("event-" + operationID + "-operation"), OutboxEventID: journalv2.EventID("event-" + operationID + "-outbox")}
	command, err = controlstore.BindOutboxCommandOperationV2(command, controlstore.OutboxOperationCoordinatesV2{OperationID: journalv2.OperationID(operationID), ProcessID: "process-performance-outbox", DoctrineRevision: "doctrine-performance", ProfileRevision: "profile-performance", PolicyRevision: "policy-performance", ObservedCreatedAt: "2026-09-06T00:00:00Z"})
	if err != nil {
		return m2.PerformanceSample{}, err
	}
	capability, err := controlstore.BuildOutboxAuthorityPlanV2(command, registry)
	if err != nil {
		return m2.PerformanceSample{}, err
	}
	plan, err := capability.Publication()
	if err != nil {
		return m2.PerformanceSample{}, err
	}
	port, err := adapter.OutboxAuthorityPortV2(coordinator)
	if err != nil {
		return m2.PerformanceSample{}, err
	}
	started := time.Now()
	ack, err := port.PublishOutbox(ctx, capability)
	duration := time.Since(started)
	if err != nil {
		return m2.PerformanceSample{}, err
	}
	families := make([]string, 0, len(plan.Events()))
	for _, event := range plan.Events() {
		families = append(families, "family/"+event.Family())
	}
	slices.Sort(families)
	families = slices.Compact(families)
	return m2.PerformanceSample{Kind: "mutation", Family: "family/outbox", OperationID: operationID, EmittedFamilies: families, DurationNS: duration.Nanoseconds(), Included: true, BeforeRevision: revision.CurrentRevision(), AfterRevision: ack.AuthorityRevision, MutationCount: uint64(ack.MutationCount), PublicationCount: 1, ProjectionRevision: ack.AuthorityRevision, PublicationIDs: []string{string(ack.PublicationID)}}, nil
}

func ObservePerformanceEnvironment(config PerformanceConfig) (m2.PerformanceEnvironment, error) {
	return observeEnvironment(config)
}

// RecoverPerformance starts a fresh admitted sidecar process for every run
// and performs the production reducer restore into clean process memory.
func RecoverPerformance(ctx context.Context, config PerformanceConfig, hostAuthorityID string) ([]RecoveryObservation, error) {
	observations := make([]RecoveryObservation, 0, config.RecoveryRuns)
	var projectionSHA string
	for run := 0; run < config.RecoveryRuns; run++ {
		runConfig := config
		runConfig.RuntimeRoot = filepath.Join(config.RuntimeRoot, fmt.Sprintf("recovery-%02d", run))
		state, err := startPerformanceRuntime(ctx, runConfig)
		if err != nil {
			return observations, err
		}
		started := time.Now()
		recovered, restoreErr := restorev2.RestoreV2(ctx, restorev2.InputV2{Reader: state.adapter, EventRegistry: state.registry, Reducers: state.reducers, HostAuthorityID: journalv2.HostAuthorityID(hostAuthorityID), PriorRuntimeEpochID: "epoch-performance-before-checkpoint", RecoveryAttemptID: fmt.Sprintf("performance-recovery-%02d", run), Budgets: restorev2.DefaultBudgetsV2()})
		duration := time.Since(started)
		if restoreErr != nil {
			state.close()
			return observations, restoreErr
		}
		if recovered.StoreRevision().CurrentRevision() < config.CheckpointRevision {
			state.close()
			return observations, fmt.Errorf("recovery revision precedes the qualified checkpoint")
		}
		observed := countOperationStates(recovered) - config.CheckpointOperations
		if observed != config.Observations {
			state.close()
			return observations, fmt.Errorf("recovery run %d restored %d/%d suffix operations", run, observed, config.Observations)
		}
		digest := recovered.Playback().CanonicalProjectionDigest().String()
		if run == 0 {
			projectionSHA = digest
		} else if digest != projectionSHA {
			state.close()
			return observations, fmt.Errorf("recovery run %d projection differs from run zero", run)
		}
		observation := RecoveryObservation{DurationNS: duration.Nanoseconds(), ProjectionSHA256: digest, ObservedOperations: observed}
		if run == 0 {
			observation.Operations, observation.RuntimeOperations, err = operationsFromEvents(ctx, state.adapter, recovered, config.CheckpointRevision)
			if err != nil {
				state.close()
				return observations, err
			}
		}
		state.close()
		observations = append(observations, observation)
	}
	return observations, nil
}

// PreparePerformanceCheckpoint independently restores and observes the exact
// production checkpoint emitted by the live Host after the warmup. A restored
// tracker deliberately has no unsealed acknowledgement ledger, so the
// qualifier must never manufacture a new checkpoint after restart.
func PreparePerformanceCheckpoint(ctx context.Context, config PerformanceConfig, hostAuthorityID string) (CheckpointResult, error) {
	state, err := startPerformanceRuntime(ctx, config)
	if err != nil {
		return CheckpointResult{}, err
	}
	defer state.close()
	recovered, err := restorev2.RestoreV2(ctx, restorev2.InputV2{Reader: state.adapter, EventRegistry: state.registry, Reducers: state.reducers, HostAuthorityID: journalv2.HostAuthorityID(hostAuthorityID), PriorRuntimeEpochID: "epoch-performance-before-checkpoint", RecoveryAttemptID: "performance-checkpoint-restore", Budgets: restorev2.DefaultBudgetsV2()})
	if err != nil {
		return CheckpointResult{}, err
	}
	checkpointOperations := 0
	for _, state := range recovered.Playback().States() {
		coordinate := state.Coordinate()
		if coordinate.Kind == "operation" && strings.HasPrefix(string(coordinate.ID), "operation-checkpoint-") {
			checkpointOperations++
		}
	}
	if checkpointOperations != 1 {
		return CheckpointResult{}, fmt.Errorf("performance warmup checkpoint census=%d want=1", checkpointOperations)
	}
	return CheckpointResult{Revision: recovered.StoreRevision().CurrentRevision(), ProjectionSHA256: recovered.Playback().CanonicalProjectionDigest().String(), OperationCount: countOperationStates(recovered)}, nil
}

func operationsFromEvents(ctx context.Context, reader restorev2.ReaderV2, result restorev2.ResultV2, afterRevision uint64) (map[string]RecoveredOperation, []string, error) {
	byOperation := map[string]map[string]struct{}{}
	publications := map[string]map[string]struct{}{}
	runtimeRevisions := map[string]uint64{}
	seen := 0
	for _, state := range result.Playback().States() {
		for version := journalv2.LogicalVersion(1); version <= state.LogicalVersion(); version++ {
			seen++
			if seen > 100_000 {
				return nil, nil, fmt.Errorf("performance event census exceeds 100000")
			}
			address, err := journalv2.NewJournalEventAddressV2(state.Coordinate(), version)
			if err != nil {
				return nil, nil, err
			}
			request, err := journalv2.NewReadRequestV2(address, result.StoreRevision())
			if err != nil {
				return nil, nil, err
			}
			record, err := reader.ReadV2(ctx, request)
			if err != nil {
				return nil, nil, err
			}
			event, err := journalv2.DecodeEventEnvelopeRecordV2(record, journalv2.DefaultEventRegistryV2())
			if err != nil {
				return nil, nil, err
			}
			operation := string(event.OperationID())
			if byOperation[operation] == nil {
				byOperation[operation] = map[string]struct{}{}
				publications[operation] = map[string]struct{}{}
			}
			byOperation[operation]["family/"+event.Family()] = struct{}{}
			publications[operation][string(event.PublicationID())] = struct{}{}
			if event.Family() == "runtime_host" && record.RecordRevision() > afterRevision && (runtimeRevisions[operation] == 0 || record.RecordRevision() < runtimeRevisions[operation]) {
				runtimeRevisions[operation] = record.RecordRevision()
			}
		}
	}
	resultMap := make(map[string]RecoveredOperation, len(byOperation))
	for operation, set := range byOperation {
		families := make([]string, 0, len(set))
		for family := range set {
			families = append(families, family)
		}
		slices.Sort(families)
		publicationIDs := make([]string, 0, len(publications[operation]))
		for publication := range publications[operation] {
			publicationIDs = append(publicationIDs, publication)
		}
		slices.Sort(publicationIDs)
		resultMap[operation] = RecoveredOperation{Families: families, PublicationIDs: publicationIDs}
	}
	runtimeOperations := make([]string, 0, len(runtimeRevisions))
	for operation := range runtimeRevisions {
		runtimeOperations = append(runtimeOperations, operation)
	}
	slices.SortFunc(runtimeOperations, func(left, right string) int {
		if runtimeRevisions[left] < runtimeRevisions[right] {
			return -1
		}
		if runtimeRevisions[left] > runtimeRevisions[right] {
			return 1
		}
		return strings.Compare(left, right)
	})
	if len(runtimeOperations) == 0 {
		return nil, nil, fmt.Errorf("performance restore omitted post-checkpoint runtime_host publication")
	}
	return resultMap, runtimeOperations, nil
}

func restoredPerformanceTracker(reducers journalv2.ReducerSetV2, recovered restorev2.ResultV2) (*journalv2.ProjectionTrackerV2, error) {
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		return nil, err
	}
	states := recovered.Playback().States()
	entries := make([]journalv2.ProjectionRestoreEntryV2, len(states))
	for index, state := range states {
		entries[index] = journalv2.ProjectionRestoreEntryV2{Coordinate: state.Coordinate(), LogicalVersion: state.LogicalVersion(), LastEventID: state.LastEventID(), CanonicalState: state.CanonicalState(), StateDigest: state.StateDigest(), Tombstone: state.Tombstone()}
	}
	if err := tracker.Restore(journalv2.ProjectionRestoreSeedV2{ThroughRecordRevision: recovered.StoreRevision().CurrentRevision(), RollingPrefixDigest: recovered.RollingPrefixDigest(), CanonicalProjectionDigest: recovered.Playback().CanonicalProjectionDigest(), Entries: entries}); err != nil {
		return nil, err
	}
	return tracker, nil
}

type performanceRuntime struct {
	lifecycle *sidecargapdb.Lifecycle
	adapter   *storegapdb.Adapter
	closer    interface{ Close() error }
	registry  journalv2.EventRegistryV2
	reducers  journalv2.ReducerSetV2
}

// MeasurePerformance times real accepted publication plans against the
// admitted GapDB sidecar. Every durable mutation crosses the foundation
// coordinator and its acknowledgement projection; raw Adapter Put/Get calls
// are deliberately not performance evidence.
func MeasurePerformance(ctx context.Context, config PerformanceConfig) (m2.RawPerformance, []byte, error) {
	var result m2.RawPerformance
	if config.RunID == "" || config.Repository == "" || config.Executable == "" || config.QualifierExecutable == "" || config.ProductExecutable == "" || config.Reference == "" || config.Observations != 10_000 || config.RecoveryRuns != 30 || config.Warmup != 100 || config.TestTimeout <= 0 || config.TestTimeout > 60*time.Minute {
		return result, nil, fmt.Errorf("M2 performance configuration is incomplete")
	}
	if err := os.MkdirAll(config.RuntimeRoot, 0o700); err != nil {
		return result, nil, err
	}
	output := filepath.Join(config.RuntimeRoot, "performance.raw.json")
	command := exec.CommandContext(ctx, "go", performanceTestArguments(config.TestTimeout)...)
	command.Dir = config.Repository
	command.Env = performanceChildEnvironment(os.Environ(), config, output)
	var transcript boundedBuffer
	transcript.limit = 64 << 20
	command.Stdout, command.Stderr = &transcript, &transcript
	err := command.Run()
	if transcript.exceeded {
		return result, transcript.Bytes(), fmt.Errorf("performance test transcript exceeds 64 MiB")
	}
	if err != nil {
		return result, transcript.Bytes(), fmt.Errorf("performance scenario: %w", err)
	}
	if err := m2.VerifyPerformanceTranscript(transcript.Bytes()); err != nil {
		return result, transcript.Bytes(), err
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		return result, transcript.Bytes(), err
	}
	if len(raw) > 64<<20 {
		return result, transcript.Bytes(), fmt.Errorf("performance artifact exceeds 64 MiB")
	}
	if err := m2.DecodeStrict(raw, &result); err != nil {
		return result, transcript.Bytes(), err
	}
	if result.RunID != config.RunID || result.SourceCommit != config.Source.Commit || result.SourceTree != config.Source.Tree {
		return result, transcript.Bytes(), fmt.Errorf("performance artifact identity differs from the requested run/source")
	}
	reference, err := os.ReadFile(config.Reference)
	if err != nil {
		return result, transcript.Bytes(), err
	}
	if err := m2.VerifyPerformanceReference(reference, result); err != nil {
		return result, transcript.Bytes(), err
	}
	if _, err := m2.SummarizePerformance(result); err != nil {
		return result, transcript.Bytes(), err
	}
	return result, transcript.Bytes(), nil
}

func performanceTestArguments(timeout time.Duration) []string {
	return []string{"test", "-json", "./internal/scenario", "-run", "^TestM2AcceptedAuthorityPerformanceCampaign$", "-count=1", "-timeout=" + timeout.String()}
}

func performanceChildEnvironment(base []string, config PerformanceConfig, output string) []string {
	keys := []string{
		"SPEC_KITTY_M2_PERFORMANCE_OUTPUT",
		"SPEC_KITTY_M2_PERFORMANCE_RUN_ID",
		"SPEC_KITTY_M2_PERFORMANCE_SOURCE_COMMIT",
		"SPEC_KITTY_M2_PERFORMANCE_SOURCE_TREE",
		"SPEC_KITTY_M2_PERFORMANCE_SOURCE_GO",
		"SPEC_KITTY_M2_PERFORMANCE_SOURCE_GOMOD",
		"SPEC_KITTY_M2_PERFORMANCE_SOURCE_GOSUM",
		"SPEC_KITTY_M2_PERFORMANCE_REFERENCE",
		"SPEC_KITTY_M2_PERFORMANCE_QUALIFIER",
		"SPEC_KITTY_M2_PERFORMANCE_PRODUCT",
		"SPEC_KITTY_WP13_GAPDBD",
		"SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE",
		"SPEC_KITTY_WP15_GAPDBD",
	}
	result := make([]string, 0, len(base)+len(keys)-1)
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if ok && slices.Contains(keys, key) {
			continue
		}
		result = append(result, entry)
	}
	return append(result,
		"SPEC_KITTY_M2_PERFORMANCE_OUTPUT="+output,
		"SPEC_KITTY_M2_PERFORMANCE_RUN_ID="+config.RunID,
		"SPEC_KITTY_M2_PERFORMANCE_SOURCE_COMMIT="+config.Source.Commit,
		"SPEC_KITTY_M2_PERFORMANCE_SOURCE_TREE="+config.Source.Tree,
		"SPEC_KITTY_M2_PERFORMANCE_SOURCE_GO="+config.Source.GoVersion,
		"SPEC_KITTY_M2_PERFORMANCE_SOURCE_GOMOD="+config.Source.GoModSHA256,
		"SPEC_KITTY_M2_PERFORMANCE_SOURCE_GOSUM="+config.Source.GoSumSHA256,
		"SPEC_KITTY_M2_PERFORMANCE_REFERENCE="+config.Reference,
		"SPEC_KITTY_M2_PERFORMANCE_QUALIFIER="+config.QualifierExecutable,
		"SPEC_KITTY_M2_PERFORMANCE_PRODUCT="+config.ProductExecutable,
		// WP14 is the production scenario's explicit admitted authority
		// executable contract. WP15 is retained for the direct/native
		// companion paths. Both receive the same already-verified binary.
		"SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE="+config.Executable,
		"SPEC_KITTY_WP15_GAPDBD="+config.Executable,
	)
}

type boundedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	if b.Buffer.Len()+len(value) > b.limit {
		b.exceeded = true
		return len(value), fmt.Errorf("bounded transcript exceeded")
	}
	return b.Buffer.Write(value)
}

func startPerformanceRuntime(ctx context.Context, config PerformanceConfig) (*performanceRuntime, error) {
	adoption := filepath.Join(config.Repository, "docs/programs/evidence/m1-assertion-extension-adoption.json")
	admissionRecord := filepath.Join(config.Repository, "docs/programs/evidence/m1-assertion-extension-admission.json")
	admission, err := sidecargapdb.VerifyAssertionExtensionAdoption(adoption, admissionRecord)
	if err != nil {
		return nil, err
	}
	for _, path := range []string{config.RuntimeRoot, config.DataRoot} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, err
		}
	}
	lifecycle, err := sidecargapdb.Start(ctx, sidecargapdb.LifecycleConfig{
		ExecutablePath: config.Executable, AdmissionRecordPath: admissionRecord, Admission: admission,
		RuntimeRoot: config.RuntimeRoot, DataDirectory: config.DataRoot, SupervisorRunID: config.RunID + "-performance",
		ProductPolicySHA256: m2.SHA256([]byte("m2-performance-policy-v1")), StartTimeout: config.StartTimeout, StopTimeout: config.StopTimeout,
	})
	if err != nil {
		return nil, err
	}
	evidence, err := lifecycle.Evidence()
	if err != nil {
		_ = lifecycle.Stop(context.Background())
		return nil, err
	}
	adapter, closer, err := storegapdb.Dial(evidence.ConfiguredDaemonSocketPath, 5*time.Second)
	if err != nil {
		_ = lifecycle.Stop(context.Background())
		return nil, err
	}
	registry := journalv2.DefaultEventRegistryV2()
	reducers, err := productionruntime.BuildReducerSetV2(registry)
	if err != nil {
		_ = closer.Close()
		_ = lifecycle.Stop(context.Background())
		return nil, err
	}
	return &performanceRuntime{lifecycle: lifecycle, adapter: adapter, closer: closer, registry: registry, reducers: reducers}, nil
}

func (r *performanceRuntime) close() {
	if r == nil {
		return
	}
	if r.closer != nil {
		_ = r.closer.Close()
	}
	if r.lifecycle != nil {
		_ = r.lifecycle.Stop(context.Background())
	}
}

func countOperationStates(result restorev2.ResultV2) int {
	count := 0
	for _, state := range result.Playback().States() {
		if state.Coordinate().Kind == "operation" {
			count++
		}
	}
	return count
}

func observeEnvironment(config PerformanceConfig) (m2.PerformanceEnvironment, error) {
	result := m2.PerformanceEnvironment{KernelSystem: runtime.GOOS, Architecture: runtime.GOARCH, GoVersion: runtime.Version()}
	if err := m2.VerifyBuiltBinary(config.QualifierExecutable, config.Source); err != nil {
		return result, fmt.Errorf("performance qualifier build identity: %w", err)
	}
	if err := m2.VerifyBuiltBinary(config.ProductExecutable, config.Source); err != nil {
		return result, fmt.Errorf("performance product build identity: %w", err)
	}
	qualifier, err := os.ReadFile(config.QualifierExecutable)
	if err != nil {
		return result, err
	}
	product, err := os.ReadFile(config.ProductExecutable)
	if err != nil {
		return result, err
	}
	result.CGOEnabled = "0"
	result.QualifierSHA256 = m2.SHA256(qualifier)
	result.ProductSHA256 = m2.SHA256(product)
	cpu, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return result, err
	}
	physical := map[string]struct{}{}
	var physicalID, coreID string
	scanner := bufio.NewScanner(cpu)
	for scanner.Scan() {
		line := scanner.Text()
		key, value, found := strings.Cut(line, ":")
		if !found {
			if physicalID != "" && coreID != "" {
				physical[physicalID+":"+coreID] = struct{}{}
			}
			physicalID, coreID = "", ""
			continue
		}
		switch strings.TrimSpace(key) {
		case "model name":
			if result.CPUModel == "" {
				result.CPUModel = strings.TrimSpace(value)
			}
		case "physical id":
			physicalID = strings.TrimSpace(value)
		case "core id":
			coreID = strings.TrimSpace(value)
		}
	}
	_ = cpu.Close()
	if physicalID != "" && coreID != "" {
		physical[physicalID+":"+coreID] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	result.LogicalCPUs = runtime.NumCPU()
	result.PhysicalCores = len(physical)
	if result.PhysicalCores == 0 {
		result.PhysicalCores = result.LogicalCPUs
	}
	meminfo, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return result, err
	}
	for _, line := range strings.Split(string(meminfo), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return result, fmt.Errorf("MemTotal is malformed")
			}
			kilobytes, parseErr := strconv.ParseUint(fields[1], 10, 64)
			if parseErr != nil {
				return result, parseErr
			}
			result.MemoryBytes = kilobytes * 1024
		}
	}
	if release, readErr := os.ReadFile("/proc/sys/kernel/osrelease"); readErr == nil {
		result.KernelRelease = strings.TrimSpace(string(release))
	} else {
		return result, readErr
	}
	governor, err := os.ReadFile("/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor")
	if err != nil {
		return result, err
	}
	result.PowerGovernor = strings.TrimSpace(string(governor))
	boost, err := os.ReadFile("/sys/devices/system/cpu/cpufreq/boost")
	if err != nil {
		return result, err
	}
	result.BoostEnabled = strings.TrimSpace(string(boost)) == "1"
	load, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return result, err
	}
	result.Load1, err = strconv.ParseFloat(strings.Fields(string(load))[0], 64)
	if err != nil {
		return result, err
	}
	result.Filesystem, err = commandField(config.DataRoot, "findmnt", "-T", config.DataRoot, "-no", "FSTYPE")
	if err != nil {
		return result, err
	}
	result.StorageDevice, err = commandField(config.DataRoot, "findmnt", "-T", config.DataRoot, "-no", "SOURCE")
	if err != nil {
		return result, err
	}
	sidecar, err := os.ReadFile(config.Executable)
	if err != nil {
		return result, err
	}
	result.SidecarSHA256 = m2.SHA256(sidecar)
	return result, nil
}

func commandField(directory, name string, args ...string) (string, error) {
	command := exec.Command(name, args...)
	command.Dir = directory
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
