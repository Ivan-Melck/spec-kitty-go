package plugin

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// sitePackages matches the oracle's installed-package paths, which the Go
// binary renders as "<spec-kitty>/...".
var sitePackages = regexp.MustCompile(`/\S*/site-packages/`)

// reSpecKittyTag is Go's highlighted "<spec-kitty>/" placeholder (raw or
// normalized SGR terminators).
var reSpecKittyTag = regexp.MustCompile("\x1b\\[1[m\x01]<\x1b\\[0[m\x01]\x1b\\[1;95[m\x01]spec-kitty\x1b\\[0[m\x01]\x1b\\[1[m\x01]>\x1b\\[0[m\x01]\x1b\\[35([m\x01])/")

// normWarnings maps the oracle's site-packages prefix to "<spec-kitty>/"
// and drops the console padding (whose length follows the path length).
func normWarnings(s string) string {
	s = sitePackages.ReplaceAllString(s, "<spec-kitty>/")
	// Colour runs: the highlighter reads Go's "<spec-kitty>" placeholder as
	// a tag; the oracle's absolute path starts the magenta path span there.
	s = reSpecKittyTag.ReplaceAllString(s, "\x1b[35${1}<spec-kitty>/")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return testkit.MaskTracebackFrames(strings.Join(lines, "\n"))
}

// TestPluginBuildProjectProfiles: the Claude bundle projects the staging
// root's project-layer agent profiles (new ids and overrides merged over
// the built-in ones, with the layer-collision warning); an invalid project
// profile aborts the build with the repository's skipped-profile BuildError.
func TestPluginBuildProjectProfiles(t *testing.T) {
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) { s.Env = append(s.Env, "COLUMNS=500") })
	const dir = ".kittify/charter-packs/agent_profiles/"
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(dir+"helper.agent.yaml", "profile-id: helper\nname: Helper\nroles: [implementer]\npurpose: Help out.\nspecialization:\n  primary-focus: helping\n")
		s.WriteFile(dir+"ivan.agent.yaml", "profile-id: implementer-ivan\nname: Ivan Custom\n")
		s.WriteFile(".kittify/profiles/legacy-larry.agent.yaml", "profile-id: legacy-larry\nname: Larry\nroles: [implementer]\npurpose: Old.\nspecialization:\n  primary-focus: legacy\n")
	})
	check := func(label string, args ...string) {
		t.Helper()
		g, o := p.Compare(false, args...)
		if a, b := normWarnings(p.Go.Normalize(g.Stderr)), normWarnings(p.Oracle.Normalize(o.Stderr)); a != b {
			t.Errorf("%s: stderr differs\n%s", label, testkit.Diff(b, a))
		}
	}
	check("build", "plugin", "build", "--target", "claude-code", "--skip-validate")
	p.CompareTree("dist", true)
	compareModes(t, p, "dist")
	// A staging root without project profiles (nested output dir).
	p.Both(func(s *testkit.Sandbox) { os.MkdirAll(filepath.Join(s.Dir, "sub"), 0o755) })
	p.RunIn("sub", "plugin", "build", "--target", "claude-code", "--skip-validate", "--output-dir", "out")
	p.CompareTree("sub", true)
	// Invalid project profiles: schema errors and YAML errors.
	for _, bad := range []string{"name: Broken\n", "a: [\n", "profile-id: x\nname: X\n"} {
		bad := bad
		p.Both(func(s *testkit.Sandbox) {
			os.RemoveAll(filepath.Join(s.Dir, "dist"))
			s.WriteFile(dir+"bad.agent.yaml", bad)
		})
		check(bad, "plugin", "build", "--target", "claude-code", "--skip-validate")
	}
}
