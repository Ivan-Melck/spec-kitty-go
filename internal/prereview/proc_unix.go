//go:build !windows

package prereview

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup is start_new_session=True: the child leads its own session
// so a timeout can signal the whole tree.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// signalProcessTree is _signal_owned_process_tree on POSIX: killpg, falling
// back to the direct child when the group is gone.
func signalProcessTree(cmd *exec.Cmd, force bool) {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if syscall.Kill(-cmd.Process.Pid, sig) != nil {
		cmd.Process.Signal(sig)
	}
}
