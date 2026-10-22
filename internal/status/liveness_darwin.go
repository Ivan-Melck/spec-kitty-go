//go:build darwin

package status

import (
	"encoding/binary"
	"syscall"
	"unsafe"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// kinfoProcSize is sizeof(struct kinfo_proc) on 64-bit darwin.
const kinfoProcSize = 648

// kinfoProc reads struct kinfo_proc for pid via sysctl(CTL_KERN,
// KERN_PROC, KERN_PROC_PID, pid); ok=false when no such process.
func kinfoProc(pid int) ([]byte, bool) {
	mib := [4]int32{1 /* CTL_KERN */, 14 /* KERN_PROC */, 1 /* KERN_PROC_PID */, int32(pid)}
	buf := make([]byte, kinfoProcSize)
	n := uintptr(len(buf))
	_, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), 4,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)), 0, 0)
	if errno != 0 || n == 0 {
		return nil, false
	}
	return buf[:n], true
}

// IsProcessAlive is process_liveness.is_process_alive (psutil.pid_exists:
// kill(pid, 0) succeeding or failing with EPERM).
func IsProcessAlive(pid int) bool {
	if pid < 0 {
		return false
	}
	if pid == 0 {
		return true
	}
	err := syscall.Kill(pid, 0)
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

// CreationTimeBaseline is str(psutil.Process(pid).create_time()) on darwin:
// kp_proc.p_starttime (tv_sec + tv_usec / 1e6) of the kinfo_proc record.
func CreationTimeBaseline(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	b, ok := kinfoProc(pid)
	if !ok || len(b) < 16 {
		return "", false
	}
	sec := int64(binary.LittleEndian.Uint64(b[0:8]))
	usec := int32(binary.LittleEndian.Uint32(b[8:12]))
	return jsonx.FormatFloat(float64(sec) + float64(usec)/1e6), true
}
