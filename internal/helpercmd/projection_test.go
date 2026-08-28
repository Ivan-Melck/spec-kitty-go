package helpercmd

import "testing"

func TestProjectionTasksStrictJSONRejectsUnknownNullAndLeftover(t *testing.T) {
	requests := [][]string{
		{"tasks", "--request-json", `{"database":"unused","correlation_id":"c","mission_id":"mission-1","rendered_at":"2026-08-28T00:00:00Z","trusted_root":"","destination":"","unknown":true}`},
		{"tasks", "--request-json", `{"database":"unused","correlation_id":"c","mission_id":"mission-1","rendered_at":"2026-08-28T00:00:00Z","trusted_root":null,"destination":""}`},
		{"tasks", "--request-json", `{"database":"unused","correlation_id":"c","mission_id":"mission-1","rendered_at":"2026-08-28T00:00:00Z","trusted_root":"","destination":""}`, "leftover"},
	}
	for _, args := range requests {
		response := invokeMethodUsage(t, RunProjection, args)
		if response.OK || response.Kind != "invalid_invocation" || response.Error == nil {
			t.Fatalf("args=%v response=%+v", args, response)
		}
	}
}
