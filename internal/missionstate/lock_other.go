//go:build !unix

package missionstate

import "os"

func tryLock(*os.File) bool { return true }

func unlock(*os.File) {}
