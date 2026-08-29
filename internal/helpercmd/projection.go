package helpercmd

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/projection"
)

type projectionTasksRequest struct {
	Database      string        `json:"database"`
	CorrelationID string        `json:"correlation_id"`
	MissionID     ids.MissionID `json:"mission_id"`
	RenderedAt    time.Time     `json:"rendered_at"`
	TrustedRoot   string        `json:"trusted_root"`
	Destination   string        `json:"destination"`
}

func RunProjection(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "tasks" {
		fmt.Fprintln(stderr, "usage: projection tasks --request-json <json>")
		return 2
	}
	var request projectionTasksRequest
	if code := decodeMethodRequest(args[1:], &request, stdout, stderr); code >= 0 {
		return code
	}
	return runProjectionTasks(ctx, request, stdout)
}

func runProjectionTasks(ctx context.Context, request projectionTasksRequest, stdout io.Writer) int {
	aggregates := map[string]string{"mission_id": request.MissionID.String()}
	if err := validateProjectionRequest(request); err != nil {
		return encodeMethodFailure(stdout, "projection_tasks_refused", request.CorrelationID, "", aggregates, err)
	}
	store, err := sqlite.Open(ctx, request.Database)
	if err != nil {
		return encodeMethodFailure(stdout, "projection_tasks_refused", request.CorrelationID, "", aggregates, controlStoreError())
	}
	observation, err := store.ObserveMissionMethod(ctx, request.MissionID)
	closeErr := store.Close()
	if err != nil {
		return encodeMethodFailure(stdout, "projection_tasks_refused", request.CorrelationID, "", aggregates, err)
	}
	if closeErr != nil {
		return encodeMethodFailure(stdout, "projection_tasks_refused", request.CorrelationID, "", aggregates, closeErr)
	}
	view, err := projection.NewTasksView(observation.Mission, observation.Workgraph, request.RenderedAt.UTC())
	if err != nil {
		return encodeMethodFailure(stdout, "projection_tasks_refused", request.CorrelationID, "", aggregates, err)
	}
	content, err := projection.RenderTasks(view)
	if err != nil {
		return encodeMethodFailure(stdout, "projection_tasks_refused", request.CorrelationID, "", aggregates, err)
	}
	if request.TrustedRoot != "" {
		if err = projection.WriteAtomic(request.TrustedRoot, request.Destination, content, 0o644); err != nil {
			return encodeMethodFailure(stdout, "projection_tasks_refused", request.CorrelationID, "", aggregates, err)
		}
	}
	return encodeMethodSuccess(stdout, "projection_tasks", request.CorrelationID, "", map[string]string{"mission_id": request.MissionID.String(), "work_revision_id": observation.Workgraph.RevisionID().String()}, struct {
		Content     string `json:"content"`
		Destination string `json:"destination,omitempty"`
		Generation  uint64 `json:"generation"`
		GraphDigest string `json:"graph_digest"`
	}{string(content), request.Destination, observation.Workgraph.Generation(), observation.Workgraph.GraphDigest()})
}

func validateProjectionRequest(request projectionTasksRequest) error {
	if err := validateAdapterPath(request.Database, "database"); err != nil {
		return err
	}
	if _, err := ids.ParseMissionID(request.MissionID.String()); err != nil {
		return invalidPreflight("mission_id", "invalid", "stable_mission_id")
	}
	if request.RenderedAt.IsZero() || request.RenderedAt.Location() != time.UTC {
		return invalidPreflight("rendered_at", "invalid", "nonzero_utc")
	}
	if request.TrustedRoot == "" != (request.Destination == "") {
		return unsafeProjectionPreflight("destination", "incomplete_path_pair")
	}
	if request.Destination != "" && !safeProjectionDestination(request.Destination) {
		return unsafeProjectionPreflight("destination", "unsafe")
	}
	if request.TrustedRoot != "" && (len(request.TrustedRoot) > 4096 || strings.ContainsRune(request.TrustedRoot, 0)) {
		return unsafeProjectionPreflight("trusted_root", "invalid")
	}
	return nil
}

func safeProjectionDestination(value string) bool {
	return value != "" && value != "." && !filepath.IsAbs(value) && !strings.ContainsAny(value, "\\\x00") && fs.ValidPath(value) && path.Clean(value) == value
}

func unsafeProjectionPreflight(field, observed string) error {
	return methodStateError("unsafe_output_path", "correct_input", field, "projection output path failed local transport preflight", map[string]string{"state": observed}, map[string]string{"state": "repository_relative_safe_path"}, false)
}
