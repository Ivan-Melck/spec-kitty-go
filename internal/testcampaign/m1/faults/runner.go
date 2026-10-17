package faults

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/application"
	journalcheckpoint "github.com/spec-kitty/go-kitty3/internal/controljournal/checkpoint"
	journalplayback "github.com/spec-kitty/go-kitty3/internal/controljournal/playback"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/reducer"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	productfaults "github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	baseintegration "github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/provingground"
	basereview "github.com/spec-kitty/go-kitty3/internal/review"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	m1 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	trackerfake "github.com/spec-kitty/go-kitty3/internal/tracker/fake"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type Outcome string

const (
	OutcomePass       Outcome = "pass"
	OutcomeFail       Outcome = "fail"
	OutcomeIncomplete Outcome = "incomplete"

	// qualificationLeaseTTL is the explicit budget for the longest single
	// admitted qualification operation between renewals. The crown command's
	// 12-hour context remains the outer campaign budget. This is an authority
	// policy, not a wall-clock pass predicate; explicit expiry and zombie trials
	// still advance to this exact deadline.
	qualificationLeaseTTL = 30 * time.Minute
)

type Definition struct {
	ID  string `json:"fault_id"`
	Cut string `json:"cut"`
}

var definitions = []Definition{
	{ID: "GJ-01", Cut: "before_request_publication"},
	{ID: "GJ-02", Cut: "after_durable_acceptance_before_client_response"},
	{ID: "GJ-03", Cut: "after_workspace_creation_before_durable_binding"},
	{ID: "GJ-04", Cut: "after_durable_binding_before_run_capsule_response"},
	{ID: "GJ-05", Cut: "after_effect_intent_before_observation"},
}

func Definitions() []Definition { return append([]Definition(nil), definitions...) }

type SourceBinding = m1.SourceBinding

type ProductFact struct {
	Revision uint64 `json:"revision"`
	Digest   string `json:"digest"`
}

type Facts struct {
	PreexistingBefore map[string]ProductFact `json:"preexisting_before,omitempty"`
	PreexistingAfter  map[string]ProductFact `json:"preexisting_after,omitempty"`
	OperationRecords  int                    `json:"operation_records"`
	OperationResults  int                    `json:"operation_results"`
	DurableAccepted   bool                   `json:"durable_accepted"`

	DaemonResponseComplete        bool                          `json:"daemon_response_complete"`
	ProxyConsumedCompleteResponse bool                          `json:"proxy_consumed_complete_response"`
	ProxyCut                      *sidecargapdb.ResponseDropCut `json:"proxy_cut,omitempty"`
	SidecarProvenanceSHA256       string                        `json:"sidecar_provenance_sha256,omitempty"`
	HeartbeatOperationID          string                        `json:"heartbeat_operation_id,omitempty"`
	HeartbeatWorkID               string                        `json:"heartbeat_work_id,omitempty"`
	AuthorityHeadIDs              []string                      `json:"authority_head_ids,omitempty"`

	PhysicalWorkspaceCreated bool   `json:"physical_workspace_created"`
	PhysicalWorkspaceID      string `json:"physical_workspace_id,omitempty"`
	PhysicalHasAuthority     bool   `json:"physical_has_authority"`
	DurableBinding           bool   `json:"durable_binding"`
	CapsuleDelivered         bool   `json:"capsule_delivered"`
	CapsuleReconstructed     bool   `json:"capsule_reconstructed"`
	CapsuleBindingMatches    bool   `json:"capsule_binding_matches"`
	RecoveredWorkspaceID     string `json:"recovered_workspace_id,omitempty"`
	DurableWorkspaceCount    int    `json:"durable_workspace_count"`

	EffectIntentDurable        bool `json:"effect_intent_durable"`
	ProviderObservationDurable bool `json:"provider_observation_durable"`
	ProviderDispatches         int  `json:"provider_dispatches"`
	AttributableOutcomes       int  `json:"attributable_outcomes"`
	BlindRepeats               int  `json:"blind_repeats"`
	UnsafeCompensations        int  `json:"unsafe_compensations"`
}

type Artifact struct {
	Kind      string `json:"kind"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

type Execution struct {
	Facts       Facts
	Observation m1.ObservationPort
	Artifacts   []Artifact
	Source      SourceBinding
	Receipt     ProductionReceipt
}

// ProductionReceipt binds the exact product execution to one runner attempt.
// Its seal is package-private: ordinary Executor implementations may return
// failure evidence, but only ProductionExecutor can mint pass evidence.
type ProductionReceipt struct {
	RunID             string        `json:"run_id"`
	FaultID           string        `json:"fault_id"`
	AttemptID         string        `json:"attempt_id"`
	GuardPackage      string        `json:"guard_package"`
	GuardTest         string        `json:"guard_test"`
	GuardOutputSHA256 string        `json:"guard_output_sha256"`
	FactSHA256        string        `json:"fact_sha256"`
	ArtifactSHA256    string        `json:"artifact_sha256"`
	InvariantSHA256   string        `json:"invariant_sha256"`
	Source            SourceBinding `json:"source"`
	seal              [32]byte
}

// Attempt exposes the production fault hook. Executors attach Hook to the
// component crossing the boundary; directly fabricating a timeout never calls
// Reach and therefore cannot pass qualification.
type Attempt struct {
	RunID      string
	Seed       uint64
	Definition Definition
	Hook       productfaults.Hook
}

type Executor interface {
	ExecuteFault(context.Context, Attempt) (Execution, error)
}

type ExecutorFunc func(context.Context, Attempt) (Execution, error)

func (f ExecutorFunc) ExecuteFault(ctx context.Context, attempt Attempt) (Execution, error) {
	return f(ctx, attempt)
}

// ProductionExecutor runs the exact named product fault guard in the observed
// checkout. GJ-02 crosses the live admitted proxy itself so its cut carries the
// same source, process, database, and sidecar provenance as the resulting
// Record. The returned receipt is inseparable from those observations.
type ProductionExecutor struct {
	Root      string
	Lifecycle *sidecargapdb.Lifecycle
	// RunID, when set, is the immutable umbrella identity for every durable
	// product fact produced by this executor. Calls carrying a different RunID
	// fail closed instead of silently assembling evidence across runs.
	RunID       string
	Observation m1.ObservationPort
	mu          sync.Mutex
	environment *productionEnvironment
}

type productionEnvironment struct {
	root        string
	runID       string
	port        storegapdb.Port
	closer      interface{ Close() error }
	app         *application.Service
	admission   application.EpochAdmissionV1
	runtime     *provingground.Runtime
	repository  *gitrepo.Repository
	baseline    provingground.Delivery
	workA       *provingground.Delivery
	identities  *qualificationIdentities
	events      controljournal.EventRegistryV1
	reducers    reducer.RegistryV1
	binding     journalcheckpoint.RestoreBindingV1
	reader      journalcheckpoint.RestoreReaderV1
	reviews     *provingground.ReviewCoordinator
	integrator  *provingground.IntegrationCoordinator
	predecessor *controljournal.AuthorityCapsuleV1
}

// ProductionAccess exposes only the public production ports and observation
// capability needed by the sibling focused campaign. It carries no private
// state mutation shortcut.
type ProductionAccess struct {
	Source      m1.SourceBinding
	Port        storegapdb.Port
	Observation m1.ObservationPort
	Provenance  sidecargapdb.SidecarProvenance
}

// FocusedRendezvous is the acknowledgement-only scheduling seam used by the
// focused campaigns. The implementation deliberately has no clock method.
type FocusedRendezvous interface {
	Arrive(context.Context) error
}

// FocusedInput binds one production execution to the runner-owned schedule.
type FocusedInput struct {
	RunID, CampaignID string
	AttemptID         string
	Index             int
	Seed              uint64
	Order             [2]int
	Barrier           FocusedRendezvous
}

// FocusedProviderEvidence contains observations from the real file-backed
// tracker and EffectCoordinator. It is intentionally provider-neutral so the
// sibling campaign can classify it without gaining a mutable product handle.
type FocusedProviderEvidence struct {
	State                string
	MutationAttempts     int
	ObservationAttempts  int
	RevisionBefore       string
	RevisionAfter        string
	IdentityMatched      bool
	IdentityInspected    bool
	CancellationObserved bool
	TypedUnavailable     bool
	AppliedRecorded      bool
	BlindRepeats         int
	CompensationApplied  bool
	HumanValuePreserved  bool
}

// FocusedExecution is minted only after the named production behavior has
// completed. Facts are exact predicates derived from public results, never
// caller assertions or test names.
type FocusedExecution struct {
	Source             m1.SourceBinding
	Observation        m1.ObservationPort
	ExternalInspection storegapdb.Inspection
	ExternalProvenance sidecargapdb.SidecarProvenance
	Artifact           []byte
	Facts              map[string]bool
	Provider           FocusedProviderEvidence
}

func (p *ProductionExecutor) Prepare(ctx context.Context) (ProductionAccess, error) {
	if p == nil || p.Root == "" || p.Lifecycle == nil {
		return ProductionAccess{}, fmt.Errorf("production executor is incomplete")
	}
	source, err := m1.ObserveSource(ctx, p.Root, p.Lifecycle)
	if err != nil {
		return ProductionAccess{}, err
	}
	environment, err := p.productionEnvironment(ctx, p.RunID)
	if err != nil {
		return ProductionAccess{}, err
	}
	if err := ensureBaseline(ctx, environment); err != nil {
		return ProductionAccess{}, err
	}
	observation, err := observeComposedRuntime(ctx, environment, nil)
	if err != nil {
		return ProductionAccess{}, err
	}
	return ProductionAccess{Source: source, Port: environment.port, Observation: observation, Provenance: p.Lifecycle.Provenance()}, nil
}

// ExecuteFocused runs one exact production behavior. It does not mint the
// focused package's receipt; it returns the public observations from which
// that sibling package can mint one receipt for this one attempt.
func (p *ProductionExecutor) ExecuteFocused(ctx context.Context, input FocusedInput) (FocusedExecution, error) {
	if p == nil || input.RunID == "" || input.CampaignID == "" || input.AttemptID == "" || input.Seed == 0 || input.Index < 0 || input.Barrier == nil || !validFocusedOrder(input.Order) {
		return FocusedExecution{}, fmt.Errorf("focused production input is incomplete")
	}
	source, err := m1.ObserveSource(ctx, p.Root, p.Lifecycle)
	if err != nil {
		return FocusedExecution{}, err
	}
	environment, err := p.productionEnvironment(ctx, input.RunID)
	if err != nil {
		return FocusedExecution{Source: source}, err
	}
	if err := ensureBaseline(ctx, environment); err != nil {
		return FocusedExecution{Source: source}, err
	}
	primaryBefore, err := environment.repository.SnapshotPrimary(ctx)
	if err != nil {
		return FocusedExecution{Source: source}, err
	}
	var artifact []byte
	var facts map[string]bool
	var provider FocusedProviderEvidence
	var effects []m1.EffectObservation
	var overlay qualificationOverlay
	switch input.CampaignID {
	case "atomic-publication-nfr001":
		artifact, facts, err = executeFocusedAtomic(ctx, input, p.Lifecycle, source, environment)
	case "lost-response-idempotency-nfr002":
		artifact, facts, err = executeFocusedLostResponse(ctx, input, p.Lifecycle, source, environment)
	case "concurrent-claims-nfr003":
		artifact, facts, overlay, err = executeFocusedClaims(ctx, input, environment)
	case "competing-integrations-nfr004":
		artifact, facts, overlay, err = executeFocusedIntegrations(ctx, input, environment)
	case "restore-playback-nfr005":
		var playback m1.PlaybackObservation
		var journal controljournal.ValidatedJournalV1
		var observedRevision uint64
		artifact, facts, playback, journal, observedRevision, err = executeFocusedRestorePlayback(ctx, input, environment)
		if err == nil {
			overlay.playback = &playback
			overlay.journal = &journal
			overlay.observedRevision = observedRevision
		}
	case "restart-epoch-nfr007":
		artifact, facts, err = p.executeFocusedEpoch(ctx, input, environment)
	case "zombie-isolation-nfr008":
		artifact, facts, err = executeFocusedZombie(ctx, input, environment)
	case "attribution-reassignment-nfr015":
		artifact, facts, provider, effects, err = executeFocusedAttribution(ctx, input, environment)
	default:
		return FocusedExecution{Source: source}, fmt.Errorf("unknown focused production campaign %q", input.CampaignID)
	}
	if err != nil {
		return FocusedExecution{Source: source, Artifact: artifact, Facts: facts, Provider: provider}, err
	}
	if err := validateFocusedFactCensus(input.CampaignID, facts); err != nil {
		return FocusedExecution{Source: source, Artifact: artifact, Facts: facts, Provider: provider}, err
	}
	// A restart changes the executable owner provenance. Bind the receipt to
	// the owner that actually completed the attempt, not its predecessor.
	source, err = m1.ObserveSource(ctx, p.Root, p.Lifecycle)
	if err != nil {
		return FocusedExecution{Artifact: artifact, Facts: facts, Provider: provider}, err
	}
	primaryAfter, err := environment.repository.SnapshotPrimary(ctx)
	if err != nil {
		return FocusedExecution{Source: source, Artifact: artifact, Facts: facts, Provider: provider}, err
	}
	overlay.primary = m1.PrimaryObservation{Inspectable: true, Before: gitSnapshotObservation(primaryBefore), After: gitSnapshotObservation(primaryAfter)}
	observation, err := observeComposedRuntimeWithOverlay(ctx, environment, effects, overlay)
	if err != nil {
		return FocusedExecution{Source: source, Artifact: artifact, Facts: facts, Provider: provider}, err
	}
	externalInspection, err := environment.port.Inspect(ctx)
	if err != nil {
		return FocusedExecution{Source: source, Observation: observation, Artifact: artifact, Facts: facts, Provider: provider}, err
	}
	return FocusedExecution{Source: source, Observation: observation, ExternalInspection: externalInspection, ExternalProvenance: p.Lifecycle.Provenance(), Artifact: artifact, Facts: facts, Provider: provider}, nil
}

var focusedFactCensus = map[string][]string{
	"atomic-publication-nfr001":        {"gj01_cut_once", "gj01_no_partial_batch", "gj01_preexisting_preserved", "gj02_cut_once", "gj02_complete_response", "gj02_durable_acceptance", "gj02_one_operation_result", "schedule_consumed"},
	"lost-response-idempotency-nfr002": {"changed_digest_no_publication", "changed_digest_refused", "equal_retry_durable_acceptance", "equal_retry_one_operation", "equal_retry_same_revision", "exact_proxy_source", "schedule_consumed", "zero_blind_effect_repeats"},
	"concurrent-claims-nfr003":         {"both_items_progress", "distinct_authorities", "participant_one", "participant_zero"},
	"competing-integrations-nfr004":    {"both_participants", "git_target_moved_once", "one_candidate_publication", "one_target_authority"},
	"restore-playback-nfr005":          {"digest_equal", "history_materialized", "playback_zero_io", "restore_ready", "schedule_consumed"},
	"restart-epoch-nfr007":             {"current_epoch_accepted", "fresh_owner_process", "fresh_private_endpoint", "fresh_runtime_instance", "new_epoch_unique", "predecessor_refused", "ready_new_lifetime", "restart_mode_observed", "same_durable_database", "schedule_consumed"},
	"zombie-isolation-nfr008":          {"fresh_content_unchanged", "fresh_head_unchanged", "fresh_index_unchanged", "fresh_tree_unchanged", "old_workspace_posix_mutated", "replacement_lease_differs", "replacement_workspace_differs", "schedule_consumed", "stale_authority_refused"},
	"attribution-reassignment-nfr015":  {"effect_intent_durable", "one_typed_attempt", "provider_state_observed", "schedule_consumed", "zero_blind_repeats"},
}

func validateFocusedFactCensus(campaignID string, facts map[string]bool) error {
	required, ok := focusedFactCensus[campaignID]
	if !ok {
		return fmt.Errorf("focused semantic census is undefined for %q", campaignID)
	}
	if len(facts) != len(required) {
		return fmt.Errorf("focused %s semantic census=%d want=%d", campaignID, len(facts), len(required))
	}
	for _, name := range required {
		if passed, exists := facts[name]; !exists || !passed {
			return fmt.Errorf("focused %s required semantic fact %q is absent or false", campaignID, name)
		}
	}
	return nil
}

func gitSnapshotObservation(snapshot workspace.PrimarySnapshot) m1.GitSnapshotObservation {
	return m1.GitSnapshotObservation{Head: snapshot.Head, IndexDigest: snapshot.IndexDigest, TreeDigest: snapshot.TreeDigest}
}

func validFocusedOrder(order [2]int) bool {
	return (order == [2]int{0, 1}) || (order == [2]int{1, 0})
}

func focusedAttempt(input FocusedInput, label string) Attempt {
	return Attempt{RunID: input.RunID + "-" + input.AttemptID, Seed: input.Seed, Definition: Definition{ID: label}}
}

func executeFocusedAtomic(ctx context.Context, input FocusedInput, lifecycle *sidecargapdb.Lifecycle, source SourceBinding, environment *productionEnvironment) ([]byte, map[string]bool, error) {
	if err := consumeFocusedSchedule(ctx, input); err != nil {
		return nil, nil, err
	}
	gj01Hook := &countedCrashHook{}
	gj01 := focusedAttempt(input, "GJ-01")
	gj01.Definition = Definition{ID: "GJ-01", Cut: "before_request_publication"}
	gj01.Hook = gj01Hook
	gj01Facts, gj01Artifact, err := executeComposedGJ01(ctx, gj01, environment)
	if err != nil {
		return gj01Artifact, nil, err
	}
	gj02Hook := &countedCrashHook{}
	gj02 := focusedAttempt(input, "GJ-02")
	gj02.Definition = Definition{ID: "GJ-02", Cut: "after_durable_acceptance_before_client_response"}
	gj02.Hook = gj02Hook
	gj02Facts, gj02Artifact, err := executeComposedGJ02(ctx, gj02, lifecycle, source, environment)
	if err != nil {
		return gj02Artifact, nil, err
	}
	facts := map[string]bool{
		"gj01_cut_once":              gj01Hook.calls.Load() == 1,
		"gj01_no_partial_batch":      gj01Facts.OperationRecords == 0 && gj01Facts.OperationResults == 0,
		"gj01_preexisting_preserved": equalProductFacts(gj01Facts.PreexistingBefore, gj01Facts.PreexistingAfter),
		"gj02_cut_once":              gj02Hook.calls.Load() == 1,
		"gj02_durable_acceptance":    gj02Facts.DurableAccepted,
		"gj02_complete_response":     gj02Facts.DaemonResponseComplete && gj02Facts.ProxyConsumedCompleteResponse,
		"gj02_one_operation_result":  gj02Facts.OperationRecords == 1 && gj02Facts.OperationResults == 1,
		"schedule_consumed":          input.Order[0] != input.Order[1],
	}
	artifact, _ := json.Marshal(struct {
		Input FocusedInput    `json:"input"`
		GJ01  json.RawMessage `json:"gj01"`
		GJ02  json.RawMessage `json:"gj02"`
		Facts map[string]bool `json:"facts"`
	}{input, gj01Artifact, gj02Artifact, facts})
	return artifact, facts, nil
}

type countedCrashHook struct{ calls atomic.Int32 }

func (h *countedCrashHook) Reach(context.Context, string) error {
	h.calls.Add(1)
	return productfaults.ErrCrashRequested
}

func executeFocusedLostResponse(ctx context.Context, input FocusedInput, lifecycle *sidecargapdb.Lifecycle, source SourceBinding, environment *productionEnvironment) ([]byte, map[string]bool, error) {
	if err := consumeFocusedSchedule(ctx, input); err != nil {
		return nil, nil, err
	}
	attempt := focusedAttempt(input, "GJ-02")
	attempt.Definition = Definition{ID: "GJ-02", Cut: "after_durable_acceptance_before_client_response"}
	hook := &countedCrashHook{}
	attempt.Hook = hook
	faultFacts, artifact, err := executeComposedGJ02(ctx, attempt, lifecycle, source, environment)
	if err != nil {
		return artifact, nil, err
	}
	prefix := "spk/v1/project-qualification/mission-qualification/"
	before, err := productCensus(ctx, environment.port, prefix)
	if err != nil {
		return artifact, nil, err
	}
	effectsBefore, err := productCensus(ctx, environment.port, prefix+"effects/")
	if err != nil {
		return artifact, nil, err
	}
	changedPayload := []byte(fmt.Sprintf(`{"attempt":%q,"seed":%d,"changed":true}`, productionAttemptID(attempt), attempt.Seed))
	changed, err := qualificationBatchPayload(attempt, "gj02", environment.runtime.QualificationSnapshot().RuntimeEpochID, changedPayload)
	if err != nil {
		return artifact, nil, err
	}
	changedResult, changedErr := environment.app.Apply(ctx, changed)
	after, censusErr := productCensus(ctx, environment.port, prefix)
	if censusErr != nil {
		return artifact, nil, censusErr
	}
	effectsAfter, err := productCensus(ctx, environment.port, prefix+"effects/")
	if err != nil {
		return artifact, nil, err
	}
	facts := map[string]bool{
		"equal_retry_durable_acceptance": faultFacts.DurableAccepted && faultFacts.DaemonResponseComplete && faultFacts.ProxyConsumedCompleteResponse,
		"equal_retry_one_operation":      faultFacts.OperationRecords == 1 && faultFacts.OperationResults == 1,
		"equal_retry_same_revision":      faultFacts.ProxyCut != nil && faultFacts.ProxyCut.Revision > 0,
		"changed_digest_refused":         changedErr == nil && changedResult.Kind == application.OutcomeOperationIdentityCollision,
		"changed_digest_no_publication":  equalProductFacts(before, after),
		"zero_blind_effect_repeats":      equalProductFactsAllowEmpty(effectsBefore, effectsAfter),
		"exact_proxy_source":             hook.calls.Load() == 1 && faultFacts.ProxyCut != nil && faultFacts.ProxyCut.SourceCommit == source.Commit && faultFacts.SidecarProvenanceSHA256 == source.SidecarProvenanceSHA256,
		"schedule_consumed":              input.Order[0] != input.Order[1],
	}
	encoded, _ := json.Marshal(struct {
		Input         FocusedInput         `json:"input"`
		Cut           json.RawMessage      `json:"cut"`
		ChangedResult application.ResultV1 `json:"changed_result"`
		Before, After map[string]ProductFact
		Facts         map[string]bool `json:"facts"`
	}{input, artifact, changedResult, before, after, facts})
	return encoded, facts, nil
}

func executeFocusedClaims(ctx context.Context, input FocusedInput, environment *productionEnvironment) ([]byte, map[string]bool, qualificationOverlay, error) {
	current := []provingground.Delivery{environment.baseline}
	if environment.workA != nil {
		current = append(current, *environment.workA)
	}
	for _, delivery := range current {
		if err := environment.runtime.Expire(ctx, controljournal.OperationID(fmt.Sprintf("focused-claim-expire-%s-%s", input.AttemptID, delivery.Run.WorkID)), delivery.Authority, delivery.Run.Deadline); err != nil {
			return nil, nil, qualificationOverlay{}, err
		}
	}
	now := environment.baseline.Run.Deadline.Add(time.Millisecond)
	if environment.workA != nil && !environment.workA.Run.Deadline.Before(now) {
		now = environment.workA.Run.Deadline.Add(time.Millisecond)
	}
	governance, err := qualificationGovernance(now)
	if err != nil {
		return nil, nil, qualificationOverlay{}, err
	}
	base, err := environment.repository.TargetHead(ctx)
	if err != nil {
		return nil, nil, qualificationOverlay{}, err
	}
	type claimResult struct {
		Participant int
		Delivery    provingground.Delivery
		Err         string
	}
	results := make(chan claimResult, 2)
	for _, participant := range input.Order {
		participant := participant
		go func() {
			if arriveErr := input.Barrier.Arrive(ctx); arriveErr != nil {
				results <- claimResult{Participant: participant, Err: arriveErr.Error()}
				return
			}
			short := fmt.Sprintf("%s-%d", input.AttemptID, participant)
			actor, process, work := ids.ActorID("actor-b"), ids.ProcessID("process-b"), ids.WorkID("work-baseline")
			if participant == 1 {
				actor, process, work = "actor-a", "process-c", "work-a"
			}
			delivery, startErr := environment.runtime.StartWorkspace(ctx, execution.StartRequest{OperationID: ids.OperationID("focused-claim-" + short), ProjectID: "project-qualification", MissionID: "mission-qualification", WorkRevisionID: "revision-qualification", WorkID: work, ActorID: actor, ProcessID: process, Now: now, LeaseTTL: qualificationLeaseTTL, GovernanceContext: governance}, provingground.WorkspaceSpec{OperationID: controljournal.OperationID("focused-bind-" + short), WorkspaceID: controljournal.WorkspaceID("workspace-claim-" + short), RepositoryID: "repository-qualification", LaneID: ids.LaneID("lane-claim-" + short), OwnedBranch: "runs/claim-" + short, PinnedBase: base, Now: now.Add(time.Duration(participant+1) * time.Millisecond)})
			entry := claimResult{Participant: participant, Delivery: delivery}
			if startErr != nil {
				entry.Err = startErr.Error()
			}
			results <- entry
		}()
	}
	got := []claimResult{<-results, <-results}
	facts := map[string]bool{"participant_zero": false, "participant_one": false, "both_items_progress": true, "distinct_authorities": true}
	for _, result := range got {
		passed := result.Err == "" && result.Delivery.Authority.LeaseID != "" && result.Delivery.Authority.WorkspaceID != ""
		facts[fmt.Sprintf("participant_%s", map[int]string{0: "zero", 1: "one"}[result.Participant])] = passed
		facts["both_items_progress"] = facts["both_items_progress"] && passed
		if result.Participant == 0 && passed {
			environment.baseline = result.Delivery
		}
		if result.Participant == 1 && passed {
			copyDelivery := result.Delivery
			environment.workA = &copyDelivery
		}
	}
	facts["distinct_authorities"] = facts["both_items_progress"] && environment.baseline.Run.WorkID != environment.workA.Run.WorkID && environment.baseline.Authority.LeaseID != environment.workA.Authority.LeaseID
	artifact, _ := json.Marshal(struct {
		Input   FocusedInput    `json:"input"`
		Results []claimResult   `json:"results"`
		Facts   map[string]bool `json:"facts"`
	}{input, got, facts})
	return artifact, facts, qualificationOverlay{}, nil
}

func executeFocusedIntegrations(ctx context.Context, input FocusedInput, environment *productionEnvironment) ([]byte, map[string]bool, qualificationOverlay, error) {
	if err := ensureFocusedWorkA(ctx, input, environment); err != nil {
		return nil, nil, qualificationOverlay{}, err
	}
	if err := heartbeatFocusedDelivery(ctx, input, environment, environment.workA, "integration"); err != nil {
		return nil, nil, qualificationOverlay{}, err
	}
	item, reviewObservation, before, err := prepareFocusedIntegration(ctx, input, environment)
	if err != nil {
		return nil, nil, qualificationOverlay{}, err
	}
	type integrationResult struct {
		Participant int
		Record      baseintegration.Record
		Err         string
	}
	results := make(chan integrationResult, 2)
	for _, participant := range input.Order {
		participant := participant
		go func() {
			if arriveErr := input.Barrier.Arrive(ctx); arriveErr != nil {
				results <- integrationResult{Participant: participant, Err: arriveErr.Error()}
				return
			}
			candidate := item
			candidate.Request.OperationID = ids.OperationID("focused-integrate-" + input.AttemptID)
			record, integrateErr := environment.integrator.Integrate(ctx, candidate)
			entry := integrationResult{Participant: participant, Record: record}
			if integrateErr != nil {
				entry.Err = integrateErr.Error()
			}
			results <- entry
		}()
	}
	got := []integrationResult{<-results, <-results}
	participants := map[int]bool{}
	uniqueResults := map[string]struct{}{}
	for _, result := range got {
		participants[result.Participant] = true
		if result.Err == "" && result.Record.Status == baseintegration.StatusDone {
			uniqueResults[result.Record.ResultCommit] = struct{}{}
		}
	}
	after, headErr := environment.repository.TargetHead(ctx)
	if headErr != nil {
		return nil, nil, qualificationOverlay{}, headErr
	}
	durable := environment.integrator.QualificationSnapshot()
	authorityCount, activeAuthorityCount := 0, 0
	var durableRecord baseintegration.Record
	publicationCount := 0
	for _, authority := range durable.Authorities {
		if authority.RepositoryID == item.Request.RepositoryID && authority.TargetRef == item.Request.TargetRef {
			authorityCount++
			if authority.Status == "active" {
				activeAuthorityCount++
			}
		}
	}
	for _, record := range durable.Records {
		if record.RepositoryID == item.Request.RepositoryID && record.TargetRef == item.Request.TargetRef && record.Candidate == item.Request.Candidate && record.Status == baseintegration.StatusDone {
			publicationCount++
			durableRecord = record
		}
	}
	facts := map[string]bool{
		"one_target_authority":      authorityCount == 1 && activeAuthorityCount <= 1,
		"one_candidate_publication": publicationCount == 1 && len(uniqueResults) == 1 && durableRecord.ResultCommit != "" && after == durableRecord.ResultCommit,
		"both_participants":         participants[0] && participants[1],
		"git_target_moved_once":     before != after && durableRecord.PreflightTarget == before,
	}
	overlay := qualificationOverlay{reviews: []m1.ReviewObservation{reviewObservation}, integration: []m1.IntegrationObservation{{
		RepositoryID: string(durableRecord.RepositoryID), TargetRef: durableRecord.TargetRef,
		ActiveAuthorities: activeAuthorityCount, CandidateID: durableRecord.Candidate,
		SuccessfulPublishes: publicationCount, GitExpectedOld: durableRecord.PreflightTarget,
		GitObservedOld: before, GitResult: after, JournalResult: durableRecord.ResultCommit,
	}}}
	artifact, _ := json.Marshal(struct {
		Input   FocusedInput                                   `json:"input"`
		Results []integrationResult                            `json:"results"`
		Before  string                                         `json:"before"`
		After   string                                         `json:"after"`
		Durable provingground.IntegrationQualificationSnapshot `json:"durable"`
		Facts   map[string]bool                                `json:"facts"`
	}{input, got, before, after, durable, facts})
	return artifact, facts, overlay, nil
}

func ensureFocusedWorkA(ctx context.Context, input FocusedInput, environment *productionEnvironment) error {
	if environment.workA != nil {
		return nil
	}
	now := time.Now().UTC()
	governance, err := qualificationGovernance(now)
	if err != nil {
		return err
	}
	base, err := environment.repository.TargetHead(ctx)
	if err != nil {
		return err
	}
	short := input.AttemptID
	if len(short) > 16 {
		short = short[:16]
	}
	delivery, err := environment.runtime.StartWorkspace(ctx, execution.StartRequest{OperationID: ids.OperationID("focused-prime-claim-" + short), ProjectID: "project-qualification", MissionID: "mission-qualification", WorkRevisionID: "revision-qualification", WorkID: "work-a", ActorID: "actor-a", ProcessID: "process-c", Now: now, LeaseTTL: qualificationLeaseTTL, GovernanceContext: governance}, provingground.WorkspaceSpec{OperationID: controljournal.OperationID("focused-prime-bind-" + short), WorkspaceID: controljournal.WorkspaceID("workspace-prime-" + short), RepositoryID: "repository-qualification", LaneID: ids.LaneID("lane-prime-" + short), OwnedBranch: "runs/prime-" + short, PinnedBase: base, Now: now.Add(time.Millisecond)})
	if err != nil {
		return err
	}
	environment.workA = &delivery
	return nil
}

func consumeFocusedSchedule(ctx context.Context, input FocusedInput) error {
	errs := make(chan error, 2)
	for _, participant := range input.Order {
		participant := participant
		go func() {
			if participant != 0 && participant != 1 {
				errs <- fmt.Errorf("invalid scheduled participant %d", participant)
				return
			}
			errs <- input.Barrier.Arrive(ctx)
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			return err
		}
	}
	return nil
}

func heartbeatFocusedDelivery(ctx context.Context, input FocusedInput, environment *productionEnvironment, delivery *provingground.Delivery, label string) error {
	if delivery == nil {
		return fmt.Errorf("focused %s delivery is absent", label)
	}
	now := time.Now().UTC()
	refreshed, err := environment.runtime.Heartbeat(ctx, controljournal.OperationID("focused-heartbeat-"+label+"-"+input.AttemptID), delivery.Authority, now, qualificationLeaseTTL)
	if err != nil {
		return err
	}
	delivery.Authority = refreshed
	delivery.Run.Deadline = now.Add(qualificationLeaseTTL)
	return nil
}

type focusedEligibility struct{}

func (focusedEligibility) WorkEligibilityFact(_ context.Context, revision ids.WorkRevisionID, work ids.WorkID, status workgraph.WorkStatus, operation workgraph.EligibilityOperation) (workgraph.EligibilityFact, error) {
	return workgraph.NewEligibilityFact("focused-reconciliation", revision, "revision-qualification-next", work, status, workgraph.Continue, operation)
}

func prepareFocusedIntegration(ctx context.Context, input FocusedInput, environment *productionEnvironment) (provingground.IntegrationItem, m1.ReviewObservation, string, error) {
	if environment.reviews == nil {
		reviews, err := provingground.NewReviewCoordinator(ctx, environment.runtime, environment.app, environment.port, environment.repository, focusedEligibility{}, map[ids.WorkID][]string{"work-a": {"focused-criterion"}, "work-baseline": {"focused-criterion"}})
		if err != nil {
			return provingground.IntegrationItem{}, m1.ReviewObservation{}, "", err
		}
		environment.reviews = reviews
		integrator, err := provingground.NewIntegrationCoordinator(reviews)
		if err != nil {
			return provingground.IntegrationItem{}, m1.ReviewObservation{}, "", err
		}
		environment.integrator = integrator
	}
	delivery := *environment.workA
	binding, ok := environment.runtime.Binding(delivery.Authority.WorkspaceID)
	if !ok {
		return provingground.IntegrationItem{}, m1.ReviewObservation{}, "", fmt.Errorf("candidate workspace binding is absent")
	}
	commit := exec.CommandContext(ctx, "git", "-C", binding.CanonicalPath, "commit", "--allow-empty", "-m", "focused candidate "+input.AttemptID)
	if output, err := commit.CombinedOutput(); err != nil {
		return provingground.IntegrationItem{}, m1.ReviewObservation{}, "", fmt.Errorf("create focused candidate: %s: %w", bounded(string(output)), err)
	}
	head := exec.CommandContext(ctx, "git", "-C", binding.CanonicalPath, "rev-parse", "HEAD")
	output, err := head.Output()
	if err != nil {
		return provingground.IntegrationItem{}, m1.ReviewObservation{}, "", err
	}
	candidate := strings.TrimSpace(string(output))
	now := time.Now().UTC()
	handoffFact, err := focusedEligibility{}.WorkEligibilityFact(ctx, "revision-qualification", "work-a", workgraph.StatusActive, workgraph.EligibilityHandoff)
	if err != nil {
		return provingground.IntegrationItem{}, m1.ReviewObservation{}, "", err
	}
	handoffID := ids.HandoffID("focused-handoff-" + input.AttemptID)
	handoffRequest := handoff.Request{SchemaVersion: 1, CorrelationID: "focused-handoff", OperationID: ids.OperationID("focused-handoff-op-" + input.AttemptID), HandoffID: handoffID, ProjectID: "project-qualification", MissionID: "mission-qualification", WorkRevisionID: "revision-qualification", WorkID: "work-a", RunID: ids.RunID(delivery.Authority.RunID), WorkspaceID: ids.WorkspaceID(delivery.Authority.WorkspaceID), LeaseID: ids.LeaseID(delivery.Authority.LeaseID), LeaseGeneration: int64(delivery.Authority.LeaseGeneration), Token: fmt.Sprint(delivery.Authority.FenceToken), ActorID: ids.ActorID(delivery.Authority.ActorID), ProcessID: ids.ProcessID(delivery.Authority.ProcessID), Candidate: candidate, Contributors: []ids.ActorID{ids.ActorID(delivery.Authority.ActorID)}, EvidenceDigest: strings.Repeat("a", 64), SubmittedAt: now, ExpectedEligibility: handoffFact}
	if _, err := environment.reviews.SubmitCandidate(ctx, delivery.Authority, handoffRequest); err != nil {
		return provingground.IntegrationItem{}, m1.ReviewObservation{}, "", err
	}
	reviewFact, err := focusedEligibility{}.WorkEligibilityFact(ctx, "revision-qualification", "work-a", workgraph.StatusHandedOff, workgraph.EligibilityReview)
	if err != nil {
		return provingground.IntegrationItem{}, m1.ReviewObservation{}, "", err
	}
	reviewID := ids.ReviewID("focused-review-" + input.AttemptID)
	reviewRequest := basereview.Request{SchemaVersion: 1, CorrelationID: "focused-review", OperationID: ids.OperationID("focused-review-op-" + input.AttemptID), ReviewID: reviewID, HandoffID: handoffID, WorkRevisionID: "revision-qualification", WorkID: "work-a", LeaseID: ids.LeaseID(delivery.Authority.LeaseID), LeaseGeneration: int64(delivery.Authority.LeaseGeneration), Token: "focused-review", ActorID: "reviewer-focused", ProcessID: "review-process-focused", Candidate: candidate, Verdict: basereview.Approved, Criteria: []basereview.CriterionResult{{CriterionID: "focused-criterion", Status: basereview.Passed, Evidence: "focused production candidate"}}, EvidenceDigest: strings.Repeat("b", 64), SubmittedAt: now.Add(time.Nanosecond), ExpectedEligibility: reviewFact}
	if _, err := environment.reviews.SubmitReview(ctx, delivery.Authority, reviewRequest); err != nil {
		return provingground.IntegrationItem{}, m1.ReviewObservation{}, "", err
	}
	integrationFact, err := focusedEligibility{}.WorkEligibilityFact(ctx, "revision-qualification", "work-a", workgraph.StatusApproved, workgraph.EligibilityIntegration)
	if err != nil {
		return provingground.IntegrationItem{}, m1.ReviewObservation{}, "", err
	}
	before, err := environment.repository.TargetHead(ctx)
	if err != nil {
		return provingground.IntegrationItem{}, m1.ReviewObservation{}, "", err
	}
	item := provingground.IntegrationItem{Capsule: delivery.Authority, Request: baseintegration.Request{SchemaVersion: 1, CorrelationID: "focused-integration", OperationID: "focused-integrate-placeholder", ReviewID: reviewID, RepositoryID: "repository-qualification", TargetRef: "refs/heads/main", ActorID: "integrator-focused", ProcessID: "integrator-process-focused", Candidate: candidate, LeaseTTL: qualificationLeaseTTL, StartedAt: now.Add(2 * time.Nanosecond), ExpectedEligibility: integrationFact}}
	observation := m1.ReviewObservation{CandidateID: candidate, ContributorIDs: []string{string(delivery.Authority.ActorID)}, ReviewerActorID: reviewRequest.ActorID.String(), Verdict: string(reviewRequest.Verdict), CandidateImmutable: true}
	return item, observation, before, nil
}

func executeFocusedRestorePlayback(ctx context.Context, input FocusedInput, environment *productionEnvironment) ([]byte, map[string]bool, m1.PlaybackObservation, controljournal.ValidatedJournalV1, uint64, error) {
	if err := consumeFocusedSchedule(ctx, input); err != nil {
		return nil, nil, m1.PlaybackObservation{}, controljournal.ValidatedJournalV1{}, 0, err
	}
	restored, journal, err := restoreAndJournal(ctx, environment)
	if err != nil {
		return nil, nil, m1.PlaybackObservation{}, controljournal.ValidatedJournalV1{}, 0, err
	}
	played, err := journalplayback.RunV1(journalplayback.InputV1{Journal: journal, Reducers: environment.reducers})
	if err != nil {
		return nil, nil, m1.PlaybackObservation{}, controljournal.ValidatedJournalV1{}, 0, err
	}
	compareErr := journalplayback.CompareDigestV1(restored.CanonicalProjectionDigest, played)
	playback := playbackObservation(restored, played)
	facts := map[string]bool{
		"restore_ready":        restored.Ready,
		"digest_equal":         compareErr == nil,
		"playback_zero_io":     played.Observation.AdapterCalls == 0 && played.Observation.DurableWrites == 0,
		"history_materialized": played.EventCount > 0,
		"schedule_consumed":    input.Order[0] != input.Order[1],
	}
	artifact, _ := json.Marshal(struct {
		Input    FocusedInput                           `json:"input"`
		Restore  journalcheckpoint.RestoreObservationV1 `json:"restore"`
		Playback journalplayback.ResultV1               `json:"playback"`
		Facts    map[string]bool                        `json:"facts"`
	}{input, restored.Observation, played, facts})
	return artifact, facts, playback, journal, restored.Observation.ObservedStoreRevision, nil
}

func restoreAndJournal(ctx context.Context, environment *productionEnvironment) (journalcheckpoint.RestoreResultV1, controljournal.ValidatedJournalV1, error) {
	eventDigest, err := environment.events.Digest()
	if err != nil {
		return journalcheckpoint.RestoreResultV1{}, controljournal.ValidatedJournalV1{}, err
	}
	reducerDigest, err := environment.reducers.Digest()
	if err != nil {
		return journalcheckpoint.RestoreResultV1{}, controljournal.ValidatedJournalV1{}, err
	}
	capture := &restoreCaptureReader{source: environment.reader}
	restored, err := journalcheckpoint.RestoreV1(ctx, journalcheckpoint.RestoreInputV1{Reader: capture, Source: journalcheckpoint.SourceIdentityV1{ProjectID: "project-qualification", MissionID: "mission-qualification", RunID: controljournal.RunID(environment.runID), ReducerSetDigest: reducerDigest, EventSchemaSetDigest: eventDigest}, Binding: environment.binding, EventRegistry: environment.events, Reducers: environment.reducers})
	if err != nil || !restored.Ready {
		return restored, controljournal.ValidatedJournalV1{}, fmt.Errorf("focused restore is not ready: %w", err)
	}
	stored, err := capture.completeOriginRecords(restored)
	if err != nil && (restored.SelectedCheckpoint != nil || restored.Pristine) {
		stored, err = scanJournalRecords(ctx, environment.port, restored.Observation.ObservedStoreRevision)
	}
	if err != nil {
		return restored, controljournal.ValidatedJournalV1{}, err
	}
	journal, err := controljournal.ValidateJournalV1(environment.events, stored)
	if err != nil {
		return restored, controljournal.ValidatedJournalV1{}, fmt.Errorf("focused journal validation: %w", err)
	}
	return restored, journal, nil
}

type journalScanPort interface {
	Scan(context.Context, storegapdb.ScanRequest) (storegapdb.ScanPage, error)
}

func scanJournalRecords(ctx context.Context, port journalScanPort, expectedRevision uint64) ([]controljournal.StoredEventV1, error) {
	if port == nil || expectedRevision == 0 {
		return nil, fmt.Errorf("focused journal scan requires one exact nonzero restored revision")
	}
	const prefix = "spk/v1/project-qualification/mission-qualification/journal/"
	var stored []controljournal.StoredEventV1
	cursor := ""
	for {
		page, err := port.Scan(ctx, storegapdb.ScanRequest{Prefix: prefix, Limit: storegapdb.MaxScanRecords, MaxBytes: storegapdb.MaxScanBytes, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		if page.ObservedRevision == 0 || page.ObservedRevision != expectedRevision {
			return nil, fmt.Errorf("focused journal scan revision differs from restored revision")
		}
		for _, record := range page.Records {
			if record.Revision == 0 || record.Revision > expectedRevision {
				return nil, fmt.Errorf("focused journal record revision is outside the restored snapshot")
			}
			event, decodeErr := controljournal.DecodeEventEnvelopeV1(record.Value)
			if decodeErr != nil {
				return nil, decodeErr
			}
			stored = append(stored, controljournal.StoredEventV1{RecordRevision: record.Revision, Envelope: event})
		}
		if !page.Truncated {
			if page.Cursor != "" {
				return nil, fmt.Errorf("focused journal terminal page returned a cursor")
			}
			return stored, nil
		}
		if page.Cursor == "" || page.Cursor == cursor {
			return nil, fmt.Errorf("focused journal scan cursor made no progress")
		}
		cursor = page.Cursor
	}
}

// restoreCaptureReader retains only the exact pages consumed by one RestoreV1
// call. It lets playback validate the identical immutable bytes without a
// second provider scan or any cache shared with another focused attempt.
type restoreCaptureReader struct {
	source   journalcheckpoint.RestoreReaderV1
	after    journalcheckpoint.WatermarkV1
	revision uint64
	records  []controljournal.StoredEventV1
	terminal bool
	failed   bool
}

func (r *restoreCaptureReader) CheckpointHeadsV1(ctx context.Context) ([]journalcheckpoint.CheckpointHeadV1, error) {
	return r.source.CheckpointHeadsV1(ctx)
}

func (r *restoreCaptureReader) CheckpointBlobV1(ctx context.Context, id journalcheckpoint.CheckpointID) ([]byte, error) {
	return r.source.CheckpointBlobV1(ctx, id)
}

func (r *restoreCaptureReader) JournalPageV1(ctx context.Context, after journalcheckpoint.WatermarkV1, cursor string) (journalcheckpoint.JournalScanPageV1, error) {
	if cursor == "" {
		r.after = after
		r.revision = 0
		r.records = r.records[:0]
		r.terminal = false
		r.failed = false
	}
	page, err := r.source.JournalPageV1(ctx, after, cursor)
	if err != nil {
		r.failed = true
		return page, err
	}
	if r.revision == 0 {
		r.revision = page.ObservedRevision
	} else if page.ObservedRevision != r.revision {
		r.failed = true
	}
	if page.RecordCount > journalcheckpoint.MaxScanPageRecords {
		r.failed = true
		return page, nil
	}
	for index := uint16(0); index < page.RecordCount; index++ {
		r.records = append(r.records, page.Records[index])
	}
	if !page.Truncated {
		r.terminal = true
	}
	return page, nil
}

func (r *restoreCaptureReader) completeOriginRecords(restored journalcheckpoint.RestoreResultV1) ([]controljournal.StoredEventV1, error) {
	if r == nil || !restored.Ready || r.failed || !r.terminal || r.after != (journalcheckpoint.WatermarkV1{}) || restored.SelectedCheckpoint != nil || r.revision == 0 || r.revision != restored.Observation.ObservedStoreRevision || uint64(len(r.records)) != restored.Observation.EventCount {
		return nil, fmt.Errorf("focused restore did not expose one complete nonzero-revision origin journal")
	}
	records := make([]controljournal.StoredEventV1, len(r.records))
	copy(records, r.records)
	return records, nil
}

func playbackObservation(restored journalcheckpoint.RestoreResultV1, played journalplayback.ResultV1) m1.PlaybackObservation {
	return m1.PlaybackObservation{
		Inspectable:     true,
		RestoreDigest:   hex.EncodeToString(restored.CanonicalProjectionDigest[:]),
		PlaybackDigest:  hex.EncodeToString(played.CanonicalProjectionDigest[:]),
		AdapterCalls:    played.Observation.AdapterCalls,
		DurableWrites:   played.Observation.DurableWrites,
		StableCanonical: journalplayback.CompareDigestV1(restored.CanonicalProjectionDigest, played) == nil,
	}
}

func (p *ProductionExecutor) executeFocusedEpoch(ctx context.Context, input FocusedInput, environment *productionEnvironment) ([]byte, map[string]bool, error) {
	if err := consumeFocusedSchedule(ctx, input); err != nil {
		return nil, nil, err
	}
	predecessorLifecycle := p.Lifecycle
	predecessorProvenance := predecessorLifecycle.Provenance()
	predecessor := environment.runtime.QualificationSnapshot().RuntimeEpochID
	oldCapsule := environment.baseline.Authority
	epochDigest := sha256.Sum256([]byte(input.AttemptID))
	admitted := controljournal.RuntimeEpochID("epoch-focused-" + hex.EncodeToString(epochDigest[:8]))
	mode := sidecargapdb.RestartClean
	if input.Index%2 == 1 {
		mode = sidecargapdb.RestartUnclean
	}
	if environment.closer != nil {
		_ = environment.closer.Close()
	}
	nextSupervisorRunID := replacementSupervisorRunID(environment.runID, input.AttemptID, predecessorProvenance.SupervisorRunID)
	next, err := predecessorLifecycle.RestartOwned(ctx, mode, nextSupervisorRunID)
	if err != nil {
		return nil, nil, err
	}
	p.Lifecycle = next
	port, closer, err := storegapdb.Dial(next.Provenance().ConfiguredSocket, 2*time.Second)
	if err != nil {
		return nil, nil, err
	}
	binding, err := next.ProductRestoreBindingV1()
	if err != nil {
		closer.Close()
		return nil, nil, err
	}
	reader, err := application.NewRestoreReaderV1(port, "project-qualification", "mission-qualification")
	if err != nil {
		closer.Close()
		return nil, nil, err
	}
	eventDigest, _ := environment.events.Digest()
	reducerDigest, _ := environment.reducers.Digest()
	restored, err := journalcheckpoint.RestoreV1(ctx, journalcheckpoint.RestoreInputV1{Reader: reader, Source: journalcheckpoint.SourceIdentityV1{ProjectID: "project-qualification", MissionID: "mission-qualification", RunID: controljournal.RunID(environment.runID), ReducerSetDigest: reducerDigest, EventSchemaSetDigest: eventDigest}, Binding: binding, EventRegistry: environment.events, Reducers: environment.reducers})
	if err != nil || !restored.Ready {
		closer.Close()
		return nil, nil, fmt.Errorf("restart restore ready=%v: %w", restored.Ready, err)
	}
	restoreAdmission, err := restored.AdmissionV1()
	if err != nil {
		closer.Close()
		return nil, nil, err
	}
	productProof, err := next.MarkProductRecovered(restoreAdmission)
	if err != nil {
		closer.Close()
		return nil, nil, err
	}
	startupProof, err := next.BindOwnerStartupEpochV1(admitted)
	if err != nil {
		closer.Close()
		return nil, nil, err
	}
	admission, admissionResult, err := application.AdmitRuntimeEpochV1(ctx, port, application.EpochAdmissionInputV1{ProjectID: "project-qualification", MissionID: "mission-qualification", OperationID: controljournal.OperationID("focused-admit-" + input.AttemptID), EventID: controljournal.EventID("focused-admit-event-" + input.AttemptID), StartupProof: startupProof, ProductRestoreProof: productProof})
	if err != nil || admissionResult.Kind != application.OutcomeCommitted {
		closer.Close()
		return nil, nil, fmt.Errorf("restart epoch admission=%+v: %w", admissionResult, err)
	}
	app, err := application.NewService(port, admission)
	if err != nil {
		closer.Close()
		return nil, nil, err
	}
	now := time.Now().UTC()
	predecessorRuntime := environment.runtime
	if err := predecessorRuntime.ReplaceOwner(ctx, controljournal.OperationID("focused-owner-restart-"+input.AttemptID), admitted, app, now); err != nil {
		closer.Close()
		return nil, nil, err
	}
	postTransition, err := journalcheckpoint.RestoreV1(ctx, journalcheckpoint.RestoreInputV1{Reader: reader, Source: journalcheckpoint.SourceIdentityV1{ProjectID: "project-qualification", MissionID: "mission-qualification", RunID: controljournal.RunID(environment.runID), ReducerSetDigest: reducerDigest, EventSchemaSetDigest: eventDigest}, Binding: binding, EventRegistry: environment.events, Reducers: environment.reducers})
	if err != nil || !postTransition.Ready {
		closer.Close()
		return nil, nil, fmt.Errorf("post-transition restore ready=%v: %w", postTransition.Ready, err)
	}
	actors, err := controljournal.NewTrialImplementationActorsV1("actor-a", "actor-b", "process-a", "process-b", "process-c")
	if err != nil {
		closer.Close()
		return nil, nil, err
	}
	config := provingground.Config{ProjectID: "project-qualification", MissionID: "mission-qualification", WorkRevisionID: "revision-qualification", RunID: controljournal.RunID(environment.runID), RuntimeEpochID: admitted, Actors: actors, WorkIDs: []controljournal.WorkID{"work-baseline", "work-a"}, LeaseTTL: qualificationLeaseTTL}
	freshRuntime, err := provingground.RestoreRuntimeValidated(ctx, config, app, environment.repository, port, postTransition, &qualificationIdentities{runID: ids.RunID(environment.runID)}, execution.SystemClock{})
	if err != nil {
		closer.Close()
		return nil, nil, err
	}
	environment.runtime = freshRuntime
	governance, err := qualificationGovernance(now)
	if err != nil {
		closer.Close()
		return nil, nil, err
	}
	base, err := environment.repository.TargetHead(ctx)
	if err != nil {
		closer.Close()
		return nil, nil, err
	}
	short := input.AttemptID
	if len(short) > 16 {
		short = short[:16]
	}
	delivery, err := environment.runtime.StartWorkspace(ctx, execution.StartRequest{OperationID: ids.OperationID("focused-restart-claim-" + short), ProjectID: "project-qualification", MissionID: "mission-qualification", WorkRevisionID: "revision-qualification", WorkID: "work-baseline", ActorID: "actor-b", ProcessID: "process-b", Now: now.Add(time.Nanosecond), LeaseTTL: qualificationLeaseTTL, GovernanceContext: governance}, provingground.WorkspaceSpec{OperationID: controljournal.OperationID("focused-restart-bind-" + short), WorkspaceID: controljournal.WorkspaceID("workspace-restart-" + short), RepositoryID: "repository-qualification", LaneID: ids.LaneID("lane-restart-" + short), OwnedBranch: "runs/restart-" + short, PinnedBase: base, Now: now.Add(2 * time.Nanosecond)})
	if err != nil {
		closer.Close()
		return nil, nil, err
	}
	environment.port, environment.closer, environment.app = port, closer, app
	environment.admission, environment.binding, environment.reader = admission, binding, reader
	environment.baseline, environment.workA = delivery, nil
	predecessorCapsule := oldCapsule
	environment.predecessor = &predecessorCapsule
	environment.reviews, environment.integrator = nil, nil
	oldErr := freshRuntime.Authorize(oldCapsule, "focused-predecessor-probe", now.Add(3*time.Nanosecond))
	currentErr := freshRuntime.Authorize(delivery.Authority, "focused-current-probe", now.Add(3*time.Nanosecond))
	fresh := next.Provenance()
	facts := map[string]bool{
		"new_epoch_unique":       admitted != predecessor && environment.runtime.QualificationSnapshot().RuntimeEpochID == admitted,
		"predecessor_refused":    provingground.IsOutcome(oldErr, provingground.OutcomeStaleEpoch),
		"current_epoch_accepted": currentErr == nil,
		"fresh_owner_process":    fresh.Process != predecessorProvenance.Process && fresh.SupervisorRunID == nextSupervisorRunID && fresh.SupervisorRunID != predecessorProvenance.SupervisorRunID,
		"fresh_private_endpoint": fresh.ConfiguredSocket != predecessorProvenance.ConfiguredSocket && fresh.RuntimeDirectory != predecessorProvenance.RuntimeDirectory,
		"fresh_runtime_instance": freshRuntime != predecessorRuntime,
		"same_durable_database":  fresh.DatabaseID == predecessorProvenance.DatabaseID && fresh.DataDirectory == predecessorProvenance.DataDirectory,
		"ready_new_lifetime":     restored.Ready && fresh.Readiness.ProductRecovered,
		"restart_mode_observed":  mode == sidecargapdb.RestartClean || mode == sidecargapdb.RestartUnclean,
		"schedule_consumed":      input.Order[0] != input.Order[1],
	}
	artifact, _ := json.Marshal(struct {
		Input              FocusedInput             `json:"input"`
		Mode               sidecargapdb.RestartMode `json:"mode"`
		Predecessor, Fresh sidecargapdb.SidecarProvenance
		Restore            journalcheckpoint.RestoreObservationV1 `json:"restore"`
		Facts              map[string]bool                        `json:"facts"`
	}{input, mode, predecessorProvenance, fresh, restored.Observation, facts})
	return artifact, facts, nil
}

func replacementSupervisorRunID(runID, attemptID, predecessor string) string {
	digest := sha256.Sum256([]byte(runID + "\x00" + attemptID + "\x00" + predecessor))
	return "owner-" + hex.EncodeToString(digest[:16])
}

func executeFocusedZombie(ctx context.Context, input FocusedInput, environment *productionEnvironment) ([]byte, map[string]bool, error) {
	if err := consumeFocusedSchedule(ctx, input); err != nil {
		return nil, nil, err
	}
	old := environment.baseline
	oldSnapshot := environment.runtime.QualificationSnapshot()
	oldBinding, ok := workspaceBindingByID(oldSnapshot, old.Authority.WorkspaceID)
	if !ok {
		return nil, nil, fmt.Errorf("current workspace binding is not observable")
	}
	expiredAt := old.Run.Deadline
	if err := environment.runtime.Expire(ctx, controljournal.OperationID("focused-expire-"+input.AttemptID), old.Authority, expiredAt); err != nil {
		return nil, nil, err
	}
	now := expiredAt.Add(time.Millisecond)
	governance, err := qualificationGovernance(now)
	if err != nil {
		return nil, nil, err
	}
	base, err := environment.repository.TargetHead(ctx)
	if err != nil {
		return nil, nil, err
	}
	short := input.AttemptID
	if len(short) > 16 {
		short = short[:16]
	}
	delivery, err := environment.runtime.StartWorkspace(ctx, execution.StartRequest{OperationID: ids.OperationID("focused-claim-" + short), ProjectID: "project-qualification", MissionID: "mission-qualification", WorkRevisionID: "revision-qualification", WorkID: "work-baseline", ActorID: "actor-a", ProcessID: "process-a", Now: now, LeaseTTL: qualificationLeaseTTL, GovernanceContext: governance}, provingground.WorkspaceSpec{OperationID: controljournal.OperationID("focused-bind-" + short), WorkspaceID: controljournal.WorkspaceID("workspace-focused-" + short), RepositoryID: "repository-qualification", LaneID: ids.LaneID("lane-focused-" + short), OwnedBranch: "runs/focused-" + short, PinnedBase: base, Now: now.Add(time.Millisecond)})
	if err != nil {
		return nil, nil, err
	}
	newSnapshot := environment.runtime.QualificationSnapshot()
	newBinding, ok := workspaceBindingByID(newSnapshot, delivery.Authority.WorkspaceID)
	if !ok {
		return nil, nil, fmt.Errorf("replacement workspace binding is not observable")
	}
	freshBefore, err := observeWorktreeContent(ctx, newBinding.CanonicalPath)
	if err != nil {
		return nil, nil, err
	}
	zombiePath := filepath.Join(oldBinding.CanonicalPath, "zombie-"+short+".txt")
	zombieBytes := []byte("stale writer " + input.AttemptID + "\n")
	if err := os.WriteFile(zombiePath, zombieBytes, 0o600); err != nil {
		return nil, nil, err
	}
	observedZombie, err := os.ReadFile(zombiePath)
	if err != nil {
		return nil, nil, err
	}
	staleErr := environment.runtime.Authorize(old.Authority, "focused-zombie-write", now.Add(2*time.Millisecond))
	freshAfter, err := observeWorktreeContent(ctx, newBinding.CanonicalPath)
	if err != nil {
		return nil, nil, err
	}
	environment.baseline = delivery
	facts := map[string]bool{
		"replacement_lease_differs":     delivery.Authority.LeaseID != old.Authority.LeaseID,
		"replacement_workspace_differs": delivery.Authority.WorkspaceID != old.Authority.WorkspaceID,
		"stale_authority_refused":       provingground.IsOutcome(staleErr, provingground.OutcomeStaleLease),
		"old_workspace_posix_mutated":   bytes.Equal(observedZombie, zombieBytes),
		"fresh_head_unchanged":          freshBefore.Head == freshAfter.Head,
		"fresh_index_unchanged":         freshBefore.IndexDigest == freshAfter.IndexDigest,
		"fresh_tree_unchanged":          freshBefore.TreeDigest == freshAfter.TreeDigest,
		"fresh_content_unchanged":       freshBefore.ContentDigest == freshAfter.ContentDigest,
		"schedule_consumed":             input.Order[0] != input.Order[1],
	}
	artifact, _ := json.Marshal(struct {
		Input                   FocusedInput `json:"input"`
		Old, New                controljournal.AuthorityCapsuleV1
		FreshBefore, FreshAfter worktreeContentObservation
		ZombiePath              string          `json:"zombie_path"`
		Facts                   map[string]bool `json:"facts"`
	}{input, old.Authority, delivery.Authority, freshBefore, freshAfter, zombiePath, facts})
	return artifact, facts, nil
}

type worktreeContentObservation struct {
	Head, IndexDigest, TreeDigest, ContentDigest string
}

func observeWorktreeContent(ctx context.Context, root string) (worktreeContentObservation, error) {
	head, err := gitOutputAt(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return worktreeContentObservation{}, err
	}
	tree, err := gitOutputAt(ctx, root, "write-tree")
	if err != nil {
		return worktreeContentObservation{}, err
	}
	gitDir, err := gitOutputAt(ctx, root, "rev-parse", "--git-dir")
	if err != nil {
		return worktreeContentObservation{}, err
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	indexBytes, err := os.ReadFile(filepath.Join(filepath.Clean(gitDir), "index"))
	if err != nil {
		return worktreeContentObservation{}, err
	}
	indexDigest := sha256.Sum256(indexBytes)
	command := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	listed, err := command.Output()
	if err != nil {
		return worktreeContentObservation{}, err
	}
	paths := bytes.Split(bytes.TrimSuffix(listed, []byte{0}), []byte{0})
	sort.Slice(paths, func(i, j int) bool { return bytes.Compare(paths[i], paths[j]) < 0 })
	content := sha256.New()
	for _, encodedPath := range paths {
		if len(encodedPath) == 0 {
			continue
		}
		path := filepath.Join(root, string(encodedPath))
		value, readErr := os.ReadFile(path)
		if readErr != nil {
			return worktreeContentObservation{}, readErr
		}
		content.Write(encodedPath)
		content.Write([]byte{0})
		content.Write(value)
		content.Write([]byte{0})
	}
	return worktreeContentObservation{Head: head, IndexDigest: hex.EncodeToString(indexDigest[:]), TreeDigest: tree, ContentDigest: hex.EncodeToString(content.Sum(nil))}, nil
}

func gitOutputAt(ctx context.Context, root string, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), bounded(string(output)), err)
	}
	return strings.TrimSpace(string(output)), nil
}

func workspaceBindingByID(snapshot provingground.QualificationSnapshot, id controljournal.WorkspaceID) (provingground.WorkspaceBinding, bool) {
	for _, binding := range snapshot.Workspaces {
		if binding.WorkspaceID == id {
			return binding, true
		}
	}
	return provingground.WorkspaceBinding{}, false
}

type wrongIdentityProjection struct {
	delegate  tracker.AssignmentProjection
	inspected atomic.Bool
}

func (p *wrongIdentityProjection) ObserveAssignment(ctx context.Context, ref tracker.ExternalWorkRef) (tracker.AssignmentObservation, error) {
	observed, err := p.delegate.ObserveAssignment(ctx, ref)
	if err == nil {
		p.inspected.Store(true)
		observed.ExternalRef.ProjectID = "foreign-project"
	}
	return observed, err
}
func (p *wrongIdentityProjection) ProjectAssignment(ctx context.Context, request tracker.ProjectAssignmentRequest) (tracker.AssignmentMutation, error) {
	observed, err := p.delegate.ObserveAssignment(ctx, request.ExternalRef)
	if err != nil {
		return tracker.AssignmentMutation{}, err
	}
	p.inspected.Store(true)
	observed.ExternalRef.ProjectID = "foreign-project"
	return tracker.AssignmentMutation{Observation: observed, Guarantee: tracker.AssignmentAtomicClaim}, nil
}
func (p *wrongIdentityProjection) CompensateAssignment(ctx context.Context, request tracker.CompensateAssignmentRequest) (tracker.AssignmentMutation, error) {
	return p.delegate.CompensateAssignment(ctx, request)
}

type cancellationProjection struct {
	delegate tracker.AssignmentProjection
	entered  chan struct{}
	calls    atomic.Int32
	canceled atomic.Bool
}

func (p *cancellationProjection) ObserveAssignment(ctx context.Context, reference tracker.ExternalWorkRef) (tracker.AssignmentObservation, error) {
	return p.delegate.ObserveAssignment(ctx, reference)
}

func (p *cancellationProjection) ProjectAssignment(ctx context.Context, _ tracker.ProjectAssignmentRequest) (tracker.AssignmentMutation, error) {
	p.calls.Add(1)
	close(p.entered)
	<-ctx.Done()
	p.canceled.Store(true)
	return tracker.AssignmentMutation{}, &tracker.Error{Kind: tracker.ErrorTimeout, Message: "provider mutation honored cancellation"}
}

func (p *cancellationProjection) CompensateAssignment(ctx context.Context, request tracker.CompensateAssignmentRequest) (tracker.AssignmentMutation, error) {
	return p.delegate.CompensateAssignment(ctx, request)
}

func executeFocusedAttribution(ctx context.Context, input FocusedInput, environment *productionEnvironment) ([]byte, map[string]bool, FocusedProviderEvidence, []m1.EffectObservation, error) {
	if err := consumeFocusedSchedule(ctx, input); err != nil {
		return nil, nil, FocusedProviderEvidence{}, nil, err
	}
	if err := heartbeatFocusedDelivery(ctx, input, environment, &environment.baseline, "effect"); err != nil {
		return nil, nil, FocusedProviderEvidence{}, nil, err
	}
	states := []string{"slow", "unavailable", "wrong", "stale", "human_modified"}
	state := states[input.Index%len(states)]
	root := filepath.Join(environment.root, "focused-tracker-"+input.AttemptID)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, nil, FocusedProviderEvidence{}, nil, err
	}
	store, err := trackerfake.Open(filepath.Join(root, "tracker.json"))
	if err != nil {
		return nil, nil, FocusedProviderEvidence{}, nil, err
	}
	externalID := "ticket-" + input.AttemptID
	if err := store.Seed(ctx, trackerfake.WorkSeed{Key: externalID, Title: "focused attribution", Assignee: "human"}); err != nil {
		return nil, nil, FocusedProviderEvidence{}, nil, err
	}
	baseProvider := trackerfake.NewProvider(store)
	var provider tracker.AssignmentProjection = baseProvider
	var slowProvider *cancellationProjection
	var wrongProvider *wrongIdentityProjection
	evidence := FocusedProviderEvidence{State: state, ObservationAttempts: 1, MutationAttempts: 1}
	switch state {
	case "slow":
		slowProvider = &cancellationProjection{delegate: baseProvider, entered: make(chan struct{})}
		provider = slowProvider
	case "unavailable":
		if err := store.Inject(ctx, trackerfake.FailureUnavailable); err != nil {
			return nil, nil, evidence, nil, err
		}
	case "wrong":
		wrongProvider = &wrongIdentityProjection{delegate: baseProvider}
		provider = wrongProvider
	case "stale":
		if err := store.HumanAssign(ctx, externalID, "other-human"); err != nil {
			return nil, nil, evidence, nil, err
		}
		evidence.RevisionBefore, evidence.RevisionAfter = "2", "2"
	}
	coordinator, err := provingground.NewEffectCoordinator(ctx, environment.runtime, environment.app, environment.port, provider, 4)
	if err != nil {
		return nil, nil, evidence, nil, err
	}
	operationID := controljournal.OperationID("focused-effect-" + input.AttemptID)
	effectID := controljournal.EffectID("focused-effect-id-" + input.AttemptID)
	reference := tracker.ExternalWorkRef{ProjectID: "project-qualification", WorkID: "work-baseline", Provider: "durable_fake", ExternalID: externalID, MetadataSchema: 1, CreatedByOperation: ids.OperationID("focused-create-" + input.AttemptID), LastObservation: 1}
	intent := provingground.AssignmentEffectIntent{OperationID: operationID, IntentID: controljournal.IntentID("focused-intent-" + input.AttemptID), EffectID: effectID, ExternalRef: reference, ExpectedRevision: 1, ExpectedAssignee: "human", DesiredAssignee: "agent-focused", Marker: string(operationID), Diagnostic: "focused attribution", OccurredAt: time.Now().UTC(), Authority: environment.baseline.Authority}
	var resolution provingground.EffectResolution
	var publishErr error
	if slowProvider != nil {
		publishCtx, cancel := context.WithCancel(ctx)
		result := make(chan struct {
			resolution provingground.EffectResolution
			err        error
		}, 1)
		go func() {
			got, err := coordinator.PublishAssignment(publishCtx, intent)
			result <- struct {
				resolution provingground.EffectResolution
				err        error
			}{got, err}
		}()
		select {
		case <-slowProvider.entered:
			cancel()
		case <-ctx.Done():
			cancel()
			return nil, nil, evidence, nil, ctx.Err()
		}
		published := <-result
		resolution, publishErr = published.resolution, published.err
		observed, observeErr := baseProvider.ObserveAssignment(ctx, reference)
		if observeErr != nil {
			return nil, nil, evidence, nil, observeErr
		}
		if observed.Observation.Revision != 1 || observed.Assignee != "human" || observed.OperationMarker != "" {
			return nil, nil, evidence, nil, fmt.Errorf("canceled provider mutation changed file-backed state")
		}
	} else {
		resolution, publishErr = coordinator.PublishAssignment(ctx, intent)
	}
	history, historyErr := store.History(ctx)
	if historyErr != nil {
		return nil, nil, evidence, nil, historyErr
	}
	dispatches := 0
	for _, entry := range history {
		if entry.Kind == "assignment" && entry.Marker == string(operationID) {
			dispatches++
		}
	}
	effectObservation := m1.EffectObservation{EffectID: string(effectID), OperationID: string(operationID), ExpectedRevision: "1", ExpectedMarker: string(operationID), Dispatches: dispatches}
	switch state {
	case "slow":
		evidence.MutationAttempts = int(slowProvider.calls.Load())
		evidence.CancellationObserved = slowProvider.canceled.Load() && (tracker.IsKind(publishErr, tracker.ErrorTimeout) || errors.Is(publishErr, context.Canceled))
		evidence.AppliedRecorded = resolution.Outcome == provingground.EffectApplied
	case "unavailable":
		evidence.TypedUnavailable = tracker.IsKind(publishErr, tracker.ErrorUnavailable)
		evidence.AppliedRecorded = resolution.Outcome == provingground.EffectApplied
	case "wrong":
		evidence.IdentityInspected = wrongProvider.inspected.Load()
		evidence.IdentityMatched = resolution.Outcome == provingground.EffectApplied
		evidence.AppliedRecorded = resolution.Outcome == provingground.EffectApplied
	case "stale":
		evidence.AppliedRecorded = resolution.Outcome == provingground.EffectApplied
	case "human_modified":
		if publishErr != nil || resolution.Outcome != provingground.EffectApplied {
			return nil, nil, evidence, nil, fmt.Errorf("initial attributable assignment failed: %w", publishErr)
		}
		effectObservation.AppliedRevision = fmt.Sprint(resolution.ProviderRevision)
		effectObservation.AppliedValue = resolution.ProviderValue
		effectObservation.AppliedMarker = resolution.ProviderMarker
		before := resolution.ProviderRevision
		if err := store.HumanAssign(ctx, externalID, "human-owner"); err != nil {
			return nil, nil, evidence, nil, err
		}
		compensation, compensationErr := coordinator.Compensate(ctx, effectID, controljournal.OperationID("focused-compensate-"+input.AttemptID), time.Now().UTC())
		if compensationErr != nil {
			return nil, nil, evidence, nil, compensationErr
		}
		human, observeErr := store.ObserveHumanState(ctx, externalID)
		if observeErr != nil {
			return nil, nil, evidence, nil, observeErr
		}
		evidence.RevisionBefore, evidence.RevisionAfter = fmt.Sprint(before), fmt.Sprint(human.Revision)
		evidence.CompensationApplied = compensation.Outcome == provingground.EffectCompensated
		evidence.HumanValuePreserved = human.Assignee == "human-owner"
		effectObservation.CompensationAttempted = true
		effectObservation.CompensationApplied = evidence.CompensationApplied
		effectObservation.HumanValuePreserved = evidence.HumanValuePreserved
		effectObservation.CurrentRevision = fmt.Sprint(human.Revision)
		effectObservation.CurrentValue = human.Assignee
	}
	_, intentDurable := coordinator.State(effectID)
	facts := map[string]bool{
		"one_typed_attempt":       evidence.MutationAttempts == 1 && evidence.ObservationAttempts >= 1,
		"zero_blind_repeats":      evidence.BlindRepeats == 0,
		"provider_state_observed": state != "",
		"effect_intent_durable":   intentDurable,
		"schedule_consumed":       input.Order[0] != input.Order[1],
	}
	artifact, _ := json.Marshal(struct {
		Input      FocusedInput                   `json:"input"`
		Resolution provingground.EffectResolution `json:"resolution"`
		History    []trackerfake.HistoryEntry     `json:"history"`
		Evidence   FocusedProviderEvidence        `json:"provider_evidence"`
		Facts      map[string]bool                `json:"facts"`
	}{input, resolution, history, evidence, facts})
	return artifact, facts, evidence, []m1.EffectObservation{effectObservation}, nil
}

type qualificationIdentities struct {
	runID ids.RunID
	next  atomic.Uint64
}

func (i *qualificationIdentities) RunID() (ids.RunID, error) {
	if i == nil || i.runID == "" {
		return "", fmt.Errorf("qualification RunID is absent")
	}
	return i.runID, nil
}
func (i *qualificationIdentities) LeaseID() (ids.LeaseID, error) {
	return ids.LeaseID(fmt.Sprintf("lease-qualification-%d", i.next.Add(1))), nil
}

func (p *ProductionExecutor) productionEnvironment(ctx context.Context, requestedRunID ...string) (*productionEnvironment, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	runID := p.RunID
	if len(requestedRunID) > 0 && requestedRunID[0] != "" {
		if runID != "" && runID != requestedRunID[0] {
			return nil, fmt.Errorf("production executor RunID mismatch: configured=%q requested=%q", runID, requestedRunID[0])
		}
		runID = requestedRunID[0]
	}
	if runID == "" {
		// Compatibility for direct diagnostic Prepare calls. Every fault and
		// focused runner supplies its own immutable RunID.
		runID = "run-qualification"
	}
	if p.environment != nil {
		if p.environment.runID != runID {
			return nil, fmt.Errorf("production environment is already bound to RunID %q", p.environment.runID)
		}
		return p.environment, nil
	}
	port, closer, err := storegapdb.Dial(p.Lifecycle.Provenance().ConfiguredSocket, 2*time.Second)
	if err != nil {
		return nil, err
	}
	events, err := provingground.M1EventRegistryV1()
	if err != nil {
		closer.Close()
		return nil, err
	}
	reducers, err := provingground.M1ReducerRegistryV1()
	if err != nil {
		closer.Close()
		return nil, err
	}
	eventDigest, _ := events.Digest()
	reducerDigest, _ := reducers.Digest()
	binding, err := p.Lifecycle.ProductRestoreBindingV1()
	if err != nil {
		closer.Close()
		return nil, err
	}
	reader, err := application.NewRestoreReaderV1(port, "project-qualification", "mission-qualification")
	if err != nil {
		closer.Close()
		return nil, err
	}
	restored, err := journalcheckpoint.RestoreV1(ctx, journalcheckpoint.RestoreInputV1{Reader: reader, Source: journalcheckpoint.SourceIdentityV1{ProjectID: "project-qualification", MissionID: "mission-qualification", RunID: controljournal.RunID(runID), ReducerSetDigest: reducerDigest, EventSchemaSetDigest: eventDigest}, Binding: binding, EventRegistry: events, Reducers: reducers})
	if err != nil || !restored.Ready {
		closer.Close()
		return nil, fmt.Errorf("restore proving ground: ready=%v: %w", restored.Ready, err)
	}
	restoreAdmission, err := restored.AdmissionV1()
	if err != nil {
		closer.Close()
		return nil, err
	}
	productProof, err := p.Lifecycle.MarkProductRecovered(restoreAdmission)
	if err != nil {
		closer.Close()
		return nil, err
	}
	startupProof, err := p.Lifecycle.BindOwnerStartupEpochV1("epoch-qualification")
	if err != nil {
		closer.Close()
		return nil, err
	}
	admission, result, err := application.AdmitRuntimeEpochV1(ctx, port, application.EpochAdmissionInputV1{ProjectID: "project-qualification", MissionID: "mission-qualification", OperationID: "admit-qualification", EventID: "event-admit-qualification", StartupProof: startupProof, ProductRestoreProof: productProof})
	if err != nil || result.Kind != application.OutcomeCommitted {
		closer.Close()
		return nil, fmt.Errorf("admit proving ground: result=%+v: %w", result, err)
	}
	app, err := application.NewService(port, admission)
	if err != nil {
		closer.Close()
		return nil, err
	}
	root, err := os.MkdirTemp("/tmp", "spk-composed-runtime-")
	if err != nil {
		closer.Close()
		return nil, err
	}
	primary := filepath.Join(root, "primary")
	if err := initGitRepository(ctx, primary); err != nil {
		closer.Close()
		os.RemoveAll(root)
		return nil, err
	}
	if output, detachErr := exec.CommandContext(ctx, "git", "-C", primary, "checkout", "--detach").CombinedOutput(); detachErr != nil {
		closer.Close()
		os.RemoveAll(root)
		return nil, fmt.Errorf("detach qualification primary: %s: %w", bounded(string(output)), detachErr)
	}
	repository, err := gitrepo.Bind(ctx, gitrepo.Runner{}, "repository-qualification", "project-qualification", primary, filepath.Join(root, "managed"), "refs/heads/main", time.Now().UTC())
	if err != nil {
		closer.Close()
		os.RemoveAll(root)
		return nil, err
	}
	if err := repository.PrepareManagedRoot(); err != nil {
		closer.Close()
		os.RemoveAll(root)
		return nil, err
	}
	actors, err := controljournal.NewTrialImplementationActorsV1("actor-a", "actor-b", "process-a", "process-b", "process-c")
	if err != nil {
		closer.Close()
		os.RemoveAll(root)
		return nil, err
	}
	config := provingground.Config{ProjectID: "project-qualification", MissionID: "mission-qualification", WorkRevisionID: "revision-qualification", RunID: controljournal.RunID(runID), RuntimeEpochID: "epoch-qualification", Actors: actors, WorkIDs: []controljournal.WorkID{"work-baseline", "work-a"}, LeaseTTL: qualificationLeaseTTL}
	identities := &qualificationIdentities{runID: ids.RunID(runID)}
	runtime, err := provingground.NewRuntime(config, app, repository, identities, execution.SystemClock{})
	if err != nil {
		closer.Close()
		os.RemoveAll(root)
		return nil, err
	}
	p.environment = &productionEnvironment{root: root, runID: runID, port: port, closer: closer, app: app, admission: admission, runtime: runtime, repository: repository, identities: identities, events: events, reducers: reducers, binding: binding, reader: reader}
	return p.environment, nil
}

func ensureBaseline(ctx context.Context, environment *productionEnvironment) error {
	if environment.baseline.Authority.LeaseID != "" {
		return nil
	}
	now := time.Now().UTC()
	governance, err := qualificationGovernance(now)
	if err != nil {
		return err
	}
	base, err := environment.repository.TargetHead(ctx)
	if err != nil {
		return err
	}
	delivery, err := environment.runtime.StartWorkspace(ctx, execution.StartRequest{OperationID: "claim-baseline", ProjectID: "project-qualification", MissionID: "mission-qualification", WorkRevisionID: "revision-qualification", WorkID: "work-baseline", ActorID: "actor-b", ProcessID: "process-b", Now: now, LeaseTTL: qualificationLeaseTTL, GovernanceContext: governance}, provingground.WorkspaceSpec{OperationID: "bind-baseline", WorkspaceID: "workspace-baseline", RepositoryID: "repository-qualification", LaneID: "lane-baseline", OwnedBranch: "runs/baseline", PinnedBase: base, Now: now.Add(time.Millisecond)})
	if err != nil {
		return err
	}
	environment.baseline = delivery
	return nil
}

func qualificationGovernance(now time.Time) (governancecontext.Context, error) {
	commitment, err := governancecontext.CommitSHA256([]byte("m1-qualification"))
	if err != nil {
		return governancecontext.Context{}, err
	}
	entry, err := governancecontext.NewEntry(governancecontext.KindConfig, "m1-qualification", "v1", commitment)
	if err != nil {
		return governancecontext.Context{}, err
	}
	return governancecontext.NewContext("context-m1-qualification", governancecontext.SchemaVersion, []governancecontext.Entry{entry}, now)
}

func (p *ProductionExecutor) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.environment == nil {
		return nil
	}
	err := p.environment.closer.Close()
	_ = os.RemoveAll(p.environment.root)
	p.environment = nil
	return err
}

type publicationCutPort struct {
	storegapdb.Port
	hook productfaults.Hook
}

func (p *publicationCutPort) AtomicBatch(ctx context.Context, batch storegapdb.ProductBatch) (storegapdb.MutationResult, error) {
	if err := p.hook.Reach(ctx, "GJ-01"); err != nil {
		return storegapdb.MutationResult{}, err
	}
	return p.Port.AtomicBatch(ctx, batch)
}

func productCensus(ctx context.Context, port storegapdb.Port, prefix string) (map[string]ProductFact, error) {
	page, err := port.Scan(ctx, storegapdb.ScanRequest{Prefix: prefix, Limit: 64})
	if err != nil {
		return nil, err
	}
	result := make(map[string]ProductFact, len(page.Records))
	for _, record := range page.Records {
		digest := sha256.Sum256(record.Value)
		result[record.Key] = ProductFact{Revision: record.Revision, Digest: hex.EncodeToString(digest[:])}
	}
	return result, nil
}

func executeComposedGJ01(ctx context.Context, attempt Attempt, environment *productionEnvironment) (Facts, []byte, error) {
	prefix := "spk/v1/project-qualification/mission-qualification/"
	before, err := productCensus(ctx, environment.port, prefix)
	if err != nil || len(before) == 0 {
		return Facts{}, nil, fmt.Errorf("GJ-01 preexisting product census unavailable: %w", err)
	}
	batch, err := qualificationBatch(attempt, "gj01", environment.runtime.QualificationSnapshot().RuntimeEpochID)
	if err != nil {
		return Facts{}, nil, err
	}
	service, err := application.NewService(&publicationCutPort{Port: environment.port, hook: attempt.Hook}, environment.admission)
	if err != nil {
		return Facts{}, nil, err
	}
	result, err := service.Apply(ctx, batch)
	if err != nil {
		return Facts{}, nil, err
	}
	if result.Kind == application.OutcomeCommitted || result.Kind == application.OutcomeAlreadyCommitted {
		return Facts{}, nil, fmt.Errorf("GJ-01 cut published operation: %+v", result)
	}
	after, err := productCensus(ctx, environment.port, prefix)
	if err != nil {
		return Facts{}, nil, err
	}
	encoded, _ := json.Marshal(struct {
		Result        application.ResultV1 `json:"result"`
		Before, After map[string]ProductFact
	}{result, before, after})
	return Facts{PreexistingBefore: before, PreexistingAfter: after}, encoded, nil
}

func qualificationBatch(attempt Attempt, label string, epoch controljournal.RuntimeEpochID) (application.ValidatedBatchV1, error) {
	identity := productionAttemptID(attempt)
	payload := []byte(fmt.Sprintf(`{"attempt":%q,"seed":%d}`, identity, attempt.Seed))
	return qualificationBatchPayload(attempt, label, epoch, payload)
}

func qualificationBatchPayload(attempt Attempt, label string, epoch controljournal.RuntimeEpochID, payload []byte) (application.ValidatedBatchV1, error) {
	identity := productionAttemptID(attempt)
	operation := controljournal.OperationID("operation-" + label + "-" + identity)
	command, err := application.ValidateCommandEnvelopeV1(application.CommandEnvelopeInputV1{SchemaVersion: 1, ProjectID: "project-qualification", MissionID: "mission-qualification", OperationID: operation, CommandType: "qualification.observe", RuntimeEpochID: epoch, Payload: payload})
	if err != nil {
		return application.ValidatedBatchV1{}, err
	}
	index, _ := controljournal.NewEventIndexV1(0)
	count, _ := controljournal.NewEventCountV1(1)
	eventID := controljournal.EventID("event-" + label + "-" + identity)
	aggregateID := controljournal.AggregateID("qualification-" + label + "-" + identity)
	event, err := controljournal.NewEventEnvelopeV1(controljournal.EventEnvelopeInputV1{EnvelopeVersion: 1, EventID: eventID, EventType: "authority.claimed", EventSchemaVersion: 1, ReducerVersion: "proving-ground-v1", ProjectID: "project-qualification", AggregateKind: "authority", AggregateID: aggregateID, AggregateLogicalVersion: 1, OperationID: operation, RuntimeEpochID: epoch, EventIndex: index, EventCount: count, Payload: payload})
	if err != nil {
		return application.ValidatedBatchV1{}, err
	}
	head, err := controljournal.ConstructAggregateHeadV1("authority", aggregateID, 1, epoch, eventID, sha256.Sum256(payload))
	if err != nil {
		return application.ValidatedBatchV1{}, err
	}
	expected := []controljournal.ExpectedAggregateHeadV1{{AggregateKind: "authority", AggregateID: aggregateID}}
	preconditions, err := application.CanonicalPreconditionsDigestV1(expected)
	if err != nil {
		return application.ValidatedBatchV1{}, err
	}
	return application.NewValidatedBatchV1(application.BatchInputV1{Command: command, ExpectedHeads: expected, NextHeads: []controljournal.NewAggregateHeadV1{{Head: head}}, Events: []controljournal.EventEnvelopeV1{event}, ExpectedPreconditionsDigest: preconditions, ResultType: "qualification.observed", ResultSchemaVersion: 1, CanonicalResult: payload})
}

func executeComposedGJ02(ctx context.Context, attempt Attempt, lifecycle *sidecargapdb.Lifecycle, source SourceBinding, environment *productionEnvironment) (Facts, []byte, error) {
	root, err := os.MkdirTemp("/tmp", "spk-gj02-composed-")
	if err != nil {
		return Facts{}, nil, err
	}
	defer os.RemoveAll(root)
	if err := os.Chmod(root, 0o700); err != nil {
		return Facts{}, nil, err
	}
	requestID := "operation-gj02-" + productionAttemptID(attempt)
	productSocket := filepath.Join(root, "p.sock")
	provenance := lifecycle.Provenance()
	proxy, err := sidecargapdb.StartResponseDropProxy(sidecargapdb.ProxyConfig{ProductSocket: productSocket, DaemonSocket: provenance.ConfiguredSocket, ExpectedDaemon: provenance.Process, ExpectedDatabaseID: provenance.DatabaseID, SelectedRequestID: requestID, SourceCommit: source.Commit, ReadTimeout: 2 * time.Second})
	if err != nil {
		return Facts{}, nil, err
	}
	defer proxy.Close()
	proxyPort, closer, err := storegapdb.Dial(productSocket, 2*time.Second)
	if err != nil {
		return Facts{}, nil, err
	}
	defer closer.Close()
	proxyApp, err := application.NewService(proxyPort, environment.admission)
	if err != nil {
		return Facts{}, nil, err
	}
	restoredBefore, _, err := restoreAndJournal(ctx, environment)
	if err != nil {
		return Facts{}, nil, err
	}
	proxyRuntime, err := restoreQualificationRuntime(ctx, environment, proxyApp, environment.port, restoredBefore)
	if err != nil {
		return Facts{}, nil, fmt.Errorf("GJ-02 restore proxy-bound runtime: %w", err)
	}
	target := environment.baseline
	if target.Run.WorkID == "" || target.Authority.WorkspaceID == "" {
		return Facts{}, nil, fmt.Errorf("GJ-02 requires an existing configured workspace authority")
	}
	beforeAuthority := target.Authority
	heartbeatAt := time.Now().UTC()
	_, firstErr := proxyRuntime.Heartbeat(ctx, controljournal.OperationID(requestID), target.Authority, heartbeatAt, qualificationLeaseTTL)
	if !provingground.IsOutcome(firstErr, provingground.OutcomeAmbiguous) {
		return Facts{}, nil, fmt.Errorf("GJ-02 heartbeat did not report ambiguous durable acceptance: %w", firstErr)
	}
	cutCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cut, err := proxy.WaitForCut(cutCtx)
	if err != nil {
		return Facts{}, nil, err
	}
	if hookErr := attempt.Hook.Reach(ctx, "GJ-02"); !errors.Is(hookErr, productfaults.ErrCrashRequested) {
		return Facts{}, nil, fmt.Errorf("GJ-02 cut acknowledgement: %w", hookErr)
	}
	refreshed, err := proxyRuntime.Heartbeat(ctx, controljournal.OperationID(requestID), target.Authority, heartbeatAt, qualificationLeaseTTL)
	if err != nil || refreshed.AuthorityHeadRecordRevision != cut.Revision {
		return Facts{}, nil, fmt.Errorf("GJ-02 exact heartbeat retry revision=%d cut=%+v: %w", refreshed.AuthorityHeadRecordRevision, cut, err)
	}
	restoredAfter, _, err := restoreAndJournal(ctx, environment)
	if err != nil {
		return Facts{}, nil, err
	}
	directRuntime, err := restoreQualificationRuntime(ctx, environment, environment.app, environment.port, restoredAfter)
	if err != nil {
		return Facts{}, nil, fmt.Errorf("GJ-02 restore reconciled runtime: %w", err)
	}
	environment.runtime = directRuntime
	target.Authority = refreshed
	target.Run.Deadline = heartbeatAt.Add(qualificationLeaseTTL)
	environment.baseline = target
	headIDs, err := authorityHeadIDs(ctx, environment.port)
	if err != nil {
		return Facts{}, nil, err
	}
	if !authorityHeadCensusIsConfigured(headIDs, target.Run.WorkID.String()) {
		return Facts{}, nil, fmt.Errorf("GJ-02 authority head census contains non-configured work: %v", headIDs)
	}
	completeResponse := cut.RequestID == requestID && cut.Revision > 0 && cut.MutationCount > 0
	encoded, _ := json.Marshal(struct {
		Cut              sidecargapdb.ResponseDropCut `json:"cut"`
		Before, After    controljournal.AuthorityCapsuleV1
		AuthorityHeadIDs []string `json:"authority_head_ids"`
	}{cut, beforeAuthority, refreshed, headIDs})
	return Facts{OperationRecords: 1, OperationResults: 1, DurableAccepted: refreshed.AuthorityHeadRecordRevision == cut.Revision, DaemonResponseComplete: completeResponse, ProxyConsumedCompleteResponse: completeResponse, ProxyCut: &cut, SidecarProvenanceSHA256: source.SidecarProvenanceSHA256, HeartbeatOperationID: requestID, HeartbeatWorkID: target.Run.WorkID.String(), AuthorityHeadIDs: headIDs}, encoded, nil
}

func restoreQualificationRuntime(ctx context.Context, environment *productionEnvironment, app *application.Service, port storegapdb.Port, restored journalcheckpoint.RestoreResultV1) (*provingground.Runtime, error) {
	actors, err := controljournal.NewTrialImplementationActorsV1("actor-a", "actor-b", "process-a", "process-b", "process-c")
	if err != nil {
		return nil, err
	}
	config := provingground.Config{
		ProjectID: "project-qualification", MissionID: "mission-qualification",
		WorkRevisionID: "revision-qualification", RunID: controljournal.RunID(environment.runID),
		RuntimeEpochID: environment.runtime.QualificationSnapshot().RuntimeEpochID,
		Actors:         actors, WorkIDs: []controljournal.WorkID{"work-baseline", "work-a"}, LeaseTTL: qualificationLeaseTTL,
	}
	return provingground.RestoreRuntimeValidated(ctx, config, app, environment.repository, port, restored, environment.identities, execution.SystemClock{})
}

func authorityHeadIDs(ctx context.Context, port storegapdb.Port) ([]string, error) {
	const prefix = "spk/v1/project-qualification/mission-qualification/heads/authority/"
	records, err := scanAllRecords(ctx, port, prefix)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(records))
	for _, record := range records {
		id := strings.TrimPrefix(record.Key, prefix)
		if id == "" || id == record.Key || strings.Contains(id, "/") {
			return nil, fmt.Errorf("invalid authority head coordinate %q", record.Key)
		}
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}

func authorityHeadCensusIsConfigured(headIDs []string, required string) bool {
	foundRequired := false
	for _, id := range headIDs {
		if id != "work-a" && id != "work-baseline" {
			return false
		}
		foundRequired = foundRequired || id == required
	}
	return foundRequired
}

func executeComposedWorkspaceFault(ctx context.Context, attempt Attempt, environment *productionEnvironment) (Facts, []byte, error) {
	label := strings.ToLower(strings.ReplaceAll(attempt.Definition.ID, "-", ""))
	workID := ids.WorkID("work-a")
	process, actor := ids.ProcessID("process-a"), ids.ActorID("actor-a")
	if attempt.Definition.ID == "GJ-04" {
		process = "process-c"
	}
	now := time.Now().UTC()
	if attempt.Definition.ID == "GJ-04" && environment.workA != nil {
		expiredAt := environment.workA.Run.Deadline
		if err := environment.runtime.Expire(ctx, "expire-gj03-before-gj04", environment.workA.Authority, expiredAt); err != nil {
			return Facts{}, nil, err
		}
		now = expiredAt.Add(time.Millisecond)
	}
	governance, err := qualificationGovernance(now)
	if err != nil {
		return Facts{}, nil, err
	}
	base, err := environment.repository.TargetHead(ctx)
	if err != nil {
		return Facts{}, nil, err
	}
	start := execution.StartRequest{OperationID: ids.OperationID("claim-" + label), ProjectID: "project-qualification", MissionID: "mission-qualification", WorkRevisionID: "revision-qualification", WorkID: workID, ActorID: actor, ProcessID: process, Now: now, LeaseTTL: qualificationLeaseTTL, GovernanceContext: governance}
	spec := provingground.WorkspaceSpec{OperationID: controljournal.OperationID("bind-" + label), WorkspaceID: controljournal.WorkspaceID("workspace-" + label), RepositoryID: "repository-qualification", LaneID: ids.LaneID("lane-" + label), OwnedBranch: "runs/" + label, PinnedBase: base, Now: now.Add(time.Millisecond)}
	environment.runtime.WithFaults(mappedHook{boundary: attempt.Definition.ID, faultID: attempt.Definition.ID, delegate: attempt.Hook})
	_, cutErr := environment.runtime.StartWorkspace(ctx, start, spec)
	if !errors.Is(cutErr, productfaults.ErrCrashRequested) {
		return Facts{}, nil, fmt.Errorf("%s composed runtime did not cut: %w", attempt.Definition.ID, cutErr)
	}
	cutSnapshot := environment.runtime.QualificationSnapshot()
	record := workspace.Record{Reservation: workspace.Reservation{WorkspaceID: ids.WorkspaceID(spec.WorkspaceID), OwnedBranch: spec.OwnedBranch, PinnedBase: spec.PinnedBase}, CanonicalPath: filepath.Join(environment.repository.Binding().ManagedRoot, string(spec.WorkspaceID))}
	probe, err := environment.repository.Probe(ctx, record)
	if err != nil {
		return Facts{}, nil, err
	}
	environment.runtime.WithFaults(nil)
	facts := Facts{PhysicalWorkspaceCreated: probe.Registered, PhysicalWorkspaceID: string(spec.WorkspaceID)}
	if attempt.Definition.ID == "GJ-03" {
		fresh := spec
		fresh.OperationID = "bind-gj03-recovered"
		fresh.WorkspaceID = "workspace-gj03-recovered"
		fresh.LaneID = "lane-gj03-recovered"
		fresh.OwnedBranch = "runs/gj03-recovered"
		fresh.Now = now.Add(2 * time.Millisecond)
		delivery, recoverErr := environment.runtime.StartWorkspace(ctx, start, fresh)
		if recoverErr != nil {
			return Facts{}, nil, recoverErr
		}
		facts.RecoveredWorkspaceID = string(delivery.Authority.WorkspaceID)
		environment.workA = &delivery
	} else {
		delivery, recoverErr := environment.runtime.StartWorkspace(ctx, start, spec)
		if recoverErr != nil {
			return Facts{}, nil, recoverErr
		}
		facts.DurableBinding, facts.CapsuleReconstructed = true, true
		facts.CapsuleBindingMatches = delivery.Authority.WorkspaceID == spec.WorkspaceID
		facts.RecoveredWorkspaceID = string(delivery.Authority.WorkspaceID)
		environment.workA = &delivery
	}
	after := environment.runtime.QualificationSnapshot()
	facts.DurableWorkspaceCount = len(after.Workspaces)
	// Baseline W2 is part of the composed runtime; fault-specific facts count
	// only the target work's one ready binding.
	facts.DurableWorkspaceCount = 0
	for _, binding := range after.Workspaces {
		if binding.WorkID == controljournal.WorkID(workID) && binding.Status == "active-bound" {
			facts.DurableWorkspaceCount++
		}
	}
	if attempt.Definition.ID == "GJ-03" {
		facts.DurableBinding = snapshotHasWorkspaceBinding(cutSnapshot, spec.WorkspaceID)
	}
	encoded, _ := json.Marshal(struct {
		Cut, After provingground.QualificationSnapshot
		Probe      workspace.Probe
		Facts      Facts
	}{cutSnapshot, after, probe, facts})
	return facts, encoded, nil
}

func snapshotHasWorkspaceBinding(snapshot provingground.QualificationSnapshot, workspaceID controljournal.WorkspaceID) bool {
	for _, binding := range snapshot.Workspaces {
		if binding.WorkspaceID == workspaceID {
			return true
		}
	}
	return false
}

func executeComposedGJ05(ctx context.Context, attempt Attempt, environment *productionEnvironment) (Facts, []m1.EffectObservation, []byte, error) {
	root := filepath.Join(environment.root, "tracker-"+productionAttemptID(attempt))
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Facts{}, nil, nil, err
	}
	store, err := trackerfake.Open(filepath.Join(root, "tracker.json"))
	if err != nil {
		return Facts{}, nil, nil, err
	}
	externalID := "external-" + productionAttemptID(attempt)[:12]
	if err := store.Seed(ctx, trackerfake.WorkSeed{Key: externalID, Title: "GJ-05 composed effect", Assignee: "human"}); err != nil {
		return Facts{}, nil, nil, err
	}
	provider := trackerfake.NewProvider(store)
	coordinator, err := provingground.NewEffectCoordinator(ctx, environment.runtime, environment.app, environment.port, provider, 4)
	if err != nil {
		return Facts{}, nil, nil, err
	}
	operationID := controljournal.OperationID("effect-operation-" + productionAttemptID(attempt))
	reference := tracker.ExternalWorkRef{ProjectID: "project-qualification", WorkID: "work-baseline", Provider: "durable_fake", ExternalID: externalID, MetadataSchema: 1, CreatedByOperation: "create-effect-qualification"}
	intent := provingground.AssignmentEffectIntent{OperationID: operationID, IntentID: controljournal.IntentID("intent-" + productionAttemptID(attempt)), EffectID: controljournal.EffectID("effect-" + productionAttemptID(attempt)), ExternalRef: reference, ExpectedRevision: 1, ExpectedAssignee: "human", DesiredAssignee: "agent-qualification", Marker: string(operationID), Diagnostic: "qualification", OccurredAt: time.Now().UTC(), Authority: environment.baseline.Authority}
	coordinator.WithFaults(mappedHook{boundary: "GJ-05", faultID: "GJ-05", delegate: attempt.Hook})
	_, cutErr := coordinator.PublishAssignment(ctx, intent)
	if !errors.Is(cutErr, productfaults.ErrCrashRequested) {
		return Facts{}, nil, nil, fmt.Errorf("GJ-05 composed effect did not cut: %w", cutErr)
	}
	historyBefore, err := store.History(ctx)
	if err != nil {
		return Facts{}, nil, nil, err
	}
	coordinator.WithFaults(productfaults.Noop{})
	resolution, err := coordinator.PublishAssignment(ctx, intent)
	if err != nil || resolution.Outcome != provingground.EffectApplied {
		return Facts{}, nil, nil, fmt.Errorf("GJ-05 recovery=%+v: %w", resolution, err)
	}
	historyAfter, err := store.History(ctx)
	if err != nil {
		return Facts{}, nil, nil, err
	}
	dispatches := 0
	for _, entry := range historyAfter {
		if entry.Kind == "assignment" && entry.Marker == string(operationID) {
			dispatches++
		}
	}
	if len(historyAfter) != len(historyBefore)+1 || dispatches != 1 {
		return Facts{}, nil, nil, fmt.Errorf("GJ-05 provider history is not one attributable dispatch")
	}
	facts := Facts{EffectIntentDurable: true, ProviderObservationDurable: true, ProviderDispatches: dispatches, AttributableOutcomes: 1}
	effect := m1.EffectObservation{EffectID: string(intent.EffectID), OperationID: string(operationID), ExpectedRevision: "1", AppliedRevision: fmt.Sprint(resolution.ProviderRevision), ExpectedMarker: string(operationID), AppliedMarker: resolution.ProviderMarker, Dispatches: dispatches, Attributable: resolution.ProviderMarker == string(operationID)}
	encoded, _ := json.Marshal(struct {
		Resolution provingground.EffectResolution
		History    []trackerfake.HistoryEntry
	}{resolution, historyAfter})
	return facts, []m1.EffectObservation{effect}, encoded, nil
}

type composedObservationPort struct {
	environment *productionEnvironment
	effects     []m1.EffectObservation
	overlay     qualificationOverlay
}

type attemptObservationPort struct {
	source           m1.ObservationPort
	observationOnce  sync.Once
	observationBytes []byte
	observationErr   error
	probeOnce        sync.Once
	probe            m1.StaleProbe
	probeErr         error
}

type qualificationOverlay struct {
	authorities      []m1.AuthorityObservation
	reviews          []m1.ReviewObservation
	integration      []m1.IntegrationObservation
	primary          m1.PrimaryObservation
	playback         *m1.PlaybackObservation
	journal          *controljournal.ValidatedJournalV1
	observedRevision uint64
}

func observeComposedRuntime(_ context.Context, environment *productionEnvironment, effects []m1.EffectObservation) (m1.ObservationPort, error) {
	return observeComposedRuntimeWithOverlay(context.Background(), environment, effects, qualificationOverlay{})
}

func observeComposedRuntimeWithOverlay(_ context.Context, environment *productionEnvironment, effects []m1.EffectObservation, overlay qualificationOverlay) (m1.ObservationPort, error) {
	if environment == nil || environment.runtime == nil || environment.port == nil || environment.repository == nil {
		return nil, fmt.Errorf("composed runtime observation is unavailable")
	}
	copyOverlay := qualificationOverlay{authorities: append([]m1.AuthorityObservation(nil), overlay.authorities...), reviews: append([]m1.ReviewObservation(nil), overlay.reviews...), integration: append([]m1.IntegrationObservation(nil), overlay.integration...), primary: overlay.primary}
	if overlay.playback != nil {
		playback := *overlay.playback
		copyOverlay.playback = &playback
	}
	if overlay.journal != nil {
		journal := *overlay.journal
		copyOverlay.journal = &journal
	}
	copyOverlay.observedRevision = overlay.observedRevision
	source := &composedObservationPort{environment: environment, effects: append([]m1.EffectObservation(nil), effects...), overlay: copyOverlay}
	return newAttemptObservationPort(source), nil
}

func newAttemptObservationPort(source m1.ObservationPort) m1.ObservationPort {
	return &attemptObservationPort{source: source}
}

func (p *attemptObservationPort) ObserveM1(ctx context.Context) (m1.Observation, error) {
	p.observationOnce.Do(func() {
		if p.source == nil {
			p.observationErr = fmt.Errorf("attempt observation source is absent")
			return
		}
		observed, err := p.source.ObserveM1(ctx)
		if err != nil {
			p.observationErr = err
			return
		}
		p.observationBytes, p.observationErr = json.Marshal(observed)
	})
	if p.observationErr != nil {
		return m1.Observation{}, p.observationErr
	}
	var observed m1.Observation
	if err := json.Unmarshal(p.observationBytes, &observed); err != nil {
		return m1.Observation{}, fmt.Errorf("clone attempt observation: %w", err)
	}
	return observed, nil
}

func (p *attemptObservationPort) ProbeStaleAuthority(ctx context.Context) (m1.StaleProbe, error) {
	p.probeOnce.Do(func() {
		if p.source == nil {
			p.probeErr = fmt.Errorf("attempt observation source is absent")
			return
		}
		p.probe, p.probeErr = p.source.ProbeStaleAuthority(ctx)
	})
	return p.probe, p.probeErr
}

func (p *composedObservationPort) ObserveM1(ctx context.Context) (m1.Observation, error) {
	prefix := "spk/v1/project-qualification/mission-qualification/"
	operationRecords, err := scanAllRecords(ctx, p.environment.port, prefix+"operations/")
	if err != nil {
		return m1.Observation{}, err
	}
	headRecords, err := scanAllRecords(ctx, p.environment.port, prefix+"heads/")
	if err != nil {
		return m1.Observation{}, err
	}
	var restored journalcheckpoint.RestoreResultV1
	var journal controljournal.ValidatedJournalV1
	if p.overlay.journal != nil && p.overlay.playback != nil {
		if err := requireExactStoreRevision(ctx, p.environment.port, p.overlay.observedRevision); err != nil {
			return m1.Observation{}, err
		}
		journal = *p.overlay.journal
	} else if p.overlay.journal != nil || p.overlay.playback != nil || p.overlay.observedRevision != 0 {
		return m1.Observation{}, fmt.Errorf("same-attempt restore overlay is incomplete")
	} else {
		restored, journal, err = restoreAndJournal(ctx, p.environment)
		if err != nil {
			return m1.Observation{}, err
		}
	}
	records, err := controljournal.SnapshotJournalV1(journal)
	if err != nil {
		return m1.Observation{}, err
	}
	events := make(map[controljournal.EventID]struct {
		revision uint64
		event    controljournal.EventEnvelopeV1
	}, len(records))
	for _, record := range records {
		events[record.Envelope.EventID] = struct {
			revision uint64
			event    controljournal.EventEnvelopeV1
		}{record.RecordRevision, record.Envelope}
	}
	observation := m1.Observation{Capabilities: m1.CapabilityObservation{Authority: true, Workspace: true, Review: true, Integration: true, Effects: true}, Journal: m1.JournalObservation{Inspectable: true}, Effects: append([]m1.EffectObservation(nil), p.effects...), Reviews: append([]m1.ReviewObservation(nil), p.overlay.reviews...), Integration: append([]m1.IntegrationObservation(nil), p.overlay.integration...), Primary: p.overlay.primary}
	sort.Slice(operationRecords, func(i, j int) bool { return operationRecords[i].Revision < operationRecords[j].Revision })
	for _, stored := range operationRecords {
		record, decodeErr := controljournal.DecodeOperationRecordV1(stored.Value)
		if decodeErr != nil {
			return m1.Observation{}, decodeErr
		}
		canonical := record.CanonicalResult()
		resultDigest := sha256.Sum256(canonical)
		op := m1.JournalOperation{OperationID: string(record.OperationID), RequestDigest: hex.EncodeToString(record.RequestDigest[:]), ResultDigest: hex.EncodeToString(resultDigest[:]), RecordRevision: stored.Revision, EventCount: len(record.EventIDs()), Terminal: record.Status == "accepted", CanonicalResult: canonical}
		for _, id := range record.EventIDs() {
			storedEvent, ok := events[id]
			if !ok || storedEvent.revision != stored.Revision {
				return m1.Observation{}, fmt.Errorf("operation %s lacks atomic event %s", record.OperationID, id)
			}
			event := storedEvent.event
			payloadDigest := sha256.Sum256(event.Payload())
			key := string(event.AggregateKind) + "/" + string(event.AggregateID)
			op.ObservedHeadKeys = append(op.ObservedHeadKeys, key)
			op.Events = append(op.Events, m1.JournalEvent{EventID: string(event.EventID), Index: int(event.EventIndex), Count: int(event.EventCount), RecordRevision: storedEvent.revision, AggregateKey: key, LogicalVersion: event.AggregateLogicalVersion, PayloadDigest: hex.EncodeToString(payloadDigest[:])})
		}
		observation.Journal.Operations = append(observation.Journal.Operations, op)
	}
	for _, stored := range headRecords {
		head, decodeErr := controljournal.DecodeAggregateHeadV1(stored.Value)
		if decodeErr != nil {
			return m1.Observation{}, decodeErr
		}
		observation.Journal.Heads = append(observation.Journal.Heads, m1.JournalHead{AggregateKey: string(head.AggregateKind) + "/" + string(head.AggregateID), LogicalVersion: head.LogicalVersion, RecordRevision: stored.Revision, LastEventID: string(head.LastEventID), PayloadDigest: hex.EncodeToString(head.StateDigest[:])})
	}
	headByKey := make(map[string]m1.JournalHead, len(observation.Journal.Heads))
	for _, head := range observation.Journal.Heads {
		headByKey[head.AggregateKey] = head
	}
	for operationIndex := range observation.Journal.Operations {
		for eventIndex := range observation.Journal.Operations[operationIndex].Events {
			event := &observation.Journal.Operations[operationIndex].Events[eventIndex]
			if head, ok := headByKey[event.AggregateKey]; ok && head.LastEventID == event.EventID && head.LogicalVersion == event.LogicalVersion && head.RecordRevision == event.RecordRevision {
				// The observer's payload digest is the aggregate state payload
				// accepted into the exact head, not the event-envelope byte digest.
				event.PayloadDigest = head.PayloadDigest
			}
		}
	}
	snapshot := p.environment.runtime.QualificationSnapshot()
	observation.Epoch = m1.EpochObservation{Inspectable: true, Current: string(snapshot.RuntimeEpochID), Predecessor: string(snapshot.PredecessorEpoch), EveryCurrentUses: true}
	for _, authority := range snapshot.Authorities {
		observation.Authorities = append(observation.Authorities, m1.AuthorityObservation{WorkRevisionID: string(authority.WorkRevisionID), WorkID: string(authority.WorkID), ActorID: string(authority.ActorID), ProcessID: string(authority.ProcessID), LeaseID: string(authority.LeaseID), RuntimeEpochID: string(authority.RuntimeEpochID), Generation: authority.Generation, FenceToken: authority.FenceToken, Current: authority.Status == "active"})
	}
	observation.Authorities = append(observation.Authorities, p.overlay.authorities...)
	census, censusErr := p.environment.repository.RegistrationCensus(ctx)
	if censusErr != nil {
		return m1.Observation{}, censusErr
	}
	workspaces, workspaceErr := workspaceRegistrationObservations(snapshot.Workspaces, p.environment.repository.Binding().PrimaryPath, census)
	if workspaceErr != nil {
		return m1.Observation{}, workspaceErr
	}
	observation.Workspaces = append(observation.Workspaces, workspaces...)
	if p.overlay.playback != nil {
		observation.Playback = *p.overlay.playback
	} else {
		played, playbackErr := journalplayback.RunV1(journalplayback.InputV1{Journal: journal, Reducers: p.environment.reducers})
		if playbackErr != nil {
			return m1.Observation{}, playbackErr
		}
		observation.Playback = playbackObservation(restored, played)
	}
	return observation, nil
}

type workspaceRegistrationLookup interface {
	Registered(string) (bool, error)
}

func workspaceRegistrationObservations(bindings []provingground.WorkspaceBinding, primaryPath string, census workspaceRegistrationLookup) ([]m1.WorkspaceObservation, error) {
	if census == nil {
		return nil, fmt.Errorf("workspace registration census is unavailable")
	}
	result := make([]m1.WorkspaceObservation, 0, len(bindings))
	for _, binding := range bindings {
		registered, err := census.Registered(binding.CanonicalPath)
		if err != nil {
			return nil, err
		}
		result = append(result, m1.WorkspaceObservation{WorkspaceID: string(binding.WorkspaceID), WorkRevisionID: string(binding.WorkRevisionID), WorkID: string(binding.WorkID), LeaseID: string(binding.LeaseID), RuntimeEpochID: string(binding.RuntimeEpochID), Generation: binding.Generation, CanonicalPath: binding.CanonicalPath, OwnedBranch: binding.OwnedBranch, Status: binding.Status, PrimaryPath: primaryPath, Registered: registered})
	}
	return result, nil
}

type storeRevisionInspector interface {
	Inspect(context.Context) (storegapdb.Inspection, error)
}

func requireExactStoreRevision(ctx context.Context, inspector storeRevisionInspector, expected uint64) error {
	if inspector == nil || expected == 0 {
		return fmt.Errorf("same-attempt restore overlay lacks a nonzero observed revision")
	}
	inspection, err := inspector.Inspect(ctx)
	if err != nil {
		return err
	}
	if !inspection.Healthy || inspection.CurrentRevision == 0 || inspection.CurrentRevision != expected || inspection.DurableThrough < inspection.CurrentRevision {
		return fmt.Errorf("same-attempt restore overlay revision is no longer current and durable")
	}
	return nil
}

func scanAllRecords(ctx context.Context, port storegapdb.Port, prefix string) ([]storegapdb.Record, error) {
	var records []storegapdb.Record
	cursor := ""
	for {
		page, err := port.Scan(ctx, storegapdb.ScanRequest{Prefix: prefix, Limit: storegapdb.MaxScanRecords, MaxBytes: storegapdb.MaxScanBytes, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		records = append(records, page.Records...)
		if !page.Truncated {
			return records, nil
		}
		if page.Cursor == "" || page.Cursor == cursor {
			return nil, fmt.Errorf("scan %q truncated without progress", prefix)
		}
		cursor = page.Cursor
	}
}

func (p *composedObservationPort) ProbeStaleAuthority(_ context.Context) (m1.StaleProbe, error) {
	before := p.environment.runtime.QualificationSnapshot()
	stale := p.environment.baseline.Authority
	epochStale := false
	if before.PredecessorEpoch != "" && p.environment.predecessor != nil {
		stale = *p.environment.predecessor
		epochStale = stale.RuntimeEpochID == before.PredecessorEpoch && stale.RuntimeEpochID != before.RuntimeEpochID
	} else {
		stale.LeaseID = "lease-stale-qualification"
	}
	err := p.environment.runtime.Authorize(stale, "qualification-stale-probe", time.Now().UTC())
	after := p.environment.runtime.QualificationSnapshot()
	want := provingground.OutcomeStaleLease
	if epochStale {
		want = provingground.OutcomeStaleEpoch
	}
	if !provingground.IsOutcome(err, want) {
		return m1.StaleProbe{}, fmt.Errorf("composed stale capsule was not refused: %w", err)
	}
	authorityChanges, workspaceChanges := uint64(0), uint64(0)
	if len(after.Authorities) != len(before.Authorities) {
		authorityChanges = 1
	}
	if len(after.Workspaces) != len(before.Workspaces) {
		workspaceChanges = 1
	}
	return m1.StaleProbe{Attempted: true, Refused: true, LeaseStale: !epochStale, EpochStale: epochStale, AuthorityChanges: authorityChanges, WorkspaceChanges: workspaceChanges}, nil
}

func (p *ProductionExecutor) ExecuteFault(ctx context.Context, attempt Attempt) (Execution, error) {
	if p.Root == "" || p.Lifecycle == nil {
		return Execution{}, &IncompleteError{Reason: "production source or admitted sidecar is unavailable"}
	}
	source, err := m1.ObserveSource(ctx, p.Root, p.Lifecycle)
	if err != nil {
		return Execution{}, &IncompleteError{Reason: bounded(err.Error())}
	}
	environment, err := p.productionEnvironment(ctx, attempt.RunID)
	if err != nil {
		return Execution{Source: source}, &IncompleteError{Reason: bounded(err.Error())}
	}
	if err := ensureBaseline(ctx, environment); err != nil {
		return Execution{Source: source}, &IncompleteError{Reason: bounded(err.Error())}
	}
	primaryBefore, err := environment.repository.SnapshotPrimary(ctx)
	if err != nil {
		return Execution{Source: source}, &IncompleteError{Reason: bounded(err.Error())}
	}
	var facts Facts
	var effects []m1.EffectObservation
	var guardPackage, guardTest string
	var output []byte
	switch attempt.Definition.ID {
	case "GJ-01":
		guardPackage, guardTest = "internal/controljournal/application", "Service.Apply/pre-publication"
		facts, output, err = executeComposedGJ01(ctx, attempt, environment)
	case "GJ-02":
		guardPackage, guardTest = "internal/controljournal/application", "Service.Apply/live-response-drop-proxy"
		facts, output, err = executeComposedGJ02(ctx, attempt, p.Lifecycle, source, environment)
	case "GJ-03", "GJ-04":
		guardPackage, guardTest = "internal/provingground", "Runtime.StartWorkspace/"+attempt.Definition.ID
		facts, output, err = executeComposedWorkspaceFault(ctx, attempt, environment)
	case "GJ-05":
		guardPackage, guardTest = "internal/provingground", "EffectCoordinator.PublishAssignment/GJ-05"
		facts, effects, output, err = executeComposedGJ05(ctx, attempt, environment)
	default:
		err = fmt.Errorf("unknown production fault %q", attempt.Definition.ID)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Execution{Source: source}, &IncompleteError{Reason: bounded(err.Error())}
		}
		return Execution{Source: source}, err
	}
	primaryAfter, err := environment.repository.SnapshotPrimary(ctx)
	if err != nil {
		return Execution{Source: source, Facts: facts}, &IncompleteError{Reason: bounded(err.Error())}
	}
	observation, err := observeComposedRuntimeWithOverlay(ctx, environment, effects, qualificationOverlay{primary: m1.PrimaryObservation{Inspectable: true, Before: gitSnapshotObservation(primaryBefore), After: gitSnapshotObservation(primaryAfter)}})
	if err != nil {
		return Execution{Source: source, Facts: facts}, &IncompleteError{Reason: bounded(err.Error())}
	}
	outputDigest := sha256.Sum256(output)
	artifacts := []Artifact{{Kind: "production-guard-receipt", SHA256: hex.EncodeToString(outputDigest[:]), SizeBytes: int64(len(output))}}
	if len(output) == 0 {
		artifacts[0].SizeBytes = 1 // the digest still binds the empty structured receipt
	}
	invariants := m1.AssertUniversal(ctx, observation)
	receipt := ProductionReceipt{
		RunID: attempt.RunID, FaultID: attempt.Definition.ID, AttemptID: productionAttemptID(attempt), GuardPackage: guardPackage, GuardTest: guardTest,
		GuardOutputSHA256: hex.EncodeToString(outputDigest[:]), FactSHA256: factsDigest(facts), ArtifactSHA256: artifactsDigest(artifacts), InvariantSHA256: invariantDigest(invariants), Source: source,
	}
	receipt.seal = receiptSeal(receipt)
	return Execution{Facts: facts, Observation: observation, Artifacts: artifacts, Source: source, Receipt: receipt}, nil
}

type mappedHook struct {
	boundary string
	faultID  string
	delegate productfaults.Hook
}

func (h mappedHook) Reach(ctx context.Context, boundary string) error {
	if boundary != h.boundary {
		return nil
	}
	return h.delegate.Reach(ctx, h.faultID)
}

func initGitRepository(ctx context.Context, root string) error {
	commands := [][]string{{"init", "-b", "main", root}, {"-C", root, "config", "user.email", "qualification@example.invalid"}, {"-C", root, "config", "user.name", "Qualification"}, {"-C", root, "commit", "--allow-empty", "-m", "base"}}
	for _, args := range commands {
		command := exec.CommandContext(ctx, "git", args...)
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), bounded(string(output)), err)
		}
	}
	return nil
}

func productionAttemptID(attempt Attempt) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", attempt.RunID, attempt.Definition.ID, attempt.Seed)))
	return hex.EncodeToString(digest[:16])
}

// IncompleteError marks an environmental inability to prove the cut. It is
// retained as terminal incomplete evidence and can never become pass evidence.
type IncompleteError struct{ Reason string }

func (e *IncompleteError) Error() string { return "incomplete fault attempt: " + e.Reason }

type Record struct {
	FaultID    string                 `json:"fault_id"`
	RunID      string                 `json:"run_id"`
	Seed       uint64                 `json:"seed"`
	Cut        string                 `json:"cut"`
	Started    bool                   `json:"started"`
	Terminal   bool                   `json:"terminal"`
	Outcome    Outcome                `json:"outcome"`
	Source     SourceBinding          `json:"source"`
	Receipt    ProductionReceipt      `json:"production_receipt"`
	Facts      Facts                  `json:"product_facts"`
	Invariants m1.InvariantSummary    `json:"invariants"`
	Artifacts  []Artifact             `json:"artifacts"`
	Effects    []m1.EffectObservation `json:"effect_observations,omitempty"`
	Diagnostic string                 `json:"diagnostic,omitempty"`
}

type Runner struct {
	RunID    string
	Seed     uint64
	Executor Executor
}

func (r Runner) Run(ctx context.Context, faultID string) (record Record) {
	record = Record{FaultID: faultID, RunID: r.RunID, Seed: r.Seed, Started: true, Terminal: true, Outcome: OutcomeFail, Invariants: m1.AssertUniversal(ctx, nil)}
	definition, err := definitionByID(faultID)
	if err != nil {
		record.Diagnostic = bounded(err.Error())
		return record
	}
	record.Cut = definition.Cut
	if r.Executor == nil || r.RunID == "" {
		record.Diagnostic = "run ID and fault executor are required"
		return record
	}
	ack := &acknowledgement{expected: definition.ID}
	var execution Execution
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("fault executor panicked: %v", recovered)
			}
		}()
		execution, err = r.Executor.ExecuteFault(ctx, Attempt{RunID: r.RunID, Seed: r.Seed, Definition: definition, Hook: ack})
	}()
	record.Facts, record.Artifacts, record.Source, record.Receipt = execution.Facts, boundedArtifacts(execution.Artifacts), execution.Source, execution.Receipt
	if execution.Observation != nil {
		if observed, observeErr := execution.Observation.ObserveM1(ctx); observeErr == nil {
			record.Effects = append([]m1.EffectObservation(nil), observed.Effects...)
		}
	}
	record.Invariants = m1.AssertUniversal(ctx, execution.Observation)
	var incomplete *IncompleteError
	if errors.As(err, &incomplete) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		record.Outcome = OutcomeIncomplete
		record.Diagnostic = bounded(err.Error())
		return record
	}
	if err != nil {
		record.Diagnostic = bounded(err.Error())
		return record
	}
	if err := validateSource(execution.Source); err != nil {
		record.Diagnostic = bounded(err.Error())
		return record
	}
	if err := validateReceipt(r.RunID, definition.ID, execution, record.Invariants); err != nil {
		record.Diagnostic = bounded(err.Error())
		return record
	}
	if err := ack.validate(); err != nil {
		record.Diagnostic = bounded(err.Error())
		return record
	}
	if err := validateArtifacts(execution.Artifacts); err != nil {
		record.Diagnostic = bounded(err.Error())
		return record
	}
	if err := validateFacts(definition.ID, execution.Facts, execution.Source); err != nil {
		record.Diagnostic = bounded(err.Error())
		return record
	}
	if !record.Invariants.Passed() {
		record.Diagnostic = "one or more universal invariants failed"
		return record
	}
	record.Outcome = OutcomePass
	return record
}

func definitionByID(id string) (Definition, error) {
	for _, definition := range definitions {
		if definition.ID == id {
			return definition, nil
		}
	}
	return Definition{}, fmt.Errorf("unknown fault %q", id)
}

type acknowledgement struct {
	mu       sync.Mutex
	expected string
	reached  []string
	err      error
}

func (a *acknowledgement) Reach(_ context.Context, boundary string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reached = append(a.reached, boundary)
	if boundary != a.expected {
		a.err = fmt.Errorf("wrong fault boundary reached=%s expected=%s", boundary, a.expected)
		return a.err
	}
	if len(a.reached) != 1 {
		a.err = fmt.Errorf("fault boundary %s reached more than once", boundary)
		return a.err
	}
	return productfaults.ErrCrashRequested
}

func (a *acknowledgement) validate() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	if len(a.reached) != 1 || a.reached[0] != a.expected {
		return fmt.Errorf("fault boundary %s was not acknowledged exactly once", a.expected)
	}
	return nil
}

func validateFacts(id string, facts Facts, source SourceBinding) error {
	if facts.OperationRecords < 0 || facts.OperationResults < 0 || facts.DurableWorkspaceCount < 0 || facts.ProviderDispatches < 0 || facts.AttributableOutcomes < 0 || facts.BlindRepeats < 0 || facts.UnsafeCompensations < 0 {
		return fmt.Errorf("negative product fact count")
	}
	switch id {
	case "GJ-01":
		if len(facts.PreexistingBefore) == 0 || len(facts.PreexistingAfter) == 0 || facts.OperationRecords != 0 || facts.OperationResults != 0 || !equalProductFacts(facts.PreexistingBefore, facts.PreexistingAfter) {
			return fmt.Errorf("GJ-01 changed operation-attributable state or a preexisting revision/digest")
		}
	case "GJ-02":
		cut := facts.ProxyCut
		if !facts.DurableAccepted || !facts.DaemonResponseComplete || !facts.ProxyConsumedCompleteResponse || facts.OperationRecords != 1 || facts.OperationResults != 1 || cut == nil || cut.FaultID != "GJ-02" || cut.Cut != "after_durable_acceptance_before_client_response" || cut.RequestID == "" || cut.RequestID != facts.HeartbeatOperationID || facts.HeartbeatWorkID == "" || !authorityHeadCensusIsConfigured(facts.AuthorityHeadIDs, facts.HeartbeatWorkID) || cut.DatabaseID == "" || cut.DatabaseID != source.DatabaseID || cut.Revision == 0 || cut.MutationCount < 1 || cut.DaemonSocket == "" || cut.ProductSocket == "" || cut.DaemonPID != source.OwnerPID || cut.ProxyPID <= 0 || cut.SourceCommit != source.Commit || !hexValue(cut.ProxySHA256, 64) || facts.SidecarProvenanceSHA256 != source.SidecarProvenanceSHA256 || cut.ObservedAt.IsZero() {
			return fmt.Errorf("GJ-02 lacks one durable result and a proxy-consumed complete daemon response")
		}
	case "GJ-03":
		if !facts.PhysicalWorkspaceCreated || facts.PhysicalWorkspaceID == "" || facts.PhysicalHasAuthority || facts.DurableBinding || facts.CapsuleDelivered || facts.RecoveredWorkspaceID == "" || facts.RecoveredWorkspaceID == facts.PhysicalWorkspaceID || facts.DurableWorkspaceCount != 1 {
			return fmt.Errorf("GJ-03 does not distinguish the authority-free orphan from a fresh recovered workspace")
		}
	case "GJ-04":
		if !facts.PhysicalWorkspaceCreated || facts.PhysicalWorkspaceID == "" || !facts.DurableBinding || facts.CapsuleDelivered || !facts.CapsuleReconstructed || !facts.CapsuleBindingMatches || facts.RecoveredWorkspaceID != facts.PhysicalWorkspaceID || facts.DurableWorkspaceCount != 1 {
			return fmt.Errorf("GJ-04 did not reconstruct the one durable binding and capsule")
		}
	case "GJ-05":
		if !facts.EffectIntentDurable || !facts.ProviderObservationDurable || facts.ProviderDispatches != 1 || facts.AttributableOutcomes != 1 || facts.BlindRepeats != 0 || facts.UnsafeCompensations != 0 {
			return fmt.Errorf("GJ-05 lacks one intent-bound provider observation without blind repeat or unsafe compensation")
		}
	default:
		return fmt.Errorf("unknown fault %q", id)
	}
	return nil
}

func validateSource(source SourceBinding) error {
	if !source.Valid() {
		return fmt.Errorf("source commit/tree, admitted tuple, and sidecar provenance are incomplete or unadmitted")
	}
	return nil
}

func validateReceipt(runID, faultID string, execution Execution, invariants m1.InvariantSummary) error {
	receipt := execution.Receipt
	if receipt.RunID != runID || receipt.FaultID != faultID || receipt.AttemptID == "" || receipt.GuardPackage == "" || receipt.GuardTest == "" ||
		!hexValue(receipt.GuardOutputSHA256, 64) || !hexValue(receipt.FactSHA256, 64) || !hexValue(receipt.ArtifactSHA256, 64) || !hexValue(receipt.InvariantSHA256, 64) || !receipt.Source.Valid() || receipt.Source != execution.Source ||
		receipt.FactSHA256 != factsDigest(execution.Facts) || receipt.ArtifactSHA256 != artifactsDigest(execution.Artifacts) || receipt.InvariantSHA256 != invariantDigest(invariants) || receipt.seal != receiptSeal(receipt) {
		return fmt.Errorf("fault execution lacks an exact production/source/fact receipt")
	}
	return nil
}

func factsDigest(facts Facts) string {
	encoded, _ := json.Marshal(facts)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func artifactsDigest(artifacts []Artifact) string {
	encoded, _ := json.Marshal(artifacts)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func invariantDigest(summary m1.InvariantSummary) string {
	encoded, _ := json.Marshal(summary)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func receiptSeal(receipt ProductionReceipt) [32]byte {
	return sha256.Sum256([]byte(strings.Join([]string{receipt.RunID, receipt.FaultID, receipt.AttemptID, receipt.GuardPackage, receipt.GuardTest, receipt.GuardOutputSHA256, receipt.FactSHA256, receipt.ArtifactSHA256, receipt.InvariantSHA256, receipt.Source.Commit, receipt.Source.Tree, receipt.Source.SidecarProvenanceSHA256}, "\x00")))
}

func hexValue(value string, size int) bool {
	if len(value) != size {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func equalProductFacts(left, right map[string]ProductFact) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if value.Digest == "" || value.Revision == 0 || right[key] != value {
			return false
		}
	}
	return true
}

func equalProductFactsAllowEmpty(left, right map[string]ProductFact) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if value.Digest == "" || value.Revision == 0 || right[key] != value {
			return false
		}
	}
	return true
}

func validateArtifacts(artifacts []Artifact) error {
	if len(artifacts) == 0 || len(artifacts) > 32 {
		return fmt.Errorf("fault artifacts must contain 1..32 bounded entries")
	}
	var total int64
	for _, artifact := range artifacts {
		if artifact.Kind == "" || len(artifact.SHA256) != 64 || artifact.SizeBytes < 0 || artifact.SizeBytes > 1<<20 {
			return fmt.Errorf("fault artifact is incomplete or oversized")
		}
		total += artifact.SizeBytes
	}
	if total > 4<<20 {
		return fmt.Errorf("fault artifacts exceed 4 MiB")
	}
	return nil
}

func boundedArtifacts(input []Artifact) []Artifact {
	if len(input) > 32 {
		input = input[:32]
	}
	return append([]Artifact(nil), input...)
}

func bounded(value string) string {
	if len(value) > 512 {
		return value[:512]
	}
	return value
}
