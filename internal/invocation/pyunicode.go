package invocation

import (
	"sort"
	"strings"
	"unicode"
)

// isWordRune is Python's re \w for str patterns (Unicode alphanumerics and
// "_"), from the table generated against the oracle's CPython.
func isWordRune(r rune) bool {
	i := sort.Search(len(pyWordRanges), func(i int) bool { return pyWordRanges[i][1] >= r })
	return i < len(pyWordRanges) && pyWordRanges[i][0] <= r
}

func isCased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r)
}

// isCaseIgnorable approximates Unicode Case_Ignorable (Mn, Me, Cf, Lm, Sk
// and the MidLetter/MidNumLet/Single_Quote word-break characters).
func isCaseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', '·', '·', '״', '‘', '’', '․', '︓', '﹒', '﹕', '＇', '．', '：', '‧':
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

// pyLower is str.lower(): the full Unicode lowercase mapping, with the
// Final_Sigma context rule CPython applies to U+03A3.
func pyLower(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		if r == 0x03A3 {
			b.WriteRune(finalSigma(rs, i))
			continue
		}
		if m, ok := pyLowerMap[r]; ok {
			b.WriteString(m)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func finalSigma(rs []rune, i int) rune {
	j := i - 1
	for j >= 0 && isCaseIgnorable(rs[j]) {
		j--
	}
	if j < 0 || !isCased(rs[j]) {
		return 0x03C3
	}
	k := i + 1
	for k < len(rs) && isCaseIgnorable(rs[k]) {
		k++
	}
	if k < len(rs) && isCased(rs[k]) {
		return 0x03C3
	}
	return 0x03C2
}
