// Package status implements the canonical WP status model: the three row
// families of status.events.jsonl (lane transitions, InnerStateChanged
// annotations, lifecycle envelopes), the append-only store, the reducer that
// materializes status.json, and the WP lane state machine.
package status

import "strings"

// Lane is a canonical WP lifecycle lane.
type Lane string

// Lanes.
const (
	Genesis       Lane = "genesis"
	Uninitialized Lane = "uninitialized"
	Planned       Lane = "planned"
	Claimed       Lane = "claimed"
	InProgress    Lane = "in_progress"
	ForReview     Lane = "for_review"
	InReview      Lane = "in_review"
	Approved      Lane = "approved"
	Done          Lane = "done"
	Blocked       Lane = "blocked"
	Canceled      Lane = "canceled"
)

// AllLanes in enum declaration order.
var AllLanes = []Lane{Genesis, Uninitialized, Planned, Claimed, InProgress, ForReview, InReview, Approved, Done, Blocked, Canceled}

// DisplayLanes are the nine displayed lanes (summary order is sorted by the
// JSON encoder; this is enum order).
var DisplayLanes = []Lane{Planned, Claimed, InProgress, ForReview, InReview, Approved, Done, Blocked, Canceled}

// ForwardOrder is the CLI forward pipeline used for multi-hop moves.
var ForwardOrder = []Lane{Planned, Claimed, InProgress, ForReview, InReview, Approved, Done}

// LaneAliases maps CLI input aliases to canonical lanes.
var LaneAliases = map[string]Lane{"doing": InProgress}

// IsLane reports whether s is a canonical lane value.
func IsLane(s string) bool {
	for _, l := range AllLanes {
		if string(l) == s {
			return true
		}
	}
	return false
}

// ResolveLane resolves a lane name or alias (case-insensitive, trimmed).
func ResolveLane(s string) (Lane, bool) {
	v := strings.ToLower(strings.TrimSpace(s))
	if a, ok := LaneAliases[v]; ok {
		return a, true
	}
	if IsLane(v) {
		return Lane(v), true
	}
	return "", false
}

// IsTerminal reports done/canceled.
func IsTerminal(l Lane) bool { return l == Done || l == Canceled }

// IsDisplay reports whether l is one of the nine display lanes.
func IsDisplay(l Lane) bool { return l != Genesis && l != Uninitialized && IsLane(string(l)) }

// ForwardIndex returns the lane's index in ForwardOrder or -1.
func ForwardIndex(l Lane) int {
	for i, x := range ForwardOrder {
		if x == l {
			return i
		}
	}
	return -1
}
