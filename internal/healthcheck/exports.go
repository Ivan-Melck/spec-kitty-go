package healthcheck

// TrackedSpecs is context.mission_resolver.tracked_mission_paths: every
// git-tracked path under kitty-specs/ (nil outside a git work tree).
func TrackedSpecs(root string) map[string]bool { return trackedSpecs(root) }

// IsMissionDir is context.mission_resolver.is_mission_dir for the
// kitty-specs/<name> directory dir (#5812 residue rule).
func IsMissionDir(dir, name string, tracked map[string]bool) bool {
	return isMissionDir(dir, name, tracked)
}
