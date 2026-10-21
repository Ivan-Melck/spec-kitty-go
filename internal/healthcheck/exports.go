package healthcheck

import "sort"

// TrackedSpecs is context.mission_resolver.tracked_mission_paths: every
// git-tracked path under kitty-specs/ (nil outside a git work tree).
func TrackedSpecs(root string) map[string]bool { return trackedSpecs(root) }

// IsMissionDir is context.mission_resolver.is_mission_dir for the
// kitty-specs/<name> directory dir (#5812 residue rule).
func IsMissionDir(dir, name string, tracked map[string]bool) bool {
	return isMissionDir(dir, name, tracked)
}

// AddFinding appends a finding to the named mission (no-op when absent) and
// re-sorts its findings by (artifact_path, code), like the engine's merge
// of invoking-checkout disagreements.
func (r *AuditReport) AddFinding(slug, code, severity, artifact, detail string) {
	for _, m := range r.Missions {
		if m.Slug != slug {
			continue
		}
		m.Findings = append(m.Findings, fnd(code, severity, artifact, detail))
		sort.SliceStable(m.Findings, func(i, j int) bool {
			a, b := m.Findings[i], m.Findings[j]
			if a.ArtifactPath != b.ArtifactPath {
				return a.ArtifactPath < b.ArtifactPath
			}
			return a.Code < b.Code
		})
	}
}
