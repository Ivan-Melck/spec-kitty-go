package orchestrator

import (
	"strings"
	"testing"
)

func TestParsePolicy(t *testing.T) {
	ok := `{"orchestrator_id":"o","orchestrator_version":"1","agent_family":"c","approval_mode":"a","sandbox_mode":"s","network_mode":"n","dangerous_flags":[],"tool_restrictions":null}`
	p, err := ParsePolicy(ok)
	if err != nil || p.OrchestratorID != "o" || p.ToolRestrictions != nil {
		t.Fatalf("ParsePolicy: %v %+v", err, p)
	}
	cases := map[string]string{
		`[`:  "--policy is not valid JSON: Expecting value: line 1 column 2 (char 1)",
		`[]`: "--policy must be a JSON object",
		`{}`: "--policy missing required field: 'orchestrator_id'",
		`{"orchestrator_id":1,"orchestrator_version":"1","agent_family":"c","approval_mode":"a","sandbox_mode":"s","network_mode":"n","dangerous_flags":[]}`:          "--policy.orchestrator_id must be a string",
		`{"orchestrator_id":"o","orchestrator_version":"1","agent_family":"c","approval_mode":"a","sandbox_mode":"s","network_mode":"n","dangerous_flags":["--yolo"]}`: "--policy.dangerous_flags contains a banned flag: '--yolo'. Banned flags must never appear in orchestrator policy payloads.",
		`{"orchestrator_id":"o","orchestrator_version":"1","agent_family":"c","approval_mode":"a","sandbox_mode":"s","network_mode":"n","dangerous_flags":[],"x":{"y":["a-token"]}}`: "--policy field 'x.y[0]' appears to contain a secret (matched pattern: token|secret|key|password|credential). Do not pass secrets via --policy.",
	}
	for in, want := range cases {
		if _, err := ParsePolicy(in); err == nil || err.Error() != want {
			t.Errorf("ParsePolicy(%s) = %v, want %q", in, err, want)
		}
	}
}

func TestVersionOrdering(t *testing.T) {
	min, _ := ParseVersion(MinProviderVersion)
	below := []string{"0.0.1", "0.1.0rc1", "0.1.0.dev3", "0.0.99.post1", "0.1a1"}
	above := []string{"0.1.0", "v0.1", "0.1.0+local", "1!0.0.1", "0.1.0.post1", "1.0"}
	for _, s := range below {
		v, ok := ParseVersion(s)
		if !ok || !v.Less(min) {
			t.Errorf("%s should be < %s", s, MinProviderVersion)
		}
	}
	for _, s := range above {
		v, ok := ParseVersion(s)
		if !ok || v.Less(min) {
			t.Errorf("%s should be >= %s", s, MinProviderVersion)
		}
	}
	if _, ok := ParseVersion("abc"); ok {
		t.Error("abc should be invalid")
	}
}

func TestEnvelopeShape(t *testing.T) {
	env := Envelope("x", false, nil, "E")
	keys := strings.Join(env.Keys(), ",")
	if keys != "contract_version,command,timestamp,correlation_id,success,error_code,data" {
		t.Errorf("keys %s", keys)
	}
	if UnsafeSegmentReason("ok-1.2") != "" || UnsafeSegmentReason("a/b") == "" || UnsafeSegmentReason(" a") == "" {
		t.Error("UnsafeSegmentReason")
	}
	if !IsAllowedErrorCode("MISSION_NOT_FOUND") || IsAllowedErrorCode("FEATURE_NOT_FOUND") {
		t.Error("IsAllowedErrorCode")
	}
}
