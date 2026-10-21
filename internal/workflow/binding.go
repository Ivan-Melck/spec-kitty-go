package workflow

import (
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/invocation"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// BindingRequest carries the dispatch options of a claim seam
// (--model/--profile/--invocation-id); it is resolved at the claim point,
// after the workspace exists, like upstream _resolve_dispatch_binding.
type BindingRequest struct {
	Model, Profile, InvocationID *string
	// ProfileVersion resolves a profile's schema version (resolve_local).
	ProfileVersion func(string) (string, error)
}

func reprOpt(s string, ok bool) string {
	if !ok {
		return "None"
	}
	return invocation.PyRepr(s)
}

// Resolve is _resolve_dispatch_binding for (wpID, action).
func (r *BindingRequest) Resolve(root, slug, wpID, action string) (*status.ResolvedBinding, error) {
	profile := r.Profile
	var model *string
	if r.InvocationID != nil && *r.InvocationID != "" {
		ev, err := invocation.ReadStartedForClaim(root, *r.InvocationID)
		if err != nil {
			return nil, err
		}
		mid := mission.ReadMetaOrEmpty(project.Specs(root, slug)).GetString("mission_id")
		if ev.MissionID == "" || ev.MissionID != mid {
			return nil, fmt.Errorf("Dispatch Op mission identity does not match claim target: recorded=%s, target=%s", reprOpt(ev.MissionID, ev.MissionID != ""), reprOpt(mid, mid != ""))
		}
		if ev.WPID == "" || ev.WPID != wpID {
			return nil, fmt.Errorf("Dispatch Op work package does not match claim target: recorded=%s, target=%s", reprOpt(ev.WPID, ev.WPID != ""), invocation.PyRepr(wpID))
		}
		if ev.Action != action {
			return nil, fmt.Errorf("Dispatch Op action does not match claim target: recorded=%s, target=%s", invocation.PyRepr(ev.Action), invocation.PyRepr(action))
		}
		if profile != nil && *profile != ev.ProfileID {
			return nil, fmt.Errorf("Dispatch Op profile does not match --profile: recorded=%s, supplied=%s", invocation.PyRepr(ev.ProfileID), invocation.PyRepr(*profile))
		}
		if r.Model != nil && *r.Model != ev.ModelID {
			return nil, fmt.Errorf("Dispatch Op model does not match --model: recorded=%s, supplied=%s", reprOpt(ev.ModelID, ev.ModelID != ""), invocation.PyRepr(*r.Model))
		}
		p := ev.ProfileID
		profile = &p
		if ev.ModelID != "" {
			m := ev.ModelID
			model = &m
		}
	} else if r.Model != nil {
		return nil, fmt.Errorf("--model cannot be recorded as resolved actual without correlated durable dispatch evidence; pass --invocation-id")
	}
	b, err := ResolveBinding(profile, r.ProfileVersion)
	if err != nil {
		return nil, err
	}
	if model != nil {
		prov, err := invocation.ModelProvider(*model)
		if err != nil {
			return nil, err
		}
		b.Model, b.Provider = model, &prov
	}
	return b, nil
}
