package status

import (
	"fmt"
	"strings"
)

// Resolved-binding explicit-absence sentinels.
const (
	ResolvedModelAbsent          = "__resolved_model_absent__"
	ResolvedProfileAbsent        = "__resolved_profile_absent__"
	ResolvedProfileVersionAbsent = "__resolved_profile_version_absent__"
	ResolvedProviderAbsent       = "__resolved_provider_absent__"
)

// ResolvedBinding is the dispatch-resolved runtime identity of a claim.
type ResolvedBinding struct {
	AgentProfile, AgentProfileVersion, Model, Provider *string
}

func orSentinel(p *string, s string) *string {
	if p != nil {
		v := *p
		return &v
	}
	return &s
}

// ToDelta is ResolvedBinding.to_delta(role=...).
func (b *ResolvedBinding) ToDelta(role string) Delta {
	r := role
	return Delta{
		Role:                &r,
		AgentProfile:        orSentinel(b.AgentProfile, ResolvedProfileAbsent),
		AgentProfileVersion: orSentinel(b.AgentProfileVersion, ResolvedProfileVersionAbsent),
		Model:               orSentinel(b.Model, ResolvedModelAbsent),
		Provider:            orSentinel(b.Provider, ResolvedProviderAbsent),
	}
}

// GenericImplementationActors are placeholder actors that name no owner.
var GenericImplementationActors = map[string]bool{"implement-command": true, "unknown": true, "user": true}

// ParseAgentBoundary is parse_agent_boundary_string: "tool:model:profile:role".
func ParseAgentBoundary(raw string) (tool string, model, profile, role *string, err error) {
	seg := strings.Split(raw, ":")
	for len(seg) < 4 {
		seg = append(seg, "")
	}
	opt := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	if seg[0] == "" {
		return "", nil, nil, nil, fmt.Errorf("Empty agent tool in --agent value: %s", PyRepr(raw))
	}
	return seg[0], opt(seg[1]), opt(seg[2]), opt(seg[3]), nil
}

// ActorKey is work_package_lifecycle._actor_key: the tool-scoped comparison
// key of an actor ("" = None).
func ActorKey(a Actor) string {
	projected := a.Identity()
	if a.Binding == nil && projected != "" {
		if tool, _, _, _, err := ParseAgentBoundary(projected); err == nil {
			projected = tool
		}
	}
	return strings.TrimSpace(projected)
}

// ActorKeyStr is ActorKey for a plain string actor.
func ActorKeyStr(s string) string { return ActorKey(StrActor(s)) }

// SelfAssertingActor is build_self_asserting_actor: the structured
// {role, profile, tool, model} claim/review actor.
func SelfAssertingActor(role, agent, fallbackTool string, binding *ResolvedBinding) Actor {
	var tool string
	var selfModel, selfProfile *string
	if agent != "" {
		if t, m, p, _, err := ParseAgentBoundary(agent); err == nil {
			tool, selfModel, selfProfile = t, m, p
		}
	}
	if tool == "" {
		tool = fallbackTool
	}
	pick := func(b, s *string) *string {
		if b != nil && *b != "" {
			v := *b
			return &v
		}
		return s
	}
	var bp, bm *string
	if binding != nil {
		bp, bm = binding.AgentProfile, binding.Model
	}
	r := role
	var tp *string
	if tool != "" {
		t := tool
		tp = &t
	}
	return Actor{Binding: &Binding{Role: &r, Profile: pick(bp, selfProfile), Tool: tp, Model: pick(bm, selfModel)}}
}

var implementingLanes = map[Lane]bool{Claimed: true, InProgress: true}
var reviewerSourceLanes = map[Lane]bool{ForReview: true, InReview: true, Approved: true}

// LatestImplementer is latest_implementer_actor: the actor identity of the
// WP's latest qualifying implementing claim ("" when none).
func LatestImplementer(events []*Event, wpID string) string {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.WPID != wpID || strings.HasPrefix(e.Actor.Identity(), MigrationActorPrefix) || !implementingLanes[e.To] {
			continue
		}
		if reviewerSourceLanes[e.From] && e.ReviewRef != nil && *e.ReviewRef != "" {
			continue
		}
		if GenericImplementationActors[ActorKey(e.Actor)] {
			continue
		}
		return e.Actor.Identity()
	}
	return ""
}

// IsLatestImplementer is is_latest_implementer.
func IsLatestImplementer(latest, actor string) bool {
	if latest == "" {
		return false
	}
	lk := ActorKeyStr(latest)
	if lk == "" || GenericImplementationActors[lk] {
		return false
	}
	rk := ActorKeyStr(actor)
	return rk != "" && rk == lk
}
