package missionfx

import (
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// WriteOpRecord writes a dispatch Op record (kitty-ops/<id>.jsonl, one
// "started" event) correlated with the single mission of s, for the
// --invocation-id claim seams.
func WriteOpRecord(t testing.TB, s *testkit.Sandbox, id, wpID, action, profile, model string) {
	t.Helper()
	slug := MissionSlug(t, s)
	meta, err := jsonx.DecodeObject([]byte(s.ReadFile(filepath.Join("kitty-specs", slug, "meta.json"))))
	if err != nil {
		t.Fatal(err)
	}
	rec := jsonx.NewObject("event", "started", "invocation_id", id, "profile_id", profile, "action", action,
		"request_text", action+" "+wpID, "actor", "claude", "mode_of_work", "task_execution",
		"governance_context_hash", "0000000000000000", "governance_context_available", false,
		"started_at", "2026-01-01T00:00:00+00:00", "mission_id", meta.GetString("mission_id"), "wp_id", wpID)
	if model != "" {
		rec.Set("model_id", model)
	}
	s.WriteFile("kitty-ops/"+id+".jsonl", jsonx.Dumps(rec, jsonx.Options{Indent: -1, EnsureASCII: true})+"\n")
}
