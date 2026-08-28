package helpercmd

import (
	"context"
	"fmt"
	"io"
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
	store, err := sqlite.Open(ctx, request.Database)
	if err != nil {
		return encodeMethodFailure(stdout, "projection_tasks_refused", request.CorrelationID, "", nil, err)
	}
	observation, err := store.ObserveMissionMethod(ctx, request.MissionID)
	closeErr := store.Close()
	if err != nil {
		return encodeMethodFailure(stdout, "projection_tasks_refused", request.CorrelationID, "", map[string]string{"mission_id": request.MissionID.String()}, err)
	}
	if closeErr != nil {
		return encodeMethodFailure(stdout, "projection_tasks_refused", request.CorrelationID, "", nil, closeErr)
	}
	view, err := projection.NewTasksView(observation.Mission, observation.Workgraph, request.RenderedAt.UTC())
	if err != nil {
		return encodeMethodFailure(stdout, "projection_tasks_refused", request.CorrelationID, "", nil, err)
	}
	content, err := projection.RenderTasks(view)
	if err != nil {
		return encodeMethodFailure(stdout, "projection_tasks_refused", request.CorrelationID, "", nil, err)
	}
	if request.TrustedRoot == "" != (request.Destination == "") {
		return encodeMethodFailure(stdout, "projection_tasks_refused", request.CorrelationID, "", nil, fmt.Errorf("trusted root and destination must be supplied together"))
	}
	if request.TrustedRoot != "" {
		if err = projection.WriteAtomic(request.TrustedRoot, request.Destination, content, 0o644); err != nil {
			return encodeMethodFailure(stdout, "projection_tasks_refused", request.CorrelationID, "", nil, err)
		}
	}
	return encodeMethodSuccess(stdout, "projection_tasks", request.CorrelationID, "", map[string]string{"mission_id": request.MissionID.String(), "work_revision_id": observation.Workgraph.RevisionID().String()}, struct {
		Content     string `json:"content"`
		Destination string `json:"destination,omitempty"`
		Generation  uint64 `json:"generation"`
		GraphDigest string `json:"graph_digest"`
	}{string(content), request.Destination, observation.Workgraph.Generation(), observation.Workgraph.GraphDigest()})
}
