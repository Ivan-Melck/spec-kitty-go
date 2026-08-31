package faults

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	productfaults "github.com/spec-kitty/go-kitty3/internal/faults"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	m1 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1"
)

type Outcome string

const (
	OutcomePass       Outcome = "pass"
	OutcomeFail       Outcome = "fail"
	OutcomeIncomplete Outcome = "incomplete"
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

type SourceBinding struct {
	Commit                  string `json:"commit"`
	Tree                    string `json:"tree"`
	AdmittedTupleSHA256     string `json:"admitted_tuple_sha256"`
	SidecarProvenanceSHA256 string `json:"sidecar_provenance_sha256"`
}

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

// IncompleteError marks an environmental inability to prove the cut. It is
// retained as terminal incomplete evidence and can never become pass evidence.
type IncompleteError struct{ Reason string }

func (e *IncompleteError) Error() string { return "incomplete fault attempt: " + e.Reason }

type Record struct {
	FaultID    string              `json:"fault_id"`
	RunID      string              `json:"run_id"`
	Seed       uint64              `json:"seed"`
	Cut        string              `json:"cut"`
	Started    bool                `json:"started"`
	Terminal   bool                `json:"terminal"`
	Outcome    Outcome             `json:"outcome"`
	Source     SourceBinding       `json:"source"`
	Facts      Facts               `json:"product_facts"`
	Invariants m1.InvariantSummary `json:"invariants"`
	Artifacts  []Artifact          `json:"artifacts"`
	Diagnostic string              `json:"diagnostic,omitempty"`
}

type Runner struct {
	RunID    string
	Seed     uint64
	Source   SourceBinding
	Executor Executor
}

func (r Runner) Run(ctx context.Context, faultID string) (record Record) {
	record = Record{FaultID: faultID, RunID: r.RunID, Seed: r.Seed, Started: true, Terminal: true, Outcome: OutcomeFail, Source: r.Source}
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
	if err := validateSource(r.Source); err != nil {
		record.Diagnostic = bounded(err.Error())
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
	record.Facts, record.Artifacts = execution.Facts, boundedArtifacts(execution.Artifacts)
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
	if err := ack.validate(); err != nil {
		record.Diagnostic = bounded(err.Error())
		return record
	}
	if err := validateArtifacts(execution.Artifacts); err != nil {
		record.Diagnostic = bounded(err.Error())
		return record
	}
	if err := validateFacts(definition.ID, execution.Facts); err != nil {
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

func validateFacts(id string, facts Facts) error {
	if facts.OperationRecords < 0 || facts.OperationResults < 0 || facts.DurableWorkspaceCount < 0 || facts.ProviderDispatches < 0 || facts.AttributableOutcomes < 0 || facts.BlindRepeats < 0 || facts.UnsafeCompensations < 0 {
		return fmt.Errorf("negative product fact count")
	}
	switch id {
	case "GJ-01":
		if facts.OperationRecords != 0 || facts.OperationResults != 0 || !equalProductFacts(facts.PreexistingBefore, facts.PreexistingAfter) {
			return fmt.Errorf("GJ-01 changed operation-attributable state or a preexisting revision/digest")
		}
	case "GJ-02":
		cut := facts.ProxyCut
		if !facts.DurableAccepted || !facts.DaemonResponseComplete || !facts.ProxyConsumedCompleteResponse || facts.OperationRecords != 1 || facts.OperationResults != 1 || cut == nil || cut.FaultID != "GJ-02" || cut.Cut != "after_durable_acceptance_before_client_response" || cut.RequestID == "" || cut.DatabaseID == "" || cut.Revision == 0 || cut.MutationCount < 1 || cut.DaemonSocket == "" || cut.ProductSocket == "" || cut.DaemonPID <= 0 || cut.ProxyPID <= 0 || cut.SourceCommit == "" || len(cut.ProxySHA256) != 64 || cut.ObservedAt.IsZero() {
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
	if !hexValue(source.Commit, 40) || !hexValue(source.Tree, 40) || source.AdmittedTupleSHA256 != "4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d" || !hexValue(source.SidecarProvenanceSHA256, 64) {
		return fmt.Errorf("source commit/tree, admitted tuple, and sidecar provenance are incomplete or unadmitted")
	}
	return nil
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
