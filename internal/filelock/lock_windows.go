//go:build windows

package filelock

import (
	"os"
	"syscall"
	"unsafe"
)

// LockFileEx / UnlockFileEx on the file's first byte: the byte range
// msvcrt.locking(fd, LK_NBLCK, 1) locks after the open (offset 0), so a Go
// and a Python writer exclude each other.
var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileFailImmediately = 0x00000001
	lockfileExclusiveLock   = 0x00000002

	errorLockViolation syscall.Errno = 33
)

const noFollowFlag = 0

func tryLock(f *os.File) (bool, error) {
	var ol syscall.Overlapped
	r1, _, e1 := procLockFileEx.Call(
		f.Fd(),
		uintptr(lockfileExclusiveLock|lockfileFailImmediately),
		0,
		1, 0,
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 != 0 {
		return true, nil
	}
	if errno, ok := e1.(syscall.Errno); ok && (errno == errorLockViolation || errno == syscall.ERROR_IO_PENDING) {
		return false, nil
	}
	return false, &os.PathError{Op: "LockFileEx", Path: f.Name(), Err: e1}
}

// lockBlocking is LockFileEx without LOCKFILE_FAIL_IMMEDIATELY: the handle
// is synchronous, so the call waits for the first byte.
func lockBlocking(f *os.File) error {
	var ol syscall.Overlapped
	r1, _, e1 := procLockFileEx.Call(f.Fd(), uintptr(lockfileExclusiveLock), 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
	if r1 != 0 {
		return nil
	}
	return &os.PathError{Op: "LockFileEx", Path: f.Name(), Err: e1}
}

func unlock(f *os.File) {
	var ol syscall.Overlapped
	procUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
}

func isSymlinkLoop(error) bool { return false }
