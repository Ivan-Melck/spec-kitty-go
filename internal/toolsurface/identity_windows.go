//go:build windows

package toolsurface

import (
	"os"
	"syscall"
)

// nodeIdentity is (st_dev, st_ino) as CPython's lstat reports them on
// Windows: the volume serial number and the 64-bit file index, read without
// following a final reparse point.
func nodeIdentity(p string, _ os.FileInfo) (dev, ino uint64) {
	name, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return 0, 0
	}
	h, err := syscall.CreateFile(name, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, 0
	}
	defer syscall.CloseHandle(h)
	var d syscall.ByHandleFileInformation
	if syscall.GetFileInformationByHandle(h, &d) != nil {
		return 0, 0
	}
	return uint64(d.VolumeSerialNumber), uint64(d.FileIndexHigh)<<32 | uint64(d.FileIndexLow)
}
