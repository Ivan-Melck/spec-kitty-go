package tracker_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// lastLine returns the final non-empty line (the exception line of a
// Python traceback).
func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// step runs args on both sides and compares exit code, stdout and stderr.
// Tracebacks (uncaught exceptions) are compared by their final line only.
func step(t *testing.T, p *testkit.Pair, args ...string) (testkit.Result, testkit.Result) {
	t.Helper()
	g, o := p.Run(args...)
	name := strings.Join(args, " ")
	if g.Code != o.Code {
		t.Errorf("%s: exit go=%d oracle=%d\n--- go stderr:\n%s\n--- oracle stderr:\n%s", name, g.Code, o.Code, g.Stderr, o.Stderr)
	}
	if gs, os_ := p.Go.Normalize(g.Stdout), p.Oracle.Normalize(o.Stdout); gs != os_ {
		t.Errorf("%s: stdout differs\n%s", name, testkit.Diff(os_, gs))
	}
	ge, oe := p.Go.Normalize(g.Stderr), p.Oracle.Normalize(o.Stderr)
	if strings.Contains(oe, "Traceback (most recent call last)") {
		ge, oe = lastLine(ge), lastLine(oe)
	}
	if ge != oe {
		t.Errorf("%s: stderr differs\n%s", name, testkit.Diff(oe, ge))
	}
	return g, o
}

func mustContain(t *testing.T, s, want string) {
	t.Helper()
	if !strings.Contains(s, want) {
		t.Errorf("output lacks %q:\n%s", want, s)
	}
}

func compareFile(t *testing.T, p *testkit.Pair, abs func(s *testkit.Sandbox) string) {
	t.Helper()
	gb, gerr := os.ReadFile(abs(p.Go))
	ob, oerr := os.ReadFile(abs(p.Oracle))
	if (gerr == nil) != (oerr == nil) {
		t.Errorf("%s: existence differs (go err=%v, oracle err=%v)", abs(p.Go), gerr, oerr)
		return
	}
	if g, o := p.Go.Normalize(string(gb)), p.Oracle.Normalize(string(ob)); g != o {
		t.Errorf("%s differs:\n%s", filepath.Base(abs(p.Go)), testkit.Diff(o, g))
	}
}

func configYAML(s *testkit.Sandbox) string { return filepath.Join(s.Dir, ".kittify", "config.yaml") }
func credentials(s *testkit.Sandbox) string {
	return filepath.Join(s.Home, ".spec-kitty", "credentials")
}

// newTrackerPair returns two oracle-initialised projects.
func newTrackerPair(t *testing.T) *testkit.Pair {
	t.Helper()
	return missionfx.NewOraclePair(t)
}

// setTracker appends a raw tracker block to both projects' config.yaml.
func setTracker(t *testing.T, p *testkit.Pair, block string) {
	t.Helper()
	p.Both(func(s *testkit.Sandbox) {
		base := s.ReadFile(".kittify/config.yaml")
		if i := strings.Index(base, "\ntracker:"); i >= 0 {
			base = base[:i+1]
		}
		s.WriteFile(".kittify/config.yaml", base+block)
	})
}

func TestOracleEgressVerdicts(t *testing.T) {
	t.Parallel()
	p := newTrackerPair(t)
	for _, raw := range []string{"refused", `"refused"`, "Refused", "", "null", "5", "true", "[a, b]", "{a: 1}", "'refuse'", "1.5"} {
		setTracker(t, p, "tracker:\n  provider: beads\n  workspace: ws\n  egress: "+raw+"\n")
		step(t, p, "tracker", "sync", "pull")
		step(t, p, "tracker", "sync", "push", "--json")
		step(t, p, "tracker", "sync", "run")
	}
	// Bind keeps the recorded decision (with its quoting) and unknown keys.
	setTracker(t, p, "tracker:\n  custom: 1\n  provider: fp\n  binding_ref: br\n  provider_context:\n    a: 1\n  workspace: old\n  egress: \"refused\"\n  ownership:\n    mode: split_ownership\n    field_owners:\n      title: local\n")
	step(t, p, "tracker", "status", "--json")
	step(t, p, "tracker", "bind", "--provider", "beads", "--workspace", "new")
	compareFile(t, p, configYAML)
	step(t, p, "tracker", "unbind")
	compareFile(t, p, configYAML)
	step(t, p, "tracker", "status")
	step(t, p, "tracker", "sync", "pull")
	step(t, p, "tracker", "bind", "--provider", "fp", "--workspace", "again")
	compareFile(t, p, configYAML)
}

func TestOracleRemovedAndIncompleteBindings(t *testing.T) {
	t.Parallel()
	p := newTrackerPair(t)
	cmds := [][]string{
		{"tracker", "status"}, {"tracker", "status", "--json"}, {"tracker", "map", "list", "--json"},
		{"tracker", "map", "add", "--wp-id", "WP01", "--external-id", "1"},
		{"tracker", "sync", "pull"}, {"tracker", "sync", "publish", "--json"},
	}
	for _, block := range []string{
		"tracker:\n  provider: linear\n  workspace: team\n",
		"tracker:\n  provider: weird\n",
		"tracker:\n  provider: beads\n",
		"tracker:\n  provider: 5\n  workspace: w\n",
		"tracker: [1, 2]\n",
		"tracker:\n  provider: '  FP  '\n  workspace: ' w '\n  ownership: nope\n",
	} {
		setTracker(t, p, block)
		for _, c := range cmds {
			step(t, p, c...)
		}
		step(t, p, "tracker", "unbind")
		compareFile(t, p, configYAML)
	}
}

func TestOracleProvidersAndHelp(t *testing.T) {
	t.Parallel()
	p := testkit.NewPair(t)
	for _, args := range [][]string{
		{"tracker", "providers"}, {"tracker", "providers", "--json"},
		{"tracker", "sync", "pull", "--limit", "0"}, {"tracker", "bind"},
		{"tracker", "sync", "push", "--limit", "abc"}, {"tracker", "sync", "run", "--limit", "10001"},
		{"tracker", "sync", "run", "--limit", "-5"},
	} {
		step(t, p, args...)
	}
	// Help layout belongs to the shared renderer (area M); compare content
	// with whitespace runs collapsed.
	for _, path := range []string{"", "bind", "unbind", "status", "providers", "map", "map add", "map list", "sync", "sync publish", "sync pull", "sync push", "sync run"} {
		for _, args := range [][]string{append(append([]string{"tracker"}, strings.Fields(path)...), "--help")} {
			if path == "" {
				args = args[:len(args)-1] // bare `tracker` prints help too
			}
			helpCompare(t, p, args...)
		}
	}
	helpCompare(t, p, "tracker", "nope")
	helpCompare(t, p, "tracker", "map")
	helpCompare(t, p, "tracker", "sync")
}

func helpCompare(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	g, o := p.Run(args...)
	if g.Code != o.Code || helpNorm(g.Stdout) != helpNorm(o.Stdout) || helpNorm(g.Stderr) != helpNorm(o.Stderr) {
		t.Errorf("%v: help differs (exit go=%d oracle=%d)\nstdout:\n%s\nstderr:\n%s", args, g.Code, o.Code,
			testkit.Diff(helpNorm(o.Stdout), helpNorm(g.Stdout)), testkit.Diff(helpNorm(o.Stderr), helpNorm(g.Stderr)))
	}
}

// helpNorm collapses layout (column widths and padding belong to the shared
// renderer) while keeping every word of the help content.
func helpNorm(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.Join(strings.Fields(strings.Trim(l, "│╭╮╰╯─ ")), " ")
		if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func TestOracleOutsideProject(t *testing.T) {
	t.Parallel()
	testkit.Oracle(t)
	p := &testkit.Pair{T: t, Go: testkit.NewSandbox(t), Oracle: testkit.NewSandbox(t)}
	for _, args := range [][]string{
		{"tracker", "status"}, {"tracker", "status", "--json"}, {"tracker", "map", "list", "--json"},
		{"tracker", "unbind"}, {"tracker", "bind", "--provider", "beads", "--workspace", "w"},
		{"tracker", "sync", "pull"}, {"tracker", "sync", "publish", "--json"},
	} {
		step(t, p, args...)
	}
}

func TestOracleUnboundAndBindErrors(t *testing.T) {
	t.Parallel()
	p := newTrackerPair(t)
	for _, args := range [][]string{
		{"tracker", "status"}, {"tracker", "status", "--json"},
		{"tracker", "map", "list"}, {"tracker", "map", "list", "--json"},
		{"tracker", "map", "add", "--wp-id", "WP01", "--external-id", "1"},
		{"tracker", "unbind"},
		{"tracker", "sync", "pull"}, {"tracker", "sync", "push", "--json"}, {"tracker", "sync", "run"},
		{"tracker", "sync", "publish"}, {"tracker", "sync", "publish", "--json"},
		{"tracker", "bind", "--provider", "linear"},
		{"tracker", "bind", "--provider", " Jira ", "--workspace", "x"},
		{"tracker", "bind", "--provider", "azure_devops"},
		{"tracker", "bind", "--provider", "foo"},
		{"tracker", "bind", "--provider", "beads"},
		{"tracker", "bind", "--provider", "fp", "--workspace", "w", "--ownership-mode", "Bogus"},
		{"tracker", "bind", "--provider", "fp", "--workspace", "w", "--credential", "nope"},
		{"tracker", "bind", "--provider", "fp", "--workspace", "w", "--field-owner", "a="},
		{"tracker", "bind", "--provider", "fp", "--workspace", "w", "--credential", "=v"},
	} {
		step(t, p, args...)
	}
	compareFile(t, p, configYAML)
}

func TestOracleBindStatusMapUnbind(t *testing.T) {
	t.Parallel()
	p := newTrackerPair(t)
	steps := [][]string{
		{"tracker", "bind", "--provider", "Beads", "--workspace", "ws", "--ownership-mode", " Split_Ownership ",
			"--field-owner", "title=local", "--field-owner", " status = external ", "--credential", "command=bd", "--credential", "actor=me"},
		{"tracker", "status"}, {"tracker", "status", "--json"},
		{"tracker", "map", "list"}, {"tracker", "map", "list", "--json"},
		{"tracker", "map", "add", "--wp-id", "WP02", "--external-id", "12", "--external-key", "K-12", "--external-url", "http://x/12"},
		{"tracker", "map", "add", "--wp-id", "WP01", "--external-id", "13"},
		{"tracker", "map", "add", "--wp-id", "WP03", "--external-id", "13"},
		{"tracker", "map", "add", "--wp-id", "WP01", "--external-id", "14", "--external-key", ""},
		{"tracker", "map", "add", "--wp-id", "WP04", "--external-id", "  "},
		{"tracker", "map", "list"}, {"tracker", "map", "list", "--json"},
		{"tracker", "status", "--json"},
		{"tracker", "sync", "pull"}, {"tracker", "sync", "pull", "--json"},
		{"tracker", "sync", "push"}, {"tracker", "sync", "run", "--json", "--limit", "5"},
		{"tracker", "sync", "publish"}, {"tracker", "sync", "publish", "--json"},
	}
	for _, args := range steps {
		step(t, p, args...)
	}
	compareFile(t, p, configYAML)
	compareFile(t, p, credentials)
	// Rebind fp without credentials keeps beads credentials.
	step(t, p, "tracker", "bind", "--provider", "fp", "--workspace", "other", "--credential", "cwd=/tmp")
	compareFile(t, p, credentials)
	step(t, p, "tracker", "status", "--json")
	step(t, p, "tracker", "unbind")
	compareFile(t, p, configYAML)
	compareFile(t, p, credentials)
	step(t, p, "tracker", "status")
	step(t, p, "tracker", "unbind")
}
