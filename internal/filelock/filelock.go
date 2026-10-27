// Package filelock is kernel.locks.machine_file_lock: a cross-process,
// machine-local exclusive lock on a dedicated lock-only file.
//
// The contract follows upstream (contracts/lock-primitive.md G1-G5):
//
//   - the caller passes a path that exists only to be locked; its parent
//     directory is created on demand and hardened to 0700 only when this call
//     created it;
//   - the file is opened O_RDWR|O_CREAT, mode 0600, without following a
//     final-component symlink;
//   - the OS lock is flock(LOCK_EX|LOCK_NB) on Unix and LockFileEx on the
//     first byte on Windows (the byte msvcrt.locking(fd, LK_NBLCK, 1) locks),
//     polled every RetryInterval until the bounded wait expires;
//   - while held the file carries a JSON holder record (host, pid,
//     schema_version, started_at, version); release truncates it and never
//     unlinks the file, so a contender can never lock a fresh inode.
//
// AcquireReentrant adds the re-entrancy upstream keys per thread. Go has no
// thread identity, so it is per process: a nested acquire of a lock this
// process already holds through AcquireReentrant succeeds at once and only
// the outermost release drops the OS lock. Two goroutines of one process
// must therefore not rely on a reentrant lock to exclude each other; the
// CLI's writers run one flow per process, like upstream's single thread.
package filelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// RetryInterval is _RETRY_SLEEP_S: the pause between two lock attempts.
var RetryInterval = 100 * time.Millisecond

// Forever is the timeout that waits without bound.
const Forever time.Duration = -1

// schemaVersion is _SCHEMA_VERSION of the holder record.
const schemaVersion = 1

// TimeoutError is LockAcquireTimeout: the bounded wait expired.
type TimeoutError struct{ Path string }

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("Could not acquire machine lock at %s within bounded wait", pyjson.ReprString(e.Path))
}

// SymlinkError is NoFollowPathError: the lock path is a symbolic link.
type SymlinkError struct{ Path string }

func (e *SymlinkError) Error() string {
	return fmt.Sprintf("refusing to follow symbolic link %s", e.Path)
}

// NoFollow is O_NOFOLLOW where the platform has it, else 0 (callers then
// Lstat first).
const NoFollow = noFollowFlag

// IsSymlinkLoop reports the open error O_NOFOLLOW gives for a symlink.
func IsSymlinkLoop(err error) bool { return isSymlinkLoop(err) }

// Lock is a held lock. Release it exactly once.
type Lock struct {
	path   string
	held   *held
	shared bool
}

type held struct {
	f     *os.File
	key   string
	depth int
}

var (
	registryMu sync.Mutex
	registry   = map[string]*held{}
)

// Path is the lock file.
func (l *Lock) Path() string { return l.path }

// Acquire takes the exclusive lock on path, waiting up to timeout (Forever
// waits without bound, 0 makes one attempt). A *TimeoutError reports the
// expired wait; any other error is a genuine I/O failure.
func Acquire(path string, timeout time.Duration) (*Lock, error) {
	f, err := acquireFile(path, timeout)
	if err != nil {
		return nil, err
	}
	return &Lock{path: path, held: &held{f: f, depth: 1}}, nil
}

// AcquireReentrant is Acquire with per-process re-entrancy (see the package
// documentation): a lock this process already holds is re-entered at once.
func AcquireReentrant(path string, timeout time.Duration) (*Lock, error) {
	key := RealPath(path)
	registryMu.Lock()
	if h, ok := registry[key]; ok {
		h.depth++
		registryMu.Unlock()
		return &Lock{path: path, held: h, shared: true}, nil
	}
	registryMu.Unlock()
	f, err := acquireFile(path, timeout)
	if err != nil {
		return nil, err
	}
	h := &held{f: f, key: key, depth: 1}
	registryMu.Lock()
	registry[key] = h
	registryMu.Unlock()
	return &Lock{path: path, held: h, shared: true}, nil
}

// HeldByProcess reports whether this process holds path through
// AcquireReentrant.
func HeldByProcess(path string) bool {
	registryMu.Lock()
	defer registryMu.Unlock()
	_, ok := registry[RealPath(path)]
	return ok
}

// Release drops the lock (for a reentrant lock, only the outermost release
// drops the OS lock). The holder record is truncated, the file kept.
func (l *Lock) Release() {
	if l == nil || l.held == nil {
		return
	}
	h := l.held
	l.held = nil
	if l.shared {
		registryMu.Lock()
		h.depth--
		if h.depth > 0 {
			registryMu.Unlock()
			return
		}
		delete(registry, h.key)
		registryMu.Unlock()
	}
	releaseFile(h.f)
}

// acquireFile waits for the lock. An unbounded wait blocks in the OS
// instead of polling (upstream polls every _RETRY_SLEEP_S either way; the
// blocking wait is the same exclusion without the latency).
func acquireFile(path string, timeout time.Duration) (*os.File, error) {
	if timeout < 0 {
		f, _, err := attempt(path, true)
		return f, err
	}
	deadline := time.Now().Add(timeout)
	for {
		f, ok, err := attempt(path, false)
		if err != nil {
			return nil, err
		}
		if ok {
			return f, nil
		}
		if !time.Now().Before(deadline) {
			return nil, &TimeoutError{Path: path}
		}
		time.Sleep(RetryInterval)
	}
}

// attempt is _attempt_acquire: open, one lock attempt (or a blocking wait),
// then the holder record. The file is closed on every non-acquiring path.
func attempt(path string, block bool) (*os.File, bool, error) {
	if err := ensureDir(path); err != nil {
		return nil, false, err
	}
	f, err := openNoFollow(path)
	if err != nil {
		return nil, false, err
	}
	ok := true
	if block {
		err = lockBlocking(f)
	} else {
		ok, err = tryLock(f)
	}
	if err != nil || !ok {
		f.Close()
		return nil, false, err
	}
	if err := writeRecord(f); err != nil {
		unlock(f)
		f.Close()
		return nil, false, err
	}
	return f, true, nil
}

func releaseFile(f *os.File) {
	_ = f.Truncate(0)
	unlock(f)
	_ = f.Close()
}

// ensureDir is _ensure_dir: create the parent, hardening it to 0700 only
// when this call created it (an existing, possibly shared, directory is
// never narrowed).
func ensureDir(path string) error {
	parent := filepath.Dir(path)
	if _, err := os.Stat(parent); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(parent), 0o777); err != nil {
		return err
	}
	if err := os.Mkdir(parent, 0o777); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(parent, 0o700)
	}
	return nil
}

func openNoFollow(path string) (*os.File, error) {
	if noFollowFlag == 0 {
		if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return nil, &SymlinkError{Path: path}
		}
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|noFollowFlag, 0o600)
	if err != nil && isSymlinkLoop(err) {
		return nil, &SymlinkError{Path: path}
	}
	return f, err
}

// writeRecord is _atomic_write_under_lock of a fresh LockRecord.
func writeRecord(f *os.File) error {
	host, _ := os.Hostname()
	rec := jsonx.NewObject(
		"host", host,
		"pid", os.Getpid(),
		"schema_version", schemaVersion,
		"started_at", clock.ISO(time.Now().UTC()),
		"version", app.Version,
	)
	payload := []byte(jsonx.Dumps(rec, jsonx.Line))
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.WriteAt(payload, 0); err != nil {
		return err
	}
	return f.Sync()
}

// RealPath is os.path.realpath / Path.resolve(strict=False): the absolute
// path with every symlink of its longest existing prefix resolved.
func RealPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	var tail []string
	cur := abs
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(append([]string{r}, tail...)...)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		cur = parent
	}
}
