package charsetnorm

import (
	"math"
	"sort"
	"strconv"
	"sync"
)

// Name-derived flags (constant.py _LATIN ... _SENTENCE_OPEN_PUNCTUATION).
const (
	fLatin = 1 << iota
	fAccentuated
	fCJK
	fHangul
	fKatakana
	fHiragana
	fThai
	fArabic
	fArabicIsolatedForm
	fHalfwidthKatakana
	fLigature
	fSuperscript
	fSentenceOpenPunctuation
)

const glyphMask = fCJK | fHangul | fKatakana | fHiragana | fThai

// charInfo is md.CharInfo: per-codepoint properties shared by detectors.
type charInfo struct {
	char                rune
	printable           bool
	alpha               bool
	upper               bool
	lower               bool
	space               bool
	digit               bool
	isASCII             bool
	caseVariable        bool
	flags               int
	accentuated         bool
	latin               bool
	isCJK               bool
	isKatakana          bool
	isHalfwidthKatakana bool
	isArabic            bool
	isLigature          bool
	isSuperscript       bool
	isSentenceOpenPunct bool
	isGlyph             bool
	punct               bool
	sym                 bool
	rng                 string
	hasRange            bool
	sep                 bool
	emoticon            bool
	safe                bool
	commonCJK           bool
	unaccented          rune
}

var commonSafeASCII = map[rune]bool{
	'<': true, '>': true, '=': true, ':': true, '/': true, '&': true, ';': true, '{': true, '}': true,
	'[': true, ']': true, ',': true, '|': true, '"': true, '-': true, '(': true, ')': true,
}

var (
	commonCJKOnce sync.Once
	commonCJKSet  map[rune]bool
)

func isCommonCJK(c rune) bool {
	commonCJKOnce.Do(func() {
		commonCJKSet = map[rune]bool{}
		for _, r := range commonCJKChars {
			commonCJKSet[r] = true
		}
	})
	return commonCJKSet[c]
}

var (
	sortedRangesOnce sync.Once
	sortedRanges     []uniRange
)

// unicodeRange is utils.unicode_range ("" , false when None).
func unicodeRange(c rune) (string, bool) {
	if c < 32 {
		return "Control character", true
	}
	if c < 128 {
		return "Basic Latin", true
	}
	sortedRangesOnce.Do(func() {
		sortedRanges = append([]uniRange(nil), unicodeRanges...)
		sort.Slice(sortedRanges, func(i, j int) bool {
			a, b := sortedRanges[i], sortedRanges[j]
			if a.start != b.start {
				return a.start < b.start
			}
			if a.stop != b.stop {
				return a.stop < b.stop
			}
			return a.name < b.name
		})
	})
	idx := sort.Search(len(sortedRanges), func(i int) bool { return sortedRanges[i].start > c }) - 1
	if idx >= 0 && c < sortedRanges[idx].stop {
		return sortedRanges[idx].name, true
	}
	return "", false
}

var (
	infoMu    sync.RWMutex
	infoCache = map[rune]*charInfo{}
)

func getInfo(c rune) *charInfo {
	infoMu.RLock()
	ci := infoCache[c]
	infoMu.RUnlock()
	if ci != nil {
		return ci
	}
	ci = buildInfo(c)
	infoMu.Lock()
	infoCache[c] = ci
	infoMu.Unlock()
	return ci
}

func buildInfo(c rune) *charInfo {
	p := uniProps(c)
	flags := int(p >> flagShift)
	ci := &charInfo{
		char:      c,
		printable: p&pPrintable != 0,
		alpha:     p&pAlpha != 0,
		upper:     p&pUpper != 0,
		lower:     p&pLower != 0,
		space:     p&pSpace != 0,
		digit:     p&pDigit != 0,
		punct:     p&pPunct != 0,
		sym:       p&pSym != 0,
		sep:       p&pSep != 0,
		emoticon:  p&pEmoticon != 0,
		flags:     flags,
		isASCII:   c < 128,
	}
	ci.caseVariable = ci.lower != ci.upper
	ci.accentuated = flags&fAccentuated != 0
	ci.latin = flags&fLatin != 0
	ci.isCJK = flags&fCJK != 0
	ci.isKatakana = flags&fKatakana != 0
	ci.isHalfwidthKatakana = flags&fHalfwidthKatakana != 0
	ci.isArabic = flags&fArabic != 0
	ci.isLigature = flags&fLigature != 0
	ci.isSuperscript = flags&fSuperscript != 0
	ci.isSentenceOpenPunct = flags&fSentenceOpenPunctuation != 0
	ci.isGlyph = flags&glyphMask != 0
	ci.unaccented = c
	if ci.latin && ci.accentuated {
		uniOnce.Do(loadUni)
		if u, ok := unaccentMap[c]; ok {
			ci.unaccented = u
		}
	}
	if c < 128 {
		ci.safe = commonSafeASCII[c]
	} else {
		ci.commonCJK = ci.isCJK && isCommonCJK(c)
	}
	ci.rng, ci.hasRange = unicodeRange(c)
	return ci
}

// pyRound is Python's round(x, ndigits) for floats (correctly rounded,
// ties to even on the exact binary value).
func pyRound(x float64, nd int) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	v, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', nd, 64), 64)
	return v
}

// pySum is builtins.sum over a list of floats with the default int start:
// 0 + xs[0], then Neumaier-compensated accumulation (CPython >= 3.12).
func pySum(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	f := 0 + xs[0]
	c := 0.0
	for _, x := range xs[1:] {
		t := f + x
		if math.Abs(f) >= math.Abs(x) {
			c += (f - t) + x
		} else {
			c += (x - t) + f
		}
		f = t
	}
	if c != 0 && !math.IsInf(c, 0) && !math.IsNaN(c) {
		f += c
	}
	return f
}
