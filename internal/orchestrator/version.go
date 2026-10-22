package orchestrator

import (
	"math/big"
	"regexp"
	"strings"
)

// pep440 is packaging.version.VERSION_PATTERN (anchored, case-insensitive).
var pep440 = regexp.MustCompile(`(?i)^\s*v?` +
	`(?:(?:([0-9]+)!)?` + // epoch
	`([0-9]+(?:\.[0-9]+)*)` + // release
	`([-_.]?(alpha|a|beta|b|preview|pre|c|rc)[-_.]?([0-9]+)?)?` + // pre
	`((?:-([0-9]+))|(?:[-_.]?(post|rev|r)[-_.]?([0-9]+)?))?` + // post
	`([-_.]?(dev)[-_.]?([0-9]+)?)?` + // dev
	`)(?:\+([a-z0-9]+(?:[-_.][a-z0-9]+)*))?\s*$`)

// Version is a parsed PEP 440 version (packaging.version.Version).
type Version struct {
	epoch   *big.Int
	release []*big.Int
	pre     *preRel // nil = none
	post    *big.Int
	dev     *big.Int
	local   []string
}

type preRel struct {
	rank int // a=0 b=1 rc=2
	n    *big.Int
}

func num(s string) *big.Int {
	n := new(big.Int)
	if s == "" {
		return n
	}
	n.SetString(s, 10)
	return n
}

// ParseVersion parses s; ok=false is packaging's InvalidVersion.
func ParseVersion(s string) (*Version, bool) {
	m := pep440.FindStringSubmatch(s)
	if m == nil {
		return nil, false
	}
	v := &Version{epoch: num(m[1])}
	for _, p := range strings.Split(m[2], ".") {
		v.release = append(v.release, num(p))
	}
	if m[3] != "" {
		rank := 0
		switch strings.ToLower(m[4]) {
		case "b", "beta":
			rank = 1
		case "c", "rc", "pre", "preview":
			rank = 2
		}
		v.pre = &preRel{rank: rank, n: num(m[5])}
	}
	if m[6] != "" {
		if m[7] != "" {
			v.post = num(m[7])
		} else {
			v.post = num(m[9])
		}
	}
	if m[10] != "" {
		v.dev = num(m[12])
	}
	if m[13] != "" {
		v.local = strings.FieldsFunc(strings.ToLower(m[13]), func(r rune) bool { return r == '-' || r == '_' || r == '.' })
	}
	return v, true
}

// Less reports v < o under PEP 440 ordering.
func (v *Version) Less(o *Version) bool { return v.cmp(o) < 0 }

func (v *Version) cmp(o *Version) int {
	if c := v.epoch.Cmp(o.epoch); c != 0 {
		return c
	}
	a, b := trimZeros(v.release), trimZeros(o.release)
	for i := 0; i < len(a) || i < len(b); i++ {
		if i >= len(a) {
			return -1
		}
		if i >= len(b) {
			return 1
		}
		if c := a[i].Cmp(b[i]); c != 0 {
			return c
		}
	}
	if c := cmpPre(v, o); c != 0 {
		return c
	}
	if c := cmpOpt(v.post, o.post, -1); c != 0 {
		return c
	}
	if c := cmpOpt(v.dev, o.dev, 1); c != 0 {
		return c
	}
	return cmpLocal(v.local, o.local)
}

func trimZeros(r []*big.Int) []*big.Int {
	n := len(r)
	for n > 0 && r[n-1].Sign() == 0 {
		n--
	}
	return r[:n]
}

// preKey: -2 = dev-only (sorts first), +2 = no pre (sorts last).
func preKey(v *Version) (int, int, *big.Int) {
	if v.pre == nil && v.post == nil && v.dev != nil {
		return -1, 0, nil
	}
	if v.pre == nil {
		return 1, 0, nil
	}
	return 0, v.pre.rank, v.pre.n
}

func cmpPre(v, o *Version) int {
	ac, ar, an := preKey(v)
	bc, br, bn := preKey(o)
	if ac != bc {
		return sign(ac - bc)
	}
	if ac != 0 {
		return 0
	}
	if ar != br {
		return sign(ar - br)
	}
	return an.Cmp(bn)
}

// cmpOpt compares optional numbers; missing sorts as missingSign*infinity.
func cmpOpt(a, b *big.Int, missingSign int) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return missingSign
	case b == nil:
		return -missingSign
	}
	return a.Cmp(b)
}

func cmpLocal(a, b []string) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	for i := 0; i < len(a) || i < len(b); i++ {
		if i >= len(a) {
			return -1
		}
		if i >= len(b) {
			return 1
		}
		an, aNum := isNum(a[i])
		bn, bNum := isNum(b[i])
		switch {
		case aNum && bNum:
			if c := an.Cmp(bn); c != 0 {
				return c
			}
		case aNum:
			return 1
		case bNum:
			return -1
		default:
			if c := strings.Compare(a[i], b[i]); c != 0 {
				return c
			}
		}
	}
	return 0
}

func isNum(s string) (*big.Int, bool) {
	for _, r := range s {
		if r < '0' || r > '9' {
			return nil, false
		}
	}
	return num(s), true
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
