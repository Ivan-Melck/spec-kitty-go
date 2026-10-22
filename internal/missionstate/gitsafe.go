package missionstate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

func gitOK(root string, args ...string) (string, bool) {
	res, err := gitx.Exec(root, nil, nil, append([]string{"-C", root}, args...)...)
	if err != nil || res.Code != 0 {
		return res.Stdout, false
	}
	return res.Stdout, true
}

func isGitRepo(root string) bool {
	_, ok := gitOK(root, "rev-parse", "--is-inside-work-tree")
	return ok
}

// gitHead is _git_head.
func gitHead(root string) *string {
	out, ok := gitOK(root, "rev-parse", "HEAD")
	if !ok {
		return nil
	}
	s := strings.TrimSpace(out)
	if s == "" {
		return nil
	}
	return &s
}

// gitWorktrees is _git_worktrees (porcelain "worktree <path>" lines).
func gitWorktrees(root string) []string {
	out, ok := gitOK(root, "worktree", "list", "--porcelain")
	if !ok {
		return []string{root}
	}
	var wts []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "worktree ") {
			wts = append(wts, strings.TrimPrefix(l, "worktree "))
		}
	}
	if len(wts) == 0 {
		return []string{root}
	}
	return wts
}

// statusEntry is one `git status --porcelain=v1 -z` record.
type statusEntry struct {
	xy, path, orig string
	hasOrig        bool
}

// display is StatusEntry.display.
func (e statusEntry) display() string {
	shown := printable(e.path)
	if e.hasOrig {
		return e.xy + " " + printable(e.orig) + " -> " + shown
	}
	return e.xy + " " + shown
}

// printable escapes control characters like _printable (unicode_escape).
func printable(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) {
			switch r {
			case '\t':
				b.WriteString(`\t`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			default:
				if r < 0x100 {
					fmt.Fprintf(&b, `\x%02x`, r)
				} else {
					fmt.Fprintf(&b, `\u%04x`, r)
				}
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// statusEntries is kernel.git.status_entries(cwd, pathspecs, untracked=None).
func statusEntries(cwd string, pathspecs []string) ([]statusEntry, error) {
	args := []string{}
	if len(pathspecs) > 0 {
		args = append(args, "--literal-pathspecs")
	}
	args = append(args, "status", "--porcelain=v1", "-z", "--")
	args = append(args, pathspecs...)
	res, err := gitx.Exec(cwd, nil, nil, args...)
	if err != nil {
		return nil, err
	}
	if res.Code != 0 {
		return nil, &gitx.Error{Args: args, Dir: cwd, Code: res.Code, Stdout: res.Stdout, Stderr: res.Stderr}
	}
	recs := strings.Split(res.Stdout, "\x00")
	var out []statusEntry
	for i := 0; i < len(recs); i++ {
		r := recs[i]
		if len(r) < 3 {
			continue
		}
		e := statusEntry{xy: r[:2], path: r[3:]}
		if strings.ContainsAny(e.xy, "RC") && i+1 < len(recs) {
			i++
			e.orig, e.hasOrig = recs[i], true
		}
		out = append(out, e)
	}
	return out, nil
}

// assertGitSafe is _assert_git_safe.
func assertGitSafe(root string, relPaths []string, allowDirty bool) error {
	if allowDirty || !isGitRepo(root) {
		return nil
	}
	var dirty []string
	for _, wt := range gitWorktrees(root) {
		if _, err := os.Stat(wt); err != nil {
			continue
		}
		entries, err := statusEntries(wt, relPaths)
		if err != nil {
			return &RepairError{fmt.Sprintf("Refusing mission-state repair: could not read git status of %s: %v", wt, err)}
		}
		if len(entries) > 0 {
			var parts []string
			for _, e := range entries {
				parts = append(parts, e.display())
			}
			dirty = append(dirty, wt+": "+strings.Join(parts, "; "))
		}
	}
	if len(dirty) > 0 {
		return &RepairError{repairDirtyMessage + "\n" + strings.Join(dirty, "\n")}
	}
	return nil
}

// NoFollowError is kernel.no_follow.NoFollowPathError (a symlink planted at
// the lock path); upstream does not catch it.
type NoFollowError struct{ Msg string }

func (e *NoFollowError) Error() string { return e.Msg }

// lockRecordJSON is kernel.locks._record_to_json(_build_record()).
func lockRecordJSON(cliVersion string) string {
	host, _ := os.Hostname()
	if cliVersion == "" {
		cliVersion = "unknown"
	}
	return jsonx.Dumps(jsonx.NewObject("host", host, "pid", os.Getpid(), "schema_version", 1,
		"started_at", clock.NowUTCISO(), "version", cliVersion), jsonx.Line)
}

// acquireRepairLock is _git_lock: kernel.locks.machine_file_lock on
// <git-common-dir>/spec-kitty-mission-state.lock (O_NOFOLLOW open, one
// non-blocking advisory flock, the holder record written while held and the
// file truncated, never unlinked, on release). The release func is never nil.
func acquireRepairLock(root, cliVersion string) (func(), error) {
	noop := func() {}
	if !isGitRepo(root) {
		return noop, nil
	}
	out, ok := gitOK(root, "rev-parse", "--git-common-dir")
	if !ok {
		return noop, nil
	}
	common := strings.TrimSpace(out)
	if !filepath.IsAbs(common) {
		common = filepath.Join(root, common)
	}
	_ = os.MkdirAll(common, 0o755)
	path := filepath.Join(common, "spec-kitty-mission-state.lock")
	f, err := openNoFollow(path)
	if err != nil {
		if _, isNF := err.(*NoFollowError); isNF {
			return noop, err
		}
		return noop, nil
	}
	if !tryLock(f) {
		f.Close()
		return noop, &RepairError{"Another mission-state repair appears to be running: " + path}
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(lockRecordJSON(cliVersion)), 0)
	return func() {
		_ = f.Truncate(0)
		unlock(f)
		f.Close()
	}, nil
}

// isTracked is kernel.git.is_tracked (ls-files --error-unmatch).
func isTracked(root, rel string) bool {
	res, err := gitx.Exec(root, nil, nil, "ls-files", "--error-unmatch", "--", rel)
	return err == nil && res.Code == 0
}
