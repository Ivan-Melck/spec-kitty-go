//go:build linux

package kittify

import "syscall"

func unameInfo() (string, string, string, bool) {
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		return "", "", "", false
	}
	return cstr(u.Sysname[:]), cstr(u.Release[:]), cstr(u.Machine[:]), true
}

func cstr[T int8 | uint8](b []T) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(out)
}
