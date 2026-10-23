package closeout_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffWPMetadataValidation covers WP frontmatter that parses but fails
// WPMetadata (a non-string list item, a recursive alias that ruamel builds
// as None, an unknown key) or does not parse at all: `next` reports the unreadable canonical task,
// `agent action implement` refuses with the ValidationError text and the
// status board leaves the row's workspace unclassified.
func TestDiffWPMetadataValidation(t *testing.T) {
	cases := map[string]string{
		"nonstring": "dependencies: [WP01, 5]\n",
		"recursive": "requirement_refs: &cyc [x, *cyc]\n",
		"extra":     "bogus_field: 1\n",
		"badyaml":   "bad_yaml: [unclosed\n",
	}
	for name, line := range cases {
		name, line := name, line
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := missionfx.NewOraclePair(t)
			missionfx.OracleCalcMission(t, p)
			field := strings.SplitN(line, ":", 2)[0]
			p.Both(func(s *testkit.Sandbox) {
				m, _ := filepath.Glob(filepath.Join(s.Dir, "kitty-specs", "*", "tasks", "WP02-*.md"))
				b, _ := os.ReadFile(m[0])
				var out []string
				skip := false
				for i, l := range strings.Split(string(b), "\n") {
					if i > 0 && strings.HasPrefix(l, field+":") {
						skip = true
						continue
					}
					if skip && (strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "  ")) {
						continue
					}
					skip = false
					out = append(out, l)
				}
				txt := strings.Replace(strings.Join(out, "\n"), "---\n", "---\n"+line, 1)
				os.WriteFile(m[0], []byte(txt), 0o644)
			})
			for _, args := range [][]string{
				{"agent", "tasks", "status", "--mission", "$M", "--json"},
				{"agent", "tasks", "status", "--mission", "$M"},
				{"next", "--mission", "$M", "--json"},
				{"agent", "action", "implement", "WP02", "--agent", "claude", "--mission", "$M"},
			} {
				comparePairTB(t, p, args...)
			}
		})
	}
}
