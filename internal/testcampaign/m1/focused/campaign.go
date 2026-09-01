package focused

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"

	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/provingground"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	m1 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1"
	faultcampaign "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1/faults"
)

const MinimumRepetitions = 100

type Definition struct {
	ID                string
	Requirement       string
	RequiresExternal  bool
	ProviderAdversary bool
}

var definitions = []Definition{
	{ID: "atomic-publication-nfr001", Requirement: "NFR-001", RequiresExternal: true},
	{ID: "lost-response-idempotency-nfr002", Requirement: "NFR-002", RequiresExternal: true},
	{ID: "concurrent-claims-nfr003", Requirement: "NFR-003", RequiresExternal: true},
	{ID: "competing-integrations-nfr004", Requirement: "NFR-004", RequiresExternal: true},
	{ID: "restore-playback-nfr005", Requirement: "NFR-005", RequiresExternal: true},
	{ID: "restart-epoch-nfr007", Requirement: "NFR-007", RequiresExternal: true},
	{ID: "zombie-isolation-nfr008", Requirement: "NFR-008", RequiresExternal: true},
	{ID: "attribution-reassignment-nfr015", Requirement: "NFR-015", RequiresExternal: true, ProviderAdversary: true},
}

func Definitions() []Definition { return append([]Definition(nil), definitions...) }

type Outcome string

const (
	OutcomePass       Outcome = "pass"
	OutcomeFail       Outcome = "fail"
	OutcomeIncomplete Outcome = "incomplete"
)

type ProviderState string

const (
	ProviderNormal        ProviderState = "normal"
	ProviderSlow          ProviderState = "slow"
	ProviderUnavailable   ProviderState = "unavailable"
	ProviderWrong         ProviderState = "wrong"
	ProviderStale         ProviderState = "stale"
	ProviderHumanModified ProviderState = "human_modified"
)

type ExternalObservation struct {
	DatabaseID      string
	EndpointID      string
	DaemonSHA256    string
	OwnerPID        int
	CurrentRevision uint64
	DurableThrough  uint64
	seal            [32]byte
}

// ObserveExternal crosses the admitted public adapter and binds its inspection
// to lifecycle provenance. Callers cannot make external evidence from labels.
func ObserveExternal(ctx context.Context, port storegapdb.Port, provenance sidecargapdb.SidecarProvenance) (ExternalObservation, error) {
	if port == nil {
		return ExternalObservation{}, fmt.Errorf("external GapDB port is absent")
	}
	inspection, err := port.Inspect(ctx)
	if err != nil {
		return ExternalObservation{}, err
	}
	return observeExternalInspection(inspection, provenance)
}

func observeExternalInspection(inspection storegapdb.Inspection, provenance sidecargapdb.SidecarProvenance) (ExternalObservation, error) {
	if !inspection.Healthy || inspection.DatabaseID == "" || inspection.EndpointID == "" || inspection.OwnerPID <= 0 || inspection.DurableThrough < inspection.CurrentRevision ||
		!provenance.Readiness.Admitted || !provenance.Readiness.Healthy || !provenance.Readiness.Configured || provenance.DatabaseID != inspection.DatabaseID || provenance.Process.PID != inspection.OwnerPID ||
		provenance.ExecutedImage.SHA256 != storegapdb.AdmittedSidecarSHA256 || provenance.Executable.SHA256 != provenance.ExecutedImage.SHA256 || provenance.ConfiguredSocket == "" {
		return ExternalObservation{}, fmt.Errorf("public inspection and admitted sidecar provenance disagree")
	}
	value := ExternalObservation{DatabaseID: inspection.DatabaseID, EndpointID: inspection.EndpointID, DaemonSHA256: provenance.ExecutedImage.SHA256, OwnerPID: inspection.OwnerPID, CurrentRevision: inspection.CurrentRevision, DurableThrough: inspection.DurableThrough}
	value.seal = externalSeal(value)
	return value, nil
}

func (e ExternalObservation) valid() bool {
	return e.DatabaseID != "" && e.EndpointID != "" && e.DaemonSHA256 == storegapdb.AdmittedSidecarSHA256 && e.OwnerPID > 0 && e.DurableThrough >= e.CurrentRevision && e.seal == externalSeal(e)
}

func externalSeal(e ExternalObservation) [32]byte {
	return sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d\x00%d", e.DatabaseID, e.EndpointID, e.DaemonSHA256, e.OwnerPID, e.CurrentRevision, e.DurableThrough)))
}

type ProviderTrace struct {
	State                ProviderState
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

// Schedule is deterministic evidence, not a timer. Scenario implementations
// use Order and Barrier to vary concurrent interleavings without sleeps.
type Schedule struct {
	Seed    uint64
	Order   [2]int
	Barrier *Barrier
}

type Attempt struct {
	RunID      string
	CampaignID string
	Index      int
	Schedule   Schedule
}

type Execution struct {
	Started       bool
	Terminal      bool
	Outcome       Outcome
	Observation   m1.ObservationPort
	External      ExternalObservation
	ProviderState ProviderState
	ProviderTrace ProviderTrace
	Artifact      []byte
	Diagnostic    string
	Source        m1.SourceBinding
	Receipt       ProductionReceipt
}

type ProductionReceipt struct {
	RunID, CampaignID, AttemptID string
	Index                        int
	Seed                         uint64
	Order                        [2]int
	FactSHA256                   string
	InvariantSHA256              string
	ExternalSHA256               string
	Source                       m1.SourceBinding
	seal                         [32]byte
}

type Scenario interface {
	RunFocused(context.Context, Attempt) (Execution, error)
}

type ScenarioFunc func(context.Context, Attempt) (Execution, error)

func (f ScenarioFunc) RunFocused(ctx context.Context, attempt Attempt) (Execution, error) {
	return f(ctx, attempt)
}

// ProductionScenario is the only focused scenario capable of minting a pass
// receipt. Its executor returns same-attempt public production observations;
// this type merely binds them to the runner-owned schedule and census.
type ProductionScenario struct {
	Executor *faultcampaign.ProductionExecutor
}

func (p *ProductionScenario) RunFocused(ctx context.Context, attempt Attempt) (Execution, error) {
	if p == nil || p.Executor == nil || attempt.Schedule.Barrier == nil {
		return Execution{}, fmt.Errorf("focused production scenario is incomplete")
	}
	attemptID := productionAttemptID(attempt)
	production, err := p.Executor.ExecuteFocused(ctx, faultcampaign.FocusedInput{RunID: attempt.RunID, CampaignID: attempt.CampaignID, AttemptID: attemptID, Index: attempt.Index, Seed: attempt.Schedule.Seed, Order: attempt.Schedule.Order, Barrier: attempt.Schedule.Barrier})
	if err != nil {
		return Execution{Started: true, Terminal: true, Outcome: OutcomeFail, Observation: production.Observation, Artifact: production.Artifact, Diagnostic: err.Error(), Source: production.Source}, err
	}
	if production.ExternalInspection.DatabaseID == "" || production.ExternalProvenance.SupervisorRunID == "" {
		err := fmt.Errorf("focused production execution omitted its same-attempt external evidence")
		return Execution{Started: true, Terminal: true, Outcome: OutcomeIncomplete, Observation: production.Observation, Artifact: production.Artifact, Source: production.Source}, err
	}
	external, err := observeExternalInspection(production.ExternalInspection, production.ExternalProvenance)
	if err != nil {
		return Execution{Started: true, Terminal: true, Outcome: OutcomeIncomplete, Observation: production.Observation, Artifact: production.Artifact, Source: production.Source}, err
	}
	execution := Execution{Started: true, Terminal: true, Outcome: OutcomePass, Observation: production.Observation, External: external, Artifact: append([]byte(nil), production.Artifact...), Source: production.Source}
	if attempt.CampaignID == "attribution-reassignment-nfr015" {
		execution.ProviderState = ProviderState(production.Provider.State)
		execution.ProviderTrace = ProviderTrace{State: execution.ProviderState, MutationAttempts: production.Provider.MutationAttempts, ObservationAttempts: production.Provider.ObservationAttempts, RevisionBefore: production.Provider.RevisionBefore, RevisionAfter: production.Provider.RevisionAfter, IdentityMatched: production.Provider.IdentityMatched, IdentityInspected: production.Provider.IdentityInspected, CancellationObserved: production.Provider.CancellationObserved, TypedUnavailable: production.Provider.TypedUnavailable, AppliedRecorded: production.Provider.AppliedRecorded, BlindRepeats: production.Provider.BlindRepeats, CompensationApplied: production.Provider.CompensationApplied, HumanValuePreserved: production.Provider.HumanValuePreserved}
	} else {
		execution.ProviderState = ProviderNormal
	}
	invariants := m1.AssertUniversal(ctx, execution.Observation)
	receipt := ProductionReceipt{RunID: attempt.RunID, CampaignID: attempt.CampaignID, AttemptID: attemptID, Index: attempt.Index, Seed: attempt.Schedule.Seed, Order: attempt.Schedule.Order, FactSHA256: bytesDigest(execution.Artifact), InvariantSHA256: invariantDigest(invariants), ExternalSHA256: externalDigest(external), Source: execution.Source}
	receipt.seal = focusedReceiptSeal(receipt)
	execution.Receipt = receipt
	return execution, nil
}

type AttemptResult struct {
	Index         int
	Seed          uint64
	Started       bool
	Terminal      bool
	Outcome       Outcome
	Invariants    m1.InvariantSummary
	ProviderState ProviderState
	Diagnostic    string
	AttemptID     string
}

type Summary struct {
	ID                    string                `json:"id"`
	RunID                 string                `json:"run_id"`
	Seed                  uint64                `json:"seed"`
	RepetitionsStarted    int                   `json:"repetitions_started"`
	RepetitionsTerminal   int                   `json:"repetitions_terminal"`
	RepetitionsPassed     int                   `json:"repetitions_passed"`
	RepetitionsFailed     int                   `json:"repetitions_failed"`
	RepetitionsIncomplete int                   `json:"repetitions_incomplete"`
	FirstFailure          []byte                `json:"first_failure_artifact,omitempty"`
	FirstDiagnostic       string                `json:"first_diagnostic,omitempty"`
	ProviderStates        map[ProviderState]int `json:"provider_states"`
	Attempts              []AttemptResult       `json:"-"`
	firstFailureCaptured  bool
}

func (s Summary) Admissible() bool {
	return s.RepetitionsStarted >= MinimumRepetitions &&
		s.RepetitionsStarted == s.RepetitionsTerminal &&
		s.RepetitionsTerminal == s.RepetitionsPassed &&
		s.RepetitionsFailed == 0 && s.RepetitionsIncomplete == 0
}

type Runner struct {
	RunID       string
	Seed        uint64
	Repetitions int
	Scenario    Scenario
}

func (r Runner) Run(ctx context.Context, campaignID string) Summary {
	summary := Summary{ID: campaignID, RunID: r.RunID, Seed: r.Seed, ProviderStates: make(map[ProviderState]int)}
	definition, err := definitionByID(campaignID)
	if err != nil || r.RunID == "" || r.Scenario == nil || r.Repetitions < MinimumRepetitions {
		summary.FirstDiagnostic = bounded(firstNonNil(err, fmt.Errorf("run ID, scenario, and at least %d repetitions are required", MinimumRepetitions)).Error())
		return summary
	}
	summary.Attempts = make([]AttemptResult, 0, r.Repetitions)
	seenAttemptIDs := make(map[string]struct{}, r.Repetitions)
	runErr := provingground.RunFocusedCampaign(ctx, r.Repetitions, func(trialCtx context.Context, index int) error {
		seed := derivedSeed(r.Seed, uint64(index))
		random := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
		order := [2]int{0, 1}
		if random.Uint64()&1 == 1 {
			order = [2]int{1, 0}
		}
		attempt := Attempt{RunID: r.RunID, CampaignID: campaignID, Index: index, Schedule: Schedule{Seed: seed, Order: order, Barrier: NewBarrier(2)}}
		execution, executeErr := executeAttempt(trialCtx, r.Scenario, attempt)
		operatorCanceled := errors.Is(executeErr, context.Canceled) || errors.Is(executeErr, context.DeadlineExceeded) ||
			errors.Is(context.Cause(trialCtx), context.Canceled) || errors.Is(context.Cause(trialCtx), context.DeadlineExceeded)
		// The runner owns the attempt envelope. Once RunFocused is invoked, even a
		// panic is a started attempt and recovery emits one terminal record.
		result := AttemptResult{Index: index, Seed: seed, Started: true, Terminal: true, Outcome: execution.Outcome, ProviderState: execution.ProviderState}
		result.Invariants = m1.AssertUniversal(trialCtx, execution.Observation)
		if executeErr != nil {
			if operatorCanceled {
				result.Outcome = OutcomeIncomplete
			} else {
				result.Outcome = OutcomeFail
			}
			result.Diagnostic = bounded(executeErr.Error())
		} else if operatorCanceled {
			result.Outcome = OutcomeIncomplete
			result.Diagnostic = bounded(context.Cause(trialCtx).Error())
		}
		if !execution.Started || !execution.Terminal {
			result.Outcome = OutcomeIncomplete
			if result.Diagnostic == "" {
				result.Diagnostic = bounded("attempt did not publish started and terminal evidence")
			}
		}
		if definition.RequiresExternal && !execution.External.valid() {
			result.Outcome = OutcomeIncomplete
			if result.Diagnostic == "" {
				result.Diagnostic = bounded("admitted external sidecar/public-client provenance was not observed")
			}
		}
		if result.Outcome == OutcomePass {
			if receiptErr := validateProductionReceipt(attempt, execution, result.Invariants); receiptErr != nil {
				result.Outcome = OutcomeFail
				result.Diagnostic = bounded(receiptErr.Error())
			} else {
				result.AttemptID = execution.Receipt.AttemptID
				if _, duplicate := seenAttemptIDs[result.AttemptID]; duplicate {
					result.Outcome = OutcomeFail
					result.Diagnostic = bounded("focused production receipt reused an AttemptID")
				} else {
					seenAttemptIDs[result.AttemptID] = struct{}{}
				}
			}
		}
		if !result.Invariants.Passed() && !operatorCanceled {
			result.Outcome = OutcomeFail
			if result.Diagnostic == "" {
				result.Diagnostic = bounded("universal invariant failed")
			}
		}
		if definition.ProviderAdversary {
			if traceErr := validateProviderTrace(execution.ProviderState, execution.ProviderTrace); traceErr != nil {
				if !operatorCanceled {
					result.Outcome = OutcomeFail
				}
				if result.Diagnostic == "" {
					result.Diagnostic = bounded(traceErr.Error())
				}
			}
		}
		if result.Outcome != OutcomePass && result.Diagnostic == "" {
			result.Diagnostic = bounded(execution.Diagnostic)
			if result.Diagnostic == "" {
				result.Diagnostic = "attempt reported a non-pass outcome"
			}
		}
		summary.record(result, execution.Artifact)
		return nil // retain all terminal evidence instead of aborting the census
	})
	if runErr != nil && summary.FirstDiagnostic == "" {
		summary.FirstDiagnostic = bounded(runErr.Error())
	}
	if definition.ProviderAdversary {
		for _, state := range []ProviderState{ProviderSlow, ProviderUnavailable, ProviderWrong, ProviderStale, ProviderHumanModified} {
			if summary.ProviderStates[state] == 0 {
				summary.RepetitionsPassed = 0
				if summary.FirstDiagnostic == "" {
					summary.FirstDiagnostic = "provider adversary census is incomplete: " + string(state)
				}
			}
		}
	}
	return summary
}

func executeAttempt(ctx context.Context, scenario Scenario, attempt Attempt) (execution Execution, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("focused scenario panicked: %v", recovered)
		}
	}()
	return scenario.RunFocused(ctx, attempt)
}

func validateProviderTrace(state ProviderState, trace ProviderTrace) error {
	if trace.State != state || trace.MutationAttempts < 0 || trace.ObservationAttempts < 1 || trace.BlindRepeats != 0 {
		return fmt.Errorf("provider trace is missing, mismatched, or contains blind repeats")
	}
	switch state {
	case ProviderSlow:
		if trace.MutationAttempts != 1 || !trace.CancellationObserved || trace.AppliedRecorded {
			return fmt.Errorf("slow provider was not cancellation-bounded")
		}
	case ProviderUnavailable:
		if trace.MutationAttempts > 1 || !trace.TypedUnavailable || trace.AppliedRecorded {
			return fmt.Errorf("unavailable provider fabricated application or retried")
		}
	case ProviderWrong:
		if !trace.IdentityInspected || trace.IdentityMatched || trace.AppliedRecorded {
			return fmt.Errorf("wrong provider identity was accepted")
		}
	case ProviderStale:
		if trace.RevisionBefore == "" || trace.RevisionAfter != trace.RevisionBefore || trace.AppliedRecorded {
			return fmt.Errorf("stale provider revision fabricated application")
		}
	case ProviderHumanModified:
		if trace.RevisionBefore == "" || trace.RevisionAfter == trace.RevisionBefore || trace.CompensationApplied || !trace.HumanValuePreserved {
			return fmt.Errorf("human reassignment was not attribution-safely preserved")
		}
	default:
		return fmt.Errorf("provider adversary state %q is not qualified", state)
	}
	return nil
}

func (s *Summary) record(result AttemptResult, artifact []byte) {
	s.Attempts = append(s.Attempts, result)
	if result.Started {
		s.RepetitionsStarted++
	}
	if result.Terminal {
		s.RepetitionsTerminal++
	}
	s.ProviderStates[result.ProviderState]++
	switch result.Outcome {
	case OutcomePass:
		s.RepetitionsPassed++
	case OutcomeIncomplete:
		s.RepetitionsIncomplete++
	default:
		s.RepetitionsFailed++
	}
	if result.Outcome != OutcomePass && !s.firstFailureCaptured {
		s.firstFailureCaptured = true
		if len(artifact) > 64<<10 {
			artifact = artifact[:64<<10]
		}
		s.FirstFailure = append([]byte(nil), artifact...)
		s.FirstDiagnostic = bounded(result.Diagnostic)
	}
}

func validateProductionReceipt(attempt Attempt, execution Execution, invariants m1.InvariantSummary) error {
	receipt := execution.Receipt
	if receipt.RunID != attempt.RunID || receipt.CampaignID != attempt.CampaignID || receipt.Index != attempt.Index || receipt.Seed != attempt.Schedule.Seed || receipt.Order != attempt.Schedule.Order || receipt.AttemptID == "" || !execution.Source.Valid() || receipt.Source != execution.Source ||
		receipt.FactSHA256 != bytesDigest(execution.Artifact) || receipt.InvariantSHA256 != invariantDigest(invariants) || receipt.ExternalSHA256 != externalDigest(execution.External) || receipt.seal != focusedReceiptSeal(receipt) {
		return fmt.Errorf("focused attempt lacks exact production/run/schedule/fact receipt")
	}
	return nil
}

func bytesDigest(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func productionAttemptID(attempt Attempt) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%d\x00%d", attempt.RunID, attempt.CampaignID, attempt.Index, attempt.Schedule.Seed, attempt.Schedule.Order[0], attempt.Schedule.Order[1])))
	return hex.EncodeToString(sum[:16])
}
func invariantDigest(value m1.InvariantSummary) string {
	encoded, _ := json.Marshal(value)
	return bytesDigest(encoded)
}
func externalDigest(value ExternalObservation) string {
	encoded, _ := json.Marshal(value)
	return bytesDigest(encoded)
}
func focusedReceiptSeal(r ProductionReceipt) [32]byte {
	return sha256.Sum256([]byte(strings.Join([]string{r.RunID, r.CampaignID, r.AttemptID, fmt.Sprint(r.Index), fmt.Sprint(r.Seed), fmt.Sprint(r.Order), r.FactSHA256, r.InvariantSHA256, r.ExternalSHA256, r.Source.Commit, r.Source.SidecarProvenanceSHA256}, "\x00")))
}

func definitionByID(id string) (Definition, error) {
	for _, definition := range definitions {
		if definition.ID == id {
			return definition, nil
		}
	}
	return Definition{}, fmt.Errorf("unknown focused campaign %q", id)
}

func derivedSeed(seed, index uint64) uint64 {
	value := seed + 0x9e3779b97f4a7c15 + index
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func firstNonNil(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return errors.New("invalid focused campaign")
}

func bounded(value string) string {
	if len(value) > 512 {
		return value[:512]
	}
	return value
}

// Barrier is an acknowledgement-driven rendezvous. It has no clock and cannot
// turn scheduler delay into a fault signal.
type Barrier struct {
	participants int
	mu           sync.Mutex
	arrived      int
	released     chan struct{}
}

func NewBarrier(participants int) *Barrier {
	if participants < 1 {
		participants = 1
	}
	return &Barrier{participants: participants, released: make(chan struct{})}
}

func (b *Barrier) Arrive(ctx context.Context) error {
	if b == nil {
		return errors.New("barrier is absent")
	}
	b.mu.Lock()
	if b.arrived >= b.participants {
		b.mu.Unlock()
		return errors.New("barrier received more arrivals than participants")
	}
	b.arrived++
	if b.arrived == b.participants {
		close(b.released)
	}
	released := b.released
	b.mu.Unlock()
	select {
	case <-released:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
