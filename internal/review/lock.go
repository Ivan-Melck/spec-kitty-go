package review

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

const LockRelPath = ".spec-kitty/review-lock.json"

type Lock struct {
	WorktreePath, WPID, Agent, StartedAt string
	PID                                  int
}

type LockReadError struct{ Path, Reason string }

func (e *LockReadError) Error() string { return e.Reason }

var lockFields = []string{"worktree_path", "wp_id", "agent", "started_at", "pid"}

// ParseLock is shared by the runtime file reader and committed-blob preflight.
func ParseLock(content []byte) (*Lock, error) {
	v, err := pyjson.LoadsBytes(content)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("expected a JSON object, found %s", pyjson.TypeName(v))
	}
	var missing, unknown, wrong []string
	for _, k := range lockFields {
		if !o.Has(k) {
			missing = append(missing, k)
		}
	}
	for _, k := range o.Keys() {
		known := false
		for _, f := range lockFields {
			if k == f {
				known = true
				break
			}
		}
		if !known {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "missing field(s) "+strings.Join(missing, ", "))
	}
	if len(unknown) > 0 {
		parts = append(parts, "unknown field(s) "+strings.Join(unknown, ", "))
	}
	if len(parts) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(parts, "; "))
	}
	for _, k := range lockFields {
		v, _ := o.Get(k)
		valid := false
		if k == "pid" {
			switch v.(type) {
			case int, int64, json.Number:
				valid = true
			}
		} else {
			_, valid = v.(string)
		}
		if !valid {
			wrong = append(wrong, k)
		}
	}
	if len(wrong) > 0 {
		return nil, fmt.Errorf("field(s) with the wrong type: %s", strings.Join(wrong, ", "))
	}
	pid, _ := o.GetInt("pid")
	return &Lock{o.GetString("worktree_path"), o.GetString("wp_id"), o.GetString("agent"), o.GetString("started_at"), pid}, nil
}

func LoadLock(worktree string) (*Lock, error) {
	p := filepath.Join(worktree, LockRelPath)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return nil, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, &LockReadError{p, err.Error()}
	}
	l, err := ParseLock(b)
	if err != nil {
		return nil, &LockReadError{p, err.Error()}
	}
	return l, nil
}

var shellSafe = regexp.MustCompile(`^[\w@%+=:,./-]+$`)

func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// LockRefusal inspects the index even when the runtime lock has been deleted.
// For a missing workspace it inspects exactly the ref worktree creation uses.
func LockRefusal(worktree string, exists bool, repoRoot, checkoutRef, branch string, claimed bool) string {
	var readErr *LockReadError
	committed := false
	gitError := func(p string, r gitx.Result, err error, args ...string) *LockReadError {
		var reason string
		if err != nil {
			reason = err.Error()
		} else {
			reason = (&gitx.Error{Args: args, Dir: repoRoot, Code: r.Code, Stdout: r.Stdout, Stderr: r.Stderr}).Error()
		}
		return &LockReadError{p, "git could not check it: " + reason}
	}
	if exists {
		_, err := LoadLock(worktree)
		readErr, _ = err.(*LockReadError)
		args := []string{"ls-files", "--error-unmatch", "--", LockRelPath}
		r, err := gitx.Exec(worktree, nil, nil, args...)
		if err == nil && r.Code == 0 {
			committed = true
		} else if !(err == nil && (r.Code == 1 || r.Code == 128 && strings.Contains(strings.ToLower(r.Stderr), "not a git repository"))) && readErr == nil {
			readErr = gitError(filepath.Join(worktree, LockRelPath), r, err, args...)
		}
	} else if checkoutRef != "" {
		spec := checkoutRef + ":" + LockRelPath
		args := []string{"rev-parse", "--verify", "--quiet", checkoutRef + "^{commit}"}
		r, err := gitx.Exec(repoRoot, nil, nil, args...)
		if err != nil || r.Code != 0 && r.Code != 1 {
			readErr = gitError(spec, r, err, args...)
		} else if r.Code == 0 {
			args = []string{"ls-tree", "-z", checkoutRef, "--", LockRelPath}
			r, err = gitx.Exec(repoRoot, nil, nil, args...)
			if err != nil || r.Code != 0 {
				readErr = gitError(spec, r, err, args...)
			} else if r.Stdout != "" {
				args = []string{"show", spec}
				r, err = gitx.Exec(repoRoot, nil, nil, args...)
				if err != nil || r.Code != 0 {
					readErr = gitError(spec, r, err, args...)
				} else {
					committed = true
					if _, err := ParseLock([]byte(r.Stdout)); err != nil {
						readErr = &LockReadError{spec, err.Error()}
					}
				}
			}
		}
	}
	claim := "  The work package was not claimed for review."
	if claimed {
		claim = "  The review claim was already recorded; once the lock is fixed, re-run the review to finish it."
	}
	if committed {
		p := filepath.Join(worktree, LockRelPath)
		if !exists {
			p = checkoutRef + ":" + LockRelPath
		}
		unreadable := ""
		if readErr != nil {
			p = readErr.Path
			unreadable = " and cannot be read (" + readErr.Reason + ")"
		}
		lines := []string{fmt.Sprintf("REVIEW_LOCK_COMMITTED: refusing to start the review; the review lock %s is tracked by git%s.", p, unreadable), "  The lock is runtime state: each review rewrites it and its release deletes it, so a tracked lock leaves the lane dirty.", claim}
		where := checkoutRef
		if exists {
			where = branch
			if where == "" {
				where, _ = gitx.Run(worktree, "symbolic-ref", "--short", "-q", "HEAD")
			}
			if where == "" {
				where = "the checked-out branch"
			}
		} else if where == "" {
			where = "the branch"
		}
		if !exists && checkoutRef != branch {
			lines = append(lines, fmt.Sprintf("  The lock is committed on %s; it must not be. Remove %s there, then re-run the review.", where, LockRelPath))
		} else {
			lines = append(lines, fmt.Sprintf("  The lock is committed on %s; it must not be. Remove it from the branch, then re-run the review:", where))
			if !exists {
				lines = append(lines, "    git worktree add "+shellQuote(worktree)+" "+shellQuote(where))
			}
			lines = append(lines, "    git -C "+shellQuote(worktree)+" rm -q "+LockRelPath, "    Then commit that deletion on "+where+".")
		}
		return strings.Join(lines, "\n")
	}
	if readErr != nil {
		return strings.Join([]string{fmt.Sprintf("REVIEW_LOCK_UNREADABLE: refusing to start the review; the review lock %s cannot be read (%s).", readErr.Path, readErr.Reason), "  It was edited by hand, or a write of it was cut short.", claim, fmt.Sprintf("  If no review is running in %s, delete it (rm %s) and re-run the review.", worktree, shellQuote(readErr.Path))}, "\n")
	}
	return ""
}
