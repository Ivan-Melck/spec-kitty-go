package acceptance

import "testing"

// TestDecisionsRuntimeLockIsBookkeeping pins #6628: the decision service's
// runtime lock is self-bookkeeping only at its depth-exact mission location.
func TestDecisionsRuntimeLockIsBookkeeping(t *testing.T) {
	for p, want := range map[string]bool{
		"kitty-specs/calc-01ABCDEF/decisions/index.json.lock":              true,
		"sub/kitty-specs/calc-01ABCDEF/decisions/index.json.lock":          true,
		"kitty-specs/calc-01ABCDEF/decisions/index.json":                   false,
		"kitty-specs/calc-01ABCDEF/nested/decisions/index.json.lock":       false,
		"decisions/index.json.lock":                                        false,
		"kitty-specs/calc-01ABCDEF/decisions/other/index.json.lock":        false,
		"kitty-specs/calc-01ABCDEF/decisions/index.json.lock.bak":          false,
		".worktrees/x/kitty-specs/calc-01ABCDEF/decisions/index.json.lock": true,
	} {
		if got := IsDecisionsRuntimeLock(p); got != want {
			t.Errorf("IsDecisionsRuntimeLock(%q) = %v, want %v", p, got, want)
		}
		if got := IsSelfBookkeeping(p); got != want {
			t.Errorf("IsSelfBookkeeping(%q) = %v, want %v", p, got, want)
		}
	}
}
