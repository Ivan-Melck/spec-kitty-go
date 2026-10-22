//go:build !linux && !darwin

package status

import (
	"os"
	"runtime"
	"syscall"
)

// IsProcessAlive is process_liveness.is_process_alive (psutil.pid_exists
// via signal 0 where available).
func IsProcessAlive(pid int) bool {
	if pid < 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}

// IsClaimingProcessAlive is process_liveness.is_claiming_process_alive.
func IsClaimingProcessAlive(pid int, baseline string) bool {
	if baseline == "" {
		return IsProcessAlive(pid)
	}
	got, ok := CreationTimeBaseline(pid)
	return ok && got == baseline
}

// CreationTimeBaseline is unavailable on this platform: the process
// creation time cannot be read, so no baseline is captured (a present
// baseline fails the claim liveness check closed, like psutil's
// AccessDenied/NoSuchProcess path).
func CreationTimeBaseline(pid int) (string, bool) { return "", false }
