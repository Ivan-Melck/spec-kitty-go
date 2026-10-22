//go:build linux || darwin

package rich

import (
	"syscall"
	"unsafe"
)

type winsize struct{ Row, Col, X, Y uint16 }

// terminalWidth is os.get_terminal_size(fd).columns (ok=false when fd is
// not a terminal).
func terminalWidth(fd int) (int, bool) {
	var ws winsize
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws))); errno != 0 {
		return 0, false
	}
	return int(ws.Col), true
}

// isatty is file.isatty().
func isatty(fd int) bool {
	_, ok := terminalWidth(fd)
	return ok
}
