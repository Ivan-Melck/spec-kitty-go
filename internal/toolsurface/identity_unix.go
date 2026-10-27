//go:build !windows

package toolsurface

import (
	"os"
	"syscall"
)

// nodeIdentity is (st_dev, st_ino) of an lstat result.
func nodeIdentity(_ string, info os.FileInfo) (dev, ino uint64) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev), uint64(st.Ino)
	}
	return 0, 0
}
