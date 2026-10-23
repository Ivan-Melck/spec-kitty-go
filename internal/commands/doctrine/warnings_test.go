package doctrine_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

var (
	reSitePackages = regexp.MustCompile(`\S*site-packages/`)
	// reSitePackagesHL is a highlighted site-packages location (colour runs):
	// the highlighter styles the absolute path that Go, printing the
	// package-relative module path, does not have.
	reSitePackagesHL = regexp.MustCompile("\x1b\\[35[m\x01]\\S*?site-packages/([^\x1b]*)\x1b\\[0[m\x01]\x1b\\[95[m\x01]([^\x1b\\s]*)\x1b\\[0[m\x01]")
	reSpaces         = regexp.MustCompile(`\s+`)
)

// logRecords unwraps RichHandler records (level column + 9-space
// continuation indent) into one line each, with the site-packages install
// prefix of Python warning locations removed (irreducible).
func logRecords(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		switch {
		case strings.HasPrefix(line, "         ") && len(out) > 0:
			out[len(out)-1] += " " + line
		case strings.TrimSpace(line) != "":
			out = append(out, line)
		}
	}
	for i, r := range out {
		r = reSitePackagesHL.ReplaceAllString(r, "$1$2")
		r = reSitePackages.ReplaceAllString(r, "")
		out[i] = strings.TrimSpace(reSpaces.ReplaceAllString(r, " "))
	}
	return out
}

// compareLogRecords compares exit codes and the unwrapped stderr records.
func compareLogRecords(t *testing.T, p *testkit.Pair, args ...string) (testkit.Result, testkit.Result) {
	t.Helper()
	g, o := p.Run(args...)
	if gr, or := strings.Join(logRecords(g.Stderr), "\n"), strings.Join(logRecords(o.Stderr), "\n"); gr != or || g.Code != o.Code {
		t.Errorf("%s: stderr records differ (go %d, oracle %d)\n%s", strings.Join(args, " "), g.Code, o.Code, testkit.Diff(or, gr))
	}
	return g, o
}

// compareDoctorHuman compares exit code + masked stdout and the stderr log
// records of one run.
func compareDoctorHuman(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	g, o := p.Run(args...)
	if g.Code != o.Code {
		t.Errorf("%s: exit go=%d oracle=%d", strings.Join(args, " "), g.Code, o.Code)
	}
	if gs, os_ := maskDur(p.Go.Normalize(g.Stdout)), maskDur(p.Oracle.Normalize(o.Stdout)); gs != os_ {
		t.Errorf("%s: stdout differs\n%s", strings.Join(args, " "), testkit.Diff(os_, gs))
	}
	if gr, or := strings.Join(logRecords(p.Go.Normalize(g.Stderr)), "\n"), strings.Join(logRecords(p.Oracle.Normalize(o.Stderr)), "\n"); gr != or {
		t.Errorf("%s: stderr records differ\n%s", strings.Join(args, " "), testkit.Diff(or, gr))
	}
}

// TestParityDoctorCharterPacksWarnings covers the logger/warnings records
// on stderr (DOC-12): merge override/dangling warnings and the layer
// collision warning; --json is silent.
func TestParityDoctorCharterPacksWarnings(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		writeOrgPack(s, acmeFragment)
		appendConfig(s, orgPackConfig)
	})
	compareLogRecords(t, p, "doctor", "charter-packs")
	g, o := p.Run("doctor", "charter-packs", "--json")
	if g.Stderr != o.Stderr {
		t.Errorf("--json stderr: go %q oracle %q", g.Stderr, o.Stderr)
	}
	// PACK_LEVEL_MISSING (warnings registry reset by catch_warnings blocks).
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("orgpack/pack.yaml", "pack_id: 01KAAAAAAAAAAAAAAAAAAAAAAA\nname: acme\n")
	})
	compareDoctorHuman(t, p, "doctor", "charter-packs")
	compareDoctorHuman(t, p, "charter", "lint")
	// charter status org layer (DOC-03): artifact-derived nodes, augmentation
	// edges, dangling endpoints in errors.
	compareDoctorHuman(t, p, "charter", "status")
	compareCharter(t, p, "charter", "status", "--json")
}
