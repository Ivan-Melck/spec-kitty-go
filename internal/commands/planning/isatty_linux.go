//go:build linux

package planning

import (
	"os"
	"syscall"
	"unsafe"
)

// stdinIsTTY is sys.stdin.isatty() (a TCGETS probe, so /dev/null is not a tty).
func stdinIsTTY() bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return e == 0
}
