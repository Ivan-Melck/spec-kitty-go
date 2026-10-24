package invocation

import (
	"fmt"
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission/create"
)

// protectionHatch is the SPEC_KITTY_ALLOW_PROTECTED_BRANCH_COMMITS escape
// hatch (truthy: 1/true/yes).
func protectionHatch() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SPEC_KITTY_ALLOW_PROTECTED_BRANCH_COMMITS"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

func currentBranch(root string) string {
	if out, err := gitx.Run(root, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		return ""
	}
	if out, err := gitx.Run(root, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	if out, err := gitx.Run(root, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		if b := strings.TrimSpace(out); b != "" && b != "HEAD" {
			return b
		}
	}
	return ""
}

// commitOpRecord is _commit_op_record for an agent close: a best-effort
// safe_commit of kitty-ops/<id>.jsonl onto the current branch.
func commitOpRecord(root, id string, started *jsonx.Object) *CommitOutcome {
	return commitOp(root, id, started, false)
}

// commitOp is _commit_op_record; a doctor-sweep close commits only the
// op-closures spine.
func commitOp(root, id string, started *jsonx.Object, sweep bool) *CommitOutcome {
	profile := started.GetString("profile_id")
	if profile == "" {
		profile = "unknown"
	}
	action := started.GetString("action")
	if action == "" {
		action = "unknown"
	}
	short := id
	if len(short) > 8 {
		short = short[:8]
	}
	msg := fmt.Sprintf("op(%s): %s [%s]", profile, action, short)
	rel := EventsDir + "/" + id + ".jsonl"
	if sweep {
		msg += " (doctor sweep)"
		rel = ClosureSpine
	}
	branch := currentBranch(root)
	if branch == "" {
		return &CommitOutcome{Refusal: "not a git worktree"}
	}
	if create.ProtectedBranches(root)[branch] && !protectionHatch() {
		m := fmt.Sprintf("safe_commit: refusing to commit to protected branch %s in %s. Retry against a non-protected topic branch, or set SPEC_KITTY_ALLOW_PROTECTED_BRANCH_COMMITS=1 if you own this branch.", pyRepr(branch), root)
		return &CommitOutcome{Refusal: "ProtectedBranchRefused(" + pyRepr(m) + ")"}
	}
	if res, err := gitx.Exec(root, nil, nil, "add", "--force", "--", rel); err != nil || res.Code != 0 {
		reason := strings.TrimSpace(res.Stderr)
		m := fmt.Sprintf("safe_commit: failed to stage requested files in %s: %s: %s", root, pyRepr(rel), reason)
		return &CommitOutcome{Refusal: "RuntimeError(" + pyRepr(m) + ")"}
	}
	res, err := gitx.Exec(root, nil, nil, "commit", "--only", "-m", msg, "--", rel)
	if err != nil || res.Code != 0 {
		gitx.Exec(root, nil, nil, "reset", "-q", "--", rel)
		detail := strings.TrimSpace(res.Stdout + "\n" + res.Stderr)
		m := fmt.Sprintf("safe_commit: git commit failed in %s for destination_ref=%s", root, pyRepr(branch))
		if detail != "" {
			m += ": " + detail
		}
		return &CommitOutcome{Refusal: "RuntimeError(" + pyRepr(m) + ")"}
	}
	out := &CommitOutcome{Committed: true}
	if s := strings.TrimSpace(res.Stderr); s != "" {
		out.Warning = fmt.Sprintf("git commit in %s produced warnings on a successful commit: %s", root, s)
	}
	return out
}
