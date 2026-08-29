// Package projection renders canonical read models into non-authoritative output.
package projection

import (
	"fmt"
	"time"

	missionreadmodel "github.com/spec-kitty/go-kitty3/internal/mission/readmodel"
	workgraphreadmodel "github.com/spec-kitty/go-kitty3/internal/workgraph/readmodel"
)

// ErrorKind is a stable machine-readable projection failure classification.
type ErrorKind string

const (
	KindNoFinalizedRevision ErrorKind = "no_finalized_revision"
	KindCorruptReadModel    ErrorKind = "corrupt_read_model"
	KindUnsupportedDrift    ErrorKind = "unsupported_drift_state"
	KindUnsafeOutputPath    ErrorKind = "unsafe_output_path"
	KindWriteFailure        ErrorKind = "write_failure"
	KindReplaceFailure      ErrorKind = "replace_failure"
)

// Recovery is a bounded caller-facing recovery category.
type Recovery string

const (
	RecoveryFinalize Recovery = "finalize_revision"
	RecoveryRebuild  Recovery = "rebuild_read_model"
	RecoveryCorrect  Recovery = "correct_input"
	RecoveryRetry    Recovery = "retry"
)

// Error deliberately excludes supplied paths, content, provider output, and
// wrapped operating-system prose from its human-readable representation.
type Error struct {
	Kind      ErrorKind
	Recovery  Recovery
	Field     string
	RetrySafe bool
	Message   string
}

func (failure *Error) Error() string {
	return fmt.Sprintf("projection failed: kind=%s recovery=%s field=%s retry_safe=%t message=%s", failure.Kind, failure.Recovery, failure.Field, failure.RetrySafe, failure.Message)
}

func projectionError(kind ErrorKind, recovery Recovery, field string) *Error {
	message := "the projection request could not be completed"
	switch kind {
	case KindNoFinalizedRevision:
		message = "finalize a WorkRevision before rendering"
	case KindCorruptReadModel:
		message = "rebuild the canonical read model"
	case KindUnsupportedDrift:
		message = "supply a supported drift observation state"
	case KindUnsafeOutputPath:
		message = "choose a regular repository-relative destination beneath the trusted root"
	case KindWriteFailure:
		message = "the complete replacement was not published"
	case KindReplaceFailure:
		message = "atomic replacement or durability confirmation failed"
	}
	return &Error{Kind: kind, Recovery: recovery, Field: field, RetrySafe: true, Message: message}
}

// TasksView is the complete immutable input for deterministic task rendering.
// It intentionally contains read values, not authority services or providers.
type TasksView struct {
	mission    missionreadmodel.Snapshot
	workgraph  workgraphreadmodel.Snapshot
	renderedAt time.Time
}

func NewTasksView(mission missionreadmodel.Snapshot, workgraph workgraphreadmodel.Snapshot, renderedAt time.Time) (TasksView, error) {
	if err := mission.Validate(); err != nil {
		return TasksView{}, projectionError(KindCorruptReadModel, RecoveryRebuild, "mission")
	}
	if mission.CurrentWorkRevisionID() == "" {
		return TasksView{}, projectionError(KindNoFinalizedRevision, RecoveryFinalize, "work_revision_id")
	}
	if err := workgraph.Validate(); err != nil {
		if workgraph.DriftStatus().Validate() != nil {
			return TasksView{}, projectionError(KindUnsupportedDrift, RecoveryRebuild, "drift_status")
		}
		return TasksView{}, projectionError(KindCorruptReadModel, RecoveryRebuild, "workgraph")
	}
	if mission.MissionID() != workgraph.MissionID() {
		return TasksView{}, projectionError(KindCorruptReadModel, RecoveryRebuild, "mission_id")
	}
	if mission.CurrentWorkRevisionID() != workgraph.RevisionID() {
		return TasksView{}, projectionError(KindCorruptReadModel, RecoveryRebuild, "work_revision_id")
	}
	if renderedAt.IsZero() || renderedAt.Location() != time.UTC {
		return TasksView{}, projectionError(KindCorruptReadModel, RecoveryCorrect, "rendered_at")
	}
	return TasksView{mission: mission, workgraph: workgraph, renderedAt: renderedAt}, nil
}

func (view TasksView) Mission() missionreadmodel.Snapshot     { return view.mission }
func (view TasksView) Workgraph() workgraphreadmodel.Snapshot { return view.workgraph }
func (view TasksView) RenderedAt() time.Time                  { return view.renderedAt }

func (view TasksView) Validate() error {
	_, err := NewTasksView(view.mission, view.workgraph, view.renderedAt)
	return err
}
