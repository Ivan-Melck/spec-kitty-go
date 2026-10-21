package safecommit

import (
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
)

// StatusEntry is one `git status --porcelain=v1 -z` record (kernel.git.StatusEntry).
type StatusEntry struct {
	X, Y     byte
	Path     string // POSIX, worktree-relative
	OrigPath string // rename/copy source ("" otherwise)
}

// Untracked reports a `??` entry.
func (e StatusEntry) Untracked() bool { return e.X == '?' && e.Y == '?' }

// Ignored reports a `!!` entry.
func (e StatusEntry) Ignored() bool { return e.X == '!' && e.Y == '!' }

// Code is the two-letter status code.
func (e StatusEntry) Code() string { return string([]byte{e.X, e.Y}) }

// StatusEntries runs `git status --porcelain=v1 -z` in dir. untracked is
// "all", "normal", "no" ("" = git default). pathspecs limit the scope.
func StatusEntries(dir, untracked string, pathspecs ...string) ([]StatusEntry, error) {
	args := []string{"--literal-pathspecs", "status", "--porcelain=v1", "-z"}
	if untracked != "" {
		args = append(args, "--untracked-files="+untracked)
	}
	if len(pathspecs) > 0 {
		args = append(append(args, "--"), pathspecs...)
	}
	res, err := gitx.Exec(dir, nil, nil, args...)
	if err != nil {
		return nil, err
	}
	if res.Code != 0 {
		return nil, &gitx.Error{Args: args, Dir: dir, Code: res.Code, Stdout: res.Stdout, Stderr: res.Stderr}
	}
	return parseStatusZ(res.Stdout), nil
}

func parseStatusZ(out string) []StatusEntry {
	var entries []StatusEntry
	recs := strings.Split(out, "\x00")
	for i := 0; i < len(recs); i++ {
		r := recs[i]
		if len(r) < 4 {
			continue
		}
		e := StatusEntry{X: r[0], Y: r[1], Path: r[3:]}
		if e.X == 'R' || e.X == 'C' || e.Y == 'R' || e.Y == 'C' {
			if i+1 < len(recs) {
				e.OrigPath = recs[i+1]
				i++
			}
		}
		entries = append(entries, e)
	}
	return entries
}

// IsTracked reports whether rel is in the index.
func IsTracked(dir, rel string) bool {
	out, err := gitx.Run(dir, "--literal-pathspecs", "ls-files", "--error-unmatch", "--", rel)
	return err == nil && strings.TrimSpace(out) != ""
}

// CachedChangedPaths lists index-vs-HEAD changes under pathspecs.
func CachedChangedPaths(dir string, pathspecs ...string) []string {
	args := append([]string{"--literal-pathspecs", "diff", "--cached", "--name-only", "--no-renames", "-z", "--"}, pathspecs...)
	res, err := gitx.Exec(dir, nil, nil, args...)
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

// toPosix converts a native path to forward slashes.
func toPosix(p string) string { return filepath.ToSlash(p) }
