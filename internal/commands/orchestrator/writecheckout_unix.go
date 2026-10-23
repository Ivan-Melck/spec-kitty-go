//go:build unix

package orchestrator

import (
	"errors"
	"os"
	"syscall"
)

// tryFlock is fcntl.flock(fd, LOCK_EX | LOCK_NB).
func tryFlock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return false, err
}

func unflock(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
