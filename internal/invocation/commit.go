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
	branch := currentBranch(root)
	if branch == "" {
		return &CommitOutcome{Refusal: "not a git worktree"}
	}
	if create.ProtectedBranches(root)[branch] && !protectionHatch() {
		m := fmt.Sprintf("safe_commit: refusing to commit to protected branch %s in %s. Retry against a non-protected topic branch, or set SPEC_KITTY_ALLOW_PROTECTED_BRANCH_COMMITS=1 if you own this branch.", pyRepr(branch), root)
		return &CommitOutcome{Refusal: "ProtectedBranchRefused(" + pyRepr(m) + ")"}
	}
	if _, err := gitx.Run(root, "add", "--force", "--", rel); err != nil {
		return &CommitOutcome{Refusal: err.Error()}
	}
	if _, err := gitx.Run(root, "commit", "--only", "-m", msg, "--", rel); err != nil {
		return &CommitOutcome{Refusal: err.Error()}
	}
	return &CommitOutcome{Committed: true}
}
