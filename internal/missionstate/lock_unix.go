//go:build unix

package missionstate

import (
	"errors"
	"os"
	"syscall"
)

func tryLock(f *os.File) bool {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}

func unlock(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

// openNoFollow is kernel.no_follow.open_no_follow(path, O_RDWR|O_CREAT, 0o600).
func openNoFollow(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil && errors.Is(err, syscall.ELOOP) {
		return nil, &NoFollowError{path + " is a symlink; refusing to open it"}
	}
	return f, err
}
