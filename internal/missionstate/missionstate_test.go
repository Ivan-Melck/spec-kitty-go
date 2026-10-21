package missionstate

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

func mustObj(t *testing.T, s string) *jsonx.Object {
	t.Helper()
	o, err := jsonx.DecodeObject([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// Expected values computed with the upstream implementation.
func TestDeterministicIDs(t *testing.T) {
	if got := DeterministicULID("seed"); got != "0SP9C5DRE1A3586K7ZS2TSP8XD" {
		t.Fatalf("deterministic_ulid: %s", got)
	}
	if got := uuid5URL("spec-kitty:teamspace-dry-run:a|b"); got != "905f0317-6947-588d-a7f6-a39797dfc472" {
		t.Fatalf("uuid5: %s", got)
	}
}

func TestScrubSecretArgs(t *testing.T) {
	got := ScrubSecretArgs([]string{"--token", "abc", "--api-key=zz", "GITHUB_TOKEN=x", "Bearer abcdefghijklmnopqrstu", "plain", "xoxb-123-abc"})
	want := "--token,<redacted>,--api-key=<redacted>,GITHUB_TOKEN=<redacted>,<redacted>,plain,<redacted>"
	if strings.Join(got, ",") != want {
		t.Fatalf("scrub: %v", got)
	}
}

func TestPlanDupKeyRepair(t *testing.T) {
	text := "---\nid: WP01\nreview_feedback: ''\ntitle: A\nreview_feedback: review-cycle://x\n---\nbody\n"
	p, err := planDupKeyRepair("WP01.md", text)
	if err != nil || p == nil {
		t.Fatalf("plan: %v %v", p, err)
	}
	if p.repaired != "---\nid: WP01\ntitle: A\nreview_feedback: review-cycle://x\n---\nbody\n" {
		t.Fatalf("repaired: %q", p.repaired)
	}
	if _, err := planDupKeyRepair("x.md", "---\na:\n  - 1\na: 2\n---\n"); err == nil {
		t.Fatal("non-scalar duplicate must refuse")
	}
}

func TestValidatePayloadTransitionRules(t *testing.T) {
	vs := validatePayload(mustObj(t, `{"mission_slug": "m", "wp_id": "WP01", "from_lane": "planned", "to_lane": "in_progress", "actor": "a", "force": false, "reason": null, "execution_mode": "worktree", "review_ref": null, "evidence": null}`))
	if len(vs) != 1 || vs[0].GetString("message") != "Transition Lane.PLANNED -> Lane.IN_PROGRESS is not allowed" {
		t.Fatalf("violations: %v", vs)
	}
}
