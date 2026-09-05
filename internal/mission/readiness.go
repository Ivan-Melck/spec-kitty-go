package mission

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

const (
	ReadinessSchemaVersion = uint16(1)
	maxReadinessMembers    = 4096
	maxReadinessText       = 256
)

// ActionID is the closed, transport-neutral capability vocabulary. It names
// useful operations, never workflow phases or presentation commands.
type ActionID string

const (
	ActionMissionCreate       ActionID = "mission.create"
	ActionMissionPause        ActionID = "mission.pause"
	ActionMissionResume       ActionID = "mission.resume"
	ActionMissionFail         ActionID = "mission.fail"
	ActionMissionCancel       ActionID = "mission.cancel"
	ActionMissionComplete     ActionID = "mission.complete"
	ActionArtifactObserve     ActionID = "artifact.observe"
	ActionArtifactPublish     ActionID = "artifact.publish"
	ActionWorkGraphObserve    ActionID = "workgraph.observe"
	ActionWorkFinalize        ActionID = "work.finalize"
	ActionWorkReconcile       ActionID = "work.reconcile"
	ActionExecutionClaim      ActionID = "execution.claim"
	ActionExecutionHeartbeat  ActionID = "execution.heartbeat"
	ActionExecutionFinish     ActionID = "execution.finish"
	ActionWorkspaceAllocate   ActionID = "workspace.allocate"
	ActionWorkspaceQuarantine ActionID = "workspace.quarantine"
	ActionCandidateHandoff    ActionID = "candidate.handoff"
	ActionReviewRecord        ActionID = "review.record"
	ActionAcceptanceEvaluate  ActionID = "acceptance.evaluate"
	ActionIntegrationPublish  ActionID = "integration.publish"
	ActionEffectDispatch      ActionID = "effect.dispatch"
	ActionEffectReconcile     ActionID = "effect.reconcile"
	ActionConflictResolve     ActionID = "conflict.resolve"
	ActionPolicyResolve       ActionID = "policy.resolve"
	ActionDecisionResolve     ActionID = "decision.resolve"
	ActionBlockerClear        ActionID = "blocker.clear"
	ActionVerificationRun     ActionID = "verification.run"
)

var allActionIDs = []ActionID{
	ActionAcceptanceEvaluate, ActionArtifactObserve, ActionArtifactPublish,
	ActionBlockerClear, ActionCandidateHandoff, ActionConflictResolve,
	ActionDecisionResolve, ActionEffectDispatch, ActionEffectReconcile,
	ActionExecutionClaim, ActionExecutionFinish, ActionExecutionHeartbeat,
	ActionIntegrationPublish, ActionMissionCancel, ActionMissionComplete,
	ActionMissionCreate, ActionMissionFail, ActionMissionPause, ActionMissionResume,
	ActionPolicyResolve, ActionReviewRecord, ActionVerificationRun,
	ActionWorkFinalize, ActionWorkGraphObserve, ActionWorkReconcile,
	ActionWorkspaceAllocate, ActionWorkspaceQuarantine,
}

func AllActionIDs() []ActionID { return slices.Clone(allActionIDs) }

func (id ActionID) Validate() error {
	if !slices.Contains(allActionIDs, id) {
		return fmt.Errorf("unsupported capability action %q", id)
	}
	return nil
}

type AuthorityScopeKind string

const (
	ScopeMission   AuthorityScopeKind = "mission"
	ScopeAction    AuthorityScopeKind = "action"
	ScopeWork      AuthorityScopeKind = "work"
	ScopeArtifact  AuthorityScopeKind = "artifact"
	ScopeCriterion AuthorityScopeKind = "criterion"
	ScopePolicy    AuthorityScopeKind = "policy"
)

type AuthorityScope struct {
	Kind       AuthorityScopeKind `json:"kind"`
	Identity   string             `json:"identity"`
	Commitment string             `json:"commitment,omitempty"`
}

func (s AuthorityScope) Validate() error {
	switch s.Kind {
	case ScopeMission, ScopeAction, ScopeWork, ScopeArtifact, ScopeCriterion, ScopePolicy:
	default:
		return fmt.Errorf("unsupported authority scope %q", s.Kind)
	}
	if !boundedReadinessText(s.Identity, 128) || s.Commitment != "" && !lowerHexDigest(s.Commitment) {
		return errors.New("invalid authority scope")
	}
	return nil
}

type ActionRef struct {
	ID    ActionID       `json:"id"`
	Scope AuthorityScope `json:"scope"`
}

func (r ActionRef) Validate() error {
	if r.ID.Validate() != nil || r.Scope.Validate() != nil {
		return errors.New("invalid action reference")
	}
	return nil
}

type MissionLifecycle string

const (
	MissionOpen      MissionLifecycle = "open"
	MissionPaused    MissionLifecycle = "paused"
	MissionFailed    MissionLifecycle = "failed"
	MissionCanceled  MissionLifecycle = "canceled"
	MissionCompleted MissionLifecycle = "completed"
)

func (l MissionLifecycle) Validate() error {
	switch l {
	case MissionOpen, MissionPaused, MissionFailed, MissionCanceled, MissionCompleted:
		return nil
	default:
		return fmt.Errorf("unsupported mission lifecycle %q", l)
	}
}

func (l MissionLifecycle) Terminal() bool {
	return l == MissionFailed || l == MissionCanceled || l == MissionCompleted
}

// InstructionAuthority distinguishes enforceable law from overridable
// guidance. Defaults and examples are retained for renderers but cannot gate.
type InstructionAuthority string

const (
	InstructionInvariant InstructionAuthority = "invariant"
	InstructionDefault   InstructionAuthority = "default"
	InstructionExample   InstructionAuthority = "example"
)

func (a InstructionAuthority) Validate() error {
	switch a {
	case InstructionInvariant, InstructionDefault, InstructionExample:
		return nil
	default:
		return fmt.Errorf("unsupported instruction authority %q", a)
	}
}

type PredicateKind string

const (
	PredicateLifecycleAllows     PredicateKind = "lifecycle_allows"
	PredicateDecisionResolved    PredicateKind = "decision_resolved"
	PredicateBlockerCleared      PredicateKind = "blocker_cleared"
	PredicateApplicable          PredicateKind = "applicable"
	PredicateArtifactCurrent     PredicateKind = "artifact_current"
	PredicateWorkGraphComplete   PredicateKind = "workgraph_complete"
	PredicateWorkGraphCurrent    PredicateKind = "workgraph_current"
	PredicateExecutionEligible   PredicateKind = "execution_eligible"
	PredicateLeaseCurrent        PredicateKind = "lease_current"
	PredicateLeaseExpired        PredicateKind = "lease_expired"
	PredicateRuntimeEpochCurrent PredicateKind = "runtime_epoch_current"
	PredicateWorkspaceBound      PredicateKind = "workspace_bound"
	PredicateCandidateCurrent    PredicateKind = "candidate_current"
	PredicateIndependentReview   PredicateKind = "independent_review"
	PredicateAcceptanceCurrent   PredicateKind = "acceptance_current"
	PredicateRepositoryTarget    PredicateKind = "repository_target_current"
	PredicateExternalEffectSafe  PredicateKind = "external_effect_safe"
	PredicateConflictAbsent      PredicateKind = "conflict_absent"
	PredicatePolicyCurrent       PredicateKind = "policy_current"
	PredicateProjectVerification PredicateKind = "project_verification"
)

var allPredicateKinds = []PredicateKind{
	PredicateAcceptanceCurrent, PredicateApplicable, PredicateArtifactCurrent,
	PredicateBlockerCleared, PredicateCandidateCurrent, PredicateConflictAbsent,
	PredicateDecisionResolved, PredicateExecutionEligible, PredicateExternalEffectSafe,
	PredicateIndependentReview, PredicateLeaseCurrent, PredicateLeaseExpired, PredicateLifecycleAllows,
	PredicatePolicyCurrent, PredicateProjectVerification, PredicateRepositoryTarget,
	PredicateRuntimeEpochCurrent, PredicateWorkspaceBound, PredicateWorkGraphComplete,
	PredicateWorkGraphCurrent,
}

func (k PredicateKind) Validate() error {
	if !slices.Contains(allPredicateKinds, k) {
		return fmt.Errorf("unsupported readiness predicate %q", k)
	}
	return nil
}

type PredicateStatus string

const (
	PredicateSatisfied    PredicateStatus = "satisfied"
	PredicateInapplicable PredicateStatus = "inapplicable"
	PredicateUnavailable  PredicateStatus = "unavailable"
	PredicateFailed       PredicateStatus = "failed"
	PredicateStale        PredicateStatus = "stale"
	PredicateWaived       PredicateStatus = "waived"
	PredicateAmbiguous    PredicateStatus = "ambiguous"
)

func (s PredicateStatus) Validate() error {
	switch s {
	case PredicateSatisfied, PredicateInapplicable, PredicateUnavailable,
		PredicateFailed, PredicateStale, PredicateWaived, PredicateAmbiguous:
		return nil
	default:
		return fmt.Errorf("unsupported predicate status %q", s)
	}
}

func (s PredicateStatus) blocks() bool {
	return s == PredicateUnavailable || s == PredicateFailed || s == PredicateStale || s == PredicateAmbiguous
}

type ReadinessRecovery string

const (
	RecoveryNone              ReadinessRecovery = "none"
	RecoveryRunProducer       ReadinessRecovery = "run_responsible_capability"
	RecoveryRefreshEvidence   ReadinessRecovery = "refresh_evidence"
	ReadinessRecoverDecision  ReadinessRecovery = "resolve_decision"
	ReadinessRecoverBlocker   ReadinessRecovery = "clear_blocker"
	RecoveryRestoreDependency ReadinessRecovery = "restore_dependency"
	RecoveryResolveConflict   ReadinessRecovery = "resolve_conflict"
	RecoveryHumanPolicy       ReadinessRecovery = "obtain_human_policy"
)

func (r ReadinessRecovery) Validate() error {
	switch r {
	case RecoveryNone, RecoveryRunProducer, RecoveryRefreshEvidence,
		ReadinessRecoverDecision, ReadinessRecoverBlocker, RecoveryRestoreDependency,
		RecoveryResolveConflict, RecoveryHumanPolicy:
		return nil
	default:
		return fmt.Errorf("unsupported readiness recovery %q", r)
	}
}

type PredicateRequirement struct {
	Kind                  PredicateKind        `json:"kind"`
	Authority             InstructionAuthority `json:"authority"`
	ResponsibleCapability ActionID             `json:"responsible_capability"`
}

type CapabilityContract struct {
	Action        ActionID               `json:"action"`
	Scope         AuthorityScope         `json:"scope"`
	Applicability *PredicateRequirement  `json:"applicability,omitempty"`
	Predicates    []PredicateRequirement `json:"predicates"`
}

func (c CapabilityContract) Ref() ActionRef { return ActionRef{ID: c.Action, Scope: c.Scope} }

// CapabilityTemplate is mission-method data. It declares safety predicates
// without imposing order. Composition instantiates templates only from the
// complete authoritative artifact, work, criterion, and policy census.
type CapabilityTemplate struct {
	Action        ActionID
	ScopeKind     AuthorityScopeKind
	Applicability *PredicateRequirement
	Predicates    []PredicateRequirement
}

func (t CapabilityTemplate) Instantiate(scope AuthorityScope) (CapabilityContract, error) {
	if t.Action.Validate() != nil || scope.Validate() != nil || t.ScopeKind != scope.Kind {
		return CapabilityContract{}, &ReadinessError{Kind: ReadinessInvalidContract, Field: "scope"}
	}
	contract := CapabilityContract{Action: t.Action, Scope: scope, Predicates: slices.Clone(t.Predicates)}
	if t.Applicability != nil {
		value := *t.Applicability
		contract.Applicability = &value
	}
	if _, err := canonicalContracts([]CapabilityContract{contract}); err != nil {
		return CapabilityContract{}, err
	}
	return contract, nil
}

type PredicateFact struct {
	Kind                   PredicateKind                `json:"kind"`
	Action                 ActionID                     `json:"action"`
	Scope                  AuthorityScope               `json:"scope"`
	Status                 PredicateStatus              `json:"status"`
	Authority              ReadinessAuthorityCoordinate `json:"authority"`
	EvidenceIdentity       string                       `json:"evidence_identity"`
	EvidenceDigest         string                       `json:"evidence_sha256"`
	Producer               ActionID                     `json:"producer"`
	Recovery               ReadinessRecovery            `json:"recovery"`
	WaiverEvidenceIdentity string                       `json:"waiver_evidence_identity,omitempty"`
	WaiverEvidenceDigest   string                       `json:"waiver_evidence_sha256,omitempty"`
	Diagnostic             string                       `json:"diagnostic,omitempty"`
}

// ReadinessAuthorityCoordinate identifies the exact authority observation that
// produced a predicate. It is deliberately provider-neutral and cannot name a
// renderer, prompt, or editable projection as authority.
type ReadinessAuthorityCoordinate struct {
	Family   string `json:"family"`
	Identity string `json:"identity"`
	Revision uint64 `json:"revision"`
}

func (c ReadinessAuthorityCoordinate) Validate() error {
	switch c.Family {
	case "mission_authority", "artifact_authority", "workgraph_authority", "execution_authority",
		"workspace_authority", "candidate_authority", "review_authority", "acceptance_authority",
		"repository_authority", "external_effect_authority", "project_policy", "verifier_authority",
		"mission_decision", "mission_blocker", "mission_conflict", "capability_contract":
	default:
		return errors.New("unsupported readiness authority family")
	}
	if !boundedReadinessText(c.Identity, 128) || c.Revision == 0 {
		return errors.New("invalid readiness authority coordinate")
	}
	return nil
}

func (c ReadinessAuthorityCoordinate) factSource() bool {
	switch c.Family {
	case "mission_authority", "artifact_authority", "workgraph_authority", "execution_authority",
		"workspace_authority", "candidate_authority", "review_authority", "acceptance_authority",
		"repository_authority", "external_effect_authority", "project_policy", "verifier_authority":
		return true
	default:
		return false
	}
}

type ReadinessDecision struct {
	ID               string                       `json:"id"`
	Affects          []ActionRef                  `json:"affects"`
	Authority        ReadinessAuthorityCoordinate `json:"authority"`
	EvidenceIdentity string                       `json:"evidence_identity"`
	EvidenceDigest   string                       `json:"evidence_sha256"`
}

type ReadinessBlocker struct {
	ID                    string                       `json:"id"`
	Affects               []ActionRef                  `json:"affects"`
	Authority             ReadinessAuthorityCoordinate `json:"authority"`
	EvidenceIdentity      string                       `json:"evidence_identity"`
	EvidenceDigest        string                       `json:"evidence_sha256"`
	ResponsibleCapability ActionID                     `json:"responsible_capability"`
	Recovery              ReadinessRecovery            `json:"recovery"`
}

type ReadinessConflict struct {
	ID               string                       `json:"id"`
	Affects          []ActionRef                  `json:"affects"`
	Authority        ReadinessAuthorityCoordinate `json:"authority"`
	EvidenceIdentity string                       `json:"evidence_identity"`
	EvidenceDigest   string                       `json:"evidence_sha256"`
}

type ReadinessInput struct {
	MissionID      string
	Revision       uint64
	SnapshotDigest string
	Lifecycle      MissionLifecycle
	Contracts      []CapabilityContract
	Facts          []PredicateFact
	Decisions      []ReadinessDecision
	Blockers       []ReadinessBlocker
	Conflicts      []ReadinessConflict
}

type BlockingPredicate struct {
	Kind                  PredicateKind                `json:"kind"`
	Status                PredicateStatus              `json:"status"`
	Authority             ReadinessAuthorityCoordinate `json:"authority"`
	EvidenceIdentity      string                       `json:"evidence_identity"`
	EvidenceDigest        string                       `json:"evidence_sha256,omitempty"`
	ResponsibleCapability ActionID                     `json:"responsible_capability"`
	Recovery              ReadinessRecovery            `json:"recovery"`
	Diagnostic            string                       `json:"diagnostic,omitempty"`
}

type UnavailableAction struct {
	Action     ActionRef           `json:"action"`
	Predicates []BlockingPredicate `json:"predicates"`
}

type AssessmentStatus string

const (
	AssessmentAuthorized   AssessmentStatus = "authorized"
	AssessmentUnavailable  AssessmentStatus = "unavailable"
	AssessmentInapplicable AssessmentStatus = "inapplicable"
)

type ActionAssessment struct {
	Action     ActionRef           `json:"action"`
	Status     AssessmentStatus    `json:"status"`
	Predicates []BlockingPredicate `json:"predicates"`
}

type ActionCensus struct {
	missionID         string
	revision          uint64
	snapshotDigest    string
	lifecycle         MissionLifecycle
	assessments       []ActionAssessment
	authorized        []ActionRef
	unavailable       []UnavailableAction
	decisionsRequired []ReadinessDecision
	conflicts         []ReadinessConflict
	terminal          bool
	canonical         []byte
}

func (c ActionCensus) MissionID() string                { return c.missionID }
func (c ActionCensus) Revision() uint64                 { return c.revision }
func (c ActionCensus) SnapshotDigest() string           { return c.snapshotDigest }
func (c ActionCensus) Lifecycle() MissionLifecycle      { return c.lifecycle }
func (c ActionCensus) Assessments() []ActionAssessment  { return cloneAssessments(c.assessments) }
func (c ActionCensus) Authorized() []ActionRef          { return slices.Clone(c.authorized) }
func (c ActionCensus) Unavailable() []UnavailableAction { return cloneUnavailable(c.unavailable) }
func (c ActionCensus) DecisionsRequired() []ReadinessDecision {
	return cloneDecisions(c.decisionsRequired)
}
func (c ActionCensus) Conflicts() []ReadinessConflict { return cloneConflicts(c.conflicts) }
func (c ActionCensus) Terminal() bool                 { return c.terminal }
func (c ActionCensus) CanonicalBytes() []byte         { return bytes.Clone(c.canonical) }

type ReadinessErrorKind string

const (
	ReadinessInvalidInput    ReadinessErrorKind = "INVALID_INPUT"
	ReadinessInvalidContract ReadinessErrorKind = "INVALID_CONTRACT"
	ReadinessInvalidFact     ReadinessErrorKind = "INVALID_FACT"
)

type ReadinessError struct {
	Kind  ReadinessErrorKind
	Field string
}

func (e *ReadinessError) Error() string { return string(e.Kind) + ": " + e.Field }

func IsReadinessKind(err error, kind ReadinessErrorKind) bool {
	var target *ReadinessError
	return errors.As(err, &target) && target.Kind == kind
}

func EvaluateReadiness(input ReadinessInput) (ActionCensus, error) {
	if !boundedReadinessText(input.MissionID, 128) || input.Revision == 0 || !lowerHexDigest(input.SnapshotDigest) || input.Lifecycle.Validate() != nil || len(input.Contracts) == 0 || len(input.Contracts) > maxReadinessMembers {
		return ActionCensus{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "snapshot"}
	}
	contracts, err := canonicalContracts(input.Contracts)
	if err != nil {
		return ActionCensus{}, err
	}
	facts, err := canonicalFacts(input.Facts, contracts)
	if err != nil {
		return ActionCensus{}, err
	}
	decisions, err := canonicalDecisions(input.Decisions, contracts)
	if err != nil {
		return ActionCensus{}, err
	}
	blockers, err := canonicalBlockers(input.Blockers, contracts)
	if err != nil {
		return ActionCensus{}, err
	}
	conflicts, err := canonicalConflicts(input.Conflicts, contracts)
	if err != nil {
		return ActionCensus{}, err
	}

	byFact := make(map[string]PredicateFact, len(facts))
	for _, fact := range facts {
		ref := ActionRef{ID: fact.Action, Scope: fact.Scope}
		byFact[factKey(ref, fact.Kind)] = fact
	}
	census := ActionCensus{missionID: input.MissionID, revision: input.Revision, snapshotDigest: input.SnapshotDigest, lifecycle: input.Lifecycle, decisionsRequired: decisions, conflicts: conflicts, terminal: input.Lifecycle.Terminal()}
	for _, contract := range contracts {
		ref := contract.Ref()
		blocked := lifecycleBlockingPredicate(input.Lifecycle, contract.Action, input.MissionID, input.Revision)
		inapplicable := false
		if contract.Applicability != nil {
			fact, ok := byFact[factKey(ref, contract.Applicability.Kind)]
			if !ok {
				blocked = append(blocked, missingPredicate(input, ref, contract.Applicability.Kind, contract.Applicability.ResponsibleCapability, "applicability has no current producer fact"))
			} else if fact.Status == PredicateInapplicable {
				inapplicable = true
			} else if fact.Status.blocks() {
				blocked = append(blocked, blockingFromFact(fact))
			}
		}
		for _, requirement := range contract.Predicates {
			fact, ok := byFact[factKey(ref, requirement.Kind)]
			if !ok {
				if requirement.Authority == InstructionInvariant {
					blocked = append(blocked, missingPredicate(input, ref, requirement.Kind, requirement.ResponsibleCapability, "required predicate has no current fact"))
				}
				continue
			}
			if requirement.Authority == InstructionInvariant && fact.Status.blocks() {
				blocked = append(blocked, blockingFromFact(fact))
			}
		}
		for _, decision := range decisions {
			if slices.Contains(decision.Affects, ref) {
				blocked = append(blocked, BlockingPredicate{Kind: PredicateDecisionResolved, Status: PredicateUnavailable, Authority: decision.Authority, EvidenceIdentity: decision.EvidenceIdentity, EvidenceDigest: decision.EvidenceDigest, ResponsibleCapability: ActionDecisionResolve, Recovery: ReadinessRecoverDecision, Diagnostic: "action-scoped decision is unresolved"})
			}
		}
		for _, blocker := range blockers {
			if slices.Contains(blocker.Affects, ref) {
				blocked = append(blocked, BlockingPredicate{Kind: PredicateBlockerCleared, Status: PredicateUnavailable, Authority: blocker.Authority, EvidenceIdentity: blocker.EvidenceIdentity, EvidenceDigest: blocker.EvidenceDigest, ResponsibleCapability: blocker.ResponsibleCapability, Recovery: blocker.Recovery, Diagnostic: "action-scoped blocker is active"})
			}
		}
		for _, conflict := range conflicts {
			if slices.Contains(conflict.Affects, ref) {
				blocked = append(blocked, BlockingPredicate{Kind: PredicateConflictAbsent, Status: PredicateAmbiguous, Authority: conflict.Authority, EvidenceIdentity: conflict.EvidenceIdentity, EvidenceDigest: conflict.EvidenceDigest, ResponsibleCapability: ActionConflictResolve, Recovery: RecoveryResolveConflict, Diagnostic: "action-scoped conflict is unresolved"})
			}
		}
		sort.Slice(blocked, func(i, j int) bool {
			if blocked[i].Kind == blocked[j].Kind {
				return blocked[i].EvidenceIdentity < blocked[j].EvidenceIdentity
			}
			return blocked[i].Kind < blocked[j].Kind
		})
		if inapplicable {
			census.assessments = append(census.assessments, ActionAssessment{Action: ref, Status: AssessmentInapplicable})
		} else if len(blocked) == 0 {
			census.authorized = append(census.authorized, ref)
			census.assessments = append(census.assessments, ActionAssessment{Action: ref, Status: AssessmentAuthorized})
		} else {
			census.unavailable = append(census.unavailable, UnavailableAction{Action: ref, Predicates: blocked})
			census.assessments = append(census.assessments, ActionAssessment{Action: ref, Status: AssessmentUnavailable, Predicates: blocked})
		}
	}
	wire := struct {
		SchemaVersion     uint16              `json:"schema_version"`
		MissionID         string              `json:"mission_id"`
		Revision          uint64              `json:"revision"`
		SnapshotSHA256    string              `json:"snapshot_sha256"`
		Lifecycle         MissionLifecycle    `json:"lifecycle"`
		Assessments       []ActionAssessment  `json:"assessments"`
		Authorized        []ActionRef         `json:"authorized"`
		Unavailable       []UnavailableAction `json:"unavailable"`
		DecisionsRequired []ReadinessDecision `json:"decisions_required"`
		Conflicts         []ReadinessConflict `json:"conflicts"`
		Terminal          bool                `json:"terminal"`
	}{ReadinessSchemaVersion, census.missionID, census.revision, census.snapshotDigest, census.lifecycle, census.assessments, census.authorized, census.unavailable, census.decisionsRequired, census.conflicts, census.terminal}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return ActionCensus{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "encoding"}
	}
	census.canonical = encoded
	return census, nil
}

func canonicalContracts(values []CapabilityContract) ([]CapabilityContract, error) {
	result := make([]CapabilityContract, len(values))
	seen := make(map[string]bool, len(values))
	for index, value := range values {
		key := actionRefKey(value.Ref())
		if value.Action.Validate() != nil || value.Scope.Validate() != nil || seen[key] || len(value.Predicates) > maxReadinessMembers {
			return nil, &ReadinessError{Kind: ReadinessInvalidContract, Field: "action"}
		}
		seen[key] = true
		if value.Applicability != nil {
			applicability := *value.Applicability
			if applicability.Kind != PredicateApplicable || applicability.Authority != InstructionInvariant || applicability.ResponsibleCapability.Validate() != nil {
				return nil, &ReadinessError{Kind: ReadinessInvalidContract, Field: "applicability"}
			}
			value.Applicability = &applicability
		}
		value.Predicates = slices.Clone(value.Predicates)
		sort.Slice(value.Predicates, func(i, j int) bool { return value.Predicates[i].Kind < value.Predicates[j].Kind })
		for predicateIndex, requirement := range value.Predicates {
			if requirement.Kind.Validate() != nil || requirement.Authority.Validate() != nil || requirement.ResponsibleCapability.Validate() != nil || predicateIndex > 0 && value.Predicates[predicateIndex-1].Kind == requirement.Kind {
				return nil, &ReadinessError{Kind: ReadinessInvalidContract, Field: "predicate"}
			}
		}
		result[index] = value
	}
	sort.Slice(result, func(i, j int) bool { return actionRefKey(result[i].Ref()) < actionRefKey(result[j].Ref()) })
	return result, nil
}

func canonicalFacts(values []PredicateFact, contracts []CapabilityContract) ([]PredicateFact, error) {
	if len(values) > maxReadinessMembers {
		return nil, &ReadinessError{Kind: ReadinessInvalidFact, Field: "facts"}
	}
	actions := make(map[string]bool, len(contracts))
	for _, contract := range contracts {
		actions[actionRefKey(contract.Ref())] = true
	}
	result := slices.Clone(values)
	sort.Slice(result, func(i, j int) bool {
		left := ActionRef{ID: result[i].Action, Scope: result[i].Scope}
		right := ActionRef{ID: result[j].Action, Scope: result[j].Scope}
		if actionRefKey(left) == actionRefKey(right) {
			return result[i].Kind < result[j].Kind
		}
		return actionRefKey(left) < actionRefKey(right)
	})
	for index, fact := range result {
		ref := ActionRef{ID: fact.Action, Scope: fact.Scope}
		if !actions[actionRefKey(ref)] || ref.Validate() != nil || fact.Kind.Validate() != nil || fact.Status.Validate() != nil || fact.Producer.Validate() != nil || fact.Recovery.Validate() != nil ||
			fact.Authority.Validate() != nil || !fact.Authority.factSource() || !boundedReadinessText(fact.EvidenceIdentity, 128) || !lowerHexDigest(fact.EvidenceDigest) ||
			(fact.Diagnostic != "" && !boundedReadinessDiagnostic(fact.Diagnostic, maxReadinessText)) || index > 0 && factKey(ActionRef{ID: result[index-1].Action, Scope: result[index-1].Scope}, result[index-1].Kind) == factKey(ref, fact.Kind) {
			return nil, &ReadinessError{Kind: ReadinessInvalidFact, Field: "fact"}
		}
		if fact.Status == PredicateWaived {
			if !boundedReadinessText(fact.WaiverEvidenceIdentity, 128) || !lowerHexDigest(fact.WaiverEvidenceDigest) {
				return nil, &ReadinessError{Kind: ReadinessInvalidFact, Field: "waiver_evidence_identity"}
			}
		} else if fact.WaiverEvidenceIdentity != "" || fact.WaiverEvidenceDigest != "" {
			return nil, &ReadinessError{Kind: ReadinessInvalidFact, Field: "waiver_evidence_identity"}
		}
		if fact.Status.blocks() && fact.Recovery == RecoveryNone {
			return nil, &ReadinessError{Kind: ReadinessInvalidFact, Field: "recovery"}
		}
	}
	return result, nil
}

func canonicalDecisions(values []ReadinessDecision, contracts []CapabilityContract) ([]ReadinessDecision, error) {
	result := cloneDecisions(values)
	valid := contractActions(contracts)
	for index := range result {
		item := &result[index]
		if !boundedReadinessText(item.ID, 128) || item.Authority.Validate() != nil || item.Authority.Family != "mission_decision" || !boundedReadinessText(item.EvidenceIdentity, 128) || !lowerHexDigest(item.EvidenceDigest) || canonicalAffected(&item.Affects, valid) != nil {
			return nil, &ReadinessError{Kind: ReadinessInvalidFact, Field: "decision"}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	if duplicateIDs(result, func(v ReadinessDecision) string { return v.ID }) {
		return nil, &ReadinessError{Kind: ReadinessInvalidFact, Field: "decision"}
	}
	return result, nil
}

func canonicalBlockers(values []ReadinessBlocker, contracts []CapabilityContract) ([]ReadinessBlocker, error) {
	result := cloneBlockers(values)
	valid := contractActions(contracts)
	for index := range result {
		item := &result[index]
		if !boundedReadinessText(item.ID, 128) || item.Authority.Validate() != nil || item.Authority.Family != "mission_blocker" || !boundedReadinessText(item.EvidenceIdentity, 128) || !lowerHexDigest(item.EvidenceDigest) || item.ResponsibleCapability.Validate() != nil || item.Recovery.Validate() != nil || item.Recovery == RecoveryNone || canonicalAffected(&item.Affects, valid) != nil {
			return nil, &ReadinessError{Kind: ReadinessInvalidFact, Field: "blocker"}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	if duplicateIDs(result, func(v ReadinessBlocker) string { return v.ID }) {
		return nil, &ReadinessError{Kind: ReadinessInvalidFact, Field: "blocker"}
	}
	return result, nil
}

func canonicalConflicts(values []ReadinessConflict, contracts []CapabilityContract) ([]ReadinessConflict, error) {
	result := cloneConflicts(values)
	valid := contractActions(contracts)
	for index := range result {
		item := &result[index]
		if !boundedReadinessText(item.ID, 128) || item.Authority.Validate() != nil || item.Authority.Family != "mission_conflict" || !boundedReadinessText(item.EvidenceIdentity, 128) || !lowerHexDigest(item.EvidenceDigest) || canonicalAffected(&item.Affects, valid) != nil {
			return nil, &ReadinessError{Kind: ReadinessInvalidFact, Field: "conflict"}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	if duplicateIDs(result, func(v ReadinessConflict) string { return v.ID }) {
		return nil, &ReadinessError{Kind: ReadinessInvalidFact, Field: "conflict"}
	}
	return result, nil
}

func lifecycleBlockingPredicate(lifecycle MissionLifecycle, action ActionID, missionID string, revision uint64) []BlockingPredicate {
	allowed := lifecycle == MissionOpen || lifecycle == MissionPaused && action == ActionMissionResume
	if action == ActionMissionCreate || lifecycle == MissionOpen && action == ActionMissionResume {
		allowed = false
	}
	if allowed {
		return nil
	}
	return []BlockingPredicate{{Kind: PredicateLifecycleAllows, Status: PredicateUnavailable, Authority: ReadinessAuthorityCoordinate{Family: "mission_authority", Identity: missionID, Revision: revision}, EvidenceIdentity: "lifecycle:" + string(lifecycle), ResponsibleCapability: lifecycleProducer(lifecycle), Recovery: lifecycleRecovery(lifecycle), Diagnostic: "coarse mission lifecycle does not allow this action"}}
}

func lifecycleProducer(lifecycle MissionLifecycle) ActionID {
	if lifecycle == MissionPaused {
		return ActionMissionResume
	}
	return ActionPolicyResolve
}

func lifecycleRecovery(lifecycle MissionLifecycle) ReadinessRecovery {
	if lifecycle == MissionPaused {
		return RecoveryRunProducer
	}
	return RecoveryHumanPolicy
}

func blockingFromFact(fact PredicateFact) BlockingPredicate {
	return BlockingPredicate{Kind: fact.Kind, Status: fact.Status, Authority: fact.Authority, EvidenceIdentity: fact.EvidenceIdentity, EvidenceDigest: fact.EvidenceDigest, ResponsibleCapability: fact.Producer, Recovery: fact.Recovery, Diagnostic: fact.Diagnostic}
}

func missingPredicate(input ReadinessInput, ref ActionRef, kind PredicateKind, producer ActionID, diagnostic string) BlockingPredicate {
	return BlockingPredicate{
		Kind: kind, Status: PredicateUnavailable,
		Authority:        ReadinessAuthorityCoordinate{Family: "capability_contract", Identity: string(ref.ID) + ":" + ref.Scope.Identity + ":" + string(kind), Revision: input.Revision},
		EvidenceIdentity: "missing:" + actionRefKey(ref) + ":" + string(kind), ResponsibleCapability: producer,
		Recovery: RecoveryRunProducer, Diagnostic: diagnostic,
	}
}

func contractActions(contracts []CapabilityContract) map[string]bool {
	result := make(map[string]bool, len(contracts))
	for _, contract := range contracts {
		result[actionRefKey(contract.Ref())] = true
	}
	return result
}

func canonicalAffected(values *[]ActionRef, valid map[string]bool) error {
	if len(*values) == 0 || len(*values) > maxReadinessMembers {
		return errors.New("empty affected action set")
	}
	sort.Slice(*values, func(i, j int) bool { return actionRefKey((*values)[i]) < actionRefKey((*values)[j]) })
	for index, action := range *values {
		if action.Validate() != nil || !valid[actionRefKey(action)] || index > 0 && actionRefKey((*values)[index-1]) == actionRefKey(action) {
			return errors.New("invalid affected action set")
		}
	}
	return nil
}

func factKey(action ActionRef, kind PredicateKind) string {
	return actionRefKey(action) + "\x00" + string(kind)
}

func actionRefKey(action ActionRef) string {
	return string(action.ID) + "\x00" + string(action.Scope.Kind) + "\x00" + action.Scope.Identity + "\x00" + action.Scope.Commitment
}

func boundedReadinessText(value string, limit int) bool {
	if value == "" || len(value) > limit || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r == 0x7f {
			return false
		}
	}
	return true
}

func boundedReadinessDiagnostic(value string, limit int) bool {
	if value == "" || len(value) > limit || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func cloneUnavailable(values []UnavailableAction) []UnavailableAction {
	result := slices.Clone(values)
	for index := range result {
		result[index].Predicates = slices.Clone(result[index].Predicates)
	}
	return result
}

func cloneAssessments(values []ActionAssessment) []ActionAssessment {
	result := slices.Clone(values)
	for index := range result {
		result[index].Predicates = slices.Clone(result[index].Predicates)
	}
	return result
}

func cloneDecisions(values []ReadinessDecision) []ReadinessDecision {
	result := slices.Clone(values)
	for index := range result {
		result[index].Affects = slices.Clone(result[index].Affects)
	}
	return result
}

func cloneBlockers(values []ReadinessBlocker) []ReadinessBlocker {
	result := slices.Clone(values)
	for index := range result {
		result[index].Affects = slices.Clone(result[index].Affects)
	}
	return result
}

func cloneConflicts(values []ReadinessConflict) []ReadinessConflict {
	result := slices.Clone(values)
	for index := range result {
		result[index].Affects = slices.Clone(result[index].Affects)
	}
	return result
}

func duplicateIDs[T any](values []T, identity func(T) string) bool {
	for index := 1; index < len(values); index++ {
		if identity(values[index-1]) == identity(values[index]) {
			return true
		}
	}
	return false
}
