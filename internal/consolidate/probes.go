package consolidate

// Read-only git probes of the reconciliation gate (consolidation.git_probes).
// Every probe distinguishes a genuinely empty answer from a git error: an
// error is a *ProbeError (GitProbeError), which callers turn into a refusal.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
)

var gitVersionRe = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// ProbeError is GitProbeError.
type ProbeError struct{ Msg string }

func (e *ProbeError) Error() string { return e.Msg }

func probeErr(format string, a ...any) error { return &ProbeError{fmt.Sprintf(format, a...)} }

func git(root string, args ...string) gitx.Result {
	res, _ := gitx.Exec(root, nil, nil, args...)
	return res
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// shaReachableFrom is sha_reachable_from (an error reads as unreachable).
func shaReachableFrom(root, sha, ref string) bool {
	if sha == "" {
		return false
	}
	return git(root, "merge-base", "--is-ancestor", sha, ref).Code == 0
}

// resolveCommit is resolve_commit.
func resolveCommit(root, ref string) (string, error) {
	res := git(root, "rev-parse", "--verify", ref+"^{commit}")
	sha := strings.TrimSpace(res.Stdout)
	if res.Code != 0 || sha == "" {
		return "", probeErr("git rev-parse --verify %s^{commit} failed (exit %d): %s", ref, res.Code, strings.TrimSpace(res.Stderr))
	}
	return sha, nil
}

func resolvesCommit(root, ref string) bool {
	_, err := resolveCommit(root, ref)
	return err == nil
}

// commitsInRange is commits_in_range (git rev-list base..tip).
func commitsInRange(root, base, tip string) ([]string, error) {
	res := git(root, "rev-list", base+".."+tip)
	if res.Code != 0 {
		return nil, probeErr("git rev-list %s..%s failed (exit %d): %s", base, tip, res.Code, strings.TrimSpace(res.Stderr))
	}
	return lines(res.Stdout), nil
}

// firstParentCommitsInRange is first_parent_commits_in_range.
func firstParentCommitsInRange(root, base, tip string) ([]string, error) {
	res := git(root, "rev-list", "--first-parent", base+".."+tip)
	if res.Code != 0 {
		return nil, probeErr("git rev-list --first-parent %s..%s failed (exit %d): %s", base, tip, res.Code, strings.TrimSpace(res.Stderr))
	}
	return lines(res.Stdout), nil
}

// patchIDOf is patch_id_of ("" for an empty diff such as a merge commit).
func patchIDOf(root, sha string) (string, error) {
	if sha == "" {
		return "", nil
	}
	show := git(root, "show", sha)
	if show.Code != 0 {
		return "", probeErr("git show %s failed (exit %d): %s", sha, show.Code, strings.TrimSpace(show.Stderr))
	}
	res, _ := gitx.Exec(root, nil, []byte(show.Stdout), "patch-id", "--stable")
	f := strings.Fields(res.Stdout)
	if len(f) == 0 {
		return "", nil
	}
	return f[0], nil
}

// blobIDAt is blob_id_at (an absent path is an error).
func blobIDAt(root, ref, path string) (string, error) {
	res := git(root, "rev-parse", ref+":"+path)
	if res.Code != 0 {
		return "", probeErr("git rev-parse %s:%s failed (exit %d): %s", ref, path, res.Code, strings.TrimSpace(res.Stderr))
	}
	blob := strings.TrimSpace(res.Stdout)
	if blob == "" {
		return "", probeErr("git rev-parse %s:%s returned no blob id", ref, path)
	}
	return blob, nil
}

// nameStatus is one entry of git diff --name-status -z.
type nameStatus struct{ status, path string }

// changedPathsInRange is changed_paths_in_range (git diff --name-status
// --no-renames base..tip).
func changedPathsInRange(root, base, tip string) ([]nameStatus, error) {
	res := git(root, "-c", "core.quotePath=false", "diff", "--name-status", "-z", "--no-renames", base+".."+tip)
	if res.Code != 0 {
		return nil, probeErr("git diff --name-status %s..%s failed (exit %d): %s", base, tip, res.Code, strings.TrimSpace(res.Stderr))
	}
	f := strings.Split(res.Stdout, "\x00")
	var out []nameStatus
	for i := 0; i+1 < len(f); i += 2 {
		if f[i] == "" {
			break
		}
		out = append(out, nameStatus{f[i], f[i+1]})
	}
	return out, nil
}

// changedPathsOf is changed_paths_of (git show --name-only --no-renames).
func changedPathsOf(root, sha string) ([]string, error) {
	if sha == "" {
		return nil, nil
	}
	res := git(root, "-c", "core.quotePath=false", "show", "--name-only", "--format=", "-z", "--no-renames", sha)
	if res.Code != 0 {
		return nil, probeErr("git show --name-only %s failed (exit %d): %s", sha, res.Code, strings.TrimSpace(res.Stderr))
	}
	var out []string
	for _, p := range strings.Split(res.Stdout, "\x00") {
		if p = strings.TrimLeft(p, "\n"); p != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

// pathStateAt is path_state_at: the blob of path at ref, "" (with ok=false)
// when absent; an unreadable ref is an error.
func pathStateAt(root, ref, path string) (string, bool, error) {
	res := git(root, "-c", "core.quotePath=false", "ls-tree", "--full-tree", "-z", ref, "--", path)
	if res.Code != 0 {
		return "", false, probeErr("git ls-tree %s -- %s failed (exit %d): %s", ref, path, res.Code, strings.TrimSpace(res.Stderr))
	}
	for _, rec := range strings.Split(res.Stdout, "\x00") {
		tab := strings.IndexByte(rec, '\t')
		if tab < 0 || rec[tab+1:] != path {
			continue
		}
		f := strings.Fields(rec[:tab])
		if len(f) == 3 {
			return f[2], true, nil
		}
	}
	return "", false, nil
}

// isMergeCommit is is_merge_commit.
func isMergeCommit(root, sha string) (bool, error) {
	res := git(root, "rev-list", "--parents", "-n1", sha)
	if res.Code != 0 {
		return false, probeErr("git rev-list --parents -n1 %s failed (exit %d): %s", sha, res.Code, strings.TrimSpace(res.Stderr))
	}
	f := strings.Fields(res.Stdout)
	if len(f) == 0 {
		return false, probeErr("git rev-list --parents -n1 %s produced no output", sha)
	}
	return len(f) >= 3, nil
}

// mergeTreeAvailable is merge_tree_write_tree_available (git >= 2.38).
func mergeTreeAvailable(root string) bool {
	res := git(root, "--version")
	if res.Code != 0 {
		return false
	}
	m := gitVersionRe.FindStringSubmatch(res.Stdout)
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > 2 || (major == 2 && minor >= 38)
}

// threeWayMergeBlob is three_way_merge_blob: the blob git merge-tree
// --write-tree resolves for path, "" on a conflict or an absent path.
func threeWayMergeBlob(root, a, b, path string) (string, error) {
	res := git(root, "merge-tree", "--write-tree", a, b)
	if res.Code == 1 {
		return "", nil
	}
	if res.Code != 0 {
		return "", probeErr("git merge-tree --write-tree %s %s failed (exit %d): %s", a, b, res.Code, strings.TrimSpace(res.Stderr))
	}
	tree := strings.TrimSpace(strings.SplitN(res.Stdout, "\n", 2)[0])
	if tree == "" {
		return "", probeErr("git merge-tree --write-tree %s %s reported a clean merge but printed no tree id", a, b)
	}
	blob, err := blobIDAt(root, tree, path)
	if err != nil {
		return "", nil
	}
	return blob, nil
}
