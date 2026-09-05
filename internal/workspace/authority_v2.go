package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

const (
	WorkspaceEventSchemaV2  = uint32(1)
	WorkspaceReducerV2      = uint32(1)
	workspaceStateSchemaV2  = uint16(1)
	MaxWorkspacePathBytesV2 = 4096
)

type StageV2 string

const (
	StageBindingCreatedV2        StageV2 = "binding_created"
	StageCreationRequestedV2     StageV2 = "creation_requested"
	StageWorktreeObservedV2      StageV2 = "worktree_observed"
	StageBaseVerifiedV2          StageV2 = "base_verified"
	StageDependenciesRequestedV2 StageV2 = "dependencies_requested"
	StageDependenciesObservedV2  StageV2 = "dependencies_observed"
	StageOwnershipVerifiedV2     StageV2 = "ownership_verified"
	StageReadyV2                 StageV2 = "ready"
	StageReconciliationV2        StageV2 = "reconciliation_required"
	StageQuarantinedV2           StageV2 = "quarantined"
)

type DependencyV2 struct {
	WorkID ids.WorkID `json:"work_id"`
	Commit string     `json:"commit"`
}

// PathIdentityV2 records the bounded facts established by the filesystem
// adapter before a binding is eligible for publication. The pure workspace
// model validates the facts but never attempts to infer them from the host.
type PathIdentityV2 struct {
	Absolute          bool `json:"absolute"`
	Canonical         bool `json:"canonical"`
	WithinManagedRoot bool `json:"within_managed_root"`
	AliasFree         bool `json:"alias_free"`
	SymlinkFree       bool `json:"symlink_free"`
}

func (p PathIdentityV2) valid() bool {
	return p.Absolute && p.Canonical && p.WithinManagedRoot && p.AliasFree && p.SymlinkFree
}

type BindingV2 struct {
	WorkspaceID     ids.WorkspaceID          `json:"workspace_id"`
	ProjectID       ids.ProjectID            `json:"project_id"`
	MissionID       ids.MissionID            `json:"mission_id"`
	WorkRevisionID  ids.WorkRevisionID       `json:"work_revision_id"`
	WorkID          ids.WorkID               `json:"work_id"`
	RunID           ids.RunID                `json:"run_id"`
	LeaseID         ids.LeaseID              `json:"lease_id"`
	Generation      uint64                   `json:"generation"`
	Fence           string                   `json:"fence"`
	RuntimeEpochID  journalv2.RuntimeEpochID `json:"runtime_epoch_id"`
	ActorID         ids.ActorID              `json:"actor_id"`
	ProcessID       ids.ProcessID            `json:"process_id"`
	RepositoryID    ids.RepositoryID         `json:"repository_id"`
	LaneID          ids.LaneID               `json:"lane_id"`
	ManagedRoot     string                   `json:"managed_root"`
	CanonicalPath   string                   `json:"canonical_path"`
	PathIdentity    PathIdentityV2           `json:"path_identity"`
	PathClaimDigest journalv2.Digest         `json:"path_claim_digest"`
	OwnedBranch     string                   `json:"owned_branch"`
	PinnedBase      string                   `json:"pinned_base"`
	Dependencies    []DependencyV2           `json:"dependencies"`
}

func (b BindingV2) Validate() error {
	for _, parse := range []func() error{
		func() error { _, e := ids.ParseWorkspaceID(b.WorkspaceID.String()); return e },
		func() error { _, e := ids.ParseProjectID(b.ProjectID.String()); return e },
		func() error { _, e := ids.ParseMissionID(b.MissionID.String()); return e },
		func() error { _, e := ids.ParseWorkRevisionID(b.WorkRevisionID.String()); return e },
		func() error { _, e := ids.ParseWorkID(b.WorkID.String()); return e },
		func() error { _, e := ids.ParseRunID(b.RunID.String()); return e },
		func() error { _, e := ids.ParseLeaseID(b.LeaseID.String()); return e },
		func() error { _, e := ids.ParseActorID(b.ActorID.String()); return e },
		func() error { _, e := ids.ParseProcessID(b.ProcessID.String()); return e },
		func() error { _, e := ids.ParseRepositoryID(b.RepositoryID.String()); return e },
		func() error { _, e := ids.ParseLaneID(b.LaneID.String()); return e },
		func() error { _, e := journalv2.NewRuntimeEpochID(string(b.RuntimeEpochID)); return e },
	} {
		if err := parse(); err != nil {
			return workspaceV2Error(KindInvalid, "binding_identity", err)
		}
	}
	if b.Generation == 0 || len(b.Fence) < 16 || len(b.Fence) > journalv2.MaxIdentityBytes || b.PathClaimDigest.IsZero() || !b.PathIdentity.valid() {
		return workspaceV2Error(KindInvalid, "binding_authority", nil)
	}
	if err := ValidateCanonicalPathV2(b.ManagedRoot, b.CanonicalPath); err != nil {
		return err
	}
	if !validBranch(b.OwnedBranch) || !validObjectID(b.PinnedBase) {
		return workspaceV2Error(KindInvalid, "repository_target", nil)
	}
	deps := append([]DependencyV2(nil), b.Dependencies...)
	sort.Slice(deps, func(i, j int) bool { return deps[i].WorkID < deps[j].WorkID })
	for i := range deps {
		if _, err := ids.ParseWorkID(deps[i].WorkID.String()); err != nil || !validObjectID(deps[i].Commit) || i > 0 && deps[i-1].WorkID == deps[i].WorkID {
			return workspaceV2Error(KindInvalid, "dependencies", err)
		}
		if deps[i] != b.Dependencies[i] {
			return workspaceV2Error(KindInvalid, "dependencies_order", nil)
		}
	}
	claim, err := WorkspacePathClaimDigestV2(b.RepositoryID, b.ManagedRoot, b.CanonicalPath)
	if err != nil || claim != b.PathClaimDigest {
		return workspaceV2Error(KindInvalid, "path_claim", err)
	}
	return nil
}

func ValidateCanonicalPathV2(managedRoot, canonicalPath string) error {
	if !utf8.ValidString(managedRoot) || !utf8.ValidString(canonicalPath) || len(managedRoot) == 0 || len(canonicalPath) == 0 || len(managedRoot) > MaxWorkspacePathBytesV2 || len(canonicalPath) > MaxWorkspacePathBytesV2 {
		return workspaceV2Error(KindPathUnsafe, "path", nil)
	}
	if !strings.HasPrefix(managedRoot, "/") || !strings.HasPrefix(canonicalPath, "/") || path.Clean(managedRoot) != managedRoot || path.Clean(canonicalPath) != canonicalPath || managedRoot == "/" || canonicalPath == managedRoot || !strings.HasPrefix(canonicalPath, managedRoot+"/") {
		return workspaceV2Error(KindPathUnsafe, "path", nil)
	}
	return nil
}

func WorkspacePathClaimDigestV2(repositoryID ids.RepositoryID, managedRoot, canonicalPath string) (journalv2.Digest, error) {
	if _, err := ids.ParseRepositoryID(repositoryID.String()); err != nil {
		return journalv2.Digest{}, workspaceV2Error(KindInvalid, "repository_id", err)
	}
	if err := ValidateCanonicalPathV2(managedRoot, canonicalPath); err != nil {
		return journalv2.Digest{}, err
	}
	sum := sha256.Sum256([]byte("spec-kitty/workspace-path-claim/v1\x00" + repositoryID.String() + "\x00" + managedRoot + "\x00" + canonicalPath))
	return journalv2.ParseDigest("workspace_path_claim", hex.EncodeToString(sum[:]))
}

type ObservationV2 struct {
	Registered     bool   `json:"registered"`
	Head           string `json:"head"`
	Branch         string `json:"branch"`
	GitDirIdentity string `json:"git_dir_identity"`
	IndexIdentity  string `json:"index_identity"`
	Clean          bool   `json:"clean"`
	DependencyHead string `json:"dependency_head,omitempty"`
	TargetBefore   string `json:"target_before,omitempty"`
	TargetAfter    string `json:"target_after,omitempty"`
}

func (o ObservationV2) validate() error {
	for _, value := range []string{o.Head, o.Branch, o.GitDirIdentity, o.IndexIdentity, o.DependencyHead, o.TargetBefore, o.TargetAfter} {
		if len(value) > journalv2.MaxIdentityBytes || !utf8.ValidString(value) {
			return workspaceV2Error(KindInvalid, "observation", nil)
		}
	}
	return nil
}

type StateV2 struct {
	SchemaVersion    uint16             `json:"schema_version"`
	LogicalVersion   uint64             `json:"logical_version"`
	Binding          BindingV2          `json:"binding"`
	Stage            StageV2            `json:"stage"`
	Observation      ObservationV2      `json:"observation"`
	EffectID         journalv2.EffectID `json:"effect_id,omitempty"`
	QuarantineReason string             `json:"quarantine_reason,omitempty"`
	OccurredAt       string             `json:"occurred_at"`
}

func (s StateV2) Validate() error {
	if s.SchemaVersion != workspaceStateSchemaV2 || s.LogicalVersion == 0 || s.Binding.Validate() != nil || s.Observation.validate() != nil || len(s.OccurredAt) == 0 || len(s.OccurredAt) > journalv2.MaxIdentityBytes {
		return workspaceV2Error(KindInvalid, "workspace_state", nil)
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, s.OccurredAt)
	if err != nil || occurredAt.Location() != time.UTC {
		return workspaceV2Error(KindInvalid, "occurred_at", err)
	}
	switch s.Stage {
	case StageBindingCreatedV2:
		if s.QuarantineReason != "" {
			return workspaceV2Error(KindInvalid, "quarantine", nil)
		}
	case StageCreationRequestedV2:
		if s.QuarantineReason != "" || s.EffectID == "" {
			return workspaceV2Error(KindInvalid, "creation_intent", nil)
		}
	case StageWorktreeObservedV2:
		if s.QuarantineReason != "" || !s.Observation.Registered {
			return workspaceV2Error(KindInvalid, "worktree_observation", nil)
		}
	case StageBaseVerifiedV2, StageDependenciesRequestedV2:
		if s.QuarantineReason != "" || !s.Observation.Registered || s.Observation.Head != s.Binding.PinnedBase {
			return workspaceV2Error(KindInvalid, "base_observation", nil)
		}
	case StageDependenciesObservedV2:
		if s.QuarantineReason != "" || !dependenciesPreparedV2(s.Binding, s.Observation) {
			return workspaceV2Error(KindInvalid, "dependency_observation", nil)
		}
	case StageOwnershipVerifiedV2:
		if s.QuarantineReason != "" || !ownedWorkspaceObservationV2(s.Binding, s.Observation) {
			return workspaceV2Error(KindInvalid, "ownership_observation", nil)
		}
	case StageReadyV2:
		if s.QuarantineReason != "" || !ownedWorkspaceObservationV2(s.Binding, s.Observation) || s.Observation.TargetBefore != s.Binding.PinnedBase || s.Observation.TargetAfter != s.Observation.TargetBefore {
			return workspaceV2Error(KindInvalid, "ready_observation", nil)
		}
	case StageReconciliationV2:
		if s.EffectID == "" {
			return workspaceV2Error(KindInvalid, "effect", nil)
		}
	case StageQuarantinedV2:
		if s.QuarantineReason == "" || len(s.QuarantineReason) > journalv2.MaxIdentityBytes {
			return workspaceV2Error(KindInvalid, "quarantine", nil)
		}
	default:
		return workspaceV2Error(KindInvalid, "stage", nil)
	}
	return nil
}

func (s StateV2) CanonicalBytes() []byte { value, _ := encodeStateV2(s); return value }

func encodeStateV2(s StateV2) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(raw)
}

func DecodeStateV2(encoded []byte) (StateV2, error) {
	var state StateV2
	if err := strictWorkspaceJSONV2(encoded, &state); err != nil {
		return StateV2{}, err
	}
	reencoded, err := encodeStateV2(state)
	if err != nil || !bytes.Equal(reencoded, encoded) {
		return StateV2{}, workspaceV2Error(KindInvalid, "state", err)
	}
	state.Binding.Dependencies = append([]DependencyV2(nil), state.Binding.Dependencies...)
	return state, nil
}

type EventV2 struct {
	SchemaVersion uint16  `json:"schema_version"`
	Kind          string  `json:"kind"`
	PriorDigest   string  `json:"prior_digest,omitempty"`
	State         StateV2 `json:"state"`
}

func encodeEventV2(event EventV2) ([]byte, error) {
	if event.SchemaVersion != 1 || event.State.Validate() != nil || len(event.Kind) == 0 || len(event.Kind) > 64 {
		return nil, workspaceV2Error(KindInvalid, "event", nil)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(raw)
}

func DecodeEventV2(encoded []byte) (EventV2, error) {
	var event EventV2
	if err := strictWorkspaceJSONV2(encoded, &event); err != nil {
		return EventV2{}, err
	}
	reencoded, err := encodeEventV2(event)
	if err != nil || !bytes.Equal(reencoded, encoded) {
		return EventV2{}, workspaceV2Error(KindInvalid, "event", err)
	}
	event.State.Binding.Dependencies = append([]DependencyV2(nil), event.State.Binding.Dependencies...)
	return event, nil
}

func WorkspaceCoordinateV2(binding BindingV2) (journalv2.AggregateCoordinate, error) {
	// WorkspaceID is host-global. The state binds its project/work coordinates;
	// putting those in the key scope would permit the same WorkspaceID to be
	// created a second time under a different work coordinate.
	return journalv2.NewAggregateCoordinate("workspace", journalv2.AggregateID(binding.WorkspaceID), journalv2.Scope{Kind: journalv2.ScopeHost})
}

func ReduceEventV2(prior []byte, envelope journalv2.EventEnvelopeV2) ([]byte, error) {
	if envelope.Family() != "workspace" || envelope.EventSchemaVersion() != WorkspaceEventSchemaV2 || envelope.ReducerVersion() != WorkspaceReducerV2 || envelope.Coordinate().Kind != "workspace" {
		return nil, workspaceV2Error(KindInvalid, "event_tuple", nil)
	}
	var event EventV2
	if err := strictWorkspaceJSONV2(envelope.Payload(), &event); err != nil {
		return nil, err
	}
	reencoded, err := encodeEventV2(event)
	if err != nil || !bytes.Equal(reencoded, envelope.Payload()) {
		return nil, workspaceV2Error(KindInvalid, "event", err)
	}
	next := event.State
	if envelope.EventType() != "workspace."+event.Kind {
		return nil, workspaceV2Error(KindInvalid, "event_type", nil)
	}
	coordinate, err := WorkspaceCoordinateV2(next.Binding)
	if err != nil || coordinate != envelope.Coordinate() || uint64(envelope.LogicalVersion()) != next.LogicalVersion || envelope.CommittedRuntimeEpochID() != next.Binding.RuntimeEpochID || envelope.ActorID() != journalv2.ActorID(next.Binding.ActorID) || envelope.ProcessID() != journalv2.ProcessID(next.Binding.ProcessID) || envelope.WorkRevisionID() != journalv2.WorkRevisionID(next.Binding.WorkRevisionID) {
		return nil, workspaceV2Error(KindStaleAuthority, "event_authority", err)
	}
	if len(prior) == 0 || bytes.Equal(prior, []byte(`{}`)) {
		if next.LogicalVersion != 1 || next.Stage != StageCreationRequestedV2 || event.Kind != "preparation_requested" || event.PriorDigest != "" {
			return nil, workspaceV2Error(KindWorkspaceConflict, "initial_transition", nil)
		}
		return next.CanonicalBytes(), nil
	}
	previous, err := DecodeStateV2(prior)
	if err != nil || next.LogicalVersion != previous.LogicalVersion+1 {
		return nil, workspaceV2Error(KindWorkspaceConflict, "binding_or_version", err)
	}
	priorDigest, _ := journalv2.ReducerStateDigestV2(previous.CanonicalBytes())
	if event.PriorDigest != priorDigest.String() {
		return nil, workspaceV2Error(KindWorkspaceConflict, "prior_digest", nil)
	}
	if event.Kind == "runtime_epoch_rebound" {
		if envelope.EventType() != "workspace.runtime_epoch_rebound" || previous.Stage == StageQuarantinedV2 || next.Stage != previous.Stage || previous.Binding.RuntimeEpochID == next.Binding.RuntimeEpochID || !bindingEqualIgnoringRuntimeEpochV2(previous.Binding, next.Binding) || next.EffectID != previous.EffectID || next.QuarantineReason != previous.QuarantineReason {
			return nil, workspaceV2Error(KindStaleAuthority, "runtime_epoch_rebind", nil)
		}
		return next.CanonicalBytes(), nil
	}
	if !bindingEqualV2(next.Binding, previous.Binding) {
		return nil, workspaceV2Error(KindWorkspaceConflict, "binding_or_version", nil)
	}
	allowed := map[StageV2]map[StageV2]string{
		StageCreationRequestedV2:     {StageWorktreeObservedV2: "preparation_observed", StageReconciliationV2: "reconciliation_required", StageQuarantinedV2: "quarantined"},
		StageWorktreeObservedV2:      {StageBaseVerifiedV2: "preparation_advanced", StageQuarantinedV2: "quarantined"},
		StageBaseVerifiedV2:          {StageDependenciesRequestedV2: "preparation_requested", StageQuarantinedV2: "quarantined"},
		StageDependenciesRequestedV2: {StageDependenciesObservedV2: "preparation_observed", StageReconciliationV2: "reconciliation_required", StageQuarantinedV2: "quarantined"},
		StageDependenciesObservedV2:  {StageOwnershipVerifiedV2: "preparation_advanced", StageQuarantinedV2: "quarantined"},
		StageOwnershipVerifiedV2:     {StageReadyV2: "ready_published", StageQuarantinedV2: "quarantined"},
		StageReconciliationV2:        {StageWorktreeObservedV2: "preparation_observed", StageDependenciesObservedV2: "preparation_observed", StageQuarantinedV2: "quarantined"},
		StageReadyV2:                 {StageQuarantinedV2: "quarantined"},
	}
	if allowed[previous.Stage][next.Stage] != event.Kind || previous.Stage == StageQuarantinedV2 || next.Stage != StageQuarantinedV2 && !validTransitionObservationV2(previous.Stage, next.Stage, next.Binding, next.Observation, next.EffectID) {
		return nil, workspaceV2Error(KindWorkspaceConflict, "stage_transition", nil)
	}
	return next.CanonicalBytes(), nil
}

func ReducerRegistrationsV2() []journalv2.ReducerRegistration {
	sum := sha256.Sum256([]byte("spec-kitty/workspace-reducer/1.0"))
	digest, _ := journalv2.ParseDigest("workspace_reducer", hex.EncodeToString(sum[:]))
	return []journalv2.ReducerRegistration{{Family: "workspace", EventSchemaVersion: WorkspaceEventSchemaV2, ReducerVersion: WorkspaceReducerV2, Owner: "internal/workspace", ImplementationDigest: digest, Reduce: ReduceEventV2}}
}

// AuthorityCapsuleV2 is a defensive, immutable snapshot of every execution
// and repository coordinate that makes a workspace current. Keeping the
// complete binding (rather than a hand-picked subset) makes additions to the
// binding fail closed at the current-workspace gate.
type AuthorityCapsuleV2 struct{ binding BindingV2 }

func NewAuthorityCapsuleV2(binding BindingV2) (AuthorityCapsuleV2, error) {
	if err := binding.Validate(); err != nil {
		return AuthorityCapsuleV2{}, err
	}
	return AuthorityCapsuleV2{binding: cloneBindingV2(binding)}, nil
}

func (c AuthorityCapsuleV2) Binding() BindingV2 { return cloneBindingV2(c.binding) }

func ValidateCurrentV2(state StateV2, capsule AuthorityCapsuleV2) error {
	if err := state.Validate(); err != nil {
		return workspaceV2Error(KindStaleAuthority, "workspace_state", err)
	}
	b := state.Binding
	expected := capsule.binding
	if expected.Validate() != nil {
		return workspaceV2Error(KindStaleAuthority, "workspace_capsule", nil)
	}
	if state.Stage == StageQuarantinedV2 {
		return workspaceV2Error(KindStaleAuthority, "workspace_quarantined", nil)
	}
	if state.Stage != StageReadyV2 {
		return workspaceV2Error(KindWorkspaceNotReady, "workspace_stage", nil)
	}
	if !bindingEqualV2(b, expected) {
		return workspaceV2Error(KindStaleAuthority, "workspace_capsule", nil)
	}
	return nil
}

func cloneBindingV2(binding BindingV2) BindingV2 {
	binding.Dependencies = append([]DependencyV2(nil), binding.Dependencies...)
	return binding
}

func bindingEqualV2(a, b BindingV2) bool { return bytes.Equal(mustJSONV2(a), mustJSONV2(b)) }
func bindingEqualIgnoringRuntimeEpochV2(a, b BindingV2) bool {
	a.RuntimeEpochID, b.RuntimeEpochID = "", ""
	return bindingEqualV2(a, b)
}
func mustJSONV2(value any) []byte {
	raw, _ := json.Marshal(value)
	canonical, _ := journalv2.CanonicalObject(raw)
	return canonical
}
func strictWorkspaceJSONV2(encoded []byte, destination any) error {
	if len(encoded) == 0 || len(encoded) > journalv2.MaxCanonicalPayloadBytes {
		return workspaceV2Error(KindInvalid, "payload_budget", nil)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return workspaceV2Error(KindInvalid, "payload", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return workspaceV2Error(KindInvalid, "payload_trailing", err)
	}
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return workspaceV2Error(KindInvalid, "payload_canonical", err)
	}
	return nil
}
func workspaceV2Error(kind ErrorKind, field string, cause error) error {
	return &Error{Kind: kind, Message: fmt.Sprintf("workspace authority refused: %s", field), Cause: cause}
}
