//go:build windows

package setup

import (
	"os"
	"os/exec"
	"syscall"
)

// nagCacheOwnedPrivately: NagCache.read skips the ownership and mode checks
// on win32.
func nagCacheOwnedPrivately(os.FileInfo) bool { return true }

// detachBackgroundCheck is creationflags=CREATE_NEW_PROCESS_GROUP.
func detachBackgroundCheck(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
