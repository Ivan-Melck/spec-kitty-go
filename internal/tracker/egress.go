package tracker

import (
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// LocalSubprocess is the only remaining egress destination
// (EgressDestination.LOCAL_SUBPROCESS).
const LocalSubprocess = "local_subprocess"

// Channel-2 states.
const (
	EgressStateAbsent = "absent"
	EgressStateFault  = "fault"
)

type unreadable struct{}

// EgressVerdict is TrackerEgressVerdict.
type EgressVerdict struct {
	Refused  bool
	State    string
	Raw      any
	Message  string
	Remedies []string
}

const localGrantRemedy = "record `tracker.egress: permitted` in this project's own .kittify/config.yaml"

func resolveChannel2(root string) (string, any) {
	c, err := LoadConfig(root)
	if err != nil {
		return EgressStateFault, unreadable{}
	}
	raw := c.Egress
	if raw == EgressAbsent {
		return EgressStateAbsent, raw
	}
	if s, ok := raw.(string); ok && (s == EgressRefused || s == EgressPermitted) {
		return s, raw
	}
	return EgressStateFault, raw
}

// Verdict mirrors tracker_egress_verdict for destination LOCAL_SUBPROCESS.
// root == "" means no project root could be resolved.
func Verdict(root string) EgressVerdict {
	dest := LocalSubprocess
	if root == "" {
		return EgressVerdict{Refused: true, State: EgressStateAbsent, Raw: EgressAbsent,
			Message: "no project root could be resolved, so no tracker.egress decision can be read; refusing tracker egress to " + dest}
	}
	state, raw := resolveChannel2(root)
	if state == EgressPermitted {
		return EgressVerdict{State: state, Raw: raw,
			Message: fmt.Sprintf("tracker egress to %s is permitted by tracker.egress: 'permitted', recorded in this project's own .kittify/config.yaml", dest)}
	}
	v := EgressVerdict{Refused: true, State: state, Raw: raw}
	switch state {
	case EgressStateFault:
		offending := "the project's tracker configuration could not be read"
		if _, ok := raw.(unreadable); !ok {
			offending = pyjson.Repr(raw)
		}
		v.Message = fmt.Sprintf("tracker.egress is set to %s, which is not a legal value; refusing tracker egress to %s (legal values are 'refused' and 'permitted')", offending, dest)
	case EgressRefused:
		v.Message = "tracker.egress is recorded as 'refused' in this project's own .kittify/config.yaml; refusing tracker egress to " + dest
	default:
		v.Message = "no tracker.egress key is recorded in this project's own .kittify/config.yaml; refusing tracker egress to " + dest
		v.Remedies = []string{localGrantRemedy}
	}
	return v
}

// Render joins message and remedies (_render_refusal).
func (v EgressVerdict) Render() string {
	if len(v.Remedies) == 0 {
		return v.Message
	}
	lines := make([]string, len(v.Remedies))
	for i, r := range v.Remedies {
		lines[i] = "  - " + r
	}
	return v.Message + "\nRemedies:\n" + strings.Join(lines, "\n")
}
