//go:build !unix && !windows

package filelock

import "os"

// No OS file locking on this platform (js/wasm, plan9): every attempt
// succeeds, so writers are serialised only within one process.
const noFollowFlag = 0

func tryLock(*os.File) (bool, error) { return true, nil }

func lockBlocking(*os.File) error { return nil }

func unlock(*os.File) {}

func isSymlinkLoop(error) bool { return false }
