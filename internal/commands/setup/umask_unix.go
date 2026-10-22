//go:build unix

package setup

import (
	"sync"
	"syscall"
)

var umaskMu sync.Mutex

// currentUmask reads the process umask (set-and-restore, like Python's
// _get_umask).
func currentUmask() int {
	umaskMu.Lock()
	defer umaskMu.Unlock()
	m := syscall.Umask(0)
	syscall.Umask(m)
	return m
}
