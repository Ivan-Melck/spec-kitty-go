// Package refadvance ports the dirty-worktree verdicts of
// specify_cli.git.ref_advance (_dirty_verdicts / _dirty_entries): which
// local state a `git reset --hard <sha>` (advance_branch_ref's resync) or a
// `git worktree remove --force` (guarded_worktree_remove) would destroy.
//
// Tracked changes always count (bar a meta.json whose only change is the VCS
// lock stamp); untracked files are listed one by one and, like collapsed
// ignored entries, count only when they obstruct a path of the target tree
// (or, for a removal, always, for untracked files).
package refadvance

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// Reason is _DirtyReason.
type Reason int

const (
	// Tracked is a staged/unstaged change against a tracked path.
	Tracked Reason = iota
	// Obstruction is an untracked/ignored path a reset to the target tree
	// would overwrite.
	Obstruction
	// Removal is an untracked file a worktree removal would discard.
	Removal
)

const (
	resetObstructionNote = "would be overwritten by reset --hard to "
	removalNote          = "untracked local file would be discarded by worktree removal"
)

// Verdict is one (reason, display line) pair.
type Verdict struct {
	Reason Reason
	Line   string
}

// Entry is one kernel.git StatusEntry.
type Entry struct {
	XY       string
	Path     string // without the trailing directory slash
	OrigPath string
	IsDir    bool
}

func (e Entry) untracked() bool { return e.XY == "??" }
func (e Entry) ignored() bool   { return e.XY == "!!" }

// Display is StatusEntry.display().
func (e Entry) Display() string {
	shown := printable(e.Path)
	if e.IsDir {
		shown += "/"
	}
	if e.OrigPath != "" {
		return e.XY + " " + printable(e.OrigPath) + " -> " + shown
	}
	return e.XY + " " + shown
}

// printable is kernel.git.listing._printable (control characters escaped).
func printable(p string) string {
	var b strings.Builder
	for _, r := range p {
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

// StatusEntries is kernel.git.status_entries(cwd, untracked=..., ignored=...).
func StatusEntries(dir, untracked string, ignored bool) ([]Entry, error) {
	args := []string{"status", "--porcelain=v1", "-z"}
	if untracked != "" {
		args = append(args, "--untracked-files="+untracked)
	}
	if ignored {
		args = append(args, "--ignored")
	}
	res, err := gitx.Exec(dir, nil, nil, args...)
	if err != nil {
		return nil, err
	}
	if res.Code != 0 {
		return nil, &gitx.Error{Args: args, Dir: dir, Code: res.Code, Stdout: res.Stdout, Stderr: res.Stderr}
	}
	var out []Entry
	recs := strings.Split(res.Stdout, "\x00")
	for i := 0; i < len(recs); i++ {
		r := recs[i]
		if len(r) < 4 {
			continue
		}
		e := Entry{XY: r[:2], Path: r[3:]}
		if strings.HasSuffix(e.Path, "/") {
			e.Path, e.IsDir = strings.TrimRight(e.Path, "/"), true
		}
		if strings.ContainsAny(e.XY, "RC") && i+1 < len(recs) {
			e.OrigPath = strings.TrimRight(recs[i+1], "/")
			i++
		}
		out = append(out, e)
	}
	return out, nil
}

// TargetTreePaths is _target_tree_paths: every file tracked in sha's tree.
func TargetTreePaths(repo, sha string) ([]string, error) {
	res, err := gitx.Exec(repo, nil, nil, "ls-tree", "--full-tree", "-r", "--name-only", "-z", sha)
	if err != nil {
		return nil, err
	}
	if res.Code != 0 {
		return nil, fmt.Errorf("Could not inspect target tree %s: %s", sha, strings.TrimSpace(res.Stderr))
	}
	var out []string
	for _, p := range strings.Split(res.Stdout, "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

// contains is GitPath.contains (component-wise; the root contains all).
func contains(a, b string) bool {
	if a == "" || a == "." {
		return true
	}
	return a == b || strings.HasPrefix(b, a+"/")
}

// obstructs is _path_obstructs_target_tree.
func obstructs(p string, targets []string) bool {
	if p == "" || p == "." {
		return false
	}
	for _, t := range targets {
		if contains(p, t) || contains(t, p) {
			return true
		}
	}
	return false
}

// statusForDirtyCheck is _status_for_dirty_check.
func statusForDirtyCheck(wt string) ([]Entry, error) {
	es, err := StatusEntries(wt, "all", false)
	if err != nil {
		return nil, err
	}
	ig, err := StatusEntries(wt, "normal", true)
	if err != nil {
		return nil, err
	}
	for _, e := range ig {
		if e.ignored() {
			es = append(es, e)
		}
	}
	return es, nil
}

// Options tune DirtyVerdicts.
type Options struct {
	// NewSHA is the reset target (display only; "HEAD" when unset).
	NewSHA string
	// TargetPaths is the target tree (nil: nothing obstructs).
	TargetPaths []string
	// IsDisposable exempts regenerable residue (nil: nothing is).
	IsDisposable func(string) bool
	// UntrackedDirty counts every untracked file (worktree removal).
	UntrackedDirty bool
}

// DirtyVerdicts is _dirty_verdicts.
func DirtyVerdicts(wt string, opt Options) ([]Verdict, error) {
	es, err := statusForDirtyCheck(wt)
	if err != nil {
		return nil, fmt.Errorf("Could not inspect worktree state at %s: %w", wt, err)
	}
	sha := opt.NewSHA
	if sha == "" {
		sha = "HEAD"
	}
	var out []Verdict
	for _, e := range es {
		if opt.IsDisposable != nil && opt.IsDisposable(e.Path) {
			continue
		}
		if e.untracked() && opt.UntrackedDirty {
			out = append(out, Verdict{Removal, e.Display() + " (" + removalNote + ")"})
			continue
		}
		if e.untracked() || e.ignored() {
			if obstructs(e.Path, opt.TargetPaths) {
				short := sha
				if len(short) > 12 {
					short = short[:12]
				}
				out = append(out, Verdict{Obstruction, e.Display() + " (" + resetObstructionNote + short + ")"})
			}
			continue
		}
		if path.Base(e.Path) == "meta.json" && metaLockOnly(wt, e.Path) {
			continue
		}
		out = append(out, Verdict{Tracked, e.Display()})
	}
	return out, nil
}

// Lines is _dirty_entries: the display lines of DirtyVerdicts.
func Lines(vs []Verdict) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.Line
	}
	return out
}

var vcsLockFields = map[string]bool{"vcs": true, "vcs_locked_at": true}

// metaLockOnly is _meta_change_is_vcs_lock_only.
func metaLockOnly(wt, rel string) bool {
	b, err := os.ReadFile(filepath.Join(wt, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	after, err := jsonx.DecodeObject(b)
	if err != nil || after == nil {
		return false
	}
	before := &jsonx.Object{}
	if res, err := gitx.Exec(wt, nil, nil, "show", "HEAD:"+rel); err == nil && res.Code == 0 {
		o, derr := jsonx.DecodeObject([]byte(res.Stdout))
		if derr != nil || o == nil {
			return false
		}
		before = o
	}
	keys := map[string]bool{}
	for _, k := range before.Keys() {
		keys[k] = true
	}
	for _, k := range after.Keys() {
		keys[k] = true
	}
	for k := range keys {
		if vcsLockFields[k] {
			continue
		}
		bv, bok := before.Get(k)
		av, aok := after.Get(k)
		if bok != aok || jsonx.Dumps(bv, jsonx.Line) != jsonx.Dumps(av, jsonx.Line) {
			return false
		}
	}
	return true
}

// FormatEntryLines is format_entry_lines (at most 20 entries, then a count).
func FormatEntryLines(entries []string, indent string) string {
	var shown []string
	for i, e := range entries {
		if i >= 20 {
			break
		}
		shown = append(shown, indent+e)
	}
	if hidden := len(entries) - 20; hidden > 0 {
		shown = append(shown, fmt.Sprintf("%s... and %d more", indent, hidden))
	}
	return strings.Join(shown, "\n")
}
