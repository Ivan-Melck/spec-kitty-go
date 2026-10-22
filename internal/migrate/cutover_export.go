package migrate

import "github.com/spec-kitty/go-kitty3/internal/mission"

// MissionNeedsCutover is m_zz_runtime_state_backfill._mission_needs_cutover:
// a mission below the snapshot-authority phase that still carries evictable
// legacy runtime state (used by the upgrade migration).
func MissionNeedsCutover(dir string) bool {
	meta, err := mission.ReadMeta(dir)
	if err == nil && meta != nil && isSnapshotAuthority(meta) {
		return false
	}
	legacy, err := readLegacyRuntime(dir)
	if err != nil {
		return false
	}
	for _, r := range legacy {
		if r.hasEvictableState() {
			return true
		}
	}
	return false
}
