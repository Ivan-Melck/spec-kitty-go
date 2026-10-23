package setup

import "github.com/spec-kitty/go-kitty3/internal/pep440"

type pepVersion = pep440.Version

func parsePEP440(text string) (pepVersion, bool) { return pep440.Parse(text) }
func versionCmp(a, b string) (int, bool) {
	left, ok := pep440.Parse(a)
	right, other := pep440.Parse(b)
	if !ok || !other {
		return 0, false
	}
	return left.Compare(right), true
}
