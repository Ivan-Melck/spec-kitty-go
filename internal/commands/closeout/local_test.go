package closeout_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

const localMatrix = `{
  "mission_slug": "calc-ops-01M54Y16",
  "mission_number": "",
  "mission_type": "software-dev",
  "overall_verdict": "pending",
  "criteria": [
    {
      "criterion_id": "FR-001",
      "description": "Verify FR-001 is satisfied",
      "proof_type": "automated_test",
      "evidence": null,
      "pass_fail": "pending",
      "verified_by": null,
      "verified_at": null,
      "notes": "TODO: replace with a real acceptance criterion"
    }
  ],
  "negative_invariants": []
}
`

func decode(t *testing.T, s string) *jsonx.Object {
	t.Helper()
	o, err := jsonx.DecodeObject([]byte(strings.TrimSpace(s)))
	if err != nil {
		t.Fatalf("bad JSON %v:\n%s", err, s)
	}
	return o
}

// TestLocalCloseout exercises the closeout commands without the oracle on
// the recorded calc fixture (lanes topology, WPs planned).
func TestLocalCloseout(t *testing.T) {
	s := missionfx.NewLocalMission(t)
	slug := missionfx.LocalSlug
	s.WriteFile(filepath.Join("kitty-specs", slug, "acceptance-matrix.json"), localMatrix)
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "matrix")

	r := s.Run("agent", "acceptance-verdict", "--mission", slug, "--criterion", "FR-001", "--result", "pass", "--actor", "me", "--json")
	if r.Code != 0 {
		t.Fatalf("verdict: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	o := decode(t, r.Stdout)
	if o.GetString("overall_verdict") != "pass" || o.GetString("write_status") != "committed" {
		t.Fatalf("unexpected verdict payload: %s", r.Stdout)
	}
	if msg := s.Git("log", "-1", "--format=%s"); msg != "chore(acceptance): record FR-001=pass for "+slug {
		t.Fatalf("commit message %q", msg)
	}
	r = s.Run("agent", "acceptance-verdict", "--mission", slug, "--criterion", "FR-001", "--result", "pass", "--actor", "me", "--json")
	if decode(t, r.Stdout).GetString("write_status") != "unchanged" {
		t.Fatalf("expected unchanged: %s", r.Stdout)
	}
	if r := s.Run("agent", "acceptance-verdict", "--mission", slug, "--json"); r.Code != 2 {
		t.Fatalf("mode validation exit %d", r.Code)
	}

	r = s.Run("agent", "mission", "accept", "--mission", slug, "--diagnose", "--json")
	if r.Code != 0 {
		t.Fatalf("diagnose: %d %s", r.Code, r.Stderr)
	}
	d := decode(t, r.Stdout)
	if d.GetBool("ok") || !d.GetBool("diagnose") || len(d.GetList("failed_checks")) == 0 {
		t.Fatalf("diagnose payload: %s", r.Stdout)
	}
	if r := s.Run("agent", "mission", "accept", "--mission", slug, "--json"); r.Code != 1 {
		t.Fatalf("accept on planned WPs should fail, got %d", r.Code)
	}
	if r := s.Run("accept", "--json"); r.Code != 2 {
		t.Fatalf("accept without mission: %d", r.Code)
	}

	r = s.Run("consolidate", "--mission", slug, "--dry-run", "--json")
	if r.Code != 0 {
		t.Fatalf("dry-run: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	if dr := decode(t, r.Stdout); dr.GetString("strategy") != "squash" || dr.GetString("mission_branch") == "" {
		t.Fatalf("dry-run payload: %s", r.Stdout)
	}
	if r := s.Run("consolidate", "--mission", slug, "--yes"); r.Code != 1 || !strings.Contains(r.Stdout, "Missing mission branch") {
		t.Fatalf("consolidate without mission branch: %d %s", r.Code, r.Stdout)
	}

	r = s.Run("review", "--mission", slug)
	if r.Code != 1 || !strings.Contains(r.Stdout, "WP lane check: 2 WP(s) not in done") {
		t.Fatalf("review: %d %s", r.Code, r.Stdout)
	}
	if !s.Exists(filepath.Join("kitty-specs", slug, "mission-review-report.md")) {
		t.Fatal("review report not written")
	}

	r = s.Run("retrospect", "summary", "--json")
	if r.Code != 0 {
		t.Fatalf("summary: %d %s", r.Code, r.Stderr)
	}
	if agg := decode(t, r.Stdout).GetObject("aggregate"); agg == nil {
		t.Fatalf("summary payload: %s", r.Stdout)
	}
	if r := s.Run("retrospect", "create", "--mission", slug, "--json"); r.Code != 1 || !strings.Contains(r.Stdout, "MISSION_NOT_COMPLETED") {
		t.Fatalf("create on open mission: %d %s", r.Code, r.Stdout)
	}
	if r := s.Run("agent", "retrospect", "synthesize", "--mission", slug, "--json"); r.Code != 1 || !strings.Contains(r.Stdout, "RETROSPECTIVE_RECORD_MISSING") {
		t.Fatalf("synthesize without record: %d %s", r.Code, r.Stdout)
	}
	if r := s.Run("archive", "create", slug, "--by", "me", "--reason", "x"); r.Code != 1 || !strings.Contains(r.Stdout, "AM-1") {
		t.Fatalf("archive non-terminal: %d %s", r.Code, r.Stdout)
	}
	if r := s.Run("archive", "list", "--json"); r.Code != 0 || strings.TrimSpace(r.Stdout) != "[]" {
		t.Fatalf("archive list: %d %s", r.Code, r.Stdout)
	}
	s.WriteFile("pyproject.toml", "[project]\nversion = \"3.1.0a7\"\n")
	r = s.Run("agent", "release", "prep", "--channel", "beta", "--json")
	if r.Code != 0 || decode(t, r.Stdout).GetString("proposed_version") != "3.1.0b1" {
		t.Fatalf("release prep: %d %s", r.Code, r.Stdout)
	}
}
