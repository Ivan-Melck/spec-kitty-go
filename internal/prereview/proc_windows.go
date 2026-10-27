//go:build windows

package prereview

import (
	"os/exec"
	"strconv"
	"syscall"
)

// ownProcessGroup is creationflags=CREATE_NEW_PROCESS_GROUP.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// signalProcessTree is _signal_owned_process_tree on Windows: the tree-aware
// taskkill, falling back to the direct child if it fails (terminate and kill
// are both TerminateProcess there; an exited child just reports an error).
func signalProcessTree(cmd *exec.Cmd, force bool) {
	args := []string{"/PID", strconv.Itoa(cmd.Process.Pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	if exec.Command("taskkill", args...).Run() != nil {
		cmd.Process.Kill()
	}
}
