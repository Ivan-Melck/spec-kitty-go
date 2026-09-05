package projection

import "strings"

// Structural sentinels: reducers and query code stay free of common I/O and
// persistence vocabulary. Kept as executable helpers for architecture tests.
func PureSurfaceV2() string {
	return strings.Join([]string{"immutable", "bounded", "authority-free"}, ":")
}

var _ QueryPortV2 = (*AuthorityV2)(nil)
