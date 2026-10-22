package orchestrator

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// authoringLockTimeout is authoring_lock's machine_file_lock timeout_s.
var authoringLockTimeout = 10 * time.Second

// authoringLockRetry is kernel.locks' retry sleep between non-blocking
// attempts.
const authoringLockRetry = 50 * time.Millisecond

type heldLock struct {
	f     *os.File
	count int
}

var (
	heldMu sync.Mutex
	held   = map[string]*heldLock{}
)

// AuthoringLockPath is <git-common-dir>/spec-kitty-locks/design-<sha256(slug)>.lock.
func AuthoringLockPath(root, slug string) (string, error) {
	cd, err := commonDir(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(cd, "spec-kitty-locks", "design-"+SHA256Hex([]byte(slug))+".lock"), nil
}

// AcquireAuthoringLock is design.receipts.authoring_lock: a reentrant,
// machine-wide exclusive lock (flock on POSIX) serializing cooperative API
// writers. It retries for up to 10 s, then fails with DESIGN_LOCK_TIMEOUT.
// The returned release must be called (deferred) by the holder; outside a
// git repository there is nothing to lock and release is a no-op.
func AcquireAuthoringLock(root, slug string) (func(), error) {
	path, err := AuthoringLockPath(root, slug)
	if err != nil {
		return func() {}, nil
	}
	heldMu.Lock()
	if h, ok := held[path]; ok {
		h.count++
		heldMu.Unlock()
		return func() { releaseAuthoringLock(path) }, nil
	}
	heldMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return func() {}, nil
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return func() {}, nil
	}
	deadline := time.Now().Add(authoringLockTimeout)
	for {
		ok, lerr := tryLockFile(f)
		if lerr != nil {
			f.Close()
			return func() {}, nil
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, NewDesignError("DESIGN_LOCK_TIMEOUT", "The mission authoring lock is busy")
		}
		time.Sleep(authoringLockRetry)
	}
	heldMu.Lock()
	held[path] = &heldLock{f: f, count: 1}
	heldMu.Unlock()
	return func() { releaseAuthoringLock(path) }, nil
}

func releaseAuthoringLock(path string) {
	heldMu.Lock()
	defer heldMu.Unlock()
	h, ok := held[path]
	if !ok {
		return
	}
	h.count--
	if h.count > 0 {
		return
	}
	unlockFile(h.f)
	h.f.Close()
	delete(held, path)
}
