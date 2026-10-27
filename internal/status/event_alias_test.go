package status

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// TestNotApplicableSubtaskIsReadAsSkipped: WPInnerStateDelta.from_dict
// normalizes the "not_applicable" input alias to "skipped" (#6605).
func TestNotApplicableSubtaskIsReadAsSkipped(t *testing.T) {
	row := jsonx.NewObject("kind", AnnotationKind, "event_id", "01KZ0000000000000000000000", "wp_id", "WP01",
		"at", "2026-10-27T00:00:00+00:00", "actor", "claude",
		"delta", jsonx.NewObject("subtasks", jsonx.NewObject("T001", "not_applicable", "T002", "done")))
	a, err := AnnotationFromObject(row)
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Delta.Subtasks.GetString("T001"); got != "skipped" {
		t.Fatalf("T001 = %q, want skipped", got)
	}
	bad := row.Clone()
	bad.Set("delta", jsonx.NewObject("subtasks", jsonx.NewObject("T001", "n/a")))
	if _, err := AnnotationFromObject(bad); err == nil {
		t.Fatal("an unknown subtask value was accepted")
	}
}
