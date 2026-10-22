package toolsurface

import (
	"strings"
	"testing"
)

// PhysicalEffect.id: sha256(json.dumps((phase, owner, root_id, path,
// action), separators=(",", ":"))) — values computed with CPython.
func TestEffectIDMatchesUpstream(t *testing.T) {
	for _, c := range []struct {
		phase, owner, root, path, action, want string
	}{
		{"managed", "managed_skills", "project", "é/x.md", "create", "abb8379646542b86056bf9e71ada05e7cb1a33aceab8396e8b0ae59b694028e4"},
		{"assets", "global_assets", "global-home", ".kittify/a b", "update", "4e892d1e89ff656d66f08c9fb36253f55234c087d48f6a2fac55fb5b673d2923"},
	} {
		e := NewEffect(c.owner, c.phase, Root{ID: c.root, Scope: "project", Path: "/r"}, c.path, c.action, Absent(), FileOf("x", 0o644), "r", nil, nil, nil)
		if got := e.ID(); got != c.want {
			t.Errorf("%v: id %s, want %s", c, got, c.want)
		}
	}
}

func TestActionAndCoalesce(t *testing.T) {
	f1, f2 := FileOf("a", 0o644), FileOf("b", 0o644)
	for _, c := range []struct {
		before, after FileState
		want          string
	}{
		{Absent(), f1, "create"}, {f1, Absent(), "delete"}, {f1, f2, "update"}, {f1, DirOf(0o755), "replace"},
		{f1, FileOf("a", 0o755), "chmod"}, {f1, f1, ""},
	} {
		if got := Action(c.before, c.after); got != c.want {
			t.Errorf("Action(%v, %v) = %q, want %q", c.before, c.after, got, c.want)
		}
	}
	root := Root{ID: "project", Scope: "project", Path: "/p"}
	dir := func(owner string, logical ...string) Effect {
		return NewEffect(owner, "surface_repair", root, ".agents", "create", Absent(), DirOf(0o755), "parent", nil, logical, nil)
	}
	// A shared directory create merges across owners (claims unioned).
	out, err := Coalesce([]Effect{dir("managed_skills", "b"), dir("command_skills", "a")})
	if err != nil || len(out) != 1 || strings.Join(out[0].LogicalOwners, ",") != "a,b" || out[0].Owner != "command_skills" {
		t.Fatalf("shared dir: %v %+v", err, out)
	}
	// Conflicting file intent at one destination is refused.
	a := NewEffect("managed_skills", "surface_repair", root, "x", "create", Absent(), f1, "r", nil, nil, nil)
	b := NewEffect("command_skills", "surface_repair", root, "x", "create", Absent(), f2, "r", nil, nil, nil)
	if _, err := Coalesce([]Effect{a, b}); err == nil || !strings.Contains(err.Error(), "differs in: owner, after") {
		t.Errorf("conflict: %v", err)
	}
	// Sorted by phase order first (global_bootstrap before surface_repair).
	g := NewEffect("zzz_owner", "global_bootstrap", Root{ID: "global-home", Path: "/h"}, "z", "create", Absent(), f1, "r", nil, nil, nil)
	out, _ = Coalesce([]Effect{a, g})
	if out[0].Owner != "zzz_owner" || out[1].Owner != "managed_skills" {
		t.Errorf("order: %s first", out[0].Owner)
	}
}
