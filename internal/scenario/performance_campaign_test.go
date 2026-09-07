package scenario

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	m2 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m2"
	m2external "github.com/spec-kitty/go-kitty3/internal/testcampaign/m2/external"
)

const (
	performanceWarmups    = 100
	performanceSuffixOps  = 10_000
	performanceFillSlots  = 4_000
	performanceQueries    = 10_000
	performanceRecoveries = 30
)

func TestM2DirectOutboxPerformanceMutationUsesDurableEffectSource(t *testing.T) {
	collector := newPerformanceCollector(t)
	reconciler := &scenarioEffectReconciler{}
	var h *portableHarness
	var sample m2.PerformanceSample
	h = newPortableHarness(t, ProductionConfig{
		ExecutablePath: productionExecutable(t), Seed: t.Name(), Reconciler: reconciler,
		PublicationBoundaryObserver: collector, CandidateVerifier: sourceBoundCandidateVerifier{},
		AfterEffectIntent: func(ctx context.Context, workID ids.WorkID) (bool, error) {
			if workID != "work-a" {
				return false, nil
			}
			source, sourceRecordRevision := collector.outboxSource(t)
			var err error
			sample, err = m2external.PublishPerformanceOutboxConnected(ctx, activeGapDBSocket(t, h), source, sourceRecordRevision, "operation-performance-outbox-direct", journalv2.HostAuthorityID("host-"+shortDigest(h.config.Seed)))
			if err == nil {
				h.observationRevision = sample.AfterRevision
			}
			return err == nil, err
		},
	})
	revision, governance := twoItemRevision(t, "project-performance-outbox", "mission-performance-outbox")
	missionResult := h.createMission("project-performance-outbox", "mission-performance-outbox", "operation-performance-outbox-source-mission")
	result := continueTwoItemMissionScenario(t, h, missionResult, revision, governance, reconciler, false)
	if result.Lifecycle != "completed" {
		t.Fatalf("outbox performance source lifecycle=%s", result.Lifecycle)
	}
	h.close()
	if sample.Family != "family/outbox" || !slices.Equal(sample.EmittedFamilies, []string{"family/operation", "family/outbox"}) || sample.AfterRevision <= sample.BeforeRevision || sample.PublicationCount != 1 || len(sample.PublicationIDs) != 1 {
		t.Fatalf("direct outbox performance sample=%+v", sample)
	}
}

func TestPerformanceSuffixReusesBoundedMissionWithRealActivityMutations(t *testing.T) {
	h := newPortableHarness(t, ProductionConfig{ExecutablePath: productionExecutable(t), Seed: t.Name()})
	result := h.createMission("project-performance-bounded", "mission-performance-bounded", "operation-performance-bounded-create")
	for sequence := 0; sequence < 2; sequence++ {
		result = h.recordMissionActivity(result, fmt.Sprintf("operation-performance-bounded-%d", sequence), sequence)
	}
	if result.State().Version() != 3 || result.State().ActivityCount() != 2 || result.PublicationID() == "" {
		t.Fatalf("bounded performance mission did not publish real activities: version=%d activities=%d publication=%s", result.State().Version(), result.State().ActivityCount(), result.PublicationID())
	}
}

// TestM2AcceptedAuthorityPerformanceCampaign is deliberately gated. The M2
// qualifier invokes this one selector in a fresh process and retains both its
// go-test JSON transcript and the create-once raw sample artifact.
func TestM2AcceptedAuthorityPerformanceCampaign(t *testing.T) {
	output := os.Getenv("SPEC_KITTY_M2_PERFORMANCE_OUTPUT")
	if output == "" {
		t.Skip("M2 performance campaign is invoked only by the qualifier")
	}
	identity := performanceIdentityFromEnvironment(t, output)
	collector := newPerformanceCollector(t)
	var h *portableHarness
	h = newPortableHarness(t, ProductionConfig{
		ExecutablePath: productionExecutable(t), RepositoryRoot: identity.repository,
		Seed: identity.runID + "-performance", CandidateVerifier: sourceBoundCandidateVerifier{},
		CheckpointEvery: performanceWarmups,
		Reconciler:      &scenarioEffectReconciler{}, MutationObserver: collector.observe, PublicationBoundaryObserver: collector,
		AfterEffectIntent: func(ctx context.Context, workID ids.WorkID) (bool, error) {
			if workID != "work-a" {
				return false, nil
			}
			source, sourceRecordRevision := collector.outboxSource(t)
			sample, err := m2external.PublishPerformanceOutboxConnected(ctx, activeGapDBSocket(t, h), source, sourceRecordRevision, "operation-performance-outbox", journalv2.HostAuthorityID("host-"+shortDigest(h.config.Seed)))
			if err != nil {
				return false, err
			}
			collector.appendDirect(t, sample)
			h.observationRevision = sample.AfterRevision
			return true, nil
		},
	})

	collector.setMode(performanceCollectWarmup)
	for index := 0; index < performanceWarmups; index++ {
		h.createMission("project-performance-warmup", fmt.Sprintf("mission-warmup-%05d", index), fmt.Sprintf("operation-warmup-%05d", index))
	}
	collector.setMode(performanceCollectIdle)
	h.close()

	config := m2external.PerformanceConfig{
		RunID: identity.runID, Repository: identity.repository, Executable: productionExecutable(t), QualifierExecutable: identity.qualifier, ProductExecutable: identity.product, Reference: identity.reference,
		RuntimeRoot: filepath.Join(h.root, "performance-runtime"), DataRoot: h.data, Source: identity.source,
		Warmup: performanceWarmups, Observations: performanceSuffixOps, RecoveryRuns: performanceRecoveries,
		StartTimeout: 5 * time.Second, StopTimeout: 5 * time.Second,
	}
	hostAuthorityID := "host-" + shortDigest(h.config.Seed)
	checkpoint, err := m2external.PreparePerformanceCheckpoint(t.Context(), config, hostAuthorityID)
	if err != nil {
		t.Fatalf("prepare performance checkpoint: %v", err)
	}
	// The live warmup Host emitted the one qualified checkpoint at its exact
	// 100-publication threshold. Runtime admission is restored before the
	// scheduler is composed and is not part of its acknowledgement ledger. A restarted suffix
	// Host uses the maximum bounded interval; its direct outbox append and
	// mandatory mid-suffix restart keep each in-memory ledger below that bound,
	// so recovery must replay the complete 10,000-operation suffix.
	h.config.CheckpointEvery = journalv2.MaxAcknowledgementsBetweenSeals
	collector.setMode(performanceCollectIdle)
	h.observationRevision = checkpoint.Revision
	h.start()
	collector.startSuffix(checkpoint.Revision)

	projectID, missionID := "project-performance", "mission-performance"
	revision, governance := twoItemRevision(t, projectID, missionID)
	missionResult := h.createMission(projectID, missionID, "operation-performance-mission-create")
	continueTwoItemMissionScenario(t, h, missionResult, revision, governance, &scenarioEffectReconciler{}, false)

	queryMissions := []performanceMission{{projectID, missionID}}
	fillMissions := make([]mission.ReadinessAuthorityResultV2, 0, performanceFillSlots)
	for index := 0; collector.suffixCount() < performanceSuffixOps; index++ {
		project := "project-performance-fill"
		operationID := fmt.Sprintf("operation-performance-fill-%05d", index)
		if index < performanceFillSlots {
			missionID := fmt.Sprintf("mission-performance-fill-%05d", index)
			fillMissions = append(fillMissions, h.createMission(project, missionID, operationID))
			queryMissions = append(queryMissions, performanceMission{project, missionID})
			continue
		}
		slot := index % len(fillMissions)
		fillMissions[slot] = h.recordMissionActivity(fillMissions[slot], operationID, index)
	}
	collector.finishSuffix(performanceSuffixOps)
	collector.assertPublicationCoverage()

	querySamples := make([]m2.PerformanceSample, 0, performanceQueries)
	for index := 0; index < performanceQueries; index++ {
		coordinate := queryMissions[index%len(queryMissions)]
		started := time.Now()
		progress, err := h.host.Progress().Mission(journalv2.ProjectID(coordinate.project), journalv2.MissionID(coordinate.mission))
		duration := time.Since(started)
		if err != nil || progress.ProjectID == "" || progress.MissionID == "" {
			t.Fatalf("performance query %d: progress=%+v err=%v", index, progress, err)
		}
		querySamples = append(querySamples, m2.PerformanceSample{Kind: "query", Family: "family/mission", DurationNS: duration.Nanoseconds(), Included: true})
	}
	finalStatus := h.host.Status()
	collector.setMode(performanceCollectIdle)
	h.close()
	config.CheckpointRevision = checkpoint.Revision
	config.CheckpointOperations = checkpoint.OperationCount
	config.ExpectedOperationIDs = collector.includedOperationIDs()
	recoveries, err := m2external.RecoverPerformance(context.Background(), config, hostAuthorityID)
	if err != nil {
		t.Fatalf("performance recovery: %v", err)
	}
	if len(recoveries) != performanceRecoveries {
		t.Fatalf("performance recovery census differs: runs=%d", len(recoveries))
	}
	mutationSamples, familyCounts := collector.finalize(t, recoveries[0].Operations, recoveries[0].RuntimeOperations)
	if finalStatus.ProjectionSHA256 != recoveries[0].ProjectionSHA256 {
		t.Fatalf("live canonical projection digest differs from clean recovery: live=%s recovered=%s", finalStatus.ProjectionSHA256, recoveries[0].ProjectionSHA256)
	}
	raw := buildRawPerformance(t, identity, config, checkpoint, finalStatus, mutationSamples, querySamples, recoveries, familyCounts)
	writePerformanceArtifact(t, output, raw)
}

type performanceIdentity struct {
	runID, repository, reference, qualifier, product string
	source                                           m2.Source
}

func performanceIdentityFromEnvironment(t *testing.T, output string) performanceIdentity {
	t.Helper()
	identity := performanceIdentity{
		runID: os.Getenv("SPEC_KITTY_M2_PERFORMANCE_RUN_ID"), repository: repositoryRoot(t),
		reference: os.Getenv("SPEC_KITTY_M2_PERFORMANCE_REFERENCE"),
		qualifier: os.Getenv("SPEC_KITTY_M2_PERFORMANCE_QUALIFIER"), product: os.Getenv("SPEC_KITTY_M2_PERFORMANCE_PRODUCT"),
		source: m2.Source{Commit: os.Getenv("SPEC_KITTY_M2_PERFORMANCE_SOURCE_COMMIT"), Tree: os.Getenv("SPEC_KITTY_M2_PERFORMANCE_SOURCE_TREE"), GoVersion: os.Getenv("SPEC_KITTY_M2_PERFORMANCE_SOURCE_GO"), GoModSHA256: os.Getenv("SPEC_KITTY_M2_PERFORMANCE_SOURCE_GOMOD"), GoSumSHA256: os.Getenv("SPEC_KITTY_M2_PERFORMANCE_SOURCE_GOSUM")},
	}
	if identity.runID == "" || len(identity.source.Commit) != 40 || len(identity.source.Tree) != 40 || !filepath.IsAbs(output) || !filepath.IsAbs(identity.reference) || !filepath.IsAbs(identity.qualifier) || !filepath.IsAbs(identity.product) {
		t.Fatal("performance campaign identity is incomplete")
	}
	parent := filepath.Dir(output)
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatalf("performance output root cannot be inspected: %v", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("performance output root is not private: mode=%v", info.Mode())
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("performance output must not exist: %v", err)
	}
	return identity
}

type performanceCollectMode uint8

const (
	performanceCollectIdle performanceCollectMode = iota
	performanceCollectWarmup
	performanceCollectSuffix
)

type performanceCollector struct {
	t                    *testing.T
	mu                   sync.Mutex
	mode                 performanceCollectMode
	samples              []m2.PerformanceSample
	byOperation          map[string]int
	publishedOperations  map[string]int
	timedPublications    map[string]int
	publicationTrace     map[string][]performancePublicationTrace
	checkpointOperations map[string]int
	sourcePlan           journalv2.SealedCommitPlanV2
	sourceRecordRevision uint64
}

type performancePublicationTrace struct {
	publicationID string
	revision      uint64
	mutationCount uint64
}

func (c *performanceCollector) ObservePublicationBoundary(_ context.Context, snapshot journalv2.PublicationBoundarySnapshotV2) {
	if snapshot.Stage == journalv2.PublicationAckCreatedBeforeProjectionV2 && snapshot.Ack != nil {
		operation := string(snapshot.Plan.OperationState().OperationID())
		c.mu.Lock()
		if c.mode == performanceCollectWarmup || c.mode == performanceCollectSuffix {
			if isPerformanceCheckpointPlan(snapshot.Plan) {
				c.checkpointOperations[operation]++
			} else {
				c.publishedOperations[operation]++
				c.publicationTrace[operation] = append(c.publicationTrace[operation], performancePublicationTrace{
					publicationID: string(snapshot.Ack.PublicationID()), revision: snapshot.Ack.ProviderRevision(), mutationCount: uint64(snapshot.Ack.MutationCount()),
				})
			}
		}
		c.mu.Unlock()
	}
	if snapshot.Stage != journalv2.PublicationDurableObservedBeforeAckV2 || len(snapshot.Plan.EffectIntents()) != 1 || snapshot.Observation == nil {
		return
	}
	var sourceRecordRevision uint64
	for _, record := range snapshot.Plan.Records() {
		if record.Role() == journalv2.RecordAggregateHead && record.Target().Coordinate().Kind == "external_effect" {
			// GapDB assigns one revision to the entire atomic batch; every
			// record in this sealed publication therefore carries the durable
			// observation revision, not a synthetic per-record offset.
			sourceRecordRevision = snapshot.Observation.Revision()
		}
	}
	if sourceRecordRevision == 0 {
		return
	}
	c.mu.Lock()
	c.sourcePlan = snapshot.Plan
	c.sourceRecordRevision = sourceRecordRevision
	c.mu.Unlock()
}

func (c *performanceCollector) outboxSource(t *testing.T) (journalv2.SealedCommitPlanV2, uint64) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sourcePlan.Digest().IsZero() || c.sourceRecordRevision == 0 {
		t.Fatal("performance Host run emitted no durable effect source for direct outbox authority")
	}
	return c.sourcePlan, c.sourceRecordRevision
}

func (c *performanceCollector) appendDirect(t *testing.T, sample m2.PerformanceSample) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if (c.mode != performanceCollectIdle && c.mode != performanceCollectSuffix) || sample.OperationID == "" || !slices.Equal(sample.EmittedFamilies, []string{"family/operation", "family/outbox"}) {
		t.Fatalf("direct outbox performance sample is invalid: %+v", sample)
	}
	if _, exists := c.byOperation[sample.OperationID]; exists {
		t.Fatalf("direct outbox performance operation is duplicated: %s", sample.OperationID)
	}
	sample.Index = len(c.samples)
	sample.Included = c.mode == performanceCollectSuffix
	c.samples = append(c.samples, sample)
	if c.mode == performanceCollectSuffix {
		c.byOperation[sample.OperationID] = len(c.samples) - 1
		c.publishedOperations[sample.OperationID]++
		c.publicationTrace[sample.OperationID] = append(c.publicationTrace[sample.OperationID], performancePublicationTrace{
			publicationID: sample.PublicationIDs[0], revision: sample.AfterRevision, mutationCount: sample.MutationCount,
		})
	}
}

func activeGapDBSocket(t *testing.T, h *portableHarness) string {
	t.Helper()
	if h == nil || h.host == nil {
		t.Fatal("performance outbox requires a running production Host")
	}
	matches, err := filepath.Glob(filepath.Join(h.runtime, "*", "gapdb.sock"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("performance Host socket census=%d err=%v", len(matches), err)
	}
	info, err := os.Lstat(matches[0])
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("performance Host socket is not one direct Unix socket: mode=%v err=%v", infoMode(info), err)
	}
	return matches[0]
}

func infoMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode()
}

func newPerformanceCollector(t *testing.T) *performanceCollector {
	return &performanceCollector{t: t, byOperation: map[string]int{}, publishedOperations: map[string]int{}, timedPublications: map[string]int{}, publicationTrace: map[string][]performancePublicationTrace{}, checkpointOperations: map[string]int{}}
}

func TestPerformanceCollectorClassifiesIdempotentReplayWithoutInventingPublication(t *testing.T) {
	collector := newPerformanceCollector(t)
	collector.setMode(performanceCollectSuffix)
	collector.observe(MutationObservation{OperationID: "operation-replay", BeforeRecordRevision: 42, AfterRecordRevision: 42, ProjectedThroughRevision: 42, Duration: time.Millisecond})
	if collector.suffixCount() != 0 || len(collector.samples) != 1 {
		t.Fatalf("replay changed suffix census: suffix=%d samples=%d", collector.suffixCount(), len(collector.samples))
	}
	sample := collector.samples[0]
	if sample.Included || sample.ExclusionReason != "idempotent_replay" || sample.MutationCount != 0 || sample.PublicationCount != 0 || len(sample.PublicationIDs) != 0 || len(sample.EmittedFamilies) != 0 {
		t.Fatalf("replay evidence fabricated durable mutation: %+v", sample)
	}
}

func TestPerformanceCollectorDoesNotCountRuntimeAdmissionAsAuthorityMutation(t *testing.T) {
	collector := newPerformanceCollector(t)
	collector.setMode(performanceCollectSuffix)
	collector.observe(MutationObservation{
		OperationID: "runtime-host-start-3", BeforeRecordRevision: 42, AfterRecordRevision: 47,
		ProjectedThroughRevision: 47, MutationCount: 5, Duration: time.Millisecond,
	})
	if collector.suffixCount() != 0 || len(collector.samples) != 1 || collector.samples[0].Included || collector.samples[0].ExclusionReason != "runtime_bootstrap" || collector.samples[0].OperationID != "runtime-host-start-3" || collector.samples[0].MutationCount != 0 || collector.samples[0].PublicationCount != 0 || len(collector.samples[0].EmittedFamilies) != 0 {
		t.Fatalf("runtime admission entered authority mutation evidence: suffix=%d samples=%d", collector.suffixCount(), len(collector.samples))
	}
}

func (c *performanceCollector) setMode(mode performanceCollectMode) {
	c.mu.Lock()
	c.mode = mode
	c.mu.Unlock()
}

func (c *performanceCollector) startSuffix(checkpointRevision uint64) {
	c.mu.Lock()
	c.mode = performanceCollectSuffix
	c.byOperation = map[string]int{}
	c.publishedOperations = map[string]int{}
	c.timedPublications = map[string]int{}
	c.publicationTrace = map[string][]performancePublicationTrace{}
	c.checkpointOperations = map[string]int{}
	for index := range c.samples {
		c.samples[index].Index = index
	}
	c.mu.Unlock()
	_ = checkpointRevision
}

func (c *performanceCollector) assertPublicationCoverage() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for operation := range c.byOperation {
		if c.publishedOperations[operation] == 0 {
			c.t.Fatalf("timed performance operation %s has no acknowledged publication trace", operation)
		}
	}
	for operation := range c.publishedOperations {
		if _, timed := c.byOperation[operation]; !timed {
			c.t.Fatalf("acknowledged performance operation %s has no timing observation", operation)
		}
	}
	if len(c.checkpointOperations) > 1 {
		c.t.Fatalf("performance suffix checkpoint operation census=%d want<=1", len(c.checkpointOperations))
	}
	for operation, count := range c.checkpointOperations {
		if count != 1 {
			c.t.Fatalf("performance checkpoint %s publication count=%d want=1", operation, count)
		}
	}
}

func isPerformanceCheckpointPlan(plan journalv2.SealedCommitPlanV2) bool {
	binding := plan.BindingCreate()
	if binding == nil {
		return false
	}
	scope := binding.Scope()
	return len(plan.Events()) == 1 && plan.Events()[0].Family() == "operation" && plan.Events()[0].EventType() == "operation.accepted" &&
		len(plan.Checkpoints()) == 1 && binding.CommandKind() == "checkpoint.write" && binding.CommandSchemaVersion() == 2 &&
		scope == (journalv2.Scope{Kind: journalv2.ScopeHost}) && binding.ActorID() == "runtime-checkpoint" && binding.ProcessID() == "runtime-checkpoint" &&
		binding.DoctrineRevision() == "runtime-v2" && binding.ProfileRevision() == "runtime-v2" && binding.PolicyRevision() == "runtime-v2" &&
		binding.OriginatingEpoch() == plan.Events()[0].CommittedRuntimeEpochID()
}

func (c *performanceCollector) observe(observation MutationObservation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mode == performanceCollectIdle {
		return
	}
	// Runtime epoch admission is direct owner-startup control metadata rather
	// than a command-family publication. Recovery timing below is the sole M2
	// runtime_host performance authority; counting this harness observation
	// would replace one of the exact 10,000 accepted product operations.
	if strings.HasPrefix(observation.OperationID, "runtime-host-start-") {
		c.samples = append(c.samples, m2.PerformanceSample{
			Index: len(c.samples), Kind: "mutation", OperationID: observation.OperationID,
			DurationNS: observation.Duration.Nanoseconds(), Included: false, ExclusionReason: "runtime_bootstrap",
		})
		return
	}
	if observation.Err != nil {
		c.t.Errorf("observed performance mutation %s failed: %v", observation.OperationID, observation.Err)
		return
	}
	acknowledged := observation.OperationID != "" && c.publishedOperations[observation.OperationID] > c.timedPublications[observation.OperationID]
	if !acknowledged && observation.OperationID != "" && observation.AfterRecordRevision == observation.BeforeRecordRevision && observation.MutationCount == 0 && observation.ProjectedThroughRevision == observation.AfterRecordRevision {
		c.samples = append(c.samples, m2.PerformanceSample{Index: len(c.samples), Kind: "mutation", OperationID: observation.OperationID, DurationNS: observation.Duration.Nanoseconds(), Included: false, ExclusionReason: "idempotent_replay", BeforeRevision: observation.BeforeRecordRevision, AfterRevision: observation.AfterRecordRevision, ProjectionRevision: observation.ProjectedThroughRevision})
		return
	}
	if !acknowledged {
		c.t.Errorf("observed performance mutation lacks durable acknowledgement: %+v", observation)
		return
	}
	c.timedPublications[observation.OperationID] = c.publishedOperations[observation.OperationID]
	families := normalizePerformanceFamilies(observation.Families)
	if existing, ok := c.byOperation[observation.OperationID]; ok && c.mode == performanceCollectSuffix {
		sample := &c.samples[existing]
		sample.DurationNS += observation.Duration.Nanoseconds()
		sample.AfterRevision = observation.AfterRecordRevision
		sample.ProjectionRevision = observation.ProjectedThroughRevision
		sample.MutationCount += observation.MutationCount
		sample.PublicationCount++
		sample.EmittedFamilies = unionPerformanceFamilies(sample.EmittedFamilies, families)
		return
	}
	sample := m2.PerformanceSample{
		Index: len(c.samples), Kind: "mutation", OperationID: observation.OperationID,
		Family: primaryPerformanceFamily(families), EmittedFamilies: families,
		DurationNS: observation.Duration.Nanoseconds(), Included: c.mode == performanceCollectSuffix,
		BeforeRevision: observation.BeforeRecordRevision, AfterRevision: observation.AfterRecordRevision,
		MutationCount: observation.MutationCount, PublicationCount: 1,
		ProjectionRevision: observation.ProjectedThroughRevision,
	}
	if c.mode == performanceCollectWarmup {
		sample.ExclusionReason = "warmup"
	}
	c.samples = append(c.samples, sample)
	if c.mode == performanceCollectSuffix {
		c.byOperation[observation.OperationID] = len(c.samples) - 1
	}
}

func (c *performanceCollector) suffixCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.byOperation)
}

func (c *performanceCollector) finishSuffix(want int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.byOperation) != want {
		c.t.Fatalf("performance suffix operation census=%d want=%d", len(c.byOperation), want)
	}
	c.mode = performanceCollectIdle
}

func (c *performanceCollector) includedOperationIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]string, 0, len(c.byOperation))
	for operation := range c.byOperation {
		result = append(result, operation)
	}
	slices.Sort(result)
	return result
}

func (c *performanceCollector) finalize(t *testing.T, actual map[string]m2external.RecoveredOperation, runtimeOperations []string) ([]m2.PerformanceSample, map[string]int) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	counts := map[string]int{}
	for index := range c.samples {
		sample := &c.samples[index]
		sample.Index = index
		if sample.ExclusionReason == "warmup" {
			// Warmups precede the qualified checkpoint. Retain their timing, but
			// do not project pre-checkpoint observer labels or publication facts
			// into the post-checkpoint evidence census.
			sample.Family = ""
			sample.EmittedFamilies = nil
			sample.BeforeRevision = 0
			sample.AfterRevision = 0
			sample.ProjectionRevision = 0
			sample.MutationCount = 0
			sample.PublicationCount = 0
			sample.PublicationIDs = nil
			continue
		}
		if sample.ExclusionReason == "idempotent_replay" {
			continue
		}
		if sample.ExclusionReason == "runtime_bootstrap" {
			continue
		}
		operation := sample.OperationID
		recovered, ok := actual[operation]
		if !ok || len(recovered.Families) == 0 {
			t.Fatalf("operation %s has no restored emitted-family evidence", operation)
		}
		// Durable event envelopes, not capability labels supplied to the timing
		// observer, are the authority for exact family membership.
		sample.EmittedFamilies = append([]string(nil), recovered.Families...)
		sample.Family = primaryPerformanceFamily(sample.EmittedFamilies)
		sample.PublicationIDs = append([]string(nil), recovered.PublicationIDs...)
		trace := append([]performancePublicationTrace(nil), c.publicationTrace[operation]...)
		slices.SortFunc(trace, func(left, right performancePublicationTrace) int {
			if left.revision < right.revision {
				return -1
			}
			if left.revision > right.revision {
				return 1
			}
			return strings.Compare(left.publicationID, right.publicationID)
		})
		traceIDs := make([]string, len(trace))
		var mutationCount uint64
		for traceIndex, publication := range trace {
			traceIDs[traceIndex] = publication.publicationID
			mutationCount += publication.mutationCount
		}
		slices.Sort(traceIDs)
		if len(trace) == 0 || !slices.Equal(traceIDs, recovered.PublicationIDs) || trace[0].revision == 0 || mutationCount == 0 {
			t.Fatalf("operation %s durable publication trace differs from recovery: trace=%v recovered=%v", operation, traceIDs, recovered.PublicationIDs)
		}
		sample.BeforeRevision = trace[0].revision - 1
		sample.AfterRevision = trace[len(trace)-1].revision
		sample.ProjectionRevision = sample.AfterRevision
		sample.MutationCount = mutationCount
		// One accepted authority operation may cross nested provider services.
		// The timing observer sees outer returns; restored durable envelopes own
		// the complete publication membership and its exact count.
		sample.PublicationCount = len(sample.PublicationIDs)
		if sample.Included {
			for _, family := range recovered.Families {
				counts[family]++
			}
		}
	}
	if len(runtimeOperations) != 0 {
		t.Fatalf("post-checkpoint product census contains runtime-host operations=%d", len(runtimeOperations))
	}
	wantFamilies := slices.DeleteFunc(m2.AuthorityFamilyIDs(), func(family string) bool { return family == "family/runtime_host" })
	if !slices.Equal(sortedMapKeys(counts), wantFamilies) {
		t.Fatalf("performance suffix family census=%v want=%v", sortedMapKeys(counts), wantFamilies)
	}
	return append([]m2.PerformanceSample(nil), c.samples...), counts
}

func TestPerformanceCollectorFinalizationProjectsOneClosedRawCensus(t *testing.T) {
	collector := newPerformanceCollector(t)
	collector.samples = []m2.PerformanceSample{
		{Index: 0, Kind: "mutation", OperationID: "warmup", DurationNS: int64(time.Millisecond), Included: false, ExclusionReason: "warmup", BeforeRevision: 1, AfterRevision: 2, MutationCount: 1, PublicationCount: 1},
		{Index: 1, Kind: "mutation", OperationID: "replay", DurationNS: int64(time.Millisecond), Included: false, ExclusionReason: "idempotent_replay", BeforeRevision: 3, AfterRevision: 3, ProjectionRevision: 3},
		{Index: 2, Kind: "mutation", OperationID: "runtime-host-start-3", DurationNS: int64(time.Millisecond), Included: false, ExclusionReason: "runtime_bootstrap"},
	}
	actual := map[string]m2external.RecoveredOperation{}
	revision := uint64(100)
	for _, family := range m2.AuthorityFamilyIDs() {
		if family == "family/runtime_host" || family == "family/operation" {
			continue
		}
		operation := "fixture-" + strings.TrimPrefix(family, "family/")
		families := []string{family, "family/operation"}
		slices.Sort(families)
		publications := []string{m2.SHA256([]byte(operation + "-publication-0"))}
		trace := []performancePublicationTrace{{publicationID: publications[0], revision: revision, mutationCount: 3}}
		if len(collector.byOperation) == 0 {
			publications = append(publications, m2.SHA256([]byte(operation+"-publication-1")))
			slices.Sort(publications)
			trace = append(trace, performancePublicationTrace{publicationID: publications[1], revision: revision + 1, mutationCount: 4})
			revision++
		}
		index := len(collector.samples)
		collector.samples = append(collector.samples, m2.PerformanceSample{Index: index, Kind: "mutation", OperationID: operation, DurationNS: int64(200 * time.Microsecond), Included: true})
		collector.byOperation[operation] = index
		collector.publishedOperations[operation] = len(trace)
		collector.timedPublications[operation] = len(trace)
		collector.publicationTrace[operation] = trace
		actual[operation] = m2external.RecoveredOperation{Families: families, PublicationIDs: publications}
		revision++
	}
	collector.checkpointOperations["opaque-checkpoint-identity"] = 1
	collector.assertPublicationCoverage()
	samples, familyCounts := collector.finalize(t, actual, nil)
	if len(samples) != len(actual)+3 || samples[0].Family != "" || samples[2].ExclusionReason != "runtime_bootstrap" {
		t.Fatalf("collector did not preserve the closed exclusion census: samples=%d first=%+v bootstrap=%+v", len(samples), samples[0], samples[2])
	}

	raw := m2.RawPerformance{
		SchemaVersion: 1, RunID: "collector-finalize", SourceCommit: strings.Repeat("a", 40), SourceTree: strings.Repeat("b", 40), ReferenceProfileSHA256: strings.Repeat("c", 64),
		Dataset:            m2.PerformanceDataset{Generator: m2external.PerformanceGenerator, WarmupOperations: 1, SuffixOperations: performanceSuffixOps, CheckpointRevision: 99, FamilyOperationCounts: familyCounts, FamilyRecoveryCounts: map[string]int{"family/runtime_host": performanceRecoveries}, ExpectedProjectionSHA: strings.Repeat("d", 64), FinalRevision: revision + 20_000},
		RecoveryOperations: performanceSuffixOps, Samples: samples, WarmupExcluded: 1, ReplayExcluded: 1, RuntimeBootstrapExcluded: 1, Method: "monotonic-nearest-rank-inclusive",
	}
	for _, sample := range samples {
		if sample.Included {
			raw.MutationMS = append(raw.MutationMS, float64(sample.DurationNS)/float64(time.Millisecond))
		}
	}
	for len(raw.MutationMS) < performanceSuffixOps {
		index := len(raw.MutationMS)
		operation := fmt.Sprintf("bulk-fixture-%05d", index)
		publication := m2.SHA256([]byte(operation + "-publication"))
		sample := m2.PerformanceSample{Index: len(raw.Samples), Kind: "mutation", Family: "family/mission", OperationID: operation, EmittedFamilies: []string{"family/mission", "family/operation"}, DurationNS: int64(200 * time.Microsecond), Included: true, BeforeRevision: revision, AfterRevision: revision + 1, MutationCount: 3, PublicationCount: 1, ProjectionRevision: revision + 1, PublicationIDs: []string{publication}}
		revision++
		raw.Samples = append(raw.Samples, sample)
		raw.MutationMS = append(raw.MutationMS, .2)
		raw.Dataset.FamilyOperationCounts["family/mission"]++
		raw.Dataset.FamilyOperationCounts["family/operation"]++
	}
	for range performanceQueries {
		raw.Samples = append(raw.Samples, m2.PerformanceSample{Index: len(raw.Samples), Kind: "query", Family: "family/mission", DurationNS: int64(100 * time.Microsecond), Included: true})
		raw.QueryMS = append(raw.QueryMS, .1)
	}
	for range performanceRecoveries {
		raw.Samples = append(raw.Samples, m2.PerformanceSample{Index: len(raw.Samples), Kind: "recovery", Family: "family/runtime_host", EmittedFamilies: []string{"family/runtime_host"}, DurationNS: int64(time.Millisecond), Included: true, ProjectionSHA256: raw.Dataset.ExpectedProjectionSHA, ObservedOperations: performanceSuffixOps, ProjectionRevision: raw.Dataset.FinalRevision})
		raw.RecoveryMS = append(raw.RecoveryMS, 1)
	}
	raw.Statistics = []m2.PerformanceStatistics{
		m2.PerformanceStatisticsFor("mutation", raw.MutationMS, raw.WarmupExcluded+raw.ReplayExcluded+raw.RuntimeBootstrapExcluded),
		m2.PerformanceStatisticsFor("query", raw.QueryMS, 0),
		m2.PerformanceStatisticsFor("recovery", raw.RecoveryMS, 0),
	}
	if _, err := m2.SummarizePerformance(raw); err != nil {
		t.Fatalf("closed collector projection failed the full raw verifier: %v", err)
	}
}

func buildRawPerformance(t *testing.T, identity performanceIdentity, config m2external.PerformanceConfig, checkpoint m2external.CheckpointResult, final productionruntime.HostStatus, mutations, queries []m2.PerformanceSample, recoveries []m2external.RecoveryObservation, familyCounts map[string]int) m2.RawPerformance {
	t.Helper()
	reference, err := os.ReadFile(identity.reference)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := m2external.ObservePerformanceEnvironment(config)
	if err != nil {
		t.Fatal(err)
	}
	raw := m2.RawPerformance{
		SchemaVersion: 1, RunID: identity.runID, SourceCommit: identity.source.Commit, SourceTree: identity.source.Tree,
		ReferenceProfileSHA256: m2.SHA256(reference), Environment: environment,
		Dataset:            m2.PerformanceDataset{Generator: m2external.PerformanceGenerator, WarmupOperations: performanceWarmups, SuffixOperations: performanceSuffixOps, CheckpointRevision: checkpoint.Revision, FamilyOperationCounts: familyCounts, FamilyRecoveryCounts: map[string]int{"family/runtime_host": len(recoveries)}, ExpectedProjectionSHA: final.ProjectionSHA256, FinalRevision: final.ProjectionRevision},
		RecoveryOperations: performanceSuffixOps, WarmupExcluded: performanceWarmups, ReplayExcluded: countExclusions(mutations, "idempotent_replay"), RuntimeBootstrapExcluded: countExclusions(mutations, "runtime_bootstrap"), Method: "monotonic-nearest-rank-inclusive",
	}
	raw.Samples = append(raw.Samples, mutations...)
	for _, sample := range mutations {
		if sample.Included {
			raw.MutationMS = append(raw.MutationMS, float64(sample.DurationNS)/float64(time.Millisecond))
		}
	}
	for index := range queries {
		queries[index].Index = len(raw.Samples)
		raw.Samples = append(raw.Samples, queries[index])
		raw.QueryMS = append(raw.QueryMS, float64(queries[index].DurationNS)/float64(time.Millisecond))
	}
	for _, recovered := range recoveries {
		sample := m2.PerformanceSample{Index: len(raw.Samples), Kind: "recovery", Family: "family/runtime_host", EmittedFamilies: []string{"family/runtime_host"}, DurationNS: recovered.DurationNS, Included: true, ProjectionSHA256: recovered.ProjectionSHA256, ObservedOperations: recovered.ObservedOperations, ProjectionRevision: final.ProjectionRevision}
		raw.Samples = append(raw.Samples, sample)
		raw.RecoveryMS = append(raw.RecoveryMS, float64(recovered.DurationNS)/float64(time.Millisecond))
	}
	raw.Statistics = []m2.PerformanceStatistics{
		m2.PerformanceStatisticsFor("mutation", raw.MutationMS, performanceWarmups+raw.ReplayExcluded+raw.RuntimeBootstrapExcluded),
		m2.PerformanceStatisticsFor("query", raw.QueryMS, 0),
		m2.PerformanceStatisticsFor("recovery", raw.RecoveryMS, 0),
	}
	return raw
}

func countExclusions(samples []m2.PerformanceSample, reason string) int {
	count := 0
	for _, sample := range samples {
		if sample.ExclusionReason == reason {
			count++
		}
	}
	return count
}

func writePerformanceArtifact(t *testing.T, path string, raw m2.RawPerformance) {
	t.Helper()
	encoded, err := m2.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

type performanceMission struct{ project, mission string }

func normalizePerformanceFamilies(values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		if !strings.HasPrefix(value, "family/") {
			value = "family/" + value
		}
		result[index] = value
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func unionPerformanceFamilies(left, right []string) []string {
	return normalizePerformanceFamilies(append(append([]string(nil), left...), right...))
}

func primaryPerformanceFamily(families []string) string {
	for _, family := range families {
		if family != "family/operation" {
			return family
		}
	}
	return "family/operation"
}

func sortedMapKeys(values map[string]int) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	slices.Sort(result)
	return result
}
