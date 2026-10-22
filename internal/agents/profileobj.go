package agents

import (
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// ProfileFromObject builds a Profile from already-loaded (and possibly
// layer-merged) profile data, like ParseProfile without the YAML step.
func ProfileFromObject(o *jsonx.Object) Profile {
	p := Profile{
		ID:              strField(o, "profile-id", "profile_id"),
		Name:            strField(o, "name"),
		Description:     strField(o, "description"),
		Purpose:         strField(o, "purpose"),
		Model:           strField(o, "model"),
		ReasoningEffort: strField(o, "model-reasoning-effort", "model_reasoning_effort"),
		SandboxMode:     strField(o, "sandbox-mode", "sandbox_mode"),
		Sentinel:        o.GetBool("sentinel"),
	}
	for _, r := range o.GetList("roles") {
		p.Roles = append(p.Roles, fmt.Sprint(r))
	}
	if s := o.GetObject("specialization"); s != nil {
		p.PrimaryFocus = strField(s, "primary-focus", "primary_focus")
		p.AvoidanceBoundary = strField(s, "avoidance-boundary", "avoidance_boundary")
	}
	return p
}
