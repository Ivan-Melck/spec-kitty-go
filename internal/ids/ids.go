package ids

import (
	"fmt"
	"regexp"
)

var valid = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func validate(kind, value string) error {
	if !valid.MatchString(value) {
		return fmt.Errorf("invalid %s %q", kind, value)
	}
	return nil
}

type ProjectID string
type RepositoryID string
type MissionID string
type WorkID string
type WorkRevisionID string
type LaneID string
type ActorID string
type ProcessID string
type RunID string
type LeaseID string
type WorkspaceID string
type OperationID string
type HandoffID string
type ReviewID string

func (v ProjectID) String() string      { return string(v) }
func (v RepositoryID) String() string   { return string(v) }
func (v MissionID) String() string      { return string(v) }
func (v WorkID) String() string         { return string(v) }
func (v WorkRevisionID) String() string { return string(v) }
func (v LaneID) String() string         { return string(v) }
func (v ActorID) String() string        { return string(v) }
func (v ProcessID) String() string      { return string(v) }
func (v RunID) String() string          { return string(v) }
func (v LeaseID) String() string        { return string(v) }
func (v WorkspaceID) String() string    { return string(v) }
func (v OperationID) String() string    { return string(v) }
func (v HandoffID) String() string      { return string(v) }
func (v ReviewID) String() string       { return string(v) }

func ParseProjectID(v string) (ProjectID, error) { return ProjectID(v), validate("ProjectID", v) }
func ParseRepositoryID(v string) (RepositoryID, error) {
	return RepositoryID(v), validate("RepositoryID", v)
}
func ParseMissionID(v string) (MissionID, error) { return MissionID(v), validate("MissionID", v) }
func ParseWorkID(v string) (WorkID, error)       { return WorkID(v), validate("WorkID", v) }
func ParseWorkRevisionID(v string) (WorkRevisionID, error) {
	return WorkRevisionID(v), validate("WorkRevisionID", v)
}
func ParseLaneID(v string) (LaneID, error)       { return LaneID(v), validate("LaneID", v) }
func ParseActorID(v string) (ActorID, error)     { return ActorID(v), validate("ActorID", v) }
func ParseProcessID(v string) (ProcessID, error) { return ProcessID(v), validate("ProcessID", v) }
func ParseRunID(v string) (RunID, error)         { return RunID(v), validate("RunID", v) }
func ParseLeaseID(v string) (LeaseID, error)     { return LeaseID(v), validate("LeaseID", v) }
func ParseWorkspaceID(v string) (WorkspaceID, error) {
	return WorkspaceID(v), validate("WorkspaceID", v)
}
func ParseOperationID(v string) (OperationID, error) {
	return OperationID(v), validate("OperationID", v)
}
func ParseHandoffID(v string) (HandoffID, error) { return HandoffID(v), validate("HandoffID", v) }
func ParseReviewID(v string) (ReviewID, error)   { return ReviewID(v), validate("ReviewID", v) }
