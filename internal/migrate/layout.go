package migrate

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
)

// Layout migration (runtime/migrate.py execute_migration): classify the
// per-project .kittify/ files against the shipped package assets.

var projectSpecificPaths = map[string]bool{"config.yaml": true, "metadata.yaml": true, "memory": true, "workspaces": true, "logs": true, "overrides": true, "merge-state.json": true}

var sharedAssetDirs = map[string]bool{"templates": true, "missions": true, "scripts": true, "command-templates": true}

// Conflict reasons (ConflictReason).
const (
	ConflictDestinationExists        = "destination_exists"
	ConflictDestinationParentBlocked = "destination_parent_blocked"
	ConflictSourceIsSymlink          = "source_is_symlink"
)

// LayoutConflict is MigrationConflict.
type LayoutConflict struct {
	Source, Destination, Reason string
}

// LayoutReport is MigrationReport (absolute paths).
type LayoutReport struct {
	Removed    []string
	Superseded []string
	Moved      [][2]string
	Kept       []string
	Unknown    []string
	Conflicts  []LayoutConflict
}

const packageAssetRoot = "packs/built-in/missions"

func packageCounterpart(rel, mission string) []byte {
	for _, p := range []string{path.Join(packageAssetRoot, mission, rel), path.Join(packageAssetRoot, rel)} {
		if st, err := assets.Stat(p); err == nil && !st.IsDir() {
			if b, err := assets.ReadFile(p); err == nil {
				return b
			}
		}
	}
	return nil
}

func isRegular(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func lexists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func isSymlink(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}

func isDirFollow(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// PyPathLess orders paths like pathlib (component-wise).
func PyPathLess(a, b string) bool {
	pa, pb := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

// rglob lists every entry below dir (pathlib rglob("*"), sorted).
func rglob(dir string) []string {
	var out []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || p == dir {
			return nil
		}
		out = append(out, p)
		return nil
	})
	sort.SliceStable(out, func(i, j int) bool { return PyPathLess(out[i], out[j]) })
	return out
}

func moveBlocker(source, dest, overridesRoot string) string {
	if isSymlink(source) {
		return ConflictSourceIsSymlink
	}
	if lexists(dest) {
		return ConflictDestinationExists
	}
	chain := []string{overridesRoot}
	rel, _ := filepath.Rel(overridesRoot, filepath.Dir(dest))
	cur := overridesRoot
	if rel != "." {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			cur = filepath.Join(cur, part)
			chain = append(chain, cur)
		}
	}
	for _, c := range chain {
		if lexists(c) && !isDirFollow(c) {
			return ConflictDestinationParentBlocked
		}
	}
	return ""
}

// OnLayoutMove, when set, observes each customized-file move (the upgrade
// auto-commit's mutation log; upstream's audit hook on os.rename).
var OnLayoutMove func(src, dst string)

// ExecuteLayoutMigration is execute_migration(project_dir, dry_run).
func ExecuteLayoutMigration(projectDir string, dryRun bool) *LayoutReport {
	const mission = "software-dev"
	kittify := filepath.Join(projectDir, ".kittify")
	rep := &LayoutReport{}
	for _, p := range rglob(kittify) {
		if isDirFollow(p) {
			continue
		}
		rel, _ := filepath.Rel(kittify, p)
		rel = filepath.ToSlash(rel)
		top := strings.SplitN(rel, "/", 2)[0]
		switch {
		case projectSpecificPaths[top]:
			rep.Kept = append(rep.Kept, p)
		case sharedAssetDirs[top] || path.Base(rel) == "AGENTS.md":
			if !isRegular(p) {
				rep.Unknown = append(rep.Unknown, p)
				continue
			}
			canon := packageCounterpart(rel, mission)
			if canon == nil {
				overrides := filepath.Join(kittify, "overrides")
				dest := filepath.Join(overrides, filepath.FromSlash(rel))
				if reason := moveBlocker(p, dest, overrides); reason != "" {
					rep.Conflicts = append(rep.Conflicts, LayoutConflict{p, dest, reason})
					continue
				}
				rep.Moved = append(rep.Moved, [2]string{p, dest})
				if !dryRun {
					os.MkdirAll(filepath.Dir(dest), 0o755)
					if OnLayoutMove != nil {
						OnLayoutMove(p, dest)
					}
					os.Rename(p, dest)
				}
				continue
			}
			cur, err := os.ReadFile(p)
			if err == nil && bytes.Equal(cur, canon) {
				if !dryRun {
					os.Remove(p)
				}
				rep.Removed = append(rep.Removed, p)
			} else {
				rep.Superseded = append(rep.Superseded, p)
			}
		default:
			rep.Unknown = append(rep.Unknown, p)
		}
	}
	if !dryRun {
		cleanupEmptyDirs(kittify)
	}
	return rep
}

func cleanupEmptyDirs(kittify string) {
	all := rglob(kittify)
	for i := len(all) - 1; i >= 0; i-- {
		d := all[i]
		if !isDirFollow(d) {
			continue
		}
		rel, _ := filepath.Rel(kittify, d)
		top := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
		if !sharedAssetDirs[top] {
			continue
		}
		if entries, err := os.ReadDir(d); err == nil && len(entries) == 0 {
			os.Remove(d)
		}
	}
}
