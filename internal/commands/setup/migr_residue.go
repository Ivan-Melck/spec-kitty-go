package setup

// The residue-safe successors of the 0.9.0, 0.9.1, 2.0.6 and 3.1.1
// migrations (upgrade/migrations/_residue_successor.py, #6669). Each shipped
// migration is superseded (a recorded no-op); its successor repairs the same
// Missions, leaves a kitty-specs/ residue folder untouched and reports it once.
// A successor is inert once its predecessor is settled in the selection view.

import (
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/migrate"
)

func init() {
	migrationImpls["0.9.0_residue_safe_frontmatter_only_lanes"] = residueSafeLaneSuccessor("0.9.0_frontmatter_only_lanes", func(p *residueProbes) migrationImpl {
		return frontmatterOnlyLanes(func(f string) bool { return hasLaneDirs(f) && !p.isResidue(f) })
	})
	migrationImpls["0.9.1_residue_safe_complete_migration"] = residueSafeLaneSuccessor("0.9.1_complete_migration", func(p *residueProbes) migrationImpl {
		return completeLaneMigration(func(f string) bool { return hasLaneDirs(f) && !p.isResidue(f) }, func(root string) []string {
			missions, _ := partitionCheckoutSpecs(root, p)
			var files []string
			for _, m := range missions {
				files = append(files, rglobMD(m)...)
			}
			return files
		})
	})
	migrationImpls["2.0.6_residue_safe_consistency_sweep"] = residueSafeWalker("2.0.6_consistency_sweep", consistencySweep)
	migrationImpls["3.1.1_residue_safe_normalize_status_json"] = residueSafeWalker("3.1.1_normalize_status_json", normalizeStatusJSON)
}

// residueProbes is ResidueProbes: one tracked-path probe per checkout root,
// shared by every residue decision of one detect or apply. A nil *residueProbes
// probes lazily per folder (outside detect/apply).
type residueProbes struct{ byRoot map[string]map[string]bool }

func newResidueProbes() *residueProbes { return &residueProbes{byRoot: map[string]map[string]bool{}} }

func (p *residueProbes) tracked(root string) map[string]bool {
	if t, ok := p.byRoot[root]; ok {
		return t
	}
	t := migrate.ProbeTrackedMissionPaths(root)
	p.byRoot[root] = t
	return t
}

// isResidue judges the kitty-specs/ child folder against its own checkout.
func (p *residueProbes) isResidue(folder string) bool {
	if p == nil {
		return migrate.IsKittySpecsResidueLazy(folder)
	}
	root := filepath.Dir(filepath.Dir(folder))
	return migrate.IsKittySpecsResidue(folder, root, p.tracked(root))
}

// partitionKittySpecs is partition_kitty_specs: the children of
// <checkout>/kitty-specs split into (missions, residue), sorted.
func partitionKittySpecs(checkout string, p *residueProbes) (missions, residue []string) {
	specs := filepath.Join(checkout, "kitty-specs")
	if !isDir(specs) {
		return nil, nil
	}
	if p == nil {
		p = newResidueProbes()
	}
	tracked := p.tracked(checkout)
	for _, child := range subdirs(specs) {
		if migrate.IsKittySpecsResidue(child, checkout, tracked) {
			residue = append(residue, child)
		} else {
			missions = append(missions, child)
		}
	}
	return missions, residue
}

// partitionCheckoutSpecs is partition_checkout_specs: the main checkout's
// kitty-specs/ children, then each .worktrees/* one's.
func partitionCheckoutSpecs(root string, p *residueProbes) (missions, residue []string) {
	missions, residue = partitionKittySpecs(root, p)
	for _, wt := range worktreeDirs(root) {
		m, r := partitionKittySpecs(wt, p)
		missions = append(missions, m...)
		residue = append(residue, r...)
	}
	return missions, residue
}

// residueWarnings is residue_warnings: one notice per folder, naming its
// project-relative POSIX path.
func residueWarnings(residue []string, root string) []string {
	var out []string
	for _, folder := range residue {
		rel, err := filepath.Rel(root, folder)
		if err != nil {
			rel = folder
		}
		out = append(out, migrate.ResidueNotice(filepath.ToSlash(rel)))
	}
	return out
}

// residueSafeLaneSuccessor is ResidueSafeLaneSuccessorMixin over a shipped
// lane walker whose folder predicates consult the run's probes.
func residueSafeLaneSuccessor(predecessor string, shipped func(*residueProbes) migrationImpl) migrationImpl {
	return migrationImpl{
		detect: func(root string) bool {
			if settledInSelectionView(root, predecessor) {
				return false
			}
			return shipped(newResidueProbes()).detect(root)
		},
		canApply: func(string) (bool, string) { return ok() },
		apply: func(root string, dryRun bool) migrationResult {
			// Probe every checkout before the shipped walk moves a file.
			probes := newResidueProbes()
			_, residue := partitionCheckoutSpecs(root, probes)
			result := shipped(probes).apply(root, dryRun)
			result.Warnings = append(result.Warnings, residueWarnings(residue, root)...)
			return result
		},
	}
}

// residueSafeWalker is the 2.0.6 / 3.1.1 successor: the frozen walk over the
// main checkout's Missions only, the residue folders reported after it.
func residueSafeWalker(predecessor string, shipped func(missions func(string) []string) migrationImpl) migrationImpl {
	missionsOnly := func(root string) []string {
		missions, _ := partitionKittySpecs(root, nil)
		return missions
	}
	walker := shipped(missionsOnly)
	return migrationImpl{
		detect: func(root string) bool {
			if settledInSelectionView(root, predecessor) {
				return false
			}
			return walker.detect(root)
		},
		canApply: walker.canApply,
		apply: func(root string, dryRun bool) migrationResult {
			// Probe first, as the 0.9.x successors do.
			missions, residue := partitionKittySpecs(root, nil)
			result := shipped(func(string) []string { return missions }).apply(root, dryRun)
			result.Warnings = append(result.Warnings, residueWarnings(residue, root)...)
			return result
		},
	}
}

// appendResidueAwareWarnings is runner._append_warnings: lines appended in
// order, except that a residue notice naming a folder (resolved under
// noticeRoot, the checkout its path is relative to) already named in this
// run is dropped.
func appendResidueAwareWarnings(target, lines []string, noticeRoot string, seen map[string]bool) []string {
	for _, line := range lines {
		if rel, ok := migrate.ResidueNoticePath(line); ok {
			key := resolvedAbsPath(filepath.Join(noticeRoot, filepath.FromSlash(rel)))
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		target = append(target, line)
	}
	return target
}

func resolvedAbsPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
