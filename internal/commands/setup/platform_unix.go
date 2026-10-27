//go:build !windows

package setup

import (
	"os"
	"os/exec"
	"syscall"
)

// nagCacheOwnedPrivately is NagCache.read's POSIX checks: the cache file is
// owned by the effective uid (CHK023) and has mode 0600 (CHK006).
func nagCacheOwnedPrivately(st os.FileInfo) bool {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Geteuid() {
		return false
	}
	return st.Mode().Perm() == 0o600
}

// detachBackgroundCheck is start_new_session=True.
func detachBackgroundCheck(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
