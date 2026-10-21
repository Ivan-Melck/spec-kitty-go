package migrate

import (
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// hasMergeMarker is _last_merge_marker_at(dir) is not None.
func hasMergeMarker(dir string) bool {
	meta, err := LoadMetaFailClosed(dir)
	if err != nil || meta == nil {
		return false
	}
	v, _ := meta.Get("merged_at")
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return false
	}
	_, perr := clock.ParseISO(strings.Replace(s, "Z", "+00:00", 1))
	return perr == nil
}

// lifecycleSnapshot reduces dir's event log write-free (empty when the log is
// missing or unreadable).
func lifecycleSnapshot(dir string) *status.Snapshot {
	snap, err := status.MaterializeSnapshot(dir)
	if err != nil || snap == nil {
		snap = status.Reduce(nil, nil, false)
		snap.MissionSlug = ""
	}
	return snap
}

// LifecycleState is derive_mission_lifecycle(dir).state.
func LifecycleState(dir string) string {
	lc := status.LifecycleObject(dir, lifecycleSnapshot(dir), clock.Now())
	return lc.GetString("state")
}

// LifecycleObject is derive_mission_lifecycle(dir).to_dict().
func LifecycleObject(dir string) *jsonx.Object {
	return status.LifecycleObject(dir, lifecycleSnapshot(dir), clock.Now())
}

// IsMissionCompleted is status.lifecycle.is_mission_completed.
func IsMissionCompleted(dir string) bool {
	if hasMergeMarker(dir) {
		return true
	}
	s := LifecycleState(dir)
	return s == "recently_completed" || s == "archived"
}
