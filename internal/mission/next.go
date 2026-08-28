package mission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

const NextSchemaVersion uint16 = 1

type NextKind string

const (
	NextQuery            NextKind = "query"
	NextAction           NextKind = "action"
	NextWorkReady        NextKind = "work_ready"
	NextReviewReady      NextKind = "review_ready"
	NextDecisionRequired NextKind = "decision_required"
	NextBlocked          NextKind = "blocked"
	NextTerminal         NextKind = "terminal"
)

func (k NextKind) valid() bool {
	switch k {
	case NextQuery, NextAction, NextWorkReady, NextReviewReady, NextDecisionRequired, NextBlocked, NextTerminal:
		return true
	}
	return false
}

type RetrySafety string

const (
	RetrySafe                 RetrySafety = "safe"
	RetryUnsafe               RetrySafety = "unsafe"
	RetryObserveThenReconcile RetrySafety = "observe_then_reconcile"
	RetryNotApplicable        RetrySafety = "not_applicable"
)

func (r RetrySafety) valid() bool {
	return r == RetrySafe || r == RetryUnsafe || r == RetryObserveThenReconcile || r == RetryNotApplicable
}

type NextDiagnostic struct {
	Kind        string           `json:"kind"`
	RetrySafety RetrySafety      `json:"retry_safety"`
	Recovery    RecoveryCategory `json:"recovery,omitempty"`
	Message     string           `json:"message,omitempty"`
}

func (d NextDiagnostic) validate() error {
	if !boundedFactText(d.Kind, 64) || !d.RetrySafety.valid() {
		return errors.New("invalid next diagnostic")
	}
	if d.Recovery != "" && d.Recovery.Validate() != nil {
		return errors.New("invalid next recovery")
	}
	if d.Message != "" && !boundedFactText(d.Message, 160) {
		return errors.New("invalid next message")
	}
	return nil
}

type ActionDescriptor struct {
	ActionKey        string  `json:"action_key"`
	Mutating         bool    `json:"mutating"`
	GovernanceAction string  `json:"governance_action"`
	Successor        StepKey `json:"-"`
}

func (a ActionDescriptor) validate() error {
	if !boundedFactText(a.ActionKey, 128) || !a.Mutating || !boundedFactText(a.GovernanceAction, 128) || a.Successor.Validate() != nil || a.Successor.Reserved() {
		return errors.New("invalid action descriptor")
	}
	return nil
}

type DecisionDescriptor struct {
	DecisionID ids.DecisionID `json:"decision_id"`
	Question   string         `json:"question"`
	Options    []string       `json:"options,omitempty"`
}
type BlockerDescriptor struct {
	BlockerID       ids.BlockerID     `json:"blocker_id"`
	Kind            BlockerKind       `json:"kind"`
	Summary         string            `json:"summary"`
	RecoveryActions []BlockerRecovery `json:"recovery_actions"`
}

type NextResult struct {
	SchemaVersion       uint16              `json:"schema_version"`
	Kind                NextKind            `json:"kind"`
	MissionID           ids.MissionID       `json:"mission_id"`
	MissionStateVersion uint64              `json:"mission_state_version"`
	StepKey             StepKey             `json:"step_key"`
	WorkRevisionID      ids.WorkRevisionID  `json:"work_revision_id,omitempty"`
	Action              *ActionDescriptor   `json:"action,omitempty"`
	Decision            *DecisionDescriptor `json:"decision,omitempty"`
	Blocker             *BlockerDescriptor  `json:"blocker,omitempty"`
	ReadyWorkIDs        []ids.WorkID        `json:"ready_work_ids,omitempty"`
	TerminalOutcome     TerminalOutcome     `json:"terminal_outcome,omitempty"`
	Diagnostic          *NextDiagnostic     `json:"diagnostic,omitempty"`
}
type NextBase struct {
	MissionID    ids.MissionID
	StateVersion uint64
	Step         StepKey
}

func nextBase(base NextBase, kind NextKind) NextResult {
	return NextResult{SchemaVersion: NextSchemaVersion, Kind: kind, MissionID: base.MissionID, MissionStateVersion: base.StateVersion, StepKey: base.Step}
}
func NewQueryResult(base NextBase, d NextDiagnostic) (NextResult, error) {
	v := nextBase(base, NextQuery)
	v.Diagnostic = &d
	return validatedNext(v)
}
func NewActionResult(base NextBase, a ActionDescriptor) (NextResult, error) {
	v := nextBase(base, NextAction)
	if a.Successor == "" {
		a.Successor = StepKey(a.ActionKey)
	}
	v.Action = &a
	return validatedNext(v)
}
func NewWorkReadyResult(base NextBase, revision ids.WorkRevisionID, work []ids.WorkID) (NextResult, error) {
	return newReadyResult(base, NextWorkReady, revision, work)
}
func NewReviewReadyResult(base NextBase, revision ids.WorkRevisionID, work []ids.WorkID) (NextResult, error) {
	return newReadyResult(base, NextReviewReady, revision, work)
}
func newReadyResult(base NextBase, kind NextKind, revision ids.WorkRevisionID, work []ids.WorkID) (NextResult, error) {
	v := nextBase(base, kind)
	v.WorkRevisionID = revision
	v.ReadyWorkIDs = append([]ids.WorkID(nil), work...)
	sort.Slice(v.ReadyWorkIDs, func(i, j int) bool { return v.ReadyWorkIDs[i] < v.ReadyWorkIDs[j] })
	return validatedNext(v)
}
func NewDecisionRequiredResult(base NextBase, d Decision) (NextResult, error) {
	v := nextBase(base, NextDecisionRequired)
	v.Decision = &DecisionDescriptor{DecisionID: d.ID(), Question: d.Question(), Options: d.Options()}
	return validatedNext(v)
}
func NewBlockedResult(base NextBase, b Blocker) (NextResult, error) {
	v := nextBase(base, NextBlocked)
	v.Blocker = &BlockerDescriptor{BlockerID: b.ID(), Kind: b.Kind(), Summary: b.Summary(), RecoveryActions: b.RecoveryActions()}
	return validatedNext(v)
}
func NewTerminalResult(base NextBase, outcome TerminalOutcome) (NextResult, error) {
	v := nextBase(base, NextTerminal)
	v.TerminalOutcome = outcome
	return validatedNext(v)
}
func validatedNext(v NextResult) (NextResult, error) {
	if err := v.Validate(); err != nil {
		return NextResult{}, err
	}
	v = cloneNext(v)
	return v, nil
}

func (v NextResult) Validate() error {
	if v.SchemaVersion != NextSchemaVersion || !v.Kind.valid() {
		return refusal(KindInvalidRequest, RecoveryCorrectInput, "next.kind", "next result kind or schema is unsupported")
	}
	if _, err := ids.ParseMissionID(v.MissionID.String()); err != nil || v.MissionStateVersion == 0 || v.StepKey.Validate() != nil || v.StepKey.Reserved() {
		return refusal(KindInvalidRequest, RecoveryCorrectInput, "next.scope", "next result scope is invalid")
	}
	fields := 0
	if v.WorkRevisionID != "" {
		fields++
	}
	if v.Action != nil {
		fields++
	}
	if v.Decision != nil {
		fields++
	}
	if v.Blocker != nil {
		fields++
	}
	if v.ReadyWorkIDs != nil {
		fields++
	}
	if v.TerminalOutcome != "" {
		fields++
	}
	if v.Diagnostic != nil {
		fields++
	}
	switch v.Kind {
	case NextQuery:
		if fields != 1 || v.Diagnostic == nil || v.Diagnostic.validate() != nil {
			return nextAlgebraRefusal()
		}
	case NextAction:
		if fields != 1 || v.Action == nil || v.Action.validate() != nil {
			return nextAlgebraRefusal()
		}
	case NextWorkReady, NextReviewReady:
		if fields != 2 || v.WorkRevisionID == "" || v.ReadyWorkIDs == nil || len(v.ReadyWorkIDs) == 0 {
			return nextAlgebraRefusal()
		}
		if _, err := ids.ParseWorkRevisionID(v.WorkRevisionID.String()); err != nil {
			return nextAlgebraRefusal()
		}
		for i, id := range v.ReadyWorkIDs {
			if _, err := ids.ParseWorkID(id.String()); err != nil || (i > 0 && v.ReadyWorkIDs[i-1] >= id) {
				return nextAlgebraRefusal()
			}
		}
	case NextDecisionRequired:
		if fields != 1 || v.Decision == nil {
			return nextAlgebraRefusal()
		}
		if _, err := ids.ParseDecisionID(v.Decision.DecisionID.String()); err != nil || !boundedFactText(v.Decision.Question, 512) {
			return nextAlgebraRefusal()
		}
		for i, o := range v.Decision.Options {
			if !boundedFactText(o, 128) || (i > 0 && v.Decision.Options[i-1] >= o) {
				return nextAlgebraRefusal()
			}
		}
	case NextBlocked:
		if fields != 1 || v.Blocker == nil {
			return nextAlgebraRefusal()
		}
		if _, err := ids.ParseBlockerID(v.Blocker.BlockerID.String()); err != nil || !boundedFactText(v.Blocker.Summary, 512) || len(v.Blocker.RecoveryActions) == 0 {
			return nextAlgebraRefusal()
		}
		for i, r := range v.Blocker.RecoveryActions {
			if !blockerRecoveryAllowed(v.Blocker.Kind, r) || (i > 0 && v.Blocker.RecoveryActions[i-1] >= r) {
				return nextAlgebraRefusal()
			}
		}
	case NextTerminal:
		if fields != 1 || v.TerminalOutcome.Validate() != nil {
			return nextAlgebraRefusal()
		}
	}
	return nil
}
func nextAlgebraRefusal() error {
	return refusal(KindInvalidRequest, RecoveryCorrectInput, "next.fields", "next result fields do not match kind")
}
func cloneNext(v NextResult) NextResult {
	if v.Action != nil {
		x := *v.Action
		v.Action = &x
	}
	if v.Decision != nil {
		x := *v.Decision
		x.Options = append([]string(nil), x.Options...)
		v.Decision = &x
	}
	if v.Blocker != nil {
		x := *v.Blocker
		x.RecoveryActions = append([]BlockerRecovery(nil), x.RecoveryActions...)
		v.Blocker = &x
	}
	v.ReadyWorkIDs = append([]ids.WorkID(nil), v.ReadyWorkIDs...)
	if v.Diagnostic != nil {
		x := *v.Diagnostic
		v.Diagnostic = &x
	}
	return v
}

func (v NextResult) MarshalJSON() ([]byte, error) {
	if err := v.Validate(); err != nil {
		return nil, err
	}
	type wire NextResult
	return json.Marshal(wire(cloneNext(v)))
}
func (v *NextResult) UnmarshalJSON(data []byte) error {
	if err := rejectNextDuplicateKeys(data); err != nil {
		return err
	}
	type wire NextResult
	var decoded wire
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&decoded); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("invalid trailing next result")
	}
	value := NextResult(decoded)
	if value.Action != nil {
		value.Action.Successor = StepKey(value.Action.ActionKey)
	}
	if err := value.Validate(); err != nil {
		return err
	}
	*v = cloneNext(value)
	return nil
}

func rejectNextDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return errors.New("duplicate or invalid next result field")
				}
				seen[key] = true
				if err := walk(); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return errors.New("invalid next result object")
			}
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return errors.New("invalid next result array")
			}
		default:
			return errors.New("invalid next result")
		}
		return nil
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("invalid trailing next result")
	}
	return nil
}

// NextSnapshot is one repository-atomic observation. Its members are accepted
// authority types, not tracker or projection surrogates.
type NextSnapshot struct {
	Mission              MissionSnapshot
	Definition           MissionDefinition
	ArtifactObservations []artifact.CurrentApprovalObservation
	CurrentWorkRevision  *workgraph.RevisionV2
	WorkDraftFindings    []workgraph.Finding
	Decisions            []Decision
	Blockers             []Blocker
}
type NextSnapshotSource interface {
	ObserveNext(context.Context, ids.MissionID) (NextSnapshot, error)
}

func Next(ctx context.Context, source NextSnapshotSource, missionID ids.MissionID) (NextResult, error) {
	if source == nil {
		return NextResult{}, refusal(KindPersistenceUnavailable, RecoveryRetryPersistence, "next.source", "next snapshot source is unavailable")
	}
	if _, err := ids.ParseMissionID(missionID.String()); err != nil {
		return NextResult{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "mission_id", "mission identity is invalid")
	}
	snapshot, err := source.ObserveNext(ctx, missionID)
	if err != nil {
		return NextResult{}, normalizeStoreError(err)
	}
	return evaluateNext(snapshot, missionID)
}
func evaluateNext(s NextSnapshot, requested ids.MissionID) (NextResult, error) {
	if s.Mission.Validate() != nil || s.Mission.MissionID != requested {
		return NextResult{}, refusal(KindInvalidStoredState, RecoveryRepairStoredState, "next.snapshot", "mission snapshot is corrupt")
	}
	if s.Definition.ID() != s.Mission.DefinitionID || s.Definition.Version() != s.Mission.DefinitionVersion || s.Definition.Commitment() != s.Mission.DefinitionCommitment {
		return NextResult{}, refusal(KindDefinitionMismatch, RecoverySelectDefinition, "next.definition", "snapshot definition does not match mission authority")
	}
	base := NextBase{s.Mission.MissionID, s.Mission.StateVersion, s.Mission.CurrentStep}
	if err := validateNextSnapshotMembers(s); err != nil {
		return NextResult{}, err
	}
	open := OpenDecisions(s.Decisions)
	active := ActiveBlockers(s.Blockers)
	if !sameDecisionCensus(s.Mission.UnresolvedDecisions, open) || !sameBlockerCensus(s.Mission.ActiveBlockers, active) {
		return NextResult{}, refusal(KindInvalidStoredState, RecoveryRepairStoredState, "next.census", "mission fact census is inconsistent")
	}
	for _, b := range active {
		if b.MissionID() != s.Mission.MissionID || b.Step() != s.Mission.CurrentStep {
			return NextResult{}, refusal(KindInvalidStoredState, RecoveryRepairStoredState, "next.blocker", "blocker scope is inconsistent")
		}
	}
	for _, d := range open {
		if d.MissionID() != s.Mission.MissionID || d.Step() != s.Mission.CurrentStep {
			return NextResult{}, refusal(KindInvalidStoredState, RecoveryRepairStoredState, "next.decision", "decision scope is inconsistent")
		}
	}
	if len(active) > 0 {
		return NewBlockedResult(base, active[0])
	}
	if len(open) > 0 {
		return NewDecisionRequiredResult(base, open[0])
	}
	if s.Mission.CurrentStep == StepWorkFinalized {
		return finalizedNext(base, s)
	}
	current, ok := s.Definition.Step(s.Mission.CurrentStep)
	if !ok {
		return NextResult{}, refusal(KindInvalidStoredState, RecoveryRepairStoredState, "next.step", "current step is absent from definition")
	}
	successors := current.Spec().Successors
	if len(successors) == 0 {
		if s.Mission.Lifecycle == LifecycleTerminal {
			return NewTerminalResult(base, s.Mission.TerminalOutcome)
		}
		return queryNext(base, "required_input", "mission step requires input")
	}
	successor := successors[0]
	if s.Mission.CurrentStep == StepWorkDraftReady {
		if len(s.WorkDraftFindings) > 0 {
			successor = StepReconciliationRequired
		} else {
			successor = StepWorkFinalized
		}
	} else if len(successors) != 1 {
		return NextResult{}, refusal(KindInvalidStoredState, RecoveryRepairStoredState, "next.successor", "mission step has ambiguous successors")
	}
	destination, ok := s.Definition.Step(successor)
	if !ok {
		return NextResult{}, refusal(KindInvalidStoredState, RecoveryRepairStoredState, "next.successor", "successor is absent from definition")
	}
	if missingArtifactInput(destination.Spec().RequiredInputs, s.ArtifactObservations, s.Mission.MissionID) {
		return queryNext(base, "required_input", "required artifact input is absent")
	}
	if !approvalFresh(destination.Spec().ApprovalPolicy, s.ArtifactObservations, s.Mission.MissionID) {
		return queryNext(base, "approval_required", "current artifact approval is absent or stale")
	}
	spec := destination.Spec()
	if !spec.Mutating || spec.GovernanceAction == "" {
		return queryNext(base, "required_input", "mission step requires structured input")
	}
	return NewActionResult(base, ActionDescriptor{ActionKey: successor.String(), Mutating: true, GovernanceAction: spec.GovernanceAction, Successor: successor})
}

func validateNextSnapshotMembers(s NextSnapshot) error {
	seenArtifacts := map[artifact.Kind]bool{}
	for _, observation := range s.ArtifactObservations {
		revision := observation.CurrentRevision()
		if revision.MissionID() != s.Mission.MissionID.String() || seenArtifacts[revision.Kind()] {
			return refusal(KindInvalidStoredState, RecoveryRepairStoredState, "next.artifacts", "artifact observation scope or census is inconsistent")
		}
		if _, err := artifact.NewCurrentApprovalObservation(revision, observation.Approvals()); err != nil {
			return refusal(KindInvalidStoredState, RecoveryRepairStoredState, "next.artifacts", "artifact observation is invalid")
		}
		seenArtifacts[revision.Kind()] = true
	}
	for _, decision := range s.Decisions {
		if decision.Validate() != nil || decision.MissionID() != s.Mission.MissionID {
			return refusal(KindInvalidStoredState, RecoveryRepairStoredState, "next.decisions", "decision observation is invalid")
		}
	}
	for _, blocker := range s.Blockers {
		if blocker.Validate() != nil || blocker.MissionID() != s.Mission.MissionID {
			return refusal(KindInvalidStoredState, RecoveryRepairStoredState, "next.blockers", "blocker observation is invalid")
		}
	}
	for _, finding := range s.WorkDraftFindings {
		if !validNextFinding(finding) {
			return refusal(KindInvalidStoredState, RecoveryRepairStoredState, "next.work_findings", "work finding is invalid")
		}
	}
	if s.CurrentWorkRevision == nil {
		if s.Mission.CurrentWorkRevisionID != "" {
			return refusal(KindInvalidStoredState, RecoveryRepairStoredState, "next.work_revision", "current revision observation is absent")
		}
		return nil
	}
	r := s.CurrentWorkRevision
	if s.Mission.CurrentWorkRevisionID == "" || r.ID() != s.Mission.CurrentWorkRevisionID || r.MissionID() != s.Mission.MissionID || r.ProjectID() != s.Mission.ProjectID {
		return refusal(KindInvalidStoredState, RecoveryRepairStoredState, "next.work_revision", "current revision observation is outside mission authority")
	}
	identity := r.MissionDefinition()
	if identity.Key != s.Mission.DefinitionID.String() || identity.Version != uint64(s.Mission.DefinitionVersion) || identity.Digest != s.Mission.DefinitionCommitment.String() {
		return refusal(KindInvalidStoredState, RecoveryRepairStoredState, "next.work_revision", "current revision definition is inconsistent")
	}
	return nil
}
func validNextFinding(f workgraph.Finding) bool {
	known := false
	switch f.Kind {
	case workgraph.FindingMissingContext, workgraph.FindingMalformedCriterion, workgraph.FindingInvalidBoundary, workgraph.FindingCriterionNotObservable, workgraph.FindingWorkOverBroad, workgraph.FindingUnrelatedOwnership, workgraph.FindingMissingCoverage, workgraph.FindingOrphanWork, workgraph.FindingUnknownScope, workgraph.FindingScopeKindMismatch, workgraph.FindingOwnershipConflict, workgraph.FindingUnknownDependency, workgraph.FindingSelfDependency, workgraph.FindingDuplicateDependency, workgraph.FindingDependencyCycle, workgraph.FindingUnknownLane, workgraph.FindingMissingLaneMembership, workgraph.FindingDuplicateLaneMembership, workgraph.FindingLaneSelfEdge, workgraph.FindingLaneCycle, workgraph.FindingOrderingConstraintLost:
		known = true
	}
	if !known || f.Severity != workgraph.SeverityError || (f.Recovery != workgraph.RecoveryReviseWork && f.Recovery != workgraph.RecoveryReviseProjection) || !boundedFactText(f.Field, 128) || !boundedFactText(f.Message, 512) {
		return false
	}
	if f.WorkID != "" {
		if _, err := ids.ParseWorkID(f.WorkID.String()); err != nil {
			return false
		}
	}
	if f.RelatedWorkID != "" {
		if _, err := ids.ParseWorkID(f.RelatedWorkID.String()); err != nil {
			return false
		}
	}
	return true
}
func finalizedNext(base NextBase, s NextSnapshot) (NextResult, error) {
	if s.CurrentWorkRevision == nil {
		return NextResult{}, refusal(KindInvalidStoredState, RecoveryRepairStoredState, "next.work_revision", "finalized mission lacks current revision")
	}
	r := *s.CurrentWorkRevision
	if r.ID() != s.Mission.CurrentWorkRevisionID || r.MissionID() != s.Mission.MissionID || r.ProjectID() != s.Mission.ProjectID || r.MissionDefinition().Key != s.Mission.DefinitionID.String() || r.MissionDefinition().Version != uint64(s.Mission.DefinitionVersion) || r.MissionDefinition().Digest != s.Mission.DefinitionCommitment.String() {
		return NextResult{}, refusal(KindInvalidStoredState, RecoveryRepairStoredState, "next.work_revision", "current revision does not match mission authority")
	}
	work := make([]ids.WorkID, 0, len(r.WorkItems()))
	for _, item := range r.WorkItems() {
		work = append(work, item.WorkID())
	}
	return NewWorkReadyResult(base, r.ID(), work)
}
func queryNext(base NextBase, kind, message string) (NextResult, error) {
	return NewQueryResult(base, NextDiagnostic{Kind: kind, RetrySafety: RetrySafe, Recovery: RecoverySupplyEvidence, Message: message})
}
func missingArtifactInput(required []ArtifactKind, observations []artifact.CurrentApprovalObservation, missionID ids.MissionID) bool {
	for _, want := range required {
		kind, ok := artifactKind(want)
		if !ok {
			continue
		}
		found := false
		for _, obs := range observations {
			r := obs.CurrentRevision()
			if r.MissionID() != missionID.String() {
				continue
			}
			if r.Kind() == kind {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}
	return false
}
func approvalFresh(policy ApprovalPolicy, observations []artifact.CurrentApprovalObservation, missionID ids.MissionID) bool {
	if policy == ApprovalNone {
		return true
	}
	want := artifact.KindSpecification
	if policy == ApprovalPlan {
		want = artifact.KindPlan
	}
	for _, obs := range observations {
		r := obs.CurrentRevision()
		if r.MissionID() != missionID.String() || r.Kind() != want {
			continue
		}
		fresh, err := artifact.EvaluateFreshness(r.ID(), obs.Approvals())
		return err == nil && fresh.Status == artifact.FreshnessFresh
	}
	return false
}
func artifactKind(k ArtifactKind) (artifact.Kind, bool) {
	switch k {
	case ArtifactIntent:
		return artifact.KindIntent, true
	case ArtifactSpecification:
		return artifact.KindSpecification, true
	case ArtifactPlan:
		return artifact.KindPlan, true
	}
	return "", false
}
func sameDecisionCensus(idsIn []ids.DecisionID, values []Decision) bool {
	want := append([]ids.DecisionID(nil), idsIn...)
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(want) != len(values) {
		return false
	}
	for i := range want {
		if want[i] != values[i].ID() {
			return false
		}
	}
	return true
}
func sameBlockerCensus(idsIn []ids.BlockerID, values []Blocker) bool {
	want := append([]ids.BlockerID(nil), idsIn...)
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(want) != len(values) {
		return false
	}
	for i := range want {
		if want[i] != values[i].ID() {
			return false
		}
	}
	return true
}
