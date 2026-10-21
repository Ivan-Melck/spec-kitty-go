//go:build !linux

package kittify

func unameInfo() (string, string, string, bool) { return "", "", "", false }
