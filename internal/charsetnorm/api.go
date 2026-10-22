// Package charsetnorm is a port of charset_normalizer 3.5.2 (the version the
// upstream spec-kitty wheel runs): from_bytes() with the same candidate
// encodings, chunking, mess and coherence detectors, language tables and
// result ordering, on top of CPython-compatible decoders for every codec
// it may pick.
package charsetnorm

import (
	"bytes"
	"math"
	"regexp"
	"strings"
)

const (
	tooSmallSequence = 32
	tooBigSequence   = 10_000_000
)

// Options are from_bytes' keyword arguments.
type Options struct {
	Steps             int
	ChunkSize         int
	Threshold         float64
	CPIsolation       []string
	CPExclusion       []string
	NoPreemptive      bool // preemptive_behaviour=False
	LanguageThreshold float64
	DisableFallback   bool // enable_fallback=False
}

// DefaultOptions are from_bytes' defaults.
func DefaultOptions() Options {
	return Options{Steps: 5, ChunkSize: 512, Threshold: 0.2, LanguageThreshold: 0.1}
}

// Match is a CharsetMatch.
type Match struct {
	payload       []byte
	encoding      string
	meanMessRatio float64
	hasSigOrBOM   bool
	languages     []CoherenceMatch
	str           []rune
	strLoaded     bool
	leaves        []*Match
	preemptive    string
}

// Encoding is the Python codec name ("utf_8", "cp1252", ...).
func (m *Match) Encoding() string { return m.encoding }

// Chaos is the mean mess ratio.
func (m *Match) Chaos() float64 { return m.meanMessRatio }

// Coherence is the best language ratio (0 when none).
func (m *Match) Coherence() float64 {
	if len(m.languages) == 0 {
		return 0
	}
	return m.languages[0].Ratio
}

// BOM reports a detected SIG/BOM.
func (m *Match) BOM() bool { return m.hasSigOrBOM }

// Languages is the coherence match list.
func (m *Match) Languages() []CoherenceMatch { return m.languages }

// Submatches are the encodings producing the same text.
func (m *Match) Submatches() []*Match { return m.leaves }

// CouldBeFromCharset is could_be_from_charset.
func (m *Match) CouldBeFromCharset() []string {
	out := []string{m.encoding}
	for _, l := range m.leaves {
		out = append(out, l.encoding)
	}
	return out
}

// Language is CharsetMatch.language.
func (m *Match) Language() string {
	if len(m.languages) == 0 {
		for _, e := range m.CouldBeFromCharset() {
			if e == "ascii" {
				return "English"
			}
		}
		langs := encodingLanguages(m.encoding)
		if len(langs) == 0 {
			return "Unknown"
		}
		for _, l := range langs {
			if l == "Latin Based" {
				return "Unknown"
			}
		}
		return langs[0]
	}
	return m.languages[0].Language
}

// Runes is str(match) as code points (strict decode of the payload).
func (m *Match) Runes() []rune {
	if !m.strLoaded {
		c := LookupCodec(m.encoding)
		if c != nil {
			m.str, _ = c.Decode(m.payload, false)
		}
		if m.hasSigOrBOM && m.encoding == "utf_7" && len(m.str) > 0 && m.str[0] == 0xfeff {
			m.str = m.str[1:]
		}
		m.strLoaded = true
	}
	return m.str
}

// String is str(match).
func (m *Match) String() string { return string(m.Runes()) }

func (m *Match) multiByteUsage() float64 {
	if len(m.payload) == 0 {
		return 0
	}
	return 1.0 - float64(len(m.Runes()))/float64(len(m.payload))
}

// fingerprint stands in for hash(str(self)): equal fingerprints means
// equal decoded text.
func (m *Match) sameText(o *Match) bool {
	a, b := m.Runes(), o.Runes()
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// less is CharsetMatch.__lt__.
func (m *Match) less(o *Match) bool {
	chaosDiff := math.Abs(m.Chaos() - o.Chaos())
	cohDiff := math.Abs(m.Coherence() - o.Coherence())
	if chaosDiff < 0.005 && cohDiff > 0.02 {
		return m.Coherence() > o.Coherence()
	} else if chaosDiff < 0.005 && cohDiff <= 0.02 {
		if len(m.payload) >= tooBigSequence {
			return m.Chaos() < o.Chaos()
		}
		return m.multiByteUsage() > o.multiByteUsage()
	}
	return m.Chaos() < o.Chaos()
}

// Matches is CharsetMatches.
type Matches struct {
	results []*Match
	sorted  bool
}

func newMatches(rs ...*Match) *Matches {
	ms := &Matches{results: rs, sorted: true}
	if len(rs) > 0 {
		pyListSort(ms.results, (*Match).less)
	}
	return ms
}

func (ms *Matches) ensureSorted() {
	if !ms.sorted {
		pyListSort(ms.results, (*Match).less)
		ms.sorted = true
	}
}

// All returns the matches in iteration order (sorted).
func (ms *Matches) All() []*Match {
	ms.ensureSorted()
	return ms.results
}

// Len is len(matches).
func (ms *Matches) Len() int { return len(ms.results) }

// Best is CharsetMatches.best() (nil when empty).
func (ms *Matches) Best() *Match {
	if len(ms.results) == 0 {
		return nil
	}
	ms.ensureSorted()
	return ms.results[0]
}

func (ms *Matches) append(item *Match) {
	if len(item.payload) < tooBigSequence {
		for _, m := range ms.results {
			if m.Chaos() == item.Chaos() && m.sameText(item) {
				item.str, item.strLoaded = nil, false
				m.leaves = append(m.leaves, item)
				return
			}
		}
	}
	ms.results = append(ms.results, item)
	ms.sorted = false
}

// get is __getitem__ with an encoding name.
func (ms *Matches) get(name string) *Match {
	name = IANAName(name, false)
	for _, r := range ms.results {
		for _, e := range r.CouldBeFromCharset() {
			if e == name {
				return r
			}
		}
	}
	return nil
}

// IANAName is utils.iana_name (non-strict: unknown names pass through
// lowercased with "-" replaced by "_").
func IANAName(cp string, strict bool) string {
	cp = strings.ReplaceAll(strings.ToLower(cp), "-", "_")
	if v, ok := ianaNames[cp]; ok {
		return v
	}
	return cp
}

func isMultiByte(name string) bool { return multiByte[name] }

var encodingMarks = []struct {
	enc   string
	marks [][]byte
}{
	{"utf_8", [][]byte{{0xef, 0xbb, 0xbf}}},
	{"utf_7", [][]byte{{0x2b, 0x2f, 0x76, 0x38}, {0x2b, 0x2f, 0x76, 0x39}, {0x2b, 0x2f, 0x76, 0x2b}, {0x2b, 0x2f, 0x76, 0x2f}}},
	{"gb18030", [][]byte{{0x84, 0x31, 0x95, 0x33}}},
	{"utf_32", [][]byte{{0, 0, 0xfe, 0xff}, {0xff, 0xfe, 0, 0}}},
	{"utf_16", [][]byte{{0xfe, 0xff}, {0xff, 0xfe}}},
}

func identifySigOrBOM(seq []byte) (string, []byte) {
	for _, em := range encodingMarks {
		for _, m := range em.marks {
			if bytes.HasPrefix(seq, m) {
				return em.enc, m
			}
		}
	}
	return "", nil
}

var rePossibleEncodingIndication = regexp.MustCompile(`(?i)(?:(?:encoding)|(?:charset)|(?:coding))(?:[\:= ]{1,10})(?:["']?)([a-zA-Z0-9\-_]+)(?:["']?)`)

// anySpecifiedEncoding is utils.any_specified_encoding.
func anySpecifiedEncoding(seq []byte) string {
	zone := seq
	if len(zone) > 8192 {
		zone = zone[:8192]
	}
	low := bytes.ToLower(zone)
	if !bytes.Contains(low, []byte("coding")) && !bytes.Contains(low, []byte("charset")) {
		return ""
	}
	ascii := make([]byte, 0, len(zone))
	for _, c := range zone {
		if c < 0x80 {
			ascii = append(ascii, c)
		}
	}
	for _, m := range rePossibleEncodingIndication.FindAllSubmatch(ascii, -1) {
		spec := strings.ReplaceAll(strings.ToLower(string(m[1])), "-", "_")
		if v, ok := ianaNames[spec]; ok {
			return v
		}
	}
	return ""
}

// FromBytes is charset_normalizer.from_bytes(sequences) with default options.
func FromBytes(seq []byte) *Matches { return FromBytesOpts(seq, DefaultOptions()) }

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func decodeWith(name string, b []byte) ([]rune, bool) {
	c := LookupCodec(name)
	if c == nil {
		return nil, false
	}
	return c.Decode(b, false)
}

// FromBytesOpts is charset_normalizer.from_bytes.
func FromBytesOpts(sequences []byte, o Options) *Matches {
	steps, chunkSize, threshold := o.Steps, o.ChunkSize, o.Threshold
	length := len(sequences)
	if length == 0 {
		return newMatches(&Match{payload: sequences, encoding: "utf_8", str: []rune{}, strLoaded: true})
	}
	var cpIsolation, cpExclusion []string
	for _, c := range o.CPIsolation {
		cpIsolation = append(cpIsolation, IANAName(c, false))
	}
	for _, c := range o.CPExclusion {
		cpExclusion = append(cpExclusion, IANAName(c, false))
	}
	if length <= chunkSize*steps {
		steps = 1
		chunkSize = length
	}
	if steps > 1 && float64(length)/float64(steps) < float64(chunkSize) {
		chunkSize = int(float64(length) / float64(steps))
	}
	isTooLarge := length >= tooBigSequence

	var prioritized []string
	specified := ""
	if !o.NoPreemptive {
		specified = anySpecifiedEncoding(sequences)
	}
	hasSpecified := specified != ""
	if hasSpecified {
		prioritized = append(prioritized, specified)
	}
	tested := map[string]bool{}
	softFailureSkip := map[string]bool{}
	definitiveMatchFound := false
	definitiveTargetLanguages := map[string]bool{}
	postDefinitiveSBSuccess := 0
	const postDefinitiveSBCap = 7
	mbDefinitiveMatchFound := false
	var fallbackASCII, fallbackU8, fallbackSpecified *Match
	results := &Matches{sorted: true}
	earlyStop := &Matches{sorted: true}

	messCache := map[string]float64{}
	cohCache := map[string][]CoherenceMatch{}

	sigEncoding, sigPayload := identifySigOrBOM(sequences)
	if sigEncoding != "" {
		prioritized = append([]string{sigEncoding}, prioritized...)
	}
	prioritized = append(prioritized, "ascii")
	if !contains(prioritized, "utf_8") {
		prioritized = append(prioritized, "utf_8")
	}
	isSpecified := func(e string) bool { return hasSpecified && e == specified }

	candidates := append(append([]string(nil), prioritized...), ianaSupportedMBFirst...)
	for _, enc := range candidates {
		if len(cpIsolation) > 0 && !contains(cpIsolation, enc) {
			continue
		}
		if len(cpExclusion) > 0 && contains(cpExclusion, enc) {
			continue
		}
		if tested[enc] {
			continue
		}
		tested[enc] = true

		var decoded []rune
		hasDecoded := false
		bomAvailable := sigEncoding == enc
		stripSig := bomAvailable && enc != "utf_16" && enc != "utf_32"
		if (enc == "utf_16" || enc == "utf_32") && !bomAvailable {
			continue
		}
		if enc == "utf_7" && !bomAvailable {
			continue
		}
		if softFailureSkip[enc] {
			continue
		}
		isMB := isMultiByte(enc)
		if definitiveMatchFound {
			ok := false
			for _, l := range encodingLanguages(enc) {
				if definitiveTargetLanguages[l] {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		if definitiveMatchFound && !isMB && postDefinitiveSBSuccess >= postDefinitiveSBCap {
			continue
		}
		if mbDefinitiveMatchFound && !isMB {
			continue
		}
		deferred := !isMB && !isTooLarge
		base := sequences
		if stripSig {
			base = sequences[len(sigPayload):]
		}
		codec := LookupCodec(enc)
		hardFail := false
		switch {
		case codec == nil:
			hardFail = true // LookupError
		case isTooLarge && !isMB:
			end := 500000
			lo := 0
			if stripSig {
				lo = len(sigPayload)
			}
			if end > length {
				end = length
			}
			if lo > end {
				lo = end
			}
			if _, ok := codec.Decode(sequences[lo:end], false); !ok {
				hardFail = true
			}
		case !deferred:
			if enc == "utf_7" && bomAvailable {
				d, ok := codec.Decode(sequences, false)
				if !ok {
					hardFail = true
				} else {
					if len(d) > 0 && d[0] == 0xfeff {
						d = d[1:]
					}
					decoded, hasDecoded = d, true
				}
			} else {
				d, ok := codec.Decode(base, false)
				if !ok {
					hardFail = true
				} else {
					decoded, hasDecoded = d, true
				}
			}
		}
		if hardFail {
			continue
		}

		stepLen := int(float64(length) / float64(steps))
		rStart := 0
		if bomAvailable {
			rStart = len(sigPayload)
		}
		var offsets []int
		for i := rStart; i < length; i += stepLen {
			offsets = append(offsets, i)
		}
		multiByteBonus := isMB && hasDecoded && len(decoded) < length
		maxChunkGaveUp := len(offsets) / 4
		if maxChunkGaveUp < 2 {
			maxChunkGaveUp = 2
		}
		earlyStopCount := 0
		lazyStrHardFailure := false
		var mdChunks [][]rune
		var mdRatios []float64

		chunkFailed := false
		cutSequenceChunks(sequences, enc, codec, offsets, chunkSize, bomAvailable, stripSig, sigPayload, isMB, decoded, hasDecoded, deferred,
			func(chunk []rune, ok bool) bool {
				if !ok {
					chunkFailed = true
					return false
				}
				mdChunks = append(mdChunks, chunk)
				key := string(chunk)
				r, cached := messCache[key]
				if !cached {
					r = messRatio(chunk, threshold)
					messCache[key] = r
				}
				mdRatios = append(mdRatios, r)
				if r >= threshold {
					earlyStopCount++
				}
				if earlyStopCount >= maxChunkGaveUp || (bomAvailable && !stripSig) {
					return false
				}
				return true
			})
		if chunkFailed {
			if deferred || codec == nil {
				continue
			}
			earlyStopCount = maxChunkGaveUp
			lazyStrHardFailure = true
		}
		meanMess := 0.0
		if len(mdRatios) > 0 {
			meanMess = pySum(mdRatios) / float64(len(mdRatios))
		}
		if !lazyStrHardFailure && isTooLarge && !isMB && meanMess < threshold && earlyStopCount < maxChunkGaveUp {
			lo := 50000
			if lo > length {
				lo = length
			}
			if _, ok := codec.Decode(sequences[lo:], false); !ok {
				continue
			}
		}
		if meanMess >= threshold || earlyStopCount >= maxChunkGaveUp {
			if sim, ok := ianaSupportedSimilar[enc]; ok {
				for _, s := range sim {
					softFailureSkip[s] = true
				}
			}
			if !o.DisableFallback && (enc == "ascii" || enc == "utf_8" || isSpecified(enc) || enc == "utf_16" || enc == "utf_32") && !lazyStrHardFailure {
				if !hasDecoded {
					d, ok := codec.Decode(base, false)
					if !ok {
						continue
					}
					decoded, hasDecoded = d, true
					if isTooLarge {
						decoded, hasDecoded = nil, false
					}
				}
				fb := &Match{payload: sequences, encoding: enc, meanMessRatio: threshold, hasSigOrBOM: bomAvailable, preemptive: specified}
				if hasDecoded {
					fb.str, fb.strLoaded = decoded, true
				}
				switch {
				case isSpecified(enc):
					fallbackSpecified = fb
				case enc == "ascii":
					fallbackASCII = fb
				default:
					fallbackU8 = fb
				}
			}
			continue
		}
		if deferred {
			d, ok := codec.Decode(base, false)
			if !ok {
				continue
			}
			decoded, hasDecoded = d, true
		}

		targetLanguages := encodingLanguages(enc)
		var cdRatios [][]CoherenceMatch
		if enc != "ascii" {
			lgInclusion := strings.Join(targetLanguages, ",")
			hasInclusion := len(targetLanguages) > 0
			for _, chunk := range mdChunks {
				key := string(chunk) + "\x00" + lgInclusion
				if !hasInclusion {
					key = string(chunk) + "\x01"
				}
				cr, ok := cohCache[key]
				if !ok {
					cr = coherenceRatio(chunk, o.LanguageThreshold, lgInclusion, hasInclusion)
					cohCache[key] = cr
				}
				cdRatios = append(cdRatios, cr)
			}
		}
		merged := mergeCoherenceRatios(cdRatios)

		cur := &Match{payload: sequences, encoding: enc, meanMessRatio: meanMess, hasSigOrBOM: bomAvailable, languages: merged, preemptive: specified}
		if !isTooLarge || isSpecified(enc) || enc == "ascii" || enc == "utf_8" {
			if hasDecoded {
				cur.str, cur.strLoaded = decoded, true
			}
		}
		results.append(cur)

		if definitiveMatchFound && !isMB && meanMess < 0.02 {
			postDefinitiveSBSuccess++
		}
		if (isSpecified(enc) || enc == "ascii" || enc == "utf_8") && meanMess < 0.1 {
			if meanMess == 0.0 {
				return newMatches(cur)
			}
			earlyStop.append(cur)
		}
		if earlyStop.Len() > 0 && (!hasSpecified || tested[specified]) && tested["ascii"] && tested["utf_8"] {
			return newMatches(earlyStop.Best())
		}
		if !definitiveMatchFound && !isMB {
			best := 0.0
			for _, m := range merged {
				if m.Ratio > best {
					best = m.Ratio
				}
			}
			if best >= 0.5 && tested["ascii"] && tested["utf_8"] {
				definitiveMatchFound = true
				for _, l := range targetLanguages {
					definitiveTargetLanguages[l] = true
				}
			}
		}
		if !mbDefinitiveMatchFound && isMB && multiByteBonus && hasDecoded && float64(len(decoded)) < float64(length)*0.98 &&
			!contains([]string{"utf_8", "utf_8_sig", "utf_16", "utf_16_be", "utf_16_le", "utf_32", "utf_32_be", "utf_32_le", "utf_7"}, enc) &&
			tested["ascii"] && tested["utf_8"] {
			mbDefinitiveMatchFound = true
		}
		if enc == sigEncoding {
			return newMatches(results.get(enc))
		}
	}
	if results.Len() == 0 {
		switch {
		case fallbackSpecified != nil:
			results.append(fallbackSpecified)
		case fallbackU8 != nil:
			results.append(fallbackU8)
		case fallbackASCII != nil:
			results.append(fallbackASCII)
		}
	}
	return results
}

// cutSequenceChunks is utils.cut_sequence_chunks; emit returns false to stop.
// A chunk decode failure is reported as emit(nil, false).
func cutSequenceChunks(seq []byte, enc string, codec Codec, offsets []int, chunkSize int, bomAvailable, stripSig bool, sigPayload []byte,
	isMB bool, decoded []rune, hasDecoded bool, deferred bool, emit func([]rune, bool) bool) {
	switch {
	case hasDecoded && len(decoded) > 0 && strings.HasPrefix(enc, "iso2022_"):
		dl, sl := len(decoded), len(seq)
		for _, i := range offsets {
			off := i * dl / sl
			chunk := runeSlice(decoded, off, off+chunkSize)
			if len(chunk) == 0 {
				return
			}
			if !emit(chunk, true) {
				return
			}
		}
	case hasDecoded && len(decoded) > 0 && !isMB:
		for _, i := range offsets {
			chunk := runeSlice(decoded, i, i+chunkSize)
			if len(chunk) == 0 {
				return
			}
			if !emit(chunk, true) {
				return
			}
		}
	case deferred:
		base := seq
		if stripSig {
			base = seq[len(sigPayload):]
		}
		for _, i := range offsets {
			cut := byteSlice(base, i, i+chunkSize)
			if len(cut) == 0 {
				return
			}
			if codec == nil {
				emit(nil, false)
				return
			}
			chunk, ok := codec.Decode(cut, false)
			if !ok {
				emit(nil, false)
				return
			}
			if !emit(chunk, true) {
				return
			}
		}
	default:
		for _, i := range offsets {
			chunkEnd := i + chunkSize
			if chunkEnd > len(seq)+8 {
				continue
			}
			cut := byteSlice(seq, i, i+chunkSize)
			if bomAvailable && !stripSig {
				cut = append(append([]byte(nil), sigPayload...), cut...)
			}
			chunk, ok := codec.Decode(cut, isMB)
			if !ok {
				emit(nil, false)
				return
			}
			if isMB && i > 0 {
				chk := chunkSize
				if chk > 16 {
					chk = 16
				}
				prefix := runeSlice(chunk, 0, chk)
				foundNearby := false
				if hasDecoded && len(decoded) > 0 {
					dl := len(decoded)
					expected := i * dl / len(seq)
					ss := expected - 32768
					if ss < 0 {
						ss = 0
					}
					se := expected + 32768
					if se > dl {
						se = dl
					}
					foundNearby = runeFind(decoded, prefix, ss, se) >= 0
				}
				if hasDecoded && len(decoded) > 0 && !foundNearby && runeFind(decoded, prefix, 0, len(decoded)) < 0 {
					for j := i; j > i-4; j-- {
						cut := byteSlice(seq, j, chunkEnd)
						if bomAvailable && !stripSig {
							cut = append(append([]byte(nil), sigPayload...), cut...)
						}
						chunk, _ = codec.Decode(cut, true)
						if runeFind(decoded, runeSlice(chunk, 0, chk), 0, len(decoded)) >= 0 {
							break
						}
					}
				}
			}
			if !emit(chunk, true) {
				return
			}
		}
	}
}

// byteSlice is Python's seq[i:j] for non-negative/negative-clamped bounds.
func byteSlice(b []byte, i, j int) []byte {
	i, j = pyBounds(len(b), i, j)
	return b[i:j]
}

func runeSlice(r []rune, i, j int) []rune {
	i, j = pyBounds(len(r), i, j)
	return r[i:j]
}

func pyBounds(n, i, j int) (int, int) {
	if i < 0 {
		i += n
		if i < 0 {
			i = 0
		}
	}
	if j < 0 {
		j += n
		if j < 0 {
			j = 0
		}
	}
	if i > n {
		i = n
	}
	if j > n {
		j = n
	}
	if j < i {
		j = i
	}
	return i, j
}

// runeFind is str.find(sub, start, end).
func runeFind(s, sub []rune, start, end int) int {
	if end > len(s) {
		end = len(s)
	}
	if start < 0 {
		start = 0
	}
	if len(sub) == 0 {
		if start <= end {
			return start
		}
		return -1
	}
	for i := start; i+len(sub) <= end; i++ {
		if s[i] == sub[0] {
			match := true
			for k := 1; k < len(sub); k++ {
				if s[i+k] != sub[k] {
					match = false
					break
				}
			}
			if match {
				return i
			}
		}
	}
	return -1
}
