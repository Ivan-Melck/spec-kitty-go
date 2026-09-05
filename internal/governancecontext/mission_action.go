package governancecontext

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

const MissionActionSchemaVersion uint16 = 1
const CapabilityActionSchemaVersion uint16 = 2

// AuthorityGrant is deliberately closed. It records the smallest domain
// authority granted to an action; it is not a tool, filesystem, or transport
// permission.
type AuthorityGrant string

const (
	GrantMissionTransition AuthorityGrant = "mission_transition"
	GrantDecisionResolve   AuthorityGrant = "decision_resolve"
	GrantBlockerClear      AuthorityGrant = "blocker_clear"
	GrantLifecycleMutate   AuthorityGrant = "lifecycle_mutate"
	GrantActivityRecord    AuthorityGrant = "activity_record"
	GrantCapabilityExecute AuthorityGrant = "capability_execute"
)

func (g AuthorityGrant) Validate() error {
	switch g {
	case GrantMissionTransition, GrantDecisionResolve, GrantBlockerClear,
		GrantLifecycleMutate, GrantActivityRecord, GrantCapabilityExecute:
		return nil
	default:
		return fmt.Errorf("unsupported mission authority grant")
	}
}

// CapabilityAction is the schema-v2 governance binding used by autonomous
// readiness. It binds a concrete scoped capability and contains no phase or
// successor field. MissionAction remains schema-v1 decode compatibility.
type CapabilityAction struct {
	schemaVersion uint16
	contextID     ids.GovernanceContextID
	aggregate     Commitment
	missionID     ids.MissionID
	actionID      string
	scopeKind     string
	scopeIdentity string
	scopeDigest   string
	actorID       ids.ActorID
	grants        []AuthorityGrant
	entries       []Entry
	commitment    Commitment
}

type CapabilityActionParams struct {
	Source          Context
	MissionID       ids.MissionID
	ActionID        string
	ScopeKind       string
	ScopeIdentity   string
	ScopeDigest     string
	ActorID         ids.ActorID
	SelectedEntries []Entry
}

func NewCapabilityAction(params CapabilityActionParams) (CapabilityAction, error) {
	if params.Source.Validate() != nil || !supportedCapabilityAction(params.ActionID) {
		return CapabilityAction{}, actionInvalid("invalid_action", "action_id")
	}
	if _, err := ids.ParseMissionID(params.MissionID.String()); err != nil || !supportedCapabilityScope(params.ScopeKind) || !boundedActionText(params.ScopeIdentity, 128) || !validCapabilityDigest(params.ScopeDigest) {
		return CapabilityAction{}, actionInvalid("invalid_scope", "scope")
	}
	if _, err := ids.ParseActorID(params.ActorID.String()); err != nil {
		return CapabilityAction{}, actionInvalid("invalid_actor", "actor_id")
	}
	required := map[EntryKind]bool{KindProfile: true, KindMissionType: true, KindDoctrine: true}
	if len(params.SelectedEntries) != len(required) {
		return CapabilityAction{}, actionInvalid("entry_set_mismatch", "selected_entries")
	}
	sourceEntries := map[string]Entry{}
	for _, entry := range params.Source.Entries() {
		sourceEntries[string(entry.Kind())+"\x00"+entry.Ref()] = entry
	}
	entries := append([]Entry(nil), params.SelectedEntries...)
	seen := map[EntryKind]bool{}
	for _, entry := range entries {
		exact, ok := sourceEntries[string(entry.Kind())+"\x00"+entry.Ref()]
		if entry.Validate() != nil || !required[entry.Kind()] || seen[entry.Kind()] || !ok || exact.Revision() != entry.Revision() || exact.Commitment() != entry.Commitment() {
			return CapabilityAction{}, actionInvalid("invalid_selection", "selected_entries")
		}
		seen[entry.Kind()] = true
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind() == entries[j].Kind() {
			return entries[i].Ref() < entries[j].Ref()
		}
		return entries[i].Kind() < entries[j].Kind()
	})
	grants := []AuthorityGrant{GrantCapabilityExecute}
	if strings.HasPrefix(params.ActionID, "mission.") {
		grants = []AuthorityGrant{GrantLifecycleMutate}
	} else if params.ActionID == "decision.resolve" {
		grants = []AuthorityGrant{GrantDecisionResolve}
	} else if params.ActionID == "blocker.clear" {
		grants = []AuthorityGrant{GrantBlockerClear}
	}
	value := CapabilityAction{schemaVersion: CapabilityActionSchemaVersion, contextID: params.Source.ID(), aggregate: params.Source.Aggregate(), missionID: params.MissionID, actionID: params.ActionID, scopeKind: params.ScopeKind, scopeIdentity: params.ScopeIdentity, scopeDigest: params.ScopeDigest, actorID: params.ActorID, grants: grants, entries: entries}
	commitment, err := value.computeCommitment()
	if err != nil {
		return CapabilityAction{}, actionInvalid("commitment_failure", "commitment")
	}
	value.commitment = commitment
	return value, nil
}

func (a CapabilityAction) SchemaVersion() uint16              { return a.schemaVersion }
func (a CapabilityAction) ContextID() ids.GovernanceContextID { return a.contextID }
func (a CapabilityAction) MissionID() ids.MissionID           { return a.missionID }
func (a CapabilityAction) ActionID() string                   { return a.actionID }
func (a CapabilityAction) ScopeKind() string                  { return a.scopeKind }
func (a CapabilityAction) ScopeIdentity() string              { return a.scopeIdentity }
func (a CapabilityAction) ScopeDigest() string                { return a.scopeDigest }
func (a CapabilityAction) ActorID() ids.ActorID               { return a.actorID }
func (a CapabilityAction) Grants() []AuthorityGrant {
	return append([]AuthorityGrant(nil), a.grants...)
}
func (a CapabilityAction) Entries() []Entry       { return append([]Entry(nil), a.entries...) }
func (a CapabilityAction) Commitment() Commitment { return a.commitment }

func (a CapabilityAction) Validate() error {
	if a.schemaVersion != CapabilityActionSchemaVersion || a.contextID == "" || a.aggregate.Validate() != nil || !supportedCapabilityAction(a.actionID) || !supportedCapabilityScope(a.scopeKind) || !boundedActionText(a.scopeIdentity, 128) || !validCapabilityDigest(a.scopeDigest) || a.commitment.Validate() != nil {
		return actionInvalid("invalid_capability", "capability_action")
	}
	if _, err := ids.ParseMissionID(a.missionID.String()); err != nil {
		return actionInvalid("invalid_scope", "mission_id")
	}
	if _, err := ids.ParseActorID(a.actorID.String()); err != nil || len(a.grants) != 1 || a.grants[0].Validate() != nil || len(a.entries) != 3 {
		return actionInvalid("invalid_set", "authority")
	}
	wantGrant := GrantCapabilityExecute
	if strings.HasPrefix(a.actionID, "mission.") {
		wantGrant = GrantLifecycleMutate
	} else if a.actionID == "decision.resolve" {
		wantGrant = GrantDecisionResolve
	} else if a.actionID == "blocker.clear" {
		wantGrant = GrantBlockerClear
	}
	if a.grants[0] != wantGrant {
		return actionInvalid("invalid_grants", "grants")
	}
	required := map[EntryKind]bool{KindProfile: true, KindMissionType: true, KindDoctrine: true}
	seen := map[EntryKind]bool{}
	for index, entry := range a.entries {
		if entry.Validate() != nil || !required[entry.Kind()] || seen[entry.Kind()] || index > 0 && (a.entries[index-1].Kind() > entry.Kind() || a.entries[index-1].Kind() == entry.Kind() && a.entries[index-1].Ref() >= entry.Ref()) {
			return actionInvalid("invalid_selection", "entries")
		}
		seen[entry.Kind()] = true
	}
	expected, err := a.computeCommitment()
	if err != nil || expected != a.commitment {
		return actionInvalid("commitment_mismatch", "commitment")
	}
	return nil
}

type capabilityActionWire struct {
	SchemaVersion uint16                  `json:"schema_version"`
	ContextID     ids.GovernanceContextID `json:"context_id"`
	Aggregate     Commitment              `json:"aggregate"`
	MissionID     ids.MissionID           `json:"mission_id"`
	ActionID      string                  `json:"action_id"`
	ScopeKind     string                  `json:"scope_kind"`
	ScopeIdentity string                  `json:"scope_identity"`
	ScopeDigest   string                  `json:"scope_digest,omitempty"`
	ActorID       ids.ActorID             `json:"actor_id"`
	Grants        []AuthorityGrant        `json:"grants"`
	Entries       []Entry                 `json:"entries"`
	Commitment    Commitment              `json:"commitment"`
}

func (a CapabilityAction) MarshalJSON() ([]byte, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(capabilityActionWire{a.schemaVersion, a.contextID, a.aggregate, a.missionID, a.actionID, a.scopeKind, a.scopeIdentity, a.scopeDigest, a.actorID, a.Grants(), a.Entries(), a.commitment})
}

func (a *CapabilityAction) UnmarshalJSON(encoded []byte) error {
	if a == nil {
		return actionInvalid("invalid_target", "capability_action")
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	var wire capabilityActionWire
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	value := CapabilityAction{schemaVersion: wire.SchemaVersion, contextID: wire.ContextID, aggregate: wire.Aggregate, missionID: wire.MissionID, actionID: wire.ActionID, scopeKind: wire.ScopeKind, scopeIdentity: wire.ScopeIdentity, scopeDigest: wire.ScopeDigest, actorID: wire.ActorID, grants: append([]AuthorityGrant(nil), wire.Grants...), entries: append([]Entry(nil), wire.Entries...), commitment: wire.Commitment}
	if err := value.Validate(); err != nil {
		return err
	}
	*a = value
	return nil
}

func (a CapabilityAction) computeCommitment() (Commitment, error) {
	payload, err := json.Marshal(struct {
		Domain        string           `json:"domain"`
		SchemaVersion uint16           `json:"schema_version"`
		ContextID     string           `json:"context_id"`
		Aggregate     Commitment       `json:"aggregate"`
		MissionID     ids.MissionID    `json:"mission_id"`
		ActionID      string           `json:"action_id"`
		ScopeKind     string           `json:"scope_kind"`
		ScopeIdentity string           `json:"scope_identity"`
		ScopeDigest   string           `json:"scope_digest,omitempty"`
		ActorID       ids.ActorID      `json:"actor_id"`
		Grants        []AuthorityGrant `json:"grants"`
		Entries       []Entry          `json:"entries"`
	}{"spec-kitty/governance-capability-action/v2", a.schemaVersion, a.contextID.String(), a.aggregate, a.missionID, a.actionID, a.scopeKind, a.scopeIdentity, a.scopeDigest, a.actorID, a.grants, a.entries})
	if err != nil {
		return Commitment{}, err
	}
	return CommitSHA256(payload)
}

func supportedCapabilityScope(value string) bool {
	switch value {
	case "mission", "action", "work", "artifact", "criterion", "policy":
		return true
	default:
		return false
	}
}

func supportedCapabilityAction(value string) bool {
	switch value {
	case "mission.create", "mission.pause", "mission.resume", "mission.fail", "mission.cancel", "mission.complete",
		"artifact.observe", "artifact.publish", "workgraph.observe", "work.finalize", "work.reconcile",
		"execution.claim", "execution.heartbeat", "execution.finish", "workspace.allocate", "workspace.quarantine",
		"candidate.handoff", "review.record", "acceptance.evaluate", "integration.publish", "effect.dispatch",
		"effect.reconcile", "conflict.resolve", "policy.resolve", "decision.resolve", "blocker.clear", "verification.run":
		return true
	default:
		return false
	}
}

func validCapabilityDigest(value string) bool {
	if value == "" {
		return true
	}
	_, err := NewCommitment(AlgorithmSHA256, value)
	return err == nil
}

type ActionValidationError struct {
	Code  string `json:"code"`
	Field string `json:"field"`
}

func (e *ActionValidationError) Error() string {
	return "mission action governance: code=" + e.Code + " field=" + e.Field
}

func actionInvalid(code, field string) error { return &ActionValidationError{Code: code, Field: field} }

type MissionActionParams struct {
	Source          Context
	MissionID       ids.MissionID
	StepKey         string
	ActionKey       string
	ActorID         ids.ActorID
	SelectedEntries []Entry
}

type MissionAction struct {
	schemaVersion uint16
	contextID     ids.GovernanceContextID
	contextSchema uint16
	aggregate     Commitment
	missionID     ids.MissionID
	stepKey       string
	actionKey     string
	actorID       ids.ActorID
	requiredRole  string
	grants        []AuthorityGrant
	entries       []Entry
	commitment    Commitment
}

func NewMissionAction(params MissionActionParams) (MissionAction, error) {
	if err := params.Source.Validate(); err != nil {
		return MissionAction{}, actionInvalid("invalid_source", "source")
	}
	required, grants, err := missionActionPolicy(params.ActionKey)
	if err != nil {
		return MissionAction{}, err
	}
	if _, err := ids.ParseMissionID(params.MissionID.String()); err != nil {
		return MissionAction{}, actionInvalid("invalid_scope", "mission_id")
	}
	if !boundedActionText(params.StepKey, 64) || !boundedActionText(params.ActionKey, 128) {
		return MissionAction{}, actionInvalid("invalid_action", "action")
	}
	if _, err := ids.ParseOperationID(params.ActionKey); err != nil {
		return MissionAction{}, actionInvalid("invalid_action", "action_key")
	}
	if _, err := ids.ParseActorID(params.ActorID.String()); err != nil {
		return MissionAction{}, actionInvalid("invalid_actor", "actor_id")
	}
	source := make(map[string]Entry)
	for _, entry := range params.Source.Entries() {
		source[string(entry.Kind())+"\x00"+entry.Ref()] = entry
	}
	entries := append([]Entry(nil), params.SelectedEntries...)
	if len(entries) != len(required) {
		return MissionAction{}, actionInvalid("entry_set_mismatch", "selected_entries")
	}
	seen := make(map[EntryKind]bool, len(entries))
	requiredRole := ""
	for _, entry := range entries {
		if entry.Validate() != nil || !required[entry.Kind()] || seen[entry.Kind()] {
			return MissionAction{}, actionInvalid("invalid_selection", "selected_entries")
		}
		if !boundedActionText(entry.Ref(), 128) || !boundedActionText(entry.Revision(), 128) {
			return MissionAction{}, actionInvalid("unbounded_selection", "selected_entries")
		}
		exact, ok := source[string(entry.Kind())+"\x00"+entry.Ref()]
		if !ok || exact.Revision() != entry.Revision() || exact.Commitment() != entry.Commitment() {
			return MissionAction{}, actionInvalid("source_mismatch", "selected_entries")
		}
		seen[entry.Kind()] = true
		if entry.Kind() == KindProfile {
			requiredRole = entry.Ref()
		}
	}
	for kind := range required {
		if !seen[kind] {
			return MissionAction{}, actionInvalid("missing_required_entry", "selected_entries")
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind() == entries[j].Kind() {
			return entries[i].Ref() < entries[j].Ref()
		}
		return entries[i].Kind() < entries[j].Kind()
	})
	value := MissionAction{schemaVersion: MissionActionSchemaVersion, contextID: params.Source.ID(), contextSchema: params.Source.SchemaVersion(), aggregate: params.Source.Aggregate(), missionID: params.MissionID, stepKey: params.StepKey, actionKey: params.ActionKey, actorID: params.ActorID, requiredRole: requiredRole, grants: grants, entries: entries}
	commitment, err := value.computeCommitment()
	if err != nil {
		return MissionAction{}, actionInvalid("commitment_failure", "commitment")
	}
	value.commitment = commitment
	return value, nil
}

func missionActionPolicy(action string) (map[EntryKind]bool, []AuthorityGrant, error) {
	missionActions := map[string]bool{
		"mission.specify": true, "mission.approve-specification": true,
		"mission.plan": true, "mission.approve-plan": true,
		"mission.decompose": true, "mission.validate-work": true,
		"mission.reconcile": true, "mission.finalize-work": true,
	}
	grant := AuthorityGrant("")
	switch {
	case missionActions[action]:
		grant = GrantMissionTransition
	case action == "mission.resolve-decision" || action == "mission.defer-decision" || action == "mission.cancel-decision":
		grant = GrantDecisionResolve
	case action == "mission.clear-blocker":
		grant = GrantBlockerClear
	default:
		return nil, nil, actionInvalid("unsupported_action", "action_key")
	}
	required := map[EntryKind]bool{KindProfile: true, KindMissionType: true}
	if missionActions[action] {
		// Lifecycle mutations execute configured mission behavior under doctrine.
		required[KindConfig], required[KindDoctrine] = true, true
	} else {
		// Fact dispositions are governed by doctrine but do not consume runtime
		// configuration; selecting config here would be unrelated overcollection.
		required[KindDoctrine] = true
	}
	return required, []AuthorityGrant{grant}, nil
}

func (a MissionAction) SchemaVersion() uint16              { return a.schemaVersion }
func (a MissionAction) ContextID() ids.GovernanceContextID { return a.contextID }
func (a MissionAction) ContextSchemaVersion() uint16       { return a.contextSchema }
func (a MissionAction) Aggregate() Commitment              { return a.aggregate }
func (a MissionAction) MissionID() ids.MissionID           { return a.missionID }
func (a MissionAction) StepKey() string                    { return a.stepKey }
func (a MissionAction) ActionKey() string                  { return a.actionKey }
func (a MissionAction) ActorID() ids.ActorID               { return a.actorID }
func (a MissionAction) RequiredRole() string               { return a.requiredRole }
func (a MissionAction) Grants() []AuthorityGrant           { return append([]AuthorityGrant(nil), a.grants...) }
func (a MissionAction) Entries() []Entry                   { return append([]Entry(nil), a.entries...) }
func (a MissionAction) Commitment() Commitment             { return a.commitment }

func (a MissionAction) Validate() error {
	if a.schemaVersion != MissionActionSchemaVersion || a.contextSchema != SchemaVersion {
		return actionInvalid("unsupported_schema", "schema_version")
	}
	if _, err := ids.ParseGovernanceContextID(a.contextID.String()); err != nil || a.aggregate.Validate() != nil {
		return actionInvalid("invalid_context", "context")
	}
	// Restore validation deliberately checks the immutable bounded projection;
	// source membership was checked at construction and is evidenced by the
	// sealed context identity and aggregate.
	params := MissionActionParams{MissionID: a.missionID, StepKey: a.stepKey, ActionKey: a.actionKey, ActorID: a.actorID}
	if _, err := ids.ParseMissionID(params.MissionID.String()); err != nil || !boundedActionText(params.StepKey, 64) || !boundedActionText(params.ActionKey, 128) {
		return actionInvalid("invalid_scope", "mission_action")
	}
	if _, err := ids.ParseOperationID(params.ActionKey); err != nil {
		return actionInvalid("invalid_action", "action_key")
	}
	if _, err := ids.ParseActorID(params.ActorID.String()); err != nil || !boundedActionText(a.requiredRole, 128) {
		return actionInvalid("invalid_actor", "actor")
	}
	required, grants, err := missionActionPolicy(a.actionKey)
	if err != nil || len(a.grants) != len(grants) || len(a.entries) != len(required) {
		return actionInvalid("invalid_set", "entries")
	}
	seenKinds := map[EntryKind]bool{}
	for i, grant := range a.grants {
		if grant.Validate() != nil || grant != grants[i] || (i > 0 && a.grants[i-1] >= grant) {
			return actionInvalid("invalid_grants", "grants")
		}
	}
	for i, entry := range a.entries {
		if entry.Validate() != nil || !boundedActionText(entry.Ref(), 128) || !boundedActionText(entry.Revision(), 128) || seenKinds[entry.Kind()] {
			return actionInvalid("invalid_selection", "entries")
		}
		if i > 0 && (a.entries[i-1].Kind() > entry.Kind() || (a.entries[i-1].Kind() == entry.Kind() && a.entries[i-1].Ref() >= entry.Ref())) {
			return actionInvalid("noncanonical_selection", "entries")
		}
		seenKinds[entry.Kind()] = true
	}
	for kind := range required {
		if !seenKinds[kind] {
			return actionInvalid("missing_required_entry", "entries")
		}
	}
	for _, entry := range a.entries {
		if entry.Kind() == KindProfile && entry.Ref() != a.requiredRole {
			return actionInvalid("profile_role_mismatch", "required_role")
		}
	}
	if a.commitment.Validate() != nil {
		return actionInvalid("invalid_commitment", "commitment")
	}
	expected, err := a.computeCommitment()
	if err != nil || expected != a.commitment {
		return actionInvalid("commitment_mismatch", "commitment")
	}
	return nil
}

func (a MissionAction) Equal(other MissionAction) bool {
	left, err1 := json.Marshal(a)
	right, err2 := json.Marshal(other)
	return err1 == nil && err2 == nil && string(left) == string(right)
}

type missionActionJSON struct {
	SchemaVersion uint16                  `json:"schema_version"`
	ContextID     ids.GovernanceContextID `json:"context_id"`
	ContextSchema uint16                  `json:"context_schema_version"`
	Aggregate     Commitment              `json:"aggregate"`
	MissionID     ids.MissionID           `json:"mission_id"`
	StepKey       string                  `json:"step_key"`
	ActionKey     string                  `json:"action_key"`
	ActorID       ids.ActorID             `json:"actor_id"`
	RequiredRole  string                  `json:"required_role"`
	Grants        []AuthorityGrant        `json:"grants"`
	Entries       []Entry                 `json:"entries"`
	Commitment    Commitment              `json:"commitment"`
}

func (a MissionAction) MarshalJSON() ([]byte, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(missionActionJSON{a.schemaVersion, a.contextID, a.contextSchema, a.aggregate, a.missionID, a.stepKey, a.actionKey, a.actorID, a.requiredRole, a.Grants(), a.Entries(), a.commitment})
}

func (a *MissionAction) UnmarshalJSON(data []byte) error {
	var wire missionActionJSON
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	value := MissionAction{schemaVersion: wire.SchemaVersion, contextID: wire.ContextID, contextSchema: wire.ContextSchema, aggregate: wire.Aggregate, missionID: wire.MissionID, stepKey: wire.StepKey, actionKey: wire.ActionKey, actorID: wire.ActorID, requiredRole: wire.RequiredRole, grants: append([]AuthorityGrant(nil), wire.Grants...), entries: append([]Entry(nil), wire.Entries...), commitment: wire.Commitment}
	if err := value.Validate(); err != nil {
		return err
	}
	*a = value
	return nil
}

func (a MissionAction) computeCommitment() (Commitment, error) {
	canonical := struct {
		Domain        string
		Schema        uint16
		ContextID     ids.GovernanceContextID
		ContextSchema uint16
		Aggregate     Commitment
		MissionID     ids.MissionID
		StepKey       string
		ActionKey     string
		ActorID       ids.ActorID
		RequiredRole  string
		Grants        []AuthorityGrant
		Entries       []Entry
	}{"spec-kitty/mission-action-governance/v1", a.schemaVersion, a.contextID, a.contextSchema, a.aggregate, a.missionID, a.stepKey, a.actionKey, a.actorID, a.requiredRole, a.Grants(), a.Entries()}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return Commitment{}, err
	}
	return CommitSHA256(encoded)
}

func boundedActionText(value string, limit int) bool {
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
