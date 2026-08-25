package workspace

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

type Stage string

const (
	StageReserved             Stage = "reserved"
	StageWorktreeCreated      Stage = "worktree_created"
	StageBaseVerified         Stage = "base_verified"
	StageDependenciesPrepared Stage = "dependencies_prepared"
	StageOwnershipVerified    Stage = "ownership_verified"
	StageReady                Stage = "ready"
)

type ErrorKind string

const (
	KindInvalid            ErrorKind = "invalid_command"
	KindBindingConflict    ErrorKind = "repository_binding_conflict"
	KindWorkspaceConflict  ErrorKind = "workspace_conflict"
	KindWorkspaceNotReady  ErrorKind = "workspace_not_ready"
	KindPathUnsafe         ErrorKind = "path_unsafe"
	KindTargetMoved        ErrorKind = "target_changed"
	KindDependencyConflict ErrorKind = "dependency_conflict"
	KindStaleAuthority     ErrorKind = "stale_authority"
	KindGitFailure         ErrorKind = "git_failure"
	KindCleanupRefused     ErrorKind = "cleanup_refused"
)

type Error struct {
	Kind     ErrorKind
	Message  string
	Observed map[string]string
	Expected map[string]string
	Cause    error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

func IsKind(err error, kind ErrorKind) bool {
	var workspaceError *Error
	return errors.As(err, &workspaceError) && workspaceError.Kind == kind
}

type PrimarySnapshot struct {
	Head        string
	IndexDigest string
	TreeDigest  string
}

type RepositoryBinding struct {
	RepositoryID ids.RepositoryID
	ProjectID    ids.ProjectID
	CommonDir    string
	PrimaryPath  string
	ManagedRoot  string
	TargetRef    string
	Primary      PrimarySnapshot
	CreatedAt    time.Time
	VerifiedAt   time.Time
}

type Dependency struct {
	WorkID    ids.WorkID
	Candidate string
}

func CanonicalDependencies(input []Dependency) ([]Dependency, error) {
	result := append([]Dependency(nil), input...)
	for _, dependency := range result {
		if _, err := ids.ParseWorkID(dependency.WorkID.String()); err != nil || !validObjectID(dependency.Candidate) {
			return nil, &Error{Kind: KindInvalid, Message: "dependency work ID and candidate commit are required"}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].WorkID == result[j].WorkID {
			return result[i].Candidate < result[j].Candidate
		}
		return result[i].WorkID < result[j].WorkID
	})
	for index := 1; index < len(result); index++ {
		if result[index-1].WorkID == result[index].WorkID {
			return nil, &Error{Kind: KindInvalid, Message: "dependency work IDs must be unique"}
		}
	}
	return result, nil
}

type Authority struct {
	LeaseID    ids.LeaseID
	Generation int64
	Token      string
}

type Reservation struct {
	OperationID    ids.OperationID
	ProjectID      ids.ProjectID
	MissionID      ids.MissionID
	WorkRevisionID ids.WorkRevisionID
	WorkID         ids.WorkID
	RunID          ids.RunID
	WorkspaceID    ids.WorkspaceID
	RepositoryID   ids.RepositoryID
	LaneID         ids.LaneID
	Authority      Authority
	OwnedBranch    string
	PinnedBase     string
	Dependencies   []Dependency
	Now            time.Time
}

func (r Reservation) Validate() error {
	checks := []error{}
	_, err := ids.ParseOperationID(r.OperationID.String())
	checks = append(checks, err)
	_, err = ids.ParseRunID(r.RunID.String())
	checks = append(checks, err)
	_, err = ids.ParseProjectID(r.ProjectID.String())
	checks = append(checks, err)
	_, err = ids.ParseMissionID(r.MissionID.String())
	checks = append(checks, err)
	_, err = ids.ParseWorkRevisionID(r.WorkRevisionID.String())
	checks = append(checks, err)
	_, err = ids.ParseWorkID(r.WorkID.String())
	checks = append(checks, err)
	_, err = ids.ParseWorkspaceID(r.WorkspaceID.String())
	checks = append(checks, err)
	_, err = ids.ParseRepositoryID(r.RepositoryID.String())
	checks = append(checks, err)
	_, err = ids.ParseLaneID(r.LaneID.String())
	checks = append(checks, err)
	_, err = ids.ParseLeaseID(r.Authority.LeaseID.String())
	checks = append(checks, err)
	for _, check := range checks {
		if check != nil {
			return &Error{Kind: KindInvalid, Message: "workspace reservation contains an invalid portable ID", Cause: check}
		}
	}
	if r.Authority.Generation <= 0 || r.Authority.Token == "" || r.Now.IsZero() || !validBranch(r.OwnedBranch) || !validObjectID(r.PinnedBase) {
		return &Error{Kind: KindInvalid, Message: "workspace authority, timestamp, branch, and pinned base are required"}
	}
	_, err = CanonicalDependencies(r.Dependencies)
	return err
}

type WorkAuthority struct {
	ProjectID ids.ProjectID
	MissionID ids.MissionID
	LaneID    ids.LaneID
}

type Record struct {
	Reservation
	CanonicalPath    string
	Stage            Stage
	Status           string
	Registered       bool
	ObservedHead     string
	DependencyHead   string
	GitDir           string
	IndexPath        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	QuarantineReason string
	QuarantinedAt    time.Time
}

func (r Record) Ready() bool {
	return r.Stage == StageReady && r.Status == "ready" && r.Registered && r.ObservedHead == r.DependencyHead && r.DependencyHead != "" && r.GitDir != "" && r.IndexPath != ""
}

type Probe struct {
	Registered bool
	Head       string
	Branch     string
	GitDir     string
	IndexPath  string
	Clean      bool
}

func validObjectID(value string) bool {
	if len(value) < 7 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return false
		}
	}
	return true
}

func validBranch(value string) bool {
	return value != "" && !strings.HasPrefix(value, "-") && !strings.ContainsAny(value, " ~^:?*[\\") && !strings.Contains(value, "..") && !strings.HasSuffix(value, ".lock")
}
