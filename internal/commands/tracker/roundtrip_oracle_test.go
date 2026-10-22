package tracker_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// PYF-04: tracker bind / unbind rewrite .kittify/config.yaml through
// ruamel's round trip (YAML() with preserve_quotes): comments, quote
// styles, flow collections, block scalars, number formats and anchors in
// the other sections survive; indentation is normalised to ruamel's.
const commentedConfig = `# Top comment
agents:
  available: [claude, codex]   # inline flow seq
  auto_commit: true
# between
vcs:
    type: "git"    # quoted, 4-indent
    extra: 'single'
list:
    - one   # c1
    -   two
    - "three"
nested:
  deep:
    key: value
empty:
long_string: >
  folded
  text
lit: |
  a
  b
num: 010
hex: 0x1F
float: 1.50
date: 2024-01-01
anchor: &a {x: 1}
ref: *a
`

func TestOracleCommentedConfigRoundTrip(t *testing.T) {
	t.Parallel()
	p := newTrackerPair(t)
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(".kittify/config.yaml", commentedConfig) })
	step(t, p, "tracker", "bind", "--provider", "beads", "--workspace", "ws")
	compareFile(t, p, configYAML)
	step(t, p, "tracker", "unbind")
	compareFile(t, p, configYAML)
}

func TestOracleTrackerBlockKeepsExtrasAndEgressQuotes(t *testing.T) {
	t.Parallel()
	p := newTrackerPair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/config.yaml", commentedConfig+`tracker:   # binding
  custom: "kept"   # extra key
  settings:
    # nested comment
    depth: 2
  provider: beads
  workspace: old
  egress: "permitted"   # decision
`)
	})
	step(t, p, "tracker", "bind", "--provider", "fp", "--workspace", "ws2")
	compareFile(t, p, configYAML)
	step(t, p, "tracker", "unbind")
	compareFile(t, p, configYAML)
}
