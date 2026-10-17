package project

import (
	"os"
	"path/filepath"
)

// UserHome returns the user's home directory (Path.home()).
func UserHome() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

// GlobalKittify is ~/.kittify, or SPEC_KITTY_HOME when set.
func GlobalKittify() string {
	if v := os.Getenv("SPEC_KITTY_HOME"); v != "" {
		return v
	}
	return filepath.Join(UserHome(), ".kittify")
}

// GlobalSpecKitty is ~/.spec-kitty, or SPEC_KITTY_HOME when set.
func GlobalSpecKitty() string {
	if v := os.Getenv("SPEC_KITTY_HOME"); v != "" {
		return v
	}
	return filepath.Join(UserHome(), ".spec-kitty")
}
