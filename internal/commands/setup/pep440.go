package setup

import (
	"regexp"
	"strconv"
	"strings"
)

// pepVersion is the subset of packaging.version.Version the upgrade system
// needs: epoch, release, pre (a/b/rc), post and dev segments.
type pepVersion struct {
	epoch   int
	release []int
	preKind int // 0=a 1=b 2=rc; 3 = none
	preNum  int
	post    int // -1 = none
	dev     int // -1 = none
}

var pepRe = regexp.MustCompile(`^\s*v?(?:(\d+)!)?(\d+(?:\.\d+)*)(?:[-_.]?(a|alpha|b|beta|c|rc|pre|preview)[-_.]?(\d*))?(?:-(\d+)|[-_.]?(post|rev|r)[-_.]?(\d*))?(?:[-_.]?(dev)[-_.]?(\d*))?(?:\+[a-z0-9]+(?:[-_.][a-z0-9]+)*)?\s*$`)

func parsePEP440(s string) (pepVersion, bool) {
	m := pepRe.FindStringSubmatch(strings.ToLower(s))
	if m == nil {
		return pepVersion{}, false
	}
	v := pepVersion{preKind: 3, post: -1, dev: -1}
	if m[1] != "" {
		v.epoch, _ = strconv.Atoi(m[1])
	}
	for _, p := range strings.Split(m[2], ".") {
		n, _ := strconv.Atoi(p)
		v.release = append(v.release, n)
	}
	switch m[3] {
	case "a", "alpha":
		v.preKind = 0
	case "b", "beta":
		v.preKind = 1
	case "c", "rc", "pre", "preview":
		v.preKind = 2
	}
	if m[3] != "" {
		v.preNum, _ = strconv.Atoi(m[4])
	}
	if m[5] != "" {
		v.post, _ = strconv.Atoi(m[5])
	} else if m[6] != "" {
		v.post, _ = strconv.Atoi(m[7])
	}
	if m[8] != "" {
		v.dev, _ = strconv.Atoi(m[9])
	}
	return v, true
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmp orders versions like packaging.version (dev-only releases sort
// before pre-releases; post after the release).
func (a pepVersion) cmp(b pepVersion) int {
	if c := cmpInt(a.epoch, b.epoch); c != 0 {
		return c
	}
	n := len(a.release)
	if len(b.release) > n {
		n = len(b.release)
	}
	for i := 0; i < n; i++ {
		x, y := 0, 0
		if i < len(a.release) {
			x = a.release[i]
		}
		if i < len(b.release) {
			y = b.release[i]
		}
		if c := cmpInt(x, y); c != 0 {
			return c
		}
	}
	key := func(v pepVersion) (int, int) {
		if v.preKind == 3 && v.post == -1 && v.dev != -1 {
			return -1, 0 // X.dev sorts before X.a0
		}
		return v.preKind, v.preNum
	}
	ak, an := key(a)
	bk, bn := key(b)
	if c := cmpInt(ak, bk); c != 0 {
		return c
	}
	if c := cmpInt(an, bn); c != 0 {
		return c
	}
	if c := cmpInt(a.post, b.post); c != 0 {
		return c
	}
	ad, bd := a.dev, b.dev
	if ad == -1 {
		ad = 1 << 30
	}
	if bd == -1 {
		bd = 1 << 30
	}
	return cmpInt(ad, bd)
}

func (a pepVersion) major() int {
	if len(a.release) == 0 {
		return 0
	}
	return a.release[0]
}

// versionLess reports a < b for valid versions (invalid compare as false).
func versionCmp(a, b string) (int, bool) {
	x, ok1 := parsePEP440(a)
	y, ok2 := parsePEP440(b)
	if !ok1 || !ok2 {
		return 0, false
	}
	return x.cmp(y), true
}
