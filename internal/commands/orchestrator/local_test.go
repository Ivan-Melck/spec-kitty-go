package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"

	orch "github.com/spec-kitty/go-kitty3/internal/orchestrator"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

type envelope struct {
	ContractVersion string         `json:"contract_version"`
	Command         string         `json:"command"`
	Timestamp       string         `json:"timestamp"`
	CorrelationID   string         `json:"correlation_id"`
	Success         bool           `json:"success"`
	ErrorCode       *string        `json:"error_code"`
	Data            map[string]any `json:"data"`
}

func parseEnv(t *testing.T, r testkit.Result) envelope {
	t.Helper()
	var e envelope
	if err := json.Unmarshal([]byte(r.Stdout), &e); err != nil {
		t.Fatalf("not one JSON envelope: %v\nstdout=%q stderr=%q", err, r.Stdout, r.Stderr)
	}
	if strings.Count(strings.TrimSpace(r.Stdout), "\n") != 0 {
		t.Fatalf("expected a single line: %q", r.Stdout)
	}
	return e
}

func expectCode(t *testing.T, r testkit.Result, exit int, code string) envelope {
	t.Helper()
	e := parseEnv(t, r)
	if r.Code != exit {
		t.Errorf("exit %d, want %d (%s)", r.Code, exit, r.Stdout)
	}
	got := ""
	if e.ErrorCode != nil {
		got = *e.ErrorCode
	}
	if got != code {
		t.Errorf("error_code %q, want %q (%s)", got, code, r.Stdout)
	}
	return e
}

// TestLocalOrchestratorAPI exercises the envelope contract and the WP loop
// without the oracle.
func TestLocalOrchestratorAPI(t *testing.T) {
	s := missionfx.NewLocalMission(t)
	slug := missionfx.LocalSlug
	// The recorded planning commit of the fixture is foreign to this
	// repository: re-pin it to the local tip.
	lanesRel := "kitty-specs/" + slug + "/lanes.json"
	s.WriteFile(lanesRel, strings.ReplaceAll(s.ReadFile(lanesRel), "e679ed7564e7d0b20650ae6f28def94c5d593db9", s.Git("rev-parse", "HEAD")))
	s.Git("commit", "-q", "-am", "re-pin planning commit")
	e := expectCode(t, s.Run(api), 2, "USAGE_ERROR")
	if e.Command != "orchestrator-api.unknown" || e.Data["message"] != "Missing command." {
		t.Errorf("bare group: %+v", e)
	}
	expectCode(t, s.Run(api, "mission-state"), 2, "USAGE_ERROR")
	expectCode(t, s.Run(api, "mission-state", "--mission", "x", "--nope"), 2, "USAGE_ERROR")
	e = expectCode(t, s.Run(api, "contract-version"), 0, "")
	if e.ContractVersion != orch.ContractVersion || !strings.HasPrefix(e.CorrelationID, "corr-") || len(e.CorrelationID) != 37 {
		t.Errorf("contract-version envelope: %+v", e)
	}
	expectCode(t, s.Run(api, "contract-version", "--provider-version", "0.0.9"), 1, "CONTRACT_VERSION_MISMATCH")
	expectCode(t, s.Run(api, "mission-state", "--mission", "../x"), 1, "INVALID_MISSION")
	expectCode(t, s.Run(api, "mission-state", "--mission", "nope"), 1, "MISSION_NOT_FOUND")
	e = expectCode(t, s.Run(api, "mission-state", "--mission", slug), 0, "")
	if wps, _ := e.Data["work_packages"].([]any); len(wps) != 2 {
		t.Errorf("work_packages: %v", e.Data)
	}
	e = expectCode(t, s.Run(api, "list-ready", "--mission", slug), 0, "")
	if r, _ := e.Data["ready_work_packages"].([]any); len(r) != 1 {
		t.Errorf("ready: %v", e.Data)
	}
	expectCode(t, s.Run(api, "start-implementation", "--mission", slug, "--wp", "WP01", "--actor", "a"), 1, "POLICY_METADATA_REQUIRED")
	expectCode(t, s.Run(api, "start-implementation", "--mission", slug, "--wp", "WP02", "--actor", "a", "--policy", testPolicy), 1, "DEPENDENCIES_NOT_SATISFIED")
	e = expectCode(t, s.Run(api, "start-implementation", "--mission", slug, "--wp", "WP01", "--actor", "a", "--policy", testPolicy), 0, "")
	if e.Data["to_lane"] != "in_progress" || e.Data["lane_id"] != "lane-a" {
		t.Errorf("start-implementation: %v", e.Data)
	}
	expectCode(t, s.Run(api, "start-implementation", "--mission", slug, "--wp", "WP01", "--actor", "b", "--policy", testPolicy), 1, "WP_ALREADY_CLAIMED")
	expectCode(t, s.Run(api, "append-history", "--mission", slug, "--wp", "WP01", "--actor", "a", "--note", "hi"), 0, "")
	expectCode(t, s.Run(api, "transition", "--mission", slug, "--wp", "WP01", "--to", "for_review", "--actor", "a"), 1, "POLICY_METADATA_REQUIRED")
	expectCode(t, s.Run(api, "design-status", "--mission", slug), 0, "")
	expectCode(t, s.Run(api, "artifact-read", "--mission", slug, "--kind", "specification"), 0, "")
	expectCode(t, s.Run(api, "design-context", "--mission-type", "software-dev"), 0, "")
	expectCode(t, s.Run(api, "open-decision", "--mission", slug, "--origin", "nope", "--input-key", "k", "--question", "q", "--actor", "a", "--policy", testPolicy), 1, "INVALID_ORIGIN_FLOW")
	expectCode(t, s.Run(api, "consolidate-mission", "--mission", slug, "--strategy", "octopus"), 1, "UNSUPPORTED_STRATEGY")
}

func TestUsageMessage(t *testing.T) {
	cases := map[string]string{
		"Invalid value for '--origin-check': 'x' is not one of 'enforce', 'warn' or 'off'.": "Invalid value: 'x' is not one of 'enforce', 'warn', 'off'.",
		"Invalid value for '--topology': 'x' is not one of 'a' or 'b'.":                     "Invalid value for '--topology': 'x' is not one of 'a', 'b'.",
		"Missing option '--mission'.": "Missing option '--mission'.",
	}
	for in, want := range cases {
		if got := usageMessage(in); got != want {
			t.Errorf("usageMessage(%q) = %q, want %q", in, got, want)
		}
	}
}
