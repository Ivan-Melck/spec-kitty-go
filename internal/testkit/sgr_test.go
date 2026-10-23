package testkit

import "testing"

// Colour-run masking: values split into styled runs by rich's highlighter.
func TestNormalizeColourRuns(t *testing.T) {
	for _, c := range [][2]string{
		{"last_change=\x1b[1;36m2026\x1b[0m-\x1b[1;36m10\x1b[0m-23T\x1b[1;92m00:58:28\x1b[0m.\x1b[1;36m012916\x1b[0m+\x1b[1;92m00:00\x1b[0m)", "last_change=\x1b[1;36\x01<TS>\x1b[0\x01)"},
		{"in \x1b[1;36m4.\x1b[0m82s", "in \x1b[1;36\x01<DUR>"},
		{"\x1b[2m01M5K4BVDYPNQ8ZK3H2W5C7E9F\x1b[0m", "\x1b[2\x01<ULID>\x1b[0\x01"},
		{"committed \x1b[1;36m4934249\x1b[0m —", "committed <SHORTSHA> —"},
		{"plain 2026-10-23T00:58:28Z", "plain <TS>"},
	} {
		if got := NormalizeValues(c[0]); got != c[1] {
			t.Errorf("%q: got %q want %q", c[0], got, c[1])
		}
	}
}
