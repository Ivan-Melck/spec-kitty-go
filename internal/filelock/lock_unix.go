//go:build unix

package filelock

import (
	"errors"
	"os"
	"syscall"
)

const noFollowFlag = syscall.O_NOFOLLOW

// tryLock is fcntl.flock(fd, LOCK_EX | LOCK_NB): (true, nil) when acquired,
// (false, nil) on contention, an error for anything else.
func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EACCES) {
		return false, nil
	}
	return false, &os.PathError{Op: "flock", Path: f.Name(), Err: err}
}

// lockBlocking is flock(fd, LOCK_EX): wait for the lock without polling.
func lockBlocking(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EINTR) {
			return &os.PathError{Op: "flock", Path: f.Name(), Err: err}
		}
	}
}

func unlock(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

func isSymlinkLoop(err error) bool { return errors.Is(err, syscall.ELOOP) }
