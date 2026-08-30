package controljournal

import (
	"bytes"
	"encoding/json"
)

// AuthorityCapsuleV1 is the complete, frozen command-authority coordinate.
// It deliberately lives in the journal root so checkpoint, command, and
// adapter code cannot invent narrower look-alike capsules.
type AuthorityCapsuleV1 struct {
	SchemaVersion               uint32
	ProjectID                   ProjectID
	MissionID                   MissionID
	WorkRevisionID              WorkRevisionID
	ActorID                     ActorID
	ProcessID                   ProcessID
	RunID                       RunID
	LeaseID                     LeaseID
	WorkspaceID                 WorkspaceID
	RuntimeEpochID              RuntimeEpochID
	LeaseGeneration             uint64
	FenceToken                  uint64
	AuthorityHeadRecordRevision uint64
}

type authorityCapsuleWireV1 struct {
	ActorID                     string `json:"actor_id"`
	AuthorityHeadRecordRevision uint64 `json:"authority_head_record_revision"`
	FenceToken                  uint64 `json:"fence_token"`
	LeaseGeneration             uint64 `json:"lease_generation"`
	LeaseID                     string `json:"lease_id"`
	MissionID                   string `json:"mission_id"`
	ProcessID                   string `json:"process_id"`
	ProjectID                   string `json:"project_id"`
	RunID                       string `json:"run_id"`
	RuntimeEpochID              string `json:"runtime_epoch_id"`
	SchemaVersion               uint32 `json:"schema_version"`
	WorkRevisionID              string `json:"work_revision_id"`
	WorkspaceID                 string `json:"workspace_id"`
}

// NewAuthorityCapsuleV1 validates every coordinate before the capsule can be
// published. Passing a struct keeps construction readable while validation
// remains centralized and exact.
func NewAuthorityCapsuleV1(capsule AuthorityCapsuleV1) (AuthorityCapsuleV1, error) {
	if capsule.SchemaVersion != 1 {
		return AuthorityCapsuleV1{}, ValidationError{Kind: RefusalUnsupportedVersion, Field: "authority_capsule"}
	}
	if err := validateTokens(
		tokenField{"project_id", string(capsule.ProjectID)},
		tokenField{"mission_id", string(capsule.MissionID)},
		tokenField{"work_revision_id", string(capsule.WorkRevisionID)},
		tokenField{"actor_id", string(capsule.ActorID)},
		tokenField{"process_id", string(capsule.ProcessID)},
		tokenField{"run_id", string(capsule.RunID)},
		tokenField{"lease_id", string(capsule.LeaseID)},
		tokenField{"workspace_id", string(capsule.WorkspaceID)},
		tokenField{"runtime_epoch_id", string(capsule.RuntimeEpochID)},
	); err != nil {
		return AuthorityCapsuleV1{}, err
	}
	if capsule.LeaseGeneration == 0 || capsule.FenceToken == 0 || capsule.AuthorityHeadRecordRevision == 0 {
		return AuthorityCapsuleV1{}, ValidationError{Kind: RefusalInvalidValue, Field: "authority_capsule_coordinate"}
	}
	return capsule, nil
}

func EncodeAuthorityCapsuleV1(capsule AuthorityCapsuleV1) ([]byte, error) {
	validated, err := NewAuthorityCapsuleV1(capsule)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(authorityCapsuleWireV1{
		SchemaVersion: validated.SchemaVersion, ProjectID: string(validated.ProjectID),
		MissionID: string(validated.MissionID), WorkRevisionID: string(validated.WorkRevisionID),
		ActorID: string(validated.ActorID), ProcessID: string(validated.ProcessID),
		RunID: string(validated.RunID), LeaseID: string(validated.LeaseID),
		WorkspaceID: string(validated.WorkspaceID), RuntimeEpochID: string(validated.RuntimeEpochID),
		LeaseGeneration: validated.LeaseGeneration, FenceToken: validated.FenceToken,
		AuthorityHeadRecordRevision: validated.AuthorityHeadRecordRevision,
	})
	if err != nil || len(encoded) > MaxOrdinaryValueBytes {
		return nil, ValidationError{Kind: RefusalBudgetExceeded, Field: "authority_capsule"}
	}
	return encoded, nil
}

func DecodeAuthorityCapsuleV1(encoded []byte) (AuthorityCapsuleV1, error) {
	canonical, err := decodeRequiredObject(encoded, "authority_capsule",
		"schema_version", "project_id", "mission_id", "work_revision_id",
		"actor_id", "process_id", "run_id", "lease_id", "workspace_id",
		"runtime_epoch_id", "lease_generation", "fence_token", "authority_head_record_revision")
	if err != nil {
		return AuthorityCapsuleV1{}, err
	}
	var wire authorityCapsuleWireV1
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return AuthorityCapsuleV1{}, ValidationError{Kind: RefusalInvalidValue, Field: "authority_capsule"}
	}
	capsule, err := NewAuthorityCapsuleV1(AuthorityCapsuleV1{
		SchemaVersion: wire.SchemaVersion, ProjectID: ProjectID(wire.ProjectID),
		MissionID: MissionID(wire.MissionID), WorkRevisionID: WorkRevisionID(wire.WorkRevisionID),
		ActorID: ActorID(wire.ActorID), ProcessID: ProcessID(wire.ProcessID),
		RunID: RunID(wire.RunID), LeaseID: LeaseID(wire.LeaseID),
		WorkspaceID: WorkspaceID(wire.WorkspaceID), RuntimeEpochID: RuntimeEpochID(wire.RuntimeEpochID),
		LeaseGeneration: wire.LeaseGeneration, FenceToken: wire.FenceToken,
		AuthorityHeadRecordRevision: wire.AuthorityHeadRecordRevision,
	})
	if err != nil {
		return AuthorityCapsuleV1{}, err
	}
	reencoded, err := EncodeAuthorityCapsuleV1(capsule)
	if err != nil || !bytes.Equal(encoded, reencoded) {
		return AuthorityCapsuleV1{}, ValidationError{Kind: RefusalJournalCorrupt, Field: "authority_capsule_canonical_bytes"}
	}
	return capsule, nil
}
