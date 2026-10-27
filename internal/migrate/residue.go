package migrate

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// The kitty-specs/ residue rule of context.mission_resolver (#5812, #6627,
// #6669): a kitty-specs/ child that is not a Mission is left untouched by
// every write seam and walker, and reported with ResidueNotice.

var missionMarkerFiles = []string{"spec.md", "meta.json"}

// missionContent keeps a folder out of residue in file-presence mode.
var missionContent = []string{"spec.md", "meta.json", "plan.md", "tasks.md", "tasks", "status.events.jsonl", "status.json", "lanes.json"}

// residueNotice is RESIDUE_NOTICE, the one operator-facing residue text.
const residueNoticeHead = "not a Mission (residue directory "
const residueNoticeTail = ": no mission_id, and no spec.md or meta.json on disk or in git); left untouched. Add a spec.md or meta.json to make it a Mission, or remove the folder."

var residueNoticeRE = regexp.MustCompile(regexp.QuoteMeta(residueNoticeHead) + `(.+?)` + regexp.QuoteMeta(residueNoticeTail))

// ResidueNotice renders RESIDUE_NOTICE for relPath (POSIX, relative to the
// checkout being changed).
func ResidueNotice(relPath string) string { return residueNoticeHead + relPath + residueNoticeTail }

// ResidueNoticePath returns the folder path embedded in a residue-notice line
// (a caller prefix such as "Worktree wt1: " is tolerated).
func ResidueNoticePath(line string) (string, bool) {
	m := residueNoticeRE.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// gitPrefix is _git_prefix: `git rev-parse --show-prefix` of root, ok=false
// outside a work tree (or without git).
func gitPrefix(root string) (string, bool) {
	res, err := gitx.Exec(root, nil, nil, "rev-parse", "--is-inside-work-tree", "--show-prefix")
	if err != nil || res.Code != 0 {
		return "", false
	}
	lines := strings.Split(res.Stdout, "\n")
	if strings.TrimSpace(lines[0]) != "true" || len(lines) < 2 {
		return "", false
	}
	return lines[1], true
}

// trackedMissionPaths is tracked_mission_paths: every git-tracked path under
// kitty-specs/ relative to root (nil = None: not inside a work tree). A
// failing `git ls-files` is returned as err, worded like GitCommandError.
func trackedMissionPaths(root string) (map[string]bool, error) {
	prefix, ok := gitPrefix(root)
	if !ok {
		return nil, nil
	}
	// kernel.git.tracked_paths: literal pathspecs after "--".
	argv := []string{"--literal-pathspecs", "ls-files", "--full-name", "-z", "--", project.SpecsDir}
	res, err := gitx.Exec(root, nil, nil, argv...)
	if err != nil {
		return nil, nil
	}
	if res.Code != 0 {
		detail := "no error output"
		if s := strings.TrimSpace(res.Stderr); s != "" {
			detail = strings.SplitN(s, "\n", 2)[0]
		}
		return nil, fmt.Errorf("git %s failed in %s (exit %d): %s", strings.Join(argv, " "), root, res.Code, detail)
	}
	tracked := map[string]bool{}
	for _, p := range strings.Split(res.Stdout, "\x00") {
		if p != "" {
			tracked[strings.TrimPrefix(p, prefix)] = true
		}
	}
	return tracked, nil
}

var degradedProbeRoots sync.Map

// ProbeTrackedMissionPaths is probe_tracked_mission_paths: the tracked set
// for residue-aware write seams and walkers. A git failure degrades to nil
// (the file-presence mode) with one warning per checkout root and run.
func ProbeTrackedMissionPaths(root string) map[string]bool {
	tracked, err := trackedMissionPaths(root)
	if err == nil {
		return tracked
	}
	key := root
	if abs, aerr := filepath.Abs(root); aerr == nil {
		key = abs
	}
	if _, seen := degradedProbeRoots.LoadOrStore(key, true); !seen {
		warnf("git could not list tracked kitty-specs/ paths in %s (%s); residue checks fall back to file presence, "+
			"so a folder holding only tasks or status files is treated as a Mission. Repair the git index and re-run.", root, err)
	}
	return nil
}

func markersExist(dir string) bool {
	for _, n := range missionMarkerFiles {
		if pathExists(filepath.Join(dir, n)) {
			return true
		}
	}
	return false
}

func carriesIdentity(dir string) bool {
	return mission.ReadMetaOrEmpty(dir).GetString("mission_id") != ""
}

func resolvedPath(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if r, err := filepath.EvalSymlinks(a); err == nil {
		return r
	}
	return a
}

// isMissionDirTracked is is_mission_dir with a precomputed tracked set.
func isMissionDirTracked(dir, root string, tracked map[string]bool) bool {
	if carriesIdentity(dir) {
		return true
	}
	rel, err := filepath.Rel(resolvedPath(root), resolvedPath(dir))
	if err != nil || strings.HasPrefix(rel, "..") {
		return markersExist(dir)
	}
	for _, n := range missionMarkerFiles {
		if tracked[filepath.ToSlash(filepath.Join(rel, n))] {
			return true
		}
	}
	return false
}

// IsKittySpecsResidue is is_kitty_specs_residue(dir, repo_root=root,
// tracked=tracked) for a walker that already probed (tracked nil = None).
func IsKittySpecsResidue(dir, root string, tracked map[string]bool) bool {
	if filepath.Base(filepath.Dir(dir)) != project.SpecsDir || markersExist(dir) {
		return false
	}
	if len(tracked) > 0 {
		return !isMissionDirTracked(dir, root, tracked)
	}
	for _, n := range missionContent {
		if pathExists(filepath.Join(dir, n)) {
			return false
		}
	}
	return !isMissionDirTracked(dir, root, map[string]bool{})
}

// IsKittySpecsResidueLazy is is_kitty_specs_residue(dir) with no tracked set:
// the checkout root is the parent of kitty-specs/, probed only when needed.
func IsKittySpecsResidueLazy(dir string) bool {
	if filepath.Base(filepath.Dir(dir)) != project.SpecsDir || markersExist(dir) {
		return false
	}
	root := filepath.Dir(filepath.Dir(dir))
	return IsKittySpecsResidue(dir, root, ProbeTrackedMissionPaths(root))
}

// PartitionMissionDirs splits the kitty-specs/ children names of root into
// (missions, residue) with one probe of the tracked set.
func PartitionMissionDirs(root string, names []string, tracked map[string]bool) (missions, residue []string) {
	for _, n := range names {
		if IsKittySpecsResidue(project.Specs(root, n), root, tracked) {
			residue = append(residue, n)
		} else {
			missions = append(missions, n)
		}
	}
	return missions, residue
}
