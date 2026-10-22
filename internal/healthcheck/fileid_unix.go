//go:build unix

package healthcheck

import (
	"os"
	"syscall"
)

// fileIdentity is (st_dev, st_ino).
func fileIdentity(st os.FileInfo) (uint64, uint64) {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return uint64(sys.Dev), uint64(sys.Ino)
	}
	return 0, 0
}
