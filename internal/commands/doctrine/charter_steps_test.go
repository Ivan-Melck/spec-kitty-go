package doctrine_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// inflightEvents is a status.events.jsonl moving WP01 to in_progress and
// WP02 to for_review (legacy transition rows).
const inflightEvents = `{"actor": "t", "at": "2026-10-17T12:40:20.732565+00:00", "event_id": "01M54Y18FWECW9HVDSE7JQG680", "evidence": null, "execution_mode": "worktree", "force": true, "from_lane": "planned", "mission_id": "01M54Y16550EHN17T75926T0NP", "mission_slug": "zz-inflight-01M54Y16", "policy_metadata": null, "reason": null, "review_ref": null, "to_lane": "in_progress", "wp_id": "WP01"}
{"actor": "t", "at": "2026-10-17T12:40:21.732565+00:00", "event_id": "01M54Y18FWECW9HVDSE7JQG681", "evidence": null, "execution_mode": "worktree", "force": true, "from_lane": "planned", "mission_id": "01M54Y16550EHN17T75926T0NP", "mission_slug": "zz-inflight-01M54Y16", "policy_metadata": null, "reason": null, "review_ref": null, "to_lane": "for_review", "wp_id": "WP02"}
{"actor": "t", "at": "2026-10-17T12:40:22.732565+00:00", "event_id": "01M54Y18FWECW9HVDSE7JQG682", "evidence": null, "execution_mode": "worktree", "force": true, "from_lane": "planned", "mission_id": "01M54Y16550EHN17T75926T0NP", "mission_slug": "zz-inflight-01M54Y16", "policy_metadata": null, "reason": null, "review_ref": null, "to_lane": "done", "wp_id": "WP03"}
`

// TestParityMissionTypeStepRemoval covers `charter activate mission-type`'s
// FR-008 in-flight step-removal warnings and FR-001 empty-sequence refusal
// over the layered (built-in/org/project) mission-type roster.
func TestParityMissionTypeStepRemoval(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/missions/mission_types/custom.yaml", "schema_version: 1\nid: custom\ndisplay_name: Custom\nextends: software-dev\n")
		s.WriteFile(".kittify/missions/mission_types/bare.yaml", "schema_version: 1\nid: bare\ndisplay_name: Bare\n")
		s.WriteFile("kitty-specs/zz-inflight-01M54Y16/status.events.jsonl", inflightEvents)
		s.WriteFile("kitty-specs/zz-inflight-01M54Y16/meta.json", `{"mission_slug": "zz-inflight-01M54Y16"}`+"\n")
		s.WriteFile("kitty-specs/plain/status.events.jsonl", inflightEvents)
		s.WriteFile(".kittify/charter-packs/README.md", "project pack\n")
	})
	for _, args := range [][]string{
		{"charter", "activate", "mission-type", "bare", "--no-compile"},
		{"charter", "activate", "mission-type", "custom", "--no-compile"},
		{"charter", "activate", "mission-type", "custom", "--no-compile"},
		{"charter", "activate", "mission-type", "software-dev", "--no-compile"},
	} {
		compareCharter(t, p, args...)
	}
	// A malformed file anywhere in the layered roster fails every
	// mission-type activation with the loader's ValueError.
	for _, bad := range []string{
		"id: [unclosed\n",
		"- a\n",
		"",
		"schema_version: 1\nid: wrong\ndisplay_name: W\n",
		"schema_version: x\nid: zz\nextends: 5\naction_sequence: [a, 3]\ntemplate_set: {}\n",
		"id: zz\ndisplay_name: Z\naction_sequence: []\n",
		"id: zz\ndisplay_name: Z\naction_sequence: [a, a]\n",
		"id: zz\ndisplay_name: Z\naction_sequence: [a]\npath_conventions: {workspace: src, bogus: x}\n",
		"id: Bad_Id\ndisplay_name: Z\n",
		"id: zz\ndisplay_name: Z\nschema_version: 1.5\npath_conventions: [x]\n",
	} {
		p.Both(func(s *testkit.Sandbox) { s.WriteFile(".kittify/missions/mission_types/zz.yaml", bad) })
		compareCharter(t, p, "charter", "activate", "mission-type", "custom", "--no-compile")
	}
}
