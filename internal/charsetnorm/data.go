package charsetnorm

import (
	"bytes"
	"compress/zlib"
	"embed"
	"io"
	"sync"
)

// The data/*.bin tables are generated from the oracle venv's CPython 3.13 /
// charset_normalizer 3.5.2 by $REF/work-ENC/gen.py (zlib-compressed varint
// streams). See docs/parity/maintenance.md, "charset detection".
//
//go:embed data/*.bin
var dataFS embed.FS

func loadData(name string) []byte {
	raw, err := dataFS.ReadFile("data/" + name)
	if err != nil {
		panic("charsetnorm: missing table " + name)
	}
	zr, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		panic("charsetnorm: corrupt table " + name)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		panic("charsetnorm: corrupt table " + name)
	}
	return out
}

type varintReader struct {
	b []byte
	i int
}

func (r *varintReader) uvarint() uint64 {
	var x uint64
	var s uint
	for {
		c := r.b[r.i]
		r.i++
		x |= uint64(c&0x7f) << s
		if c < 0x80 {
			return x
		}
		s += 7
	}
}

func (r *varintReader) int() int { return int(r.uvarint()) }

func (r *varintReader) zigzag() int {
	u := r.uvarint()
	if u&1 == 0 {
		return int(u >> 1)
	}
	return -int((u + 1) >> 1)
}

func (r *varintReader) byte() byte {
	c := r.b[r.i]
	r.i++
	return c
}

func (r *varintReader) str() string {
	n := r.int()
	s := string(r.b[r.i : r.i+n])
	r.i += n
	return s
}

// ---------------------------------------------------------------------------
// Unicode properties (CharInfo inputs)
// ---------------------------------------------------------------------------

const (
	pPrintable = 1 << iota
	pAlpha
	pUpper
	pLower
	pSpace
	pDigit
	pPunct
	pSym
	pSep
	pEmoticon
	pCased
	pCaseIgnorable
)

const flagShift = 16

type propRun struct {
	start rune
	props uint32
}

var (
	uniOnce     sync.Once
	uniRuns     []propRun
	lowerMap    map[rune][]rune
	unaccentMap map[rune]rune
)

func loadUni() {
	r := &varintReader{b: loadData("uniprops.bin")}
	cp := 0
	for cp < 0x110000 {
		n := r.int()
		p := uint32(r.uvarint())
		uniRuns = append(uniRuns, propRun{rune(cp), p})
		cp += n
	}
	nl := r.int()
	lowerMap = make(map[rune][]rune, nl)
	last := 0
	for i := 0; i < nl; i++ {
		last += r.int()
		k := r.int()
		rs := make([]rune, k)
		for j := range rs {
			rs[j] = rune(last + r.zigzag())
		}
		lowerMap[rune(last)] = rs
	}
	nu := r.int()
	unaccentMap = make(map[rune]rune, nu)
	last = 0
	for i := 0; i < nu; i++ {
		last += r.int()
		unaccentMap[rune(last)] = rune(last + r.zigzag())
	}
}

func uniProps(c rune) uint32 {
	uniOnce.Do(loadUni)
	if c < 0 || c > 0x10ffff {
		return 0
	}
	lo, hi := 0, len(uniRuns)
	for hi-lo > 1 {
		m := (lo + hi) / 2
		if uniRuns[m].start <= c {
			lo = m
		} else {
			hi = m
		}
	}
	return uniRuns[lo].props
}

// pyLower is str.lower() (full case mapping plus the Final_Sigma rule).
func pyLower(s []rune) []rune {
	uniOnce.Do(loadUni)
	out := make([]rune, 0, len(s))
	for i, c := range s {
		if c == 0x3a3 {
			out = append(out, capitalSigma(s, i))
			continue
		}
		if m, ok := lowerMap[c]; ok {
			out = append(out, m...)
		} else {
			out = append(out, c)
		}
	}
	return out
}

// capitalSigma is CPython's handle_capital_sigma.
func capitalSigma(s []rune, i int) rune {
	j := i - 1
	var c rune
	for ; j >= 0; j-- {
		c = s[j]
		if uniProps(c)&pCaseIgnorable == 0 {
			break
		}
	}
	final := j >= 0 && uniProps(c)&pCased != 0
	if final && i+1 < len(s) {
		for j = i + 1; j < len(s); j++ {
			c = s[j]
			if uniProps(c)&pCaseIgnorable == 0 {
				break
			}
		}
		final = j == len(s) || uniProps(c)&pCased == 0
	}
	if final {
		return 0x3c2
	}
	return 0x3c3
}
