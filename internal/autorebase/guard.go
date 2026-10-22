package autorebase

// guarded_merge_abort (git/destructive_guard.py): `git merge --abort` in a
// worktree, refusing when it would discard an operator's own edit.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/acceptance"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// AbortRefused is DestructiveOpRefused(DESTRUCTIVE_OP_ONLY_COPY).
type AbortRefused struct {
	Worktree    string
	Entries     []string
	Remediation string
}

func (e *AbortRefused) Error() string {
	var lines []string
	for _, x := range e.Entries {
		lines = append(lines, "    "+x)
	}
	return fmt.Sprintf("Refusing destructive operation (DESTRUCTIVE_OP_ONLY_COPY).\n  Worktree: %s\n  Dirty entries:\n%s\n  Remediation: %s",
		e.Worktree, strings.Join(lines, "\n"), e.Remediation)
}

const resumeNote = "then resume the operation (e.g. `spec-kitty consolidate --resume`)"

func (a *attempt) mergeOwnedPaths() map[string]bool {
	out := map[string]bool{}
	if a.git("rev-parse", "-q", "--verify", "MERGE_HEAD").Code != 0 {
		return out
	}
	base := a.git("merge-base", "HEAD", "MERGE_HEAD")
	if base.Code != 0 {
		return out
	}
	r := a.git("-c", "core.quotePath=false", "diff", "--name-only", "-z", "--no-renames", strings.TrimSpace(base.Stdout), "MERGE_HEAD")
	if r.Code != 0 {
		return out
	}
	for _, p := range strings.Split(r.Stdout, "\x00") {
		if p != "" {
			out[p] = true
		}
	}
	return out
}

type statusEntry struct{ xy, path string }

func (a *attempt) statusEntries(args ...string) []statusEntry {
	r := a.git(append([]string{"-c", "core.quotePath=false", "status", "--porcelain=v1", "-z"}, args...)...)
	var out []statusEntry
	recs := strings.Split(r.Stdout, "\x00")
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if len(rec) < 4 {
			continue
		}
		e := statusEntry{rec[:2], rec[3:]}
		if strings.ContainsAny(e.xy, "RC") {
			i++
		}
		out = append(out, e)
	}
	return out
}

func stageBlob(wt string, stage int, path string, env []string) ([]byte, bool) {
	cmd := exec.Command("git", "show", fmt.Sprintf(":%d:%s", stage, path))
	cmd.Dir = wt
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	return out, true
}

func stripMarkers(data []byte) []byte {
	var kept [][]byte
	inBase := false
	for _, l := range bytes.Split(data, []byte("\n")) {
		switch {
		case bytes.HasPrefix(l, []byte("|||||||")):
			inBase = true
		case bytes.HasPrefix(l, []byte("=======")):
			inBase = false
		case !inBase && !bytes.HasPrefix(l, []byte("<<<<<<<")) && !bytes.HasPrefix(l, []byte(">>>>>>>")):
			kept = append(kept, l)
		}
	}
	return bytes.Join(kept, []byte("\n"))
}

func (a *attempt) conflictResult(path string) ([]byte, bool) {
	ours, ok2 := stageBlob(a.wt, 2, path, a.o.Env)
	theirs, ok3 := stageBlob(a.wt, 3, path, a.o.Env)
	if !ok2 || !ok3 {
		if !ok3 {
			return ours, ok2
		}
		return theirs, true
	}
	base, _ := stageBlob(a.wt, 1, path, a.o.Env)
	dir, err := os.MkdirTemp("", "kitty-merge-file-")
	if err != nil {
		return nil, false
	}
	defer os.RemoveAll(dir)
	files := []string{filepath.Join(dir, "ours"), filepath.Join(dir, "base"), filepath.Join(dir, "theirs")}
	for i, b := range [][]byte{ours, base, theirs} {
		os.WriteFile(files[i], b, 0o644)
	}
	cmd := exec.Command("git", append([]string{"merge-file", "-p"}, files...)...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() >= 128 || ee.ExitCode() < 0 {
			return nil, false
		}
	}
	return out, true
}

// guardedAbort is guarded_merge_abort; disposable judges a non-merge path.
func (a *attempt) guardedAbort(disposable func(string) bool) error {
	owned := a.mergeOwnedPaths()
	var dirty []string
	entries := a.statusEntries("--untracked-files=all")
	for _, e := range a.statusEntries("--ignored", "--untracked-files=normal") {
		if e.xy == "!!" {
			entries = append(entries, e)
		}
	}
	for _, e := range entries {
		if owned[e.path] || (disposable != nil && disposable(e.path)) {
			continue
		}
		if e.xy == "??" || e.xy == "!!" {
			continue // target_paths is empty: nothing obstructs
		}
		dirty = append(dirty, e.xy+" "+e.path)
	}
	for _, e := range a.statusEntries() {
		if !isConflicted(e.xy) {
			continue
		}
		expected, ok := a.conflictResult(e.path)
		actual, err := os.ReadFile(filepath.Join(a.wt, e.path))
		if !ok || err != nil || !bytes.Equal(stripMarkers(actual), stripMarkers(expected)) {
			dirty = append(dirty, e.xy+" "+e.path+" (conflict resolution edited in the working tree)")
		}
	}
	if len(dirty) > 0 {
		return &AbortRefused{Worktree: a.wt, Entries: dirty,
			Remediation: fmt.Sprintf("Commit, stash, or move the local changes in %s, %s.", a.wt, resumeNote)}
	}
	a.git("merge", "--abort")
	return nil
}

func isConflicted(xy string) bool {
	switch xy {
	case "DD", "AU", "UD", "UA", "DU", "AA", "UU":
		return true
	}
	return false
}

// LaneResidue is ResidueContext.for_mission(root, slug, LANE).is_disposable:
// spec-kitty's own bookkeeping, and (under a coordination topology) the
// mission's stale coordination-partition copies; another mission's files
// never. A mission without meta.json has no residue at all.
func LaneResidue(root, slug string) func(string) bool {
	if slug == "" {
		return nil
	}
	meta, err := mission.ReadMeta(project.Specs(root, slug))
	if err != nil || meta == nil {
		return nil
	}
	topo := meta.GetString("topology")
	coordTopo := topo != "lanes" && topo != "single_branch"
	return func(p string) bool {
		n := filepath.ToSlash(p)
		if parts := strings.Split(n, "/"); len(parts) > 2 && parts[0] == "kitty-specs" && parts[1] != slug && !strings.HasPrefix(parts[1], slug+"-") {
			return false
		}
		if acceptance.IsSelfBookkeeping(n) {
			return true
		}
		return coordTopo && acceptance.IsCoordResidueFor(n, slug)
	}
}
