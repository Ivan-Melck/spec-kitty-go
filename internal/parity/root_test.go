// Package parity holds cross-area differential tests against the oracle.
package parity

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestVersion(t *testing.T) {
	s := testkit.NewSandbox(t)
	r := s.MustRun("--version")
	if r.Stdout != "spec-kitty-cli version 4.0.0rc8\n" {
		t.Fatalf("got %q", r.Stdout)
	}
}

func TestVersionParity(t *testing.T) {
	p := testkit.NewPair(t)
	p.Compare(false, "--version")
	p.Compare(false, "nosuch-command")
}
