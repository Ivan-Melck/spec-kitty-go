package doctrine_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestParityDispatchLanguageFilter covers the routing catalog's language
// scope filter (DOC-11): with interview answers naming python only, the
// language-scoped implementers (frontend-freddy, java-jenny, ...) drop out.
func TestParityDispatchLanguageFilter(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	compareMasked(t, p, true, "dispatch", "build a react frontend component with typescript", "--dry-run", "--json")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/charter/interview/answers.yaml", "schema_version: 1.0.0\nmission: software-dev\nprofile: minimal\nanswers:\n  languages: python\nselected_paradigms: []\nselected_directives: []\navailable_tools: []\n")
	})
	compareMasked(t, p, true, "dispatch", "build a react frontend component with typescript", "--dry-run", "--json")
	compareMasked(t, p, true, "dispatch", "build it", "--profile", "frontend-freddy", "--dry-run", "--json")
}
