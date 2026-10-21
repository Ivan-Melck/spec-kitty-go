package doctrine_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

var (
	reISOTime     = regexp.MustCompile(`20\d\d-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(\+00:00|Z)?`)
	reDurationSec = regexp.MustCompile(`"duration_seconds": [0-9.]+`)
	reRepFiles    = regexp.MustCompile(`(?s)"representative_files_preview": \[.*?\]`)
)

// maskCharter masks the built-in root, timestamps and filesystem-order
// dependent evidence previews.
func maskCharter(s string) string {
	s = maskBuiltin(s)
	s = reISOTime.ReplaceAllString(s, "<TS>")
	s = reDurationSec.ReplaceAllString(s, `"duration_seconds": <D>`)
	return reRepFiles.ReplaceAllString(s, `"representative_files_preview": <P>`)
}

func compareCharterMasked(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	g, o := p.Run(args...)
	if g.Code != o.Code {
		t.Errorf("%s: exit go=%d oracle=%d\n--- go:\n%s%s\n--- oracle:\n%s%s", strings.Join(args, " "), g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
	}
	gs, os_ := p.Go.Normalize(maskCharter(g.Stdout)), p.Oracle.Normalize(maskCharter(o.Stdout))
	if gs != os_ {
		t.Errorf("%s: stdout differs\n%s", strings.Join(args, " "), testkit.Diff(os_, gs))
	}
}

func TestCharterGenerateGoOnly(t *testing.T) {
	s := newProject(t)
	r := s.MustRun("charter", "interview", "--defaults", "--json")
	if !strings.Contains(r.Stdout, `"interview_path"`) && !strings.Contains(r.Stdout, "answers.yaml") {
		t.Fatalf("interview: %s", r.Stdout)
	}
	r = s.MustRun("charter", "generate", "--json")
	if !strings.Contains(r.Stdout, `"result": "success"`) || !s.Exists(".kittify/charter/charter.yaml") || !s.Exists(".kittify/charter/charter.md") {
		t.Fatalf("generate: %s", r.Stdout)
	}
	r = s.MustRun("charter", "context", "--action", "review", "--mission-type", "software-dev")
	for _, want := range []string{"Charter Context (Bootstrap):", "Action-Critical Charter Sections (review):", "Action Doctrine (review):", "Reference Docs:"} {
		if !strings.Contains(r.Stdout, want) {
			t.Fatalf("context missing %q:\n%s", want, r.Stdout)
		}
	}
	r = s.MustRun("charter", "status", "--json")
	if !strings.Contains(r.Stdout, `"generation_state": "not_started"`) || !strings.Contains(r.Stdout, `"state": "fresh"`) {
		t.Fatalf("status: %s", r.Stdout)
	}
	r = s.MustRun("charter", "synthesize", "--json")
	if !strings.Contains(r.Stdout, `"mode": "fresh_project_seed"`) || !s.Exists(".kittify/charter-packs/PROVENANCE.md") {
		t.Fatalf("synthesize: %s", r.Stdout)
	}
	if !strings.Contains(s.ReadFile(".kittify/charter/synthesis-manifest.yaml"), "built_in_only: true") {
		t.Fatalf("manifest: %s", s.ReadFile(".kittify/charter/synthesis-manifest.yaml"))
	}
	r = s.MustRun("charter", "preflight", "--json")
	if !strings.Contains(r.Stdout, `"passed": true`) {
		t.Fatalf("preflight: %s", r.Stdout)
	}
	r = s.MustRun("charter", "new", "directive", "LOVE_THY_ENEMY")
	if !s.Exists(".kittify/charter-packs/directive/love-thy-enemy.directive.yaml") {
		t.Fatalf("new: %s", r.Stdout)
	}
	if r := s.Run("charter", "new", "directive", "bad-id"); r.Code != 1 || !strings.Contains(r.Stdout, "String should match pattern") {
		t.Fatalf("new bad id: %d %s", r.Code, r.Stdout)
	}
	if r := s.Run("charter", "validate", ".kittify/charter-packs"); r.Code != 0 || !strings.Contains(r.Stdout, "1 artifact(s) passed validation.") {
		t.Fatalf("validate: %d %s", r.Code, r.Stdout)
	}
	s.WriteFile("arts/x.tactic.yaml", "schema_version: \"2.0\"\nid: X\nname: 5\nsteps: []\n")
	if r := s.Run("charter", "validate", "arts"); r.Code != 1 || !strings.Contains(r.Stdout, "4 validation errors for Tactic") {
		t.Fatalf("validate bad: %d %s", r.Code, r.Stdout)
	}
}

func TestParityCharterGenerateAndRuntime(t *testing.T) {
	p := newOraclePair(t)
	for _, args := range [][]string{
		{"charter", "status", "--json"},
		{"charter", "bundle", "validate", "--json"},
		{"charter", "synthesize", "--json"},
		{"charter", "interview", "--defaults", "--json"},
		{"charter", "generate", "--json"},
		{"charter", "context", "--action", "specify", "--json"},
		{"charter", "context", "--action", "implement", "--mission-type", "software-dev", "--json"},
		{"charter", "context", "--action", "review", "--mission-type", "software-dev", "--json"},
		{"charter", "status", "--json"},
		{"charter", "status"},
		{"charter", "synthesize", "--dry-run-evidence", "--json"},
		{"charter", "synthesize", "--dry-run", "--json"},
		{"charter", "synthesize", "--json"},
		{"charter", "resynthesize", "--list-topics", "--json"},
		{"charter", "preflight", "--json"},
		{"charter", "preflight", "--strict"},
		{"charter", "bundle", "validate", "--json"},
		{"charter", "new", "tactic", "my-tactic"},
		{"charter", "new", "foo", "x"},
		{"charter", "validate", ".kittify/charter-packs"},
		{"charter", "synthesize", "--adapter", "bogus", "--json"},
	} {
		compareCharterMasked(t, p, args...)
	}
	for _, f := range []string{".kittify/charter/charter.yaml", ".kittify/charter/charter.md", ".kittify/charter/synthesis-manifest.yaml", ".kittify/config.yaml"} {
		if g, o := p.Go.Normalize(maskCharter(p.Go.ReadFile(f))), p.Oracle.Normalize(maskCharter(p.Oracle.ReadFile(f))); g != o {
			t.Errorf("%s differs:\n%s", f, testkit.Diff(o, g))
		}
	}
}
