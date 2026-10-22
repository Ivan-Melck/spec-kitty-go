package workflow

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

var (
	reportTokenRe  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	reportCommitRe = regexp.MustCompile(`^[0-9a-f]{40,64}$`)
)

// reportIsQualified is git.report_transaction.report_is_qualified (the
// same check as planning.ReportIsQualified): the report-only receipt under
// <git dir>/spec-kitty-report-transactions/<token>.json must be qualified,
// name this report, pin its current sha256 and a commit on HEAD's history
// that carries exactly this content. A missing or mismatching receipt fails
// closed.
func reportIsQualified(root, report string, token any) bool {
	t, ok := token.(string)
	if !ok || !reportTokenRe.MatchString(t) {
		return false
	}
	gd := git(root, "--literal-pathspecs", "rev-parse", "--absolute-git-dir")
	if gd.Code != 0 {
		return false
	}
	b, err := os.ReadFile(filepath.Join(strings.TrimSpace(gd.Stdout), "spec-kitty-report-transactions", t+".json"))
	if err != nil {
		return false
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil || o == nil {
		return false
	}
	commit := o.GetString("commit")
	if !reportCommitRe.MatchString(commit) {
		return false
	}
	if git(root, "--literal-pathspecs", "merge-base", "--is-ancestor", commit, "HEAD").Code != 0 {
		return false
	}
	rel := report
	if r, err := filepath.Rel(root, report); err == nil && !strings.HasPrefix(r, "..") {
		rel = filepath.ToSlash(r)
	}
	cur, err := os.ReadFile(report)
	if err != nil {
		return false
	}
	shown := git(root, "--literal-pathspecs", "show", commit+":"+rel)
	return shown.Code == 0 && o.GetString("state") == "qualified" && o.GetString("report") == rel &&
		o.GetString("sha256") == sha256Hex(cur) && shown.Stdout == string(cur)
}
