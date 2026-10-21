package orchestrator

import (
	"fmt"
	"regexp"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// BannedFlags may never appear in a policy's dangerous_flags.
var BannedFlags = map[string]bool{
	"--full-auto": true,
	"--yolo":      true,
	"--dangerously-bypass-approvals-and-sandbox": true,
	"--dangerously-skip-permissions":             true,
}

var secretPattern = regexp.MustCompile(`(?i)(token|secret|key|password|credential)`)

var requiredPolicyFields = []string{
	"orchestrator_id", "orchestrator_version", "agent_family", "approval_mode",
	"sandbox_mode", "network_mode", "dangerous_flags",
}

var stringPolicyFields = requiredPolicyFields[:6]

// Policy is the validated --policy metadata (PolicyMetadata).
type Policy struct {
	OrchestratorID      string
	OrchestratorVersion string
	AgentFamily         string
	ApprovalMode        string
	SandboxMode         string
	NetworkMode         string
	DangerousFlags      []string
	ToolRestrictions    *string
}

// Object is policy_to_dict (dataclasses.asdict field order).
func (p *Policy) Object() *jsonx.Object {
	flags := make([]any, len(p.DangerousFlags))
	for i, f := range p.DangerousFlags {
		flags[i] = f
	}
	var tr any
	if p.ToolRestrictions != nil {
		tr = *p.ToolRestrictions
	}
	return jsonx.NewObject(
		"orchestrator_id", p.OrchestratorID,
		"orchestrator_version", p.OrchestratorVersion,
		"agent_family", p.AgentFamily,
		"approval_mode", p.ApprovalMode,
		"sandbox_mode", p.SandboxMode,
		"network_mode", p.NetworkMode,
		"dangerous_flags", flags,
		"tool_restrictions", tr,
	)
}

// PolicyError is a --policy validation failure (ValueError text).
type PolicyError struct{ Msg string }

func (e *PolicyError) Error() string { return e.Msg }

func policyErr(format string, a ...any) error { return &PolicyError{Msg: fmt.Sprintf(format, a...)} }

// ParsePolicy is parse_and_validate_policy.
func ParsePolicy(raw string) (*Policy, error) {
	v, err := pyjson.Loads(raw)
	if err != nil {
		return nil, policyErr("--policy is not valid JSON: %s", err.Error())
	}
	data, ok := v.(*jsonx.Object)
	if !ok {
		return nil, policyErr("--policy must be a JSON object")
	}
	for _, f := range requiredPolicyFields {
		if !data.Has(f) {
			return nil, policyErr("--policy missing required field: '%s'", f)
		}
	}
	for _, f := range stringPolicyFields {
		if _, ok := mustGet(data, f).(string); !ok {
			return nil, policyErr("--policy.%s must be a string", f)
		}
	}
	rawFlags, ok := mustGet(data, "dangerous_flags").([]any)
	if !ok {
		return nil, policyErr("--policy.dangerous_flags must be a JSON array")
	}
	flags := make([]string, 0, len(rawFlags))
	for _, f := range rawFlags {
		s, ok := f.(string)
		if !ok {
			return nil, policyErr("--policy.dangerous_flags entries must be strings")
		}
		if BannedFlags[s] {
			return nil, policyErr("--policy.dangerous_flags contains a banned flag: %s. Banned flags must never appear in orchestrator policy payloads.", pyjson.ReprString(s))
		}
		flags = append(flags, s)
	}
	var tr *string
	if t, present := data.Get("tool_restrictions"); present && t != nil {
		s, ok := t.(string)
		if !ok {
			return nil, policyErr("--policy.tool_restrictions must be a string or null")
		}
		tr = &s
	}
	for _, kv := range data.Pairs() {
		if err := rejectSecret(kv.Key, kv.Value); err != nil {
			return nil, err
		}
	}
	return &Policy{
		OrchestratorID:      mustGet(data, "orchestrator_id").(string),
		OrchestratorVersion: mustGet(data, "orchestrator_version").(string),
		AgentFamily:         mustGet(data, "agent_family").(string),
		ApprovalMode:        mustGet(data, "approval_mode").(string),
		SandboxMode:         mustGet(data, "sandbox_mode").(string),
		NetworkMode:         mustGet(data, "network_mode").(string),
		DangerousFlags:      flags,
		ToolRestrictions:    tr,
	}, nil
}

func mustGet(o *jsonx.Object, k string) any {
	v, _ := o.Get(k)
	return v
}

func rejectSecret(path string, v any) error {
	switch t := v.(type) {
	case string:
		if secretPattern.MatchString(t) {
			return policyErr("--policy field '%s' appears to contain a secret (matched pattern: token|secret|key|password|credential). Do not pass secrets via --policy.", path)
		}
	case []any:
		for i, item := range t {
			if err := rejectSecret(fmt.Sprintf("%s[%d]", path, i), item); err != nil {
				return err
			}
		}
	case *jsonx.Object:
		for _, kv := range t.Pairs() {
			if err := rejectSecret(path+"."+kv.Key, kv.Value); err != nil {
				return err
			}
		}
	}
	return nil
}
