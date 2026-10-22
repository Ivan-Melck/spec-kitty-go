package retrospective

// Retrospective lifecycle events beyond RetrospectiveCaptured
// (retrospective.lifecycle_events emit_skipped / emit_capture_failed).

import (
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
)

// NotFoundError is the FileNotFoundError generate_retrospective raises for
// a mission without a directory.
type NotFoundError struct{ Msg string }

func (e *NotFoundError) Error() string { return e.Msg }

func cliActor() *jsonx.Object {
	return jsonx.NewObject("kind", "human", "id", "cli", "display", "spec-kitty retrospect")
}

func lifecycleBase(typ, dir, missionID, slug string) *jsonx.Object {
	return jsonx.NewObject("type", typ, "schema_version", 1, "event_id", ulid.New(), "lamport", nextLamport(dir),
		"at", Now(), "actor", cliActor(), "mission_id", missionID, "mission_slug", slug, "wp_id", nil, "force", false,
		"execution_mode", "main")
}

// EmitSkipped is emit_skipped (CLI actor, cli_flag source, empty policy
// source) appended to dir/status.events.jsonl.
func EmitSkipped(dir, missionID, slug, reason string) error {
	ev := lifecycleBase("RetrospectiveSkipped", dir, missionID, slug)
	ev.Set("skip_reason", reason)
	ev.Set("skip_reason_source", "cli_flag")
	ev.Set("policy_source", &jsonx.Object{})
	ev.Set("bypassed_provenance_kind", "runtime_strict_gate")
	ev.Set("would_have_attempted", true)
	return status.AppendRows(filepath.Join(dir, "status.events.jsonl"), ev)
}

// EmitCaptureFailed is emit_capture_failed (CLI actor, empty policy source)
// appended to dir/status.events.jsonl. remediation "" and missing nil are null.
func EmitCaptureFailed(dir, missionID, slug, category, message, remediation, attemptedKind string, missing []string) error {
	ev := lifecycleBase("RetrospectiveCaptureFailed", dir, missionID, slug)
	ev.Set("failure_category", category)
	ev.Set("failure_message", message)
	if remediation == "" {
		ev.Set("remediation_hint", nil)
	} else {
		ev.Set("remediation_hint", remediation)
	}
	ev.Set("policy_source", &jsonx.Object{})
	ev.Set("attempted_provenance_kind", attemptedKind)
	if missing == nil {
		ev.Set("missing_artifacts", nil)
	} else {
		l := []any{}
		for _, m := range missing {
			l = append(l, m)
		}
		ev.Set("missing_artifacts", l)
	}
	return status.AppendRows(filepath.Join(dir, "status.events.jsonl"), ev)
}
