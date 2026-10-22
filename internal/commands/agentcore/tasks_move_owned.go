package agentcore

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/ownedcheckout"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// The owned local review lifecycle of `agent tasks move-task
// --owned-checkout` (tasks_move_task.py: _mt_preflight_owned_request,
// _mt_apply_owned_targets, _mt_resolve_owned_review_base,
// _mt_require_owned_implementation and the owned deliverable scope).

const orphanedPinRecoveryHint = "spec-kitty agent mission finalize-tasks --refresh-planning-commit --allow-orphaned"

func ownedRefusal(code, msg string) error { return &ownedcheckout.Error{Code: code, Message: msg} }

var ownedLifecycleLanes = map[string]bool{
	string(status.Planned): true, string(status.Claimed): true, string(status.InProgress): true,
	string(status.ForReview): true, string(status.InReview): true, string(status.Approved): true,
}

// preflightOwned is _mt_preflight_owned_request.
func (st *moveState) preflightOwned() error {
	if err := ownedcheckout.RequireUnstagedIndex(st.owned); err != nil {
		return err
	}
	if !ownedLifecycleLanes[st.target] {
		return ownedRefusal("OWNED_TRANSITION_UNSUPPORTED", "Owned checkout supports the local review lifecycle through approval only.")
	}
	if st.force || st.skipGate || st.skipArtifact || st.selfReviewFallback || st.intendedReviewer != "" || st.reviewerFailureReason != "" || st.doneOverride != "" {
		return ownedRefusal(ownedcheckout.OwnedOptionUnsupported, "Review overrides are not supported with --owned-checkout.")
	}
	return nil
}

// pyIntError mirrors int(value)'s ValueError text ("" when value parses).
func pyIntError(value string) string {
	s := strings.TrimSpace(value)
	digits := strings.TrimLeft(s, "+-")
	if len(s)-len(digits) <= 1 && digits != "" && !strings.HasPrefix(digits, "_") && !strings.HasSuffix(digits, "_") && !strings.Contains(digits, "__") {
		if _, err := strconv.ParseInt(strings.ReplaceAll(digits, "_", ""), 10, 64); err == nil {
			return ""
		}
	}
	return "invalid literal for int() with base 10: " + mission.PyRepr(value)
}

// applyOwnedTargets is _mt_apply_owned_targets.
func (st *moveState) applyOwnedTargets() error {
	if !st.autoCommit {
		return ownedRefusal(ownedcheckout.OwnedOptionUnsupported, "Owned status changes require auto-commit.")
	}
	if st.shellPID != "" {
		if e := pyIntError(st.shellPID); e != "" {
			return ownedRefusal(ownedcheckout.OwnedInputInvalid, e)
		}
	}
	st.targetBranch = st.owned.WriteBranch
	return nil
}

// ownedWorkspace is _mt_owned_workspace.
func (st *moveState) ownedWorkspace() *lanes.Workspace {
	mode, _ := lanes.WPExecutionMode(st.owned.MissionDir, st.taskID)
	return lanes.OwnedWorkspace(st.owned.MissionSlug, st.taskID, mode, st.owned.OwnedRoot, st.owned.WriteBranch)
}

func revParseCommit(dir, ref string) string {
	out, err := gitx.Run(dir, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// resolveOwnedReviewBase is _mt_resolve_owned_review_base.
func (st *moveState) resolveOwnedReviewBase() (string, error) {
	m, err := lanes.Read(st.featureDir)
	if err != nil {
		return "", err
	}
	if m == nil {
		return "", fmt.Errorf("lanes.json is required for %s. Run 'spec-kitty agent mission finalize-tasks' to compute execution lanes, or if the mission has already begun executing, run 'spec-kitty doctor mission-state --fix --mission <slug>' to rebuild lanes.json from the event log.", st.featureDir)
	}
	if m.PlanningCommitSHA == nil || *m.PlanningCommitSHA == "" {
		return "", ownedRefusal("OWNED_REVIEW_BASE_INVALID", "Owned review requires lanes.json planning_commit_sha.")
	}
	declared := *m.PlanningCommitSHA
	p := st.owned.OwnedRoot
	if tip, err := gitx.Run(st.owned.RepositoryRoot, "rev-parse", "--verify", "refs/heads/"+st.targetBranch); err == nil && strings.TrimSpace(tip) != "" {
		if !gitx.OK(p, "merge-base", "--is-ancestor", declared, strings.TrimSpace(tip)) && gitx.OK(p, "cat-file", "-e", declared+"^{commit}") {
			return "", ownedRefusal("OWNED_REVIEW_BASE_ORPHANED", fmt.Sprintf("Recorded planning commit %s is orphaned against the target-branch tip -- the owned-review base would be computed against a dead ancestor. Run %s to re-point it, then retry.", declared, mission.PyRepr(orphanedPinRecoveryHint)))
		}
	}
	base, head := revParseCommit(p, declared), revParseCommit(p, "HEAD")
	if base == "" || head == "" {
		return "", ownedRefusal("OWNED_REVIEW_BASE_INVALID", "Owned review base and HEAD must resolve to commits in the selected checkout.")
	}
	if base == head {
		return "", ownedRefusal("OWNED_REVIEW_BASE_INVALID", "Owned review base must differ from HEAD.")
	}
	if mb, err := gitx.Run(p, "merge-base", head, base); err != nil || strings.TrimSpace(mb) != base {
		return "", ownedRefusal("OWNED_REVIEW_BASE_INVALID", "Owned review base must be an ancestor of HEAD in the selected checkout.")
	}
	return base, nil
}

// ownedFilePatterns is _mt_owned_file_patterns.
func (st *moveState) ownedFilePatterns() []string {
	var out []string
	for _, p := range st.w.List("owned_files") {
		if strings.TrimSpace(p) == "" {
			continue
		}
		out = append(out, strings.TrimPrefix(strings.ReplaceAll(p, `\`, "/"), "./"))
	}
	return out
}

// matchesOwnedFile is _mt_matches_owned_file.
func matchesOwnedFile(path string, patterns []string) bool {
	n := strings.TrimPrefix(strings.ReplaceAll(path, `\`, "/"), "./")
	for _, pat := range patterns {
		if lanes.Fnmatch(n, pat) {
			return true
		}
		prefix := strings.TrimRight(strings.ReplaceAll(strings.ReplaceAll(pat, "/**", ""), "/*", ""), "/")
		if prefix != "" && strings.HasPrefix(n, prefix+"/") {
			return true
		}
	}
	return false
}

// requireOwnedImplementation is _mt_require_owned_implementation.
func (st *moveState) requireOwnedImplementation() error {
	patterns := st.ownedFilePatterns()
	p := st.owned.OwnedRoot
	var changed []string
	ok := false
	if mb, err := gitx.Run(p, "merge-base", "HEAD", st.reviewBaseRef); err == nil && strings.TrimSpace(mb) != "" {
		if out, err := gitx.Run(p, "diff", "--name-only", "--diff-filter=ACDMRTUXB", strings.TrimSpace(mb), "HEAD"); err == nil {
			ok = true
			for _, l := range strings.Split(out, "\n") {
				if l = strings.TrimSpace(l); l != "" {
					changed = append(changed, l)
				}
			}
		}
	}
	if !ok {
		return ownedRefusal("OWNED_IMPLEMENTATION_MISSING", fmt.Sprintf("Owned review could not read the committed change set: the merge-base diff against review base %s (lanes.json `planning_commit_sha`) failed in %s. Make sure that commit is reachable in this checkout (fetch it, or recreate the local branch that carries it), or repoint `planning_commit_sha` in %s to a commit that is an ancestor of HEAD, then retry.",
			st.reviewBaseRef, p, filepath.Join(st.featureDir, "lanes.json")))
	}
	matched := false
	for _, c := range changed {
		if matchesOwnedFile(c, patterns) {
			matched = true
			break
		}
	}
	if len(patterns) == 0 || !matched {
		return ownedRefusal("OWNED_IMPLEMENTATION_MISSING", "Owned review requires a committed change matching WP owned_files.")
	}
	return nil
}

// ownedDeliverableScope refuses an owned auto-commit that carries files
// outside the WP's owned_files (OWNED_DELIVERABLE_SCOPE_REFUSED).
func (st *moveState) ownedDeliverableScope(paths []string) error {
	patterns := st.ownedFilePatterns()
	var outside []string
	for _, p := range paths {
		if !matchesOwnedFile(filepath.ToSlash(p), patterns) {
			outside = append(outside, filepath.ToSlash(p))
		}
	}
	if len(outside) == 0 {
		return nil
	}
	sort.Strings(outside)
	return ownedRefusal("OWNED_DELIVERABLE_SCOPE_REFUSED", "Owned auto-commit includes files outside WP owned_files: "+strings.Join(outside, ", "))
}

// emitOwnedError renders an owned ActionContextError the way _do_move_task
// does: {"error_code", "error"} (indent=2) or `Error: [<code>] <msg>` on
// stderr.
func emitOwnedError(st *moveState, e *ownedcheckout.Error) {
	if st.json {
		fmt.Fprintln(st.ctx.Stdout, jsonx.Dumps(jsonx.NewObject("error_code", e.Code, "error", e.Message), jsonx.Pretty))
		return
	}
	ownedcheckout.Emit(st.ctx.Stdout, st.ctx.Stderr, e, false, nil)
}

