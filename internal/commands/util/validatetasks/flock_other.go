//go:build !unix

package validatetasks

import "os"

func flockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	return func() { f.Close() }, nil
}
