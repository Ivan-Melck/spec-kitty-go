// Package safecommit ports upstream's commit primitives: the destination-ref
// asserting safe_commit (git/commit_helpers.py), the mission-aware commit
// router (coordination/commit_router.py) with its per-surface outcome
// contract (coordination/commit_outcome.py), and the kind-aware placement
// projection (resolve_placement_only).
package safecommit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// Error codes (SafeCommitError.error_code).
const (
	CodeGeneric             = "SAFE_COMMIT_GENERIC"
	CodeHeadMismatch        = "SAFE_COMMIT_HEAD_MISMATCH"
	CodeRefShape            = "SAFE_COMMIT_DESTINATION_REF_SHAPE"
	CodeDestinationNotFound = "SAFE_COMMIT_DESTINATION_NOT_FOUND"
	CodeEmptyChangeset      = "SAFE_COMMIT_EMPTY_CHANGESET"
	CodeStagedTreeUnchanged = "SAFE_COMMIT_STAGED_TREE_UNCHANGED"
	CodeNotAWorktree        = "SAFE_COMMIT_NOT_A_WORKTREE"
	CodeRecoveryFailed      = "SAFE_COMMIT_RECOVERY_FAILED"
	CodeProtectedBranch     = "SAFE_COMMIT_PROTECTED_BRANCH"
	CodePathPolicy          = "SAFE_COMMIT_PATH_POLICY"
	CodeUnrequestedPaths    = "SAFE_COMMIT_UNREQUESTED_PATHS"
	CodeRuntime             = "RUNTIME" // plain RuntimeError (stage / commit failure)
)

// Error is a structured safe_commit failure; Message is upstream's text.
type Error struct {
	Code      string
	Message   string
	CommitSHA string // set when the commit landed (UNREQUESTED_PATHS)
}

func (e *Error) Error() string { return e.Message }

// IsCode reports whether err is a *Error with code.
func IsCode(err error, code string) bool {
	e, ok := err.(*Error)
	return ok && e.Code == code
}

// Repr is Python repr() of a string.
func Repr(s string) string { return mission.PyRepr(s) }

// Result of a landed commit.
type Result struct {
	SHA          string
	Ref          string
	WorktreeRoot string
}

// Options tune Commit.
type Options struct {
	// PostConsolidation authorizes a protected destination
	// (GuardCapability.POST_CONSOLIDATION_WRITE).
	PostConsolidation bool
}

func readHead(worktree string) (string, bool) {
	out, err := gitx.Run(worktree, "symbolic-ref", "HEAD")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", false
	}
	return strings.TrimPrefix(strings.TrimSpace(out), "refs/heads/"), true
}

func realpath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		p = a
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func commonDir(dir string) string {
	out, err := gitx.Run(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return ""
	}
	return realpath(strings.TrimSpace(out))
}

// IsWorktreeOf reports whether worktree is (a linked worktree of) repo.
func IsWorktreeOf(repo, worktree string) bool {
	top, err := gitx.Run(worktree, "rev-parse", "--show-toplevel")
	if err != nil || realpath(strings.TrimSpace(top)) != realpath(worktree) {
		return false
	}
	if realpath(worktree) == realpath(repo) {
		return true
	}
	a, b := commonDir(worktree), commonDir(repo)
	return a != "" && a == b
}

// ResolveCommitPath is kernel.resolution.resolve_commit_path: parents
// resolved, the final component kept (a symlink names the link).
func ResolveCommitPath(root, candidate string) string {
	p := candidate
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p = filepath.Clean(p)
	dir, base := filepath.Split(p)
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	if base == "" {
		return filepath.Clean(dir)
	}
	return filepath.Join(dir, base)
}

// singleMissionSlug returns the slug when every path is under one
// kitty-specs/<slug>/.
func singleMissionSlug(files []string) string {
	slugs := map[string]bool{}
	for _, f := range files {
		parts := strings.Split(f, "/")
		if len(parts) < 3 || parts[0] != "kitty-specs" {
			return ""
		}
		slugs[parts[1]] = true
	}
	if len(slugs) != 1 {
		return ""
	}
	for s := range slugs {
		return s
	}
	return ""
}

// isProtectedFor is ProtectionPolicy.resolve(root)[.for_mission(repo, slug)].is_protected(ref).
func isProtectedFor(policyRoot, repoRoot, slug, ref string) bool {
	if !coord.IsProtected(policyRoot, "", ref) {
		return false
	}
	if slug == "" {
		return true
	}
	// Mission-scoped commit_to_target fold (#5100 FR-008).
	meta := mission.ReadMetaOrEmpty(filepath.Join(repoRoot, "kitty-specs", slug))
	if v, ok := meta.Get("commit_to_target"); ok {
		if b, ok := v.(bool); ok && b && ref == meta.GetString("target_branch") {
			return false
		}
	}
	return true
}

// Preflight validates the destination and paths without mutating git and
// returns the worktree-relative POSIX paths.
func Preflight(repoRoot, worktreeRoot, ref string, paths []string, opts Options) ([]string, error) {
	if strings.HasPrefix(ref, "refs/heads/") {
		return nil, &Error{Code: CodeRefShape, Message: fmt.Sprintf("safe_commit: destination_ref must be a short branch name, got fully-qualified ref %s. Strip the 'refs/heads/' prefix at the call boundary.", Repr(ref))}
	}
	if len(paths) == 0 {
		return nil, &Error{Code: CodeEmptyChangeset, Message: "safe_commit: paths is empty. Pass at least one path to commit; an empty changeset is a programming error."}
	}
	if !IsWorktreeOf(repoRoot, worktreeRoot) {
		return nil, &Error{Code: CodeNotAWorktree, Message: fmt.Sprintf("safe_commit: %s is not a git worktree. Pass a resolved worktree path.", worktreeRoot)}
	}
	head, ok := readHead(worktreeRoot)
	if !ok || head != ref {
		if !ok {
			head = "<detached>"
		}
		return nil, &Error{Code: CodeHeadMismatch, Message: fmt.Sprintf("safe_commit: worktree %s HEAD is %s, expected %s. Run `git -C %s checkout %s` first.", worktreeRoot, Repr(head), Repr(ref), worktreeRoot, ref)}
	}
	if !gitx.OK(worktreeRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+ref) {
		return nil, &Error{Code: CodeDestinationNotFound, Message: fmt.Sprintf("safe_commit: destination ref %s does not exist in the repo. Create the branch first, or check the spelling.", Repr(ref))}
	}
	rw := realpath(worktreeRoot)
	var norm []string
	for _, p := range paths {
		c := p
		if filepath.IsAbs(c) {
			r := ResolveCommitPath(rw, c)
			if rel, err := filepath.Rel(rw, r); err == nil && !strings.HasPrefix(rel, "..") {
				c = rel
			}
		}
		norm = append(norm, toPosix(c))
	}
	for _, n := range norm {
		if parts := strings.Split(n, "/"); len(parts) > 0 && parts[0] == ".worktrees" {
			return nil, &Error{Code: CodePathPolicy, Message: fmt.Sprintf("safe_commit: refusing to stage path under .worktrees/: %s. Planning artifacts must be committed from the coordination worktree, not the primary repo root.", n)}
		}
	}
	slug := singleMissionSlug(norm)
	protected := isProtectedFor(repoRoot, repoRoot, slug, ref) || isProtectedFor(worktreeRoot, repoRoot, slug, ref)
	if protected && !opts.PostConsolidation {
		return nil, &Error{Code: CodeProtectedBranch, Message: fmt.Sprintf("safe_commit: refusing to commit to protected branch %s in %s. Retry against a non-protected topic branch, or set SPEC_KITTY_ALLOW_PROTECTED_BRANCH_COMMITS=1 if you own this branch.", Repr(ref), worktreeRoot)}
	}
	return norm, nil
}

// Commit is safe_commit: commit exactly paths onto ref inside worktreeRoot.
func Commit(repoRoot, worktreeRoot, ref, message string, paths []string, opts Options) (*Result, error) {
	norm, err := Preflight(repoRoot, worktreeRoot, ref, paths, opts)
	if err != nil {
		return nil, err
	}
	patch, perr := stagedPatch(worktreeRoot, norm)
	if perr != nil {
		return nil, &Error{Code: CodeRecoveryFailed, Message: fmt.Sprintf("safe_commit: refusing to mutate index in %s; could not capture pre-existing staged requested-file state.", worktreeRoot)}
	}
	unstage(worktreeRoot, norm)
	for _, f := range norm {
		res, err := gitx.Exec(worktreeRoot, nil, nil, "add", "--force", "--", f)
		if err != nil || res.Code != 0 {
			restorePatch(worktreeRoot, norm, patch)
			return nil, &Error{Code: CodeRuntime, Message: fmt.Sprintf("safe_commit: failed to stage requested files in %s: %s: %s", worktreeRoot, Repr(f), strings.TrimSpace(res.Stderr))}
		}
	}
	args := append([]string{"-c", "commit.gpgsign=false", "commit", "--only", "-m", message, "--"}, norm...)
	res, err := gitx.Exec(worktreeRoot, nil, nil, args...)
	if err != nil || res.Code != 0 {
		out := strings.TrimSpace(res.Stdout + "\n" + res.Stderr)
		empty := gitx.OK(worktreeRoot, append([]string{"diff", "--cached", "--quiet", "--"}, norm...)...)
		restorePatch(worktreeRoot, norm, patch)
		if empty {
			return nil, &Error{Code: CodeStagedTreeUnchanged, Message: fmt.Sprintf("safe_commit: nothing to commit for destination_ref=%s (empty changeset)", Repr(ref))}
		}
		detail := ""
		if out != "" {
			detail = ": " + out
		}
		return nil, &Error{Code: CodeRuntime, Message: fmt.Sprintf("safe_commit: git commit failed in %s for destination_ref=%s%s", worktreeRoot, Repr(ref), detail)}
	}
	sha, _ := gitx.HeadSHA(worktreeRoot)
	if err := assertCommittedRequested(worktreeRoot, ref, sha, norm); err != nil {
		return nil, err
	}
	return &Result{SHA: sha, Ref: ref, WorktreeRoot: worktreeRoot}, nil
}

func stagedPatch(dir string, files []string) (string, error) {
	args := append([]string{"diff", "--cached", "--binary", "--no-ext-diff", "--no-renames", "--"}, files...)
	res, err := gitx.Exec(dir, nil, nil, args...)
	if err != nil || res.Code != 0 {
		return "", fmt.Errorf("diff failed")
	}
	return res.Stdout, nil
}

func unstage(dir string, files []string) {
	staged := CachedChangedPaths(dir, files...)
	if len(staged) == 0 {
		return
	}
	if gitx.OK(dir, "rev-parse", "--verify", "HEAD") {
		gitx.Exec(dir, nil, nil, append([]string{"--literal-pathspecs", "restore", "--staged", "--"}, staged...)...)
		return
	}
	gitx.Exec(dir, nil, nil, append([]string{"--literal-pathspecs", "rm", "--cached", "--ignore-unmatch", "-q", "--"}, staged...)...)
}

func restorePatch(dir string, files []string, patch string) {
	unstage(dir, files)
	if patch == "" {
		return
	}
	gitx.Exec(dir, nil, []byte(patch), "apply", "--cached", "--whitespace=nowarn", "-")
}

// CommittedPaths lists the paths commit sha changed versus its parent.
func CommittedPaths(dir, sha string) []string {
	res, err := gitx.Exec(dir, nil, nil, "diff-tree", "--no-commit-id", "--name-only", "-r", "--root", "--no-renames", "-z", sha)
	if err != nil || res.Code != 0 {
		return nil
	}
	var out []string
	for _, p := range strings.Split(res.Stdout, "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func assertCommittedRequested(dir, ref, sha string, requested []string) error {
	req := map[string]bool{}
	for _, r := range requested {
		req[toPosix(r)] = true
	}
	var extra []string
	for _, p := range CommittedPaths(dir, sha) {
		if !req[p] {
			extra = append(extra, p)
		}
	}
	if len(extra) == 0 {
		return nil
	}
	sort.Strings(extra)
	gitx.Exec(dir, nil, nil, append([]string{"reset", "-q", "HEAD", "--"}, extra...)...)
	return &Error{Code: CodeUnrequestedPaths, CommitSHA: sha, Message: fmt.Sprintf("safe_commit: commit %s landed, but it also carries path(s) the operation did not request (a pre-commit hook staged them): %s", sha, strings.Join(extra, ", "))}
}

// exists reports os.Lstat success.
func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
