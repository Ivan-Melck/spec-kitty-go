package coord

// Area FIX2 extension (additive): the failure arm of the transactional
// status doors (BookkeepingTransaction.commit): a refused commit cuts the
// rows the emit appended, restores status.json and unstages the paths.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missionwrite"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// txnPoint captures the pre-emit state of dir for a transactional emit
// (nil for a non-transactional write, which never rolls back).
func txnPoint(transactional bool, dir string) *missionwrite.RollbackPoint {
	if !transactional {
		return nil
	}
	return missionwrite.Capture(dir)
}

// itemEventIDs is the transaction's _attempted_event_ids.
func itemEventIDs(items []any) []string {
	var ids []string
	for _, it := range items {
		switch t := it.(type) {
		case *status.Event:
			ids = append(ids, t.EventID)
		case *status.Annotation:
			ids = append(ids, t.EventID)
		}
	}
	return ids
}

// commitStatusTxn commits the status pair of dir in checkout like
// BookkeepingTransaction.commit (safe_commit: exactly these paths, hooks
// run). On failure the rows appended since rb are cut (only when they are
// exactly ids and uncommitted; a refusal leaves both files byte-identical
// and is appended to the error) and the paths' staging is restored:
// "safe_commit failed on '<ref>': <safe_commit error>[ [<refusal>]]".
func (s *Surface) commitStatusTxn(checkout, dir, message string, rb *missionwrite.RollbackPoint, ids []string) error {
	if rb == nil {
		return s.commitStatusFiles(checkout, dir, message)
	}
	paths := []string{status.EventsPath(dir)}
	if _, err := os.Stat(filepath.Join(dir, status.SnapshotFile)); err == nil {
		paths = append(paths, filepath.Join(dir, status.SnapshotFile))
	}
	ref, _ := gitx.Run(checkout, "symbolic-ref", "--short", "HEAD")
	ref = strings.TrimSpace(ref)
	err := safeCommitStatus(checkout, ref, message, paths)
	if err == nil {
		return nil
	}
	out := rb.Restore(os.Stderr, ids, len(ids) > 0)
	suffix := ""
	if msg := out.Message(); msg != "" {
		suffix = " [" + msg + "]"
	}
	return fmt.Errorf("safe_commit failed on %s: %v%s", mission.PyRepr(ref), err, suffix)
}

// safeCommitStatus is the safe_commit core for the status pair: stage
// exactly paths, `commit --only` them (hooks run), and on a refused commit
// restore the paths' pre-call staging and report git's combined output.
func safeCommitStatus(checkout, ref, message string, paths []string) error {
	var rel []string
	for _, p := range paths {
		if r, err := filepath.Rel(checkout, p); err == nil {
			p = r
		}
		rel = append(rel, filepath.ToSlash(p))
	}
	patch, _ := gitx.Exec(checkout, nil, nil, append([]string{"diff", "--cached", "--binary", "--no-ext-diff", "--no-renames", "--"}, rel...)...)
	if r, err := gitx.Exec(checkout, nil, nil, append([]string{"add", "--force", "--"}, rel...)...); err != nil || r.Code != 0 {
		restoreStaging(checkout, rel, patch.Stdout)
		return fmt.Errorf("safe_commit: failed to stage requested files in %s: %s", checkout, strings.TrimSpace(r.Stderr))
	}
	if gitx.OK(checkout, append([]string{"diff", "--cached", "--quiet", "--"}, rel...)...) {
		return nil // already durable at HEAD
	}
	res, err := gitx.Exec(checkout, nil, nil, append([]string{"-c", "commit.gpgsign=false", "commit", "--only", "-m", message, "--"}, rel...)...)
	if err == nil && res.Code == 0 {
		return nil
	}
	restoreStaging(checkout, rel, patch.Stdout)
	detail := ""
	if o := strings.TrimSpace(res.Stdout + "\n" + res.Stderr); o != "" {
		detail = ": " + o
	}
	return fmt.Errorf("safe_commit: git commit failed in %s for destination_ref=%s%s", checkout, mission.PyRepr(ref), detail)
}

// restoreStaging is _restore_staged_patch: unstage rel, then re-apply the
// pre-call staged patch.
func restoreStaging(checkout string, rel []string, patch string) {
	if gitx.OK(checkout, "rev-parse", "--verify", "HEAD") {
		gitx.Exec(checkout, nil, nil, append([]string{"--literal-pathspecs", "restore", "--staged", "--"}, rel...)...)
	} else {
		gitx.Exec(checkout, nil, nil, append([]string{"--literal-pathspecs", "rm", "--cached", "--ignore-unmatch", "-q", "--"}, rel...)...)
	}
	if patch != "" {
		gitx.Exec(checkout, nil, []byte(patch), "apply", "--cached", "--whitespace=nowarn", "-")
	}
}
