package ownedcheckout

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/decision"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
)

// The owned mission's decision runtime lock (decisions/index.json.lock) and
// its local ignore rule (decisions/service.py _runtime_lock_ignore_state /
// _ensure_runtime_lock_ignore / _restore_runtime_lock_ignore).

// RuntimeLockFile is the decision index lock's file name.
const RuntimeLockFile = "index.json.lock"

// PreconditionError is the ValueError family the runtime-lock helpers raise.
type PreconditionError struct{ Msg string }

func (e *PreconditionError) Error() string { return e.Msg }

// RelTo renders p relative to root (POSIX), or p itself.
func RelTo(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(r)
}

// RuntimeLockIgnoreState validates the exact local ignore destination and
// observes its effective owner rule: (lock, ignore, ignored).
func RuntimeLockIgnoreState(f *Fact) (lock, ignore string, ignored bool, err error) {
	lock = decision.LockPath(f.MissionDir)
	ignore = filepath.Join(filepath.Dir(lock), ".gitignore")
	if _, err := f.Files([]string{lock, ignore}, false); err != nil {
		return "", "", false, err
	}
	for _, p := range []string{lock, ignore} {
		for cur := p; cur != f.OwnedRoot && cur != filepath.Dir(cur); cur = filepath.Dir(cur) {
			st, lerr := os.Lstat(cur)
			if lerr != nil {
				continue
			}
			if st.Mode()&os.ModeSymlink != 0 {
				return "", "", false, &PreconditionError{"Runtime lock repair refuses symlink: " + cur}
			}
			if cur == p && !st.Mode().IsRegular() {
				return "", "", false, &PreconditionError{"Runtime lock repair requires a regular file: " + cur}
			}
			if cur != p && !st.IsDir() {
				return "", "", false, &PreconditionError{"Runtime lock repair requires a directory: " + cur}
			}
		}
	}
	relIgnore := RelTo(f.OwnedRoot, ignore)
	res, gerr := gitx.Exec(f.OwnedRoot, nil, nil, "--literal-pathspecs", "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored", "--", relIgnore)
	if gerr != nil || res.Code != 0 {
		return "", "", false, fmt.Errorf("git status failed in %s", f.OwnedRoot)
	}
	if strings.TrimSpace(res.Stdout) != "" {
		return "", "", false, &PreconditionError{"Runtime lock repair refuses preexisting changes or staging on .gitignore"}
	}
	ci, gerr := gitx.Exec(f.OwnedRoot, nil, nil, "check-ignore", "--no-index", "--quiet", "--", RelTo(f.OwnedRoot, lock))
	if gerr != nil || (ci.Code != 0 && ci.Code != 1) {
		return "", "", false, errors.New("Runtime lock repair could not inspect effective ignore rules")
	}
	exact := false
	if b, rerr := os.ReadFile(ignore); rerr == nil {
		for _, l := range agents.SplitLines(strings.TrimPrefix(string(b), "\uFEFF")) {
			if l == "/"+RuntimeLockFile {
				exact = true
			}
		}
	}
	return lock, ignore, ci.Code == 0 && exact, nil
}

// IgnoreDelta is the (ignore, before, written) triple of an ensure.
type IgnoreDelta struct {
	Path    string
	Before  []byte // nil when the file did not exist
	Written []byte
}

// EnsureRuntimeLockIgnore provisions only the exact owner rule, retaining
// authored local ignore text.
func EnsureRuntimeLockIgnore(f *Fact) (*IgnoreDelta, error) {
	_, ignore, ignored, err := RuntimeLockIgnoreState(f)
	if err != nil {
		return nil, err
	}
	d := &IgnoreDelta{Path: ignore}
	if b, err := os.ReadFile(ignore); err == nil {
		d.Before = b
	}
	if !ignored {
		os.MkdirAll(filepath.Dir(ignore), 0o755)
		if _, err := agents.EnsureGitignoreEntries(filepath.Dir(ignore), []string{"/" + RuntimeLockFile}, true); err != nil {
			return nil, err
		}
	}
	if b, err := os.ReadFile(ignore); err == nil {
		d.Written = b
	} else {
		d.Written = []byte{}
	}
	return d, nil
}

// Restore undoes an unchanged ignore delta (after a refused commit).
func (d *IgnoreDelta) Restore() {
	st, err := os.Lstat(d.Path)
	if err != nil || st.Mode()&os.ModeSymlink != 0 {
		return
	}
	if cur, err := os.ReadFile(d.Path); err == nil && string(cur) == string(d.Written) {
		if d.Before == nil {
			os.Remove(d.Path)
		} else {
			os.WriteFile(d.Path, d.Before, 0o644)
		}
	}
}
