//go:build linux

package maintenance

import (
	"syscall"
	"unsafe"
)

// stdinIsTTY is sys.stdin.isatty() (TCGETS succeeds).
func stdinIsTTY() bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, 0, syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return e == 0
}
