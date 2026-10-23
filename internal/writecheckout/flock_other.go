//go:build !unix

package writecheckout

import "os"

func tryFlock(*os.File) (bool, error) { return true, nil }

func unflock(*os.File) {}
