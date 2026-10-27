//go:build unix

package invocation

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// oNoFollow refuses a final-component symlink at open (kernel.no_follow).
const oNoFollow = syscall.O_NOFOLLOW

func isNoFollowRefusal(err error) bool { return errors.Is(err, syscall.ELOOP) }

// acquireFileLock is kernel.locks.machine_file_lock(blocking, timeout_s): an
// exclusive flock(2) on lockPath (0600, never through a symlink), polled until
// the deadline. acquired is false on timeout.
func acquireFileLock(lockPath string, timeout time.Duration) (release func(), acquired bool, err error) {
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, false, err
	}
	deadline := time.Now().Add(timeout)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			f.Close()
			return nil, false, err
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, false, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return func() {
		_ = f.Truncate(0)
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, true, nil
}
