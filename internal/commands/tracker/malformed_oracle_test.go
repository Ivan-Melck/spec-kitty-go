package tracker_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestOracleMalformedFiles: an unparseable .kittify/config.yaml reports
// ruamel's exact error ("Failed to parse <path>: ..."), and a malformed
// credentials file tomllib's ("Failed to load credentials: ...").
func TestOracleMalformedFiles(t *testing.T) {
	t.Parallel()
	p := newTrackerPair(t)
	for _, bad := range []string{
		"tracker: [\n",
		"tracker:\n  provider: beads\n  provider: fp\n",
		"tracker:\n  provider: \"beads\n",
		"tracker: {provider: beads, workspace: w}\r\nother: \x01\n",
		"tracker:\n  provider: \xff\n",
		"a: b: c\n",
	} {
		bad := bad
		p.Both(func(s *testkit.Sandbox) {
			os.WriteFile(filepath.Join(s.Dir, ".kittify", "config.yaml"), []byte(bad), 0o644)
		})
		step(t, p, "tracker", "status")
		step(t, p, "tracker", "status", "--json")
		step(t, p, "tracker", "bind", "--provider", "beads", "--workspace", "w")
	}
	// A valid binding, then broken credentials.
	p.Both(func(s *testkit.Sandbox) {
		os.WriteFile(filepath.Join(s.Dir, ".kittify", "config.yaml"), []byte("tracker:\n  provider: beads\n  workspace: w\n"), 0o644)
	})
	for _, bad := range []string{
		"[tracker\n",
		"a = 1\na = 2\n",
		"x = \"unterminated\n",
		"k = 1979-02-30\n",
		"bad = \xff\n",
		"v = [1, 2\n",
		"\r\n  \r\n",
	} {
		bad := bad
		p.Both(func(s *testkit.Sandbox) {
			os.MkdirAll(filepath.Dir(credentials(s)), 0o700)
			os.WriteFile(credentials(s), []byte(bad), 0o600)
		})
		step(t, p, "tracker", "status")
		step(t, p, "tracker", "bind", "--provider", "beads", "--workspace", "w", "--credential", "command=bd")
		step(t, p, "tracker", "sync", "pull", "--json")
	}
}
