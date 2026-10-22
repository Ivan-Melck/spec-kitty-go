//go:build !unix

package healthcheck

import "os"

// fileIdentity is (st_dev, st_ino); unavailable off unix (the snapshot then
// compares types and presence only).
func fileIdentity(os.FileInfo) (uint64, uint64) { return 0, 0 }
