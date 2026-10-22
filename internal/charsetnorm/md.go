package charsetnorm

// Port of charset_normalizer/md.py (mess detection).

type spPlugin struct {
	punctuation, symbol, count int
	last                       rune
	hasLast                    bool
}

func (d *spPlugin) feed(c rune, ci *charInfo) {
	d.count++
	if (!d.hasLast || c != d.last) && !ci.safe {
		if ci.punct {
			d.punctuation++
		} else if !ci.digit && ci.sym && !ci.emoticon {
			d.symbol += 2
		}
	}
	d.last, d.hasLast = c, true
}

func (d *spPlugin) ratio() float64 {
	if d.count == 0 {
		return 0
	}
	r := float64(d.punctuation+d.symbol) / float64(d.count)
	if r >= 0.3 {
		return r
	}
	return 0
}

type taPlugin struct{ count, accentuated int }

func (d *taPlugin) feed(c rune, ci *charInfo) {
	d.count++
	if ci.accentuated {
		d.accentuated++
	}
}

func (d *taPlugin) ratio() float64 {
	if d.count < 8 {
		return 0
	}
	r := float64(d.accentuated) / float64(d.count)
	if r >= 0.35 {
		return r
	}
	return 0
}

type upPlugin struct {
	unprintable, count int
	hasEscape          bool
}

func (d *upPlugin) feed(c rune, ci *charInfo) {
	if c == 0x1b {
		d.hasEscape = true
	}
	if !ci.printable && !ci.space && c != 0x1a && c != 0xfeff {
		d.unprintable++
	}
	d.count++
}

func (d *upPlugin) ratio() float64 {
	if d.count == 0 {
		return 0
	}
	if d.hasEscape {
		return 1
	}
	return float64(d.unprintable*8) / float64(d.count)
}

type sdaPlugin struct {
	successive, count int
	last              *charInfo
	lastAccentuated   bool
}

func (d *sdaPlugin) feed(c rune, ci *charInfo) {
	d.count++
	if d.last != nil && ci.accentuated && d.lastAccentuated {
		if ci.upper && d.last.upper {
			d.successive++
		}
		if ci.unaccented == d.last.unaccented {
			d.successive++
		}
	}
	d.last = ci
	d.lastAccentuated = ci.accentuated
}

func (d *sdaPlugin) ratio() float64 {
	if d.count == 0 {
		return 0
	}
	return float64(d.successive*2) / float64(d.count)
}

type srPlugin struct {
	suspicious, count int
	hasLastSeen       bool
	lastRange         string
	lastHasRange      bool
}

func (d *srPlugin) feed(c rune, ci *charInfo) {
	d.count++
	if ci.space || ci.punct || ci.safe {
		d.hasLastSeen = false
		d.lastRange, d.lastHasRange = "", false
		return
	}
	if !d.hasLastSeen {
		d.hasLastSeen = true
		d.lastRange, d.lastHasRange = ci.rng, ci.hasRange
		return
	}
	if d.lastRange != ci.rng || d.lastHasRange != ci.hasRange || !d.lastHasRange {
		if isSuspiciouslySuccessiveRange(d.lastRange, d.lastHasRange, ci.rng, ci.hasRange) {
			d.suspicious++
		}
	}
	d.lastRange, d.lastHasRange = ci.rng, ci.hasRange
}

func (d *srPlugin) ratio() float64 {
	if d.count <= 13 {
		return 0
	}
	return float64(d.suspicious*2) / float64(d.count)
}

type swPlugin struct {
	wordCount, badWordCount, foreignLongCount int
	isCurrentWordBad, foreignLongWatch        bool
	characterCount, badCharacterCount         int
	bufLen                                    int
	bufLast                                   rune
	bufLastAccentuated                        bool
	bufAccent, bufGlyph, bufUpper             int
	bufFirstLower, bufNonASCII                bool
	bufLastLigature, bufInternalLigature      bool
	isCurrentWordInvalid                      bool
	invalidWordCount                          int
}

func (d *swPlugin) feed(c rune, ci *charInfo) {
	if ci.alpha {
		if d.bufLastLigature {
			d.bufInternalLigature = true
		}
		d.bufLastLigature = ci.isLigature
		if d.bufLen == 0 {
			d.bufFirstLower = ci.lower
		}
		d.bufLen++
		d.bufLast = c
		if ci.upper {
			d.bufUpper++
		}
		if !ci.isASCII {
			d.bufNonASCII = true
		}
		d.bufLastAccentuated = ci.accentuated
		if ci.accentuated {
			d.bufAccent++
		}
		if ci.isGlyph {
			d.bufGlyph++
		} else if !d.foreignLongWatch && (!ci.latin || ci.accentuated) {
			d.foreignLongWatch = true
		}
		return
	}
	if d.bufLen == 0 {
		return
	}
	if ci.isSentenceOpenPunct || (ci.isSuperscript && d.bufInternalLigature) {
		d.isCurrentWordBad = true
		d.isCurrentWordInvalid = true
	}
	if ci.space || ci.punct || ci.sep {
		d.wordCount++
		n := d.bufLen
		d.characterCount += n
		if n >= 4 {
			switch {
			case float64(d.bufAccent)/float64(n) >= 0.5:
				d.isCurrentWordBad = true
			case d.bufLastAccentuated && getInfo(d.bufLast).upper && d.bufUpper != n:
				d.foreignLongCount++
				d.isCurrentWordBad = true
			case d.bufGlyph == 1:
				d.isCurrentWordBad = true
				d.foreignLongCount++
			case d.bufNonASCII && d.bufFirstLower && d.bufUpper == n-1:
				d.foreignLongCount++
				d.isCurrentWordBad = true
			}
		}
		if n >= 24 && d.foreignLongWatch {
			camel := d.bufUpper > 0 && float64(d.bufUpper)/float64(n) <= 0.3
			if !camel {
				d.foreignLongCount++
				d.isCurrentWordBad = true
			}
		}
		if d.isCurrentWordBad {
			d.badWordCount++
			d.badCharacterCount += n
			d.isCurrentWordBad = false
		}
		if d.isCurrentWordInvalid {
			d.invalidWordCount++
			d.isCurrentWordInvalid = false
		}
		d.foreignLongWatch = false
		d.bufLen = 0
		d.bufLast = 0
		d.bufLastAccentuated = false
		d.bufAccent, d.bufGlyph, d.bufUpper = 0, 0, 0
		d.bufFirstLower, d.bufNonASCII = false, false
		d.bufLastLigature, d.bufInternalLigature = false, false
	} else if c != '<' && c != '>' && c != '-' && c != '=' && c != '~' && c != '|' && c != '_' && !ci.digit && ci.sym {
		d.isCurrentWordBad = true
		d.bufLen++
		d.bufLast = c
		d.bufLastAccentuated = false
	}
}

func (d *swPlugin) ratio() float64 {
	if d.invalidWordCount > 0 {
		return 1
	}
	if d.wordCount <= 10 && d.foreignLongCount == 0 {
		return 0
	}
	return float64(d.badCharacterCount) / float64(d.characterCount)
}

type cuPlugin struct{ count, uncommon int }

func (d *cuPlugin) feed(c rune, ci *charInfo) {
	d.count++
	if !ci.commonCJK {
		d.uncommon++
	}
}

func (d *cuPlugin) ratio() float64 {
	if d.count < 4 {
		return 0
	}
	m := d.count
	if m < 16 {
		m = 16
	}
	u := float64(2*d.uncommon-d.count) / float64(5*m)
	if u < 0 {
		return 0
	}
	return u
}

type skPlugin struct{ katakana, halfwidth, cjk, uncommonCJK int }

func (d *skPlugin) feed(c rune, ci *charInfo) {
	if ci.isKatakana {
		d.katakana++
		if ci.isHalfwidthKatakana {
			d.halfwidth++
		}
		return
	}
	d.cjk++
	if !ci.commonCJK {
		d.uncommonCJK++
	}
}

func (d *skPlugin) ratio() float64 {
	if d.halfwidth >= 4 && d.halfwidth == d.katakana && 3 <= d.cjk && d.cjk == d.uncommonCJK {
		return 1
	}
	return 0
}

type auPlugin struct {
	buf                         bool
	sinceSep                    int
	successive, successiveFinal int
	count                       int
	lastUpper, lastLower        bool
	currentASCIIOnly            bool
}

func (d *auPlugin) feed(c rune, ci *charInfo) {
	concerned := ci.alpha && ci.caseVariable
	if !concerned && d.sinceSep > 0 {
		if d.sinceSep <= 64 && !ci.digit && !d.currentASCIIOnly {
			d.successiveFinal += d.successive
		}
		d.successive = 0
		d.sinceSep = 0
		d.buf = false
		d.count++
		d.currentASCIIOnly = true
		return
	}
	if d.currentASCIIOnly && !ci.isASCII {
		d.currentASCIIOnly = false
	}
	if d.sinceSep > 0 {
		if (ci.upper && d.lastLower) || (ci.lower && d.lastUpper) {
			if d.buf {
				d.successive += 2
				d.buf = false
			} else {
				d.buf = true
			}
		} else {
			d.buf = false
		}
	}
	d.count++
	d.sinceSep++
	d.lastUpper = ci.upper
	d.lastLower = ci.lower
}

func (d *auPlugin) ratio() float64 {
	if d.count == 0 {
		return 0
	}
	return float64(d.successiveFinal) / float64(d.count)
}

type aiPlugin struct{ count, isolated int }

func (d *aiPlugin) feed(c rune, ci *charInfo) {
	d.count++
	if ci.flags&fArabicIsolatedForm != 0 {
		d.isolated++
	}
}

func (d *aiPlugin) ratio() float64 {
	if d.count < 8 {
		return 0
	}
	return float64(d.isolated) / float64(d.count)
}

type familyPair struct{ a, b string }

var compatibleRangeFamilies = func() map[familyPair]bool {
	m := map[familyPair]bool{}
	for _, p := range [][2]string{
		{"CJK", "Hiragana"}, {"CJK", "Katakana"}, {"CJK", "Kana"}, {"CJK", "Bopomofo"}, {"CJK", "Kanbun"},
		{"Hiragana", "Katakana"}, {"Hiragana", "Kana"}, {"Katakana", "Kana"}, {"CJK", "Hangul"},
		{"Latin", "IPA"}, {"Latin", "Phonetic"}, {"IPA", "Phonetic"},
		{"Latin", "Spacing Modifier Letters"}, {"IPA", "Spacing Modifier Letters"}, {"Phonetic", "Spacing Modifier Letters"},
		{"Alphabetic Presentation Forms", "Latin"}, {"Alphabetic Presentation Forms", "Armenian"}, {"Alphabetic Presentation Forms", "Hebrew"},
		{"Halfwidth and Fullwidth Forms", "Latin"}, {"Halfwidth and Fullwidth Forms", "CJK"}, {"Halfwidth and Fullwidth Forms", "Hiragana"},
		{"Halfwidth and Fullwidth Forms", "Katakana"}, {"Halfwidth and Fullwidth Forms", "Kana"}, {"Halfwidth and Fullwidth Forms", "Hangul"},
	} {
		m[familyPair{p[0], p[1]}] = true
		m[familyPair{p[1], p[0]}] = true
	}
	return m
}()

var compatibleWithAnyFamilies = map[string]bool{"Combining": true, "Variation Selectors": true, "Emoticons": true, "Pictographs": true}

var basicLatinCompatibleFamilies = map[string]bool{"CJK": true, "Hangul": true, "Hiragana": true, "Katakana": true, "Kana": true, "Bopomofo": true, "Kanbun": true}

// isSuspiciouslySuccessiveRange is md.is_suspiciously_successive_range.
func isSuspiciouslySuccessiveRange(a string, aOK bool, b string, bOK bool) bool {
	if !aOK || !bOK {
		return true
	}
	fa, fb := rangeFamilies[a], rangeFamilies[b]
	if fa == fb {
		return false
	}
	if compatibleWithAnyFamilies[fa] || compatibleWithAnyFamilies[fb] {
		return false
	}
	if compatibleRangeFamilies[familyPair{fa, fb}] {
		return false
	}
	if a == "Basic Latin" {
		return !basicLatinCompatibleFamilies[fb]
	}
	if b == "Basic Latin" {
		return !basicLatinCompatibleFamilies[fa]
	}
	return true
}

func isASCIIRunes(s []rune) bool {
	for _, c := range s {
		if c >= 128 {
			return false
		}
	}
	return true
}

// messRatio is md.mess_ratio.
func messRatio(seq []rune, maximumThreshold float64) float64 {
	n := len(seq)
	step := 128
	if n < 511 {
		step = 32
	} else if n < 1024 {
		step = 64
	}
	pureASCII := isASCIIRunes(seq)
	var (
		sp  spPlugin
		ta  taPlugin
		up  upPlugin
		sda sdaPlugin
		sr  srPlugin
		sw  swPlugin
		cu  cuPlugin
		sk  skPlugin
		au  = auPlugin{currentASCIIOnly: true}
		ai  aiPlugin
	)
	total := func() float64 {
		return sp.ratio() + ta.ratio() + up.ratio() + sda.ratio() + sr.ratio() + sw.ratio() + cu.ratio() + sk.ratio() + au.ratio() + ai.ratio()
	}
	var mean float64
	broke := false
	for start := 0; start < n; start += step {
		end := start + step
		if end > n {
			end = n
		}
		for _, c := range seq[start:end] {
			ci := getInfo(c)
			up.feed(c, ci)
			sw.feed(c, ci)
			if pureASCII {
				if ci.printable {
					sp.feed(c, ci)
				}
				continue
			}
			au.feed(c, ci)
			if ci.printable {
				sp.feed(c, ci)
				sr.feed(c, ci)
			}
			if ci.alpha {
				ta.feed(c, ci)
				if ci.latin {
					sda.feed(c, ci)
				}
				if ci.isCJK {
					cu.feed(c, ci)
					sk.feed(c, ci)
				} else if ci.isKatakana {
					sk.feed(c, ci)
				}
				if ci.isArabic {
					ai.feed(c, ci)
				}
			}
		}
		mean = total()
		if mean >= maximumThreshold {
			broke = true
			break
		}
	}
	if !broke {
		nl := getInfo('\n')
		sw.feed('\n', nl)
		if !pureASCII {
			au.feed('\n', nl)
		}
		up.feed('\n', nl)
		mean = total()
	}
	return pyRound(mean, 3)
}
