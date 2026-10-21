package doctrine_test

import (
	"strings"
	"testing"
)

func TestProfilesAndMissionTypesGoOnly(t *testing.T) {
	s := newProject(t)
	r := s.MustRun("profiles", "list", "--json")
	if !strings.Contains(r.Stdout, `"profile_id": "implementer-ivan"`) {
		t.Fatalf("profiles list: %s", r.Stdout)
	}
	r = s.MustRun("agent", "profile", "show", "reviewer-renata", "--json")
	if !strings.Contains(r.Stdout, `"profile_id": "reviewer-renata"`) {
		t.Fatalf("agent profile show: %s", r.Stdout)
	}
	r = s.MustRun("mission", "list", "--json")
	if !strings.Contains(r.Stdout, "software-dev") {
		t.Fatalf("mission list: %s", r.Stdout)
	}
	r = s.MustRun("charter", "context", "--action", "specify", "--json")
	if !strings.Contains(r.Stdout, `"mode": "missing"`) {
		t.Fatalf("charter context: %s", r.Stdout)
	}
}

func TestParityProfilesMissionCharterContext(t *testing.T) {
	p := newOraclePair(t)
	for _, args := range [][]string{
		{"profiles", "list", "--json"},
		{"profiles", "list"},
		{"profiles", "list", "--all"},
		{"agent", "profile", "list", "--json"},
		{"profiles", "show", "implementer-ivan", "--json"},
		{"profiles", "show", "reviewer-renata"},
		{"profiles", "get", "nope", "--json"},
		{"mission", "list", "--json"},
		{"mission-type", "list"},
		{"mission", "show", "software-dev", "--json"},
		{"charter", "context", "--action", "specify", "--json"},
		{"charter", "context", "--action", "implement"},
		{"charter", "context", "--include", "directive:DIRECTIVE_010"},
		{"charter", "context", "--include", "tactic:acceptance-test-first", "--json"},
	} {
		compareMasked(t, p, false, args...)
	}
	p.CompareTree(".kittify/charter", true)
}
