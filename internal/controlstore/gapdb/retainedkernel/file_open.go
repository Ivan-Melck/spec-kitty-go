package retainedkernel

import (
	"os"

	"golang.org/x/sys/unix"
)

// os.Open retries EINTR internally, repeating path-buffer allocation without
// consulting our cancellation/liveness checks. Fail closed after ONE syscall
// attempt instead. Only fixed proc paths from this package reach this helper;
// no provider, descriptor, admission or caller-selected opening API is added.
func openRetainedFileOnce(path string, check func() error) (*os.File, error) {
	return openRetainedFileWith(path, check, unix.Open)
}

// Private deterministic syscall seam, not a production-selectable callback.
// A failed syscall's FD is not owned; only nonnegative success is wrapped/closed.
func openRetainedFileWith(path string, check func() error, open func(string, int, uint32) (int, error)) (*os.File, error) {
	return openRetainedFileWithCalls(path, check, open, (*os.File).Close, unix.Close)
}

// Call-only seams, never stored or selected by production input.
func openRetainedFileWithCalls(path string, check func() error, open func(string, int, uint32) (int, error), closeFile func(*os.File) error, closeFD func(int) error) (*os.File, error) {
	if check == nil || open == nil {
		return nil, ErrIdentity
	}
	if err := check(); err != nil {
		return nil, err
	}
	fd, err := open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err // do NOT close an error-associated/unowned sentinel FD
	}
	if fd < 0 {
		return nil, ErrIdentity
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil { // defensive; pinned NewFile accepts every nonnegative FD
		return nil, retainedCloseResult(ErrIdentity, "file-wrap", closeFD(fd))
	}
	if err := check(); err != nil {
		return nil, retainedCloseResult(err, "file-postcheck", closeFile(file))
	}
	return file, nil // existing caller owns Close; all identity checks remain
}
