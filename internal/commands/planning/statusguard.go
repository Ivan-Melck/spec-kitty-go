package planning

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// statusGuard is finalize_status_surface.StatusSurfaceGuard (#5641): the
// status surface (branch, tip, index tree and, outside the mission
// directory, the status files' bytes) captured before the run's first
// status write, so a refused run can undo its own status commits.
type statusGuard struct {
	root, branch, tipBefore, indexTree string
	statusDir                          string
	ownPaths                           map[string]bool
	bytes                              *writeScope // nil when the mission-dir snapshot covers it
}

func gitTrim(dir string, args ...string) (string, bool) {
	out, err := gitx.Run(dir, args...)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(out), true
}

func captureStatusSurface(statusDir, missionDir string) *statusGuard {
	g := &statusGuard{statusDir: statusDir}
	if !strings.HasPrefix(statusDir+"/", missionDir+"/") {
		g.bytes = snapshotWriteScope(statusDir)
	}
	probe := statusDir
	if !isRealDir(probe) {
		probe = filepath.Dir(statusDir)
	}
	if !isRealDir(probe) {
		return g
	}
	top, ok1 := gitTrim(probe, "rev-parse", "--show-toplevel")
	tip, ok2 := gitTrim(probe, "rev-parse", "HEAD")
	if !ok1 || !ok2 {
		return g
	}
	branch, ok := gitTrim(top, "symbolic-ref", "-q", "--short", "HEAD")
	tree, ok3 := gitTrim(top, "write-tree")
	if !ok || !ok3 || branch == "" {
		return g
	}
	g.root, g.branch, g.tipBefore, g.indexTree = top, branch, tip, tree
	if rel, err := filepath.Rel(top, statusDir); err == nil && !strings.HasPrefix(rel, "..") {
		g.ownPaths = map[string]bool{}
		for _, n := range []string{status.EventsFile, status.SnapshotFile, "acceptance-matrix.json"} {
			g.ownPaths[filepath.ToSlash(filepath.Join(rel, n))] = true
		}
	}
	return g
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// restore is StatusSurfaceGuard.restore: the branch moves back (compare and
// swap) over commits provably this run's own (non-merge, touching only the
// mission's status files) and the index is put back; otherwise the leftover
// report names the kept commits. Returns the leftover reason ("" = none),
// its commit lines and the files kept commits changed (which the byte
// restore must leave as they are).
func (g *statusGuard) restore() (string, []string, map[string]bool) {
	if g == nil || g.root == "" {
		return "", nil, nil
	}
	ref := "refs/heads/" + g.branch
	current, _ := gitTrim(g.root, "rev-parse", "--verify", ref)
	if current == g.tipBefore || current == "" {
		return "", nil, nil
	}
	leftover := func(reason string) (string, []string, map[string]bool) {
		log, _ := gitTrim(g.root, "log", "--format=%h %s", g.tipBefore+".."+current)
		var commits []string
		if log != "" {
			commits = strings.Split(log, "\n")
		}
		kept := map[string]bool{}
		if out, ok := gitTrim(g.root, "diff", "--name-only", g.tipBefore, ref); ok {
			for _, p := range strings.Split(out, "\n") {
				if p != "" {
					kept[filepath.Join(g.root, p)] = true
				}
			}
		}
		return fmt.Sprintf("status commits on %s were not undone: %s", mission.PyRepr(g.branch), reason), commits, kept
	}
	span := fmt.Sprintf("the commits between %s and %s", short(g.tipBefore), short(current))
	if !gitx.OK(g.root, "merge-base", "--is-ancestor", g.tipBefore, current) {
		return leftover(span + " could not be read back as one line of history, so the branch was left as is; a commit this run did not make may be among them")
	}
	listing, ok := gitTrim(g.root, "rev-list", "--parents", g.tipBefore+".."+current)
	if !ok {
		return leftover(span + " could not be listed, so the branch was left as is; a commit this run did not make may be among them")
	}
	for _, line := range strings.Split(listing, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 {
			return leftover(fmt.Sprintf("%s include a merge commit (%s), so the branch was left as is; a commit this run did not make may be among them", span, short(f[0])))
		}
		touched := safecommit.CommittedPaths(g.root, f[0])
		only := len(touched) > 0
		for _, p := range touched {
			if !g.ownPaths[p] {
				only = false
			}
		}
		if !only {
			return leftover(fmt.Sprintf("%s include %s, which is not limited to this Mission's status files, so the branch was left as is; a commit this run did not make may be among them", span, short(f[0])))
		}
	}
	if _, err := gitx.Run(g.root, "update-ref", ref, g.tipBefore, current); err != nil {
		return leftover(err.Error())
	}
	if b, _ := gitTrim(g.root, "symbolic-ref", "-q", "--short", "HEAD"); b == g.branch && g.indexTree != "" {
		if _, err := gitx.Run(g.root, "read-tree", g.indexTree); err != nil {
			return fmt.Sprintf("status commits on %s were not undone: the branch was restored to %s but the index of %s was not; run `git -C %s read-tree %s`",
				mission.PyRepr(g.branch), short(g.tipBefore), g.root, g.root, g.indexTree), nil, nil
		}
	}
	if g.bytes != nil {
		g.bytes.restore(false, nil)
	}
	return "", nil, nil
}
