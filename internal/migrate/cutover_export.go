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

// LegacyRuntimeReadError is the seed phase's legacy-runtime read of
// cutover_mission: a tasks/WP*.md the frontmatter reader cannot parse
// (LegacyRuntimeReadError upstream) fails the seed, so the cutover never
// flips status_phase.
func LegacyRuntimeReadError(dir string) error {
	_, err := readLegacyRuntime(dir)
	return err
}
