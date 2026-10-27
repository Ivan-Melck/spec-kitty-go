package status

// The Mission status lock (status.locking.feature_status_lock keyed on
// mission_write.mission_lock_key): every writer of a Mission's
// status.events.jsonl, from any worktree, takes
// <git-common-dir>/spec-kitty-locks/<key>.status.lock, so a read-modify-write
// of the log never loses another writer's rows.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/filelock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// LockDirectory is kernel.locks.LOCK_DIRECTORY.
const LockDirectory = "spec-kitty-locks"

const statusLockSuffix = ".status.lock"

// nonGitLockHome is locking._NON_GIT_LOCK_HOME.
const nonGitLockHome = ".spec-kitty"

// TransactionLockTimeout is BookkeepingTransaction.acquire's default bound.
const TransactionLockTimeout = 30 * time.Second

// LockTimeoutError is FeatureStatusLockTimeoutError (error_code
// STATUS_LOCK_HELD): the lock file and its recorded holder.
type LockTimeoutError struct {
	Msg      string
	LockPath string
}

func (e *LockTimeoutError) Error() string { return e.Msg }

// ErrorCode is FeatureStatusLockTimeoutError.error_code.
func (e *LockTimeoutError) ErrorCode() string { return "STATUS_LOCK_HELD" }

// MissionLock is a held Mission status lock.
type MissionLock struct {
	lock   *filelock.Lock
	holder string
	outer  bool
}

// Path is the lock file.
func (l *MissionLock) Path() string { return l.lock.Path() }

// Release drops the lock; the outermost release clears the holder sidecar.
func (l *MissionLock) Release() {
	if l == nil || l.lock == nil {
		return
	}
	if l.outer {
		_ = os.Remove(l.holder)
	}
	l.lock.Release()
	l.lock = nil
}

// LockMission takes the Mission status lock of featureDir (a primary or
// coordination directory; repoRoot "" resolves it like
// resolve_status_lock_root). It is re-entrant within the process; timeout
// < 0 waits without bound, like feature_status_lock's default.
func LockMission(featureDir, repoRoot string, timeout time.Duration) (*MissionLock, error) {
	lockPath := MissionLockPath(featureDir, repoRoot)
	outer := !filelock.HeldByProcess(lockPath)
	l, err := filelock.AcquireReentrant(lockPath, timeout)
	if err != nil {
		if _, ok := err.(*filelock.TimeoutError); ok {
			return nil, &LockTimeoutError{
				Msg: fmt.Sprintf("Timed out acquiring status lock %s after %ss: %s",
					lockPath, jsonx.FormatFloat(timeout.Seconds()), describeHolder(lockPath)),
				LockPath: lockPath,
			}
		}
		return nil, err
	}
	holder := lockPath + ".holder"
	if outer {
		payload := jsonx.NewObject("acquired_at", clock.NowUTCISO(), "pid", os.Getpid(), "thread", "MainThread")
		_ = os.WriteFile(holder, []byte(jsonx.Dumps(payload, jsonx.Options{Indent: -1, SortKeys: true})), 0o644)
	}
	return &MissionLock{lock: l, holder: holder, outer: outer}, nil
}

// describeHolder is _describe_holder(_read_holder(lock_path)).
func describeHolder(lockPath string) string {
	b, err := os.ReadFile(lockPath + ".holder")
	if err != nil {
		return "holder unknown (no holder record)"
	}
	var h map[string]any
	if json.Unmarshal(b, &h) != nil || h == nil {
		return "holder unknown (no holder record)"
	}
	field := func(k string) string {
		v, ok := h[k]
		if !ok {
			return "?"
		}
		if f, ok := v.(float64); ok && f == float64(int64(f)) {
			return fmt.Sprintf("%d", int64(f))
		}
		return fmt.Sprint(v)
	}
	return fmt.Sprintf("held by pid %s (thread %s) since %s", field("pid"), field("thread"), field("acquired_at"))
}

// MissionLockPath is feature_status_lock_path(resolve_status_lock_root(...),
// mission_lock_key(...)).
func MissionLockPath(featureDir, repoRoot string) string {
	root := statusLockRoot(featureDir, repoRoot)
	return filepath.Join(lockCommonDir(root), LockDirectory, MissionLockKey(featureDir, root)+statusLockSuffix)
}

// statusLockRoot is root_resolver.resolve_status_lock_root.
func statusLockRoot(featureDir, repoRoot string) string {
	if repoRoot != "" {
		return repoRoot
	}
	if filepath.Base(filepath.Dir(featureDir)) != project.SpecsDir {
		return featureDir
	}
	if root := canonicalRoot(featureDir); root != "" {
		return root
	}
	return filepath.Dir(filepath.Dir(featureDir))
}

// lockCommonDir is locking._git_common_dir: the git common dir shared by the
// repository and its worktrees, else <root>/.git, <root>/.kittify or
// <root>/.spec-kitty (never minting a project or repository marker).
func lockCommonDir(root string) string {
	if common, err := gitx.CommonDir(root); err == nil {
		return filelock.RealPath(common)
	}
	for _, marker := range []string{".git", project.KittifyDir} {
		if st, err := os.Stat(filepath.Join(root, marker)); err == nil && st.IsDir() {
			return filepath.Join(root, marker)
		}
	}
	return filepath.Join(root, nonGitLockHome)
}

// canonicalRoot is core.paths.resolve_canonical_root ("" = not found).
func canonicalRoot(start string) string {
	if abs, err := filepath.Abs(start); err == nil {
		start = abs
	}
	if r, err := filepath.EvalSymlinks(start); err == nil {
		start = r
	}
	ceilings := map[string]bool{}
	for _, p := range filepath.SplitList(os.Getenv("GIT_CEILING_DIRECTORIES")) {
		if p != "" {
			ceilings[filelock.RealPath(p)] = true
		}
	}
	for cand := start; ; {
		g := filepath.Join(cand, ".git")
		if st, err := os.Stat(g); err == nil {
			if st.IsDir() {
				return cand
			}
			if m := project.MainRepoRoot(cand); m != cand {
				return m
			}
			if st, err := os.Stat(filepath.Join(cand, project.KittifyDir)); err == nil && st.IsDir() {
				return cand
			}
		}
		if ceilings[cand] {
			return ""
		}
		parent := filepath.Dir(cand)
		if parent == cand {
			return ""
		}
		cand = parent
	}
}

var legacySlug = regexp.MustCompile(`^\d{3}-`)

// MissionLockKey is mission_write.mission_lock_key: the coordination
// directory name <slug>-<mid8> of a coordination-routed Mission (read from
// its canonical primary meta.json under root), else the directory name. A
// coordination-routed Mission whose mid8 cannot be resolved is keyed on the
// directory name (upstream refuses with MISSION_LOCK_KEY_UNRESOLVED; the
// migration writers fall back the same way).
func MissionLockKey(featureDir, root string) string {
	name := filepath.Base(featureDir)
	var meta *jsonx.Object
	if filepath.Base(filepath.Dir(featureDir)) != project.SpecsDir {
		meta = mission.ReadMetaOrEmpty(featureDir)
	} else {
		meta = mission.ReadMetaOrEmpty(filepath.Join(root, project.SpecsDir, name))
		if meta.Len() == 0 {
			meta = mission.ReadMetaOrEmpty(featureDir)
		}
	}
	if strings.TrimSpace(meta.GetString("coordination_branch")) == "" {
		return name
	}
	slug := meta.GetString("mission_slug")
	if slug == "" {
		slug = name
	}
	mid8 := meta.GetString("mid8")
	if mid8 == "" {
		mid8 = mission.ResolveMid8(slug, meta.GetString("mission_id"))
	}
	if mid8 == "" {
		mid8 = mission.Mid8FromSlug(slug)
	}
	if mid8 == "" {
		if legacySlug.MatchString(slug) {
			return slug
		}
		return name
	}
	return mission.CoordMissionDirName(slug, mid8)
}
