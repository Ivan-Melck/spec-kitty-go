package charsetnorm

import (
	"sync"
	"unicode/utf8"
)

// Codec decodes bytes the way CPython's codec of the same name does.
//
// decode returns the decoded code points. With ignore=false (errors="strict")
// ok is false as soon as an undecodable sequence is met; with ignore=true
// (errors="ignore") undecodable sequences are skipped exactly as CPython's
// error callback would skip them and ok is always true.
type Codec interface {
	Decode(b []byte, ignore bool) (out []rune, ok bool)
}

var (
	codecMu    sync.Mutex
	codecCache = map[string]Codec{}
	sbcsOnce   sync.Once
	sbcsTables map[string]*[256]rune
)

func loadSBCS() {
	r := &varintReader{b: loadData("sbcs.bin")}
	n := r.int()
	sbcsTables = make(map[string]*[256]rune, n)
	for i := 0; i < n; i++ {
		name := r.str()
		var t [256]rune
		for b := range t {
			t[b] = rune(r.int()) - 1
		}
		sbcsTables[name] = &t
	}
}

// LookupCodec returns the decoder for a Python codec name (as normalised by
// charset_normalizer, e.g. "cp1252", "shift_jis", "utf_16_le"), or nil when
// CPython would raise LookupError (unknown or non-text codec).
func LookupCodec(name string) Codec {
	codecMu.Lock()
	defer codecMu.Unlock()
	if c, ok := codecCache[name]; ok {
		return c
	}
	c := newCodec(name)
	codecCache[name] = c
	return c
}

func newCodec(name string) Codec {
	switch name {
	case "utf_8":
		return utf8Codec{}
	case "utf_16":
		return utf16Codec{bom: true}
	case "utf_16_le":
		return utf16Codec{}
	case "utf_16_be":
		return utf16Codec{big: true}
	case "utf_32":
		return utf32Codec{bom: true}
	case "utf_32_le":
		return utf32Codec{}
	case "utf_32_be":
		return utf32Codec{big: true}
	case "utf_7":
		return utf7Codec{}
	case "hz":
		return hzCodec{}
	case "iso2022_jp", "iso2022_jp_1", "iso2022_jp_2", "iso2022_jp_2004", "iso2022_jp_3", "iso2022_jp_ext", "iso2022_kr":
		return newISO2022(name)
	}
	sbcsOnce.Do(loadSBCS)
	if t, ok := sbcsTables[name]; ok {
		return sbcsCodec{t}
	}
	for _, n := range mbTrieNames {
		if n == name {
			return loadTrieCodec(name)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Single-byte (charmap) codecs
// ---------------------------------------------------------------------------

type sbcsCodec struct{ t *[256]rune }

func (c sbcsCodec) Decode(b []byte, ignore bool) ([]rune, bool) {
	out := make([]rune, 0, len(b))
	for _, x := range b {
		r := c.t[x]
		if r < 0 {
			if ignore {
				continue
			}
			return nil, false
		}
		out = append(out, r)
	}
	return out, true
}

// ---------------------------------------------------------------------------
// UTF-8 / UTF-16 / UTF-32
// ---------------------------------------------------------------------------

type utf8Codec struct{}

func (utf8Codec) Decode(b []byte, ignore bool) ([]rune, bool) {
	if utf8.Valid(b) {
		return []rune(string(b)), true
	}
	if !ignore {
		return nil, false
	}
	// CPython skips each maximal invalid subpart; every byte of such a
	// subpart is itself an invalid start, so skipping one byte at a time
	// yields the same output.
	out := make([]rune, 0, len(b))
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && n <= 1 {
			i++
			continue
		}
		out = append(out, r)
		i += n
	}
	return out, true
}

type utf16Codec struct{ big, bom bool }

func (c utf16Codec) Decode(b []byte, ignore bool) ([]rune, bool) {
	big := c.big
	q := 0
	if c.bom && len(b) >= 2 {
		switch {
		case b[0] == 0xff && b[1] == 0xfe:
			q, big = 2, false
		case b[0] == 0xfe && b[1] == 0xff:
			q, big = 2, true
		}
	}
	unit := func(i int) rune {
		if big {
			return rune(b[i])<<8 | rune(b[i+1])
		}
		return rune(b[i+1])<<8 | rune(b[i])
	}
	out := make([]rune, 0, (len(b)-q)/2)
	for q < len(b) {
		if len(b)-q < 2 { // truncated data
			if !ignore {
				return nil, false
			}
			break
		}
		ch := unit(q)
		q += 2
		if ch < 0xd800 || ch > 0xdfff {
			out = append(out, ch)
			continue
		}
		if ch >= 0xdc00 { // illegal encoding (lone low surrogate)
			if !ignore {
				return nil, false
			}
			continue
		}
		if len(b)-q < 2 { // unexpected end of data: skip the rest
			if !ignore {
				return nil, false
			}
			break
		}
		ch2 := unit(q)
		if ch2 < 0xdc00 || ch2 > 0xdfff { // illegal UTF-16 surrogate (2 bytes)
			if !ignore {
				return nil, false
			}
			continue
		}
		q += 2
		out = append(out, 0x10000+((ch-0xd800)<<10)+(ch2-0xdc00))
	}
	return out, true
}

type utf32Codec struct{ big, bom bool }

func (c utf32Codec) Decode(b []byte, ignore bool) ([]rune, bool) {
	big := c.big
	q := 0
	if c.bom && len(b) >= 4 {
		switch {
		case b[0] == 0xff && b[1] == 0xfe && b[2] == 0 && b[3] == 0:
			q, big = 4, false
		case b[0] == 0 && b[1] == 0 && b[2] == 0xfe && b[3] == 0xff:
			q, big = 4, true
		}
	}
	out := make([]rune, 0, (len(b)-q)/4)
	for q < len(b) {
		if len(b)-q < 4 {
			if !ignore {
				return nil, false
			}
			break
		}
		var ch uint32
		if big {
			ch = uint32(b[q])<<24 | uint32(b[q+1])<<16 | uint32(b[q+2])<<8 | uint32(b[q+3])
		} else {
			ch = uint32(b[q+3])<<24 | uint32(b[q+2])<<16 | uint32(b[q+1])<<8 | uint32(b[q])
		}
		q += 4
		if (ch >= 0xd800 && ch <= 0xdfff) || ch >= 0x110000 {
			if !ignore {
				return nil, false
			}
			continue
		}
		out = append(out, rune(ch))
	}
	return out, true
}

// ---------------------------------------------------------------------------
// Stateless CJK multibyte codecs: byte tries probed from CPython's codecs
// ---------------------------------------------------------------------------

const (
	kOut = iota
	kErr
	kMore
	kGB4
	kJamo
)

type trieEntry struct {
	kind uint8
	n    uint8  // output rune count / error length
	val  uint32 // rune, index into extra, or child node
}

type trieNode [256]trieEntry

type trieCodec struct {
	nodes []trieNode
	extra []rune
	name  string
}

func loadTrieCodec(name string) *trieCodec {
	r := &varintReader{b: loadData("mb_" + name + ".bin")}
	c := &trieCodec{name: name}
	prev := 0
	var parse func() uint32
	parse = func() uint32 {
		idx := len(c.nodes)
		c.nodes = append(c.nodes, trieNode{})
		for b := 0; b < 256; b++ {
			kind := r.byte()
			e := trieEntry{kind: kind}
			switch kind {
			case kOut:
				n := r.int()
				e.n = uint8(n)
				rs := make([]rune, n)
				for i := range rs {
					prev += r.zigzag()
					rs[i] = rune(prev)
				}
				if n == 1 {
					e.val = uint32(rs[0])
				} else {
					e.val = uint32(len(c.extra))
					c.extra = append(c.extra, rs...)
				}
			case kErr:
				e.n = uint8(r.int())
			case kMore:
				e.val = parse()
			}
			c.nodes[idx][b] = e
		}
		return uint32(idx)
	}
	parse()
	return c
}

var (
	gb4Once sync.Once
	gb4BMP  []rune
)

func loadGB4() {
	r := &varintReader{b: loadData("gb18030_4.bin")}
	gb4BMP = make([]rune, 39420)
	prev := 0
	for i := range gb4BMP {
		prev += r.zigzag()
		gb4BMP[i] = rune(prev)
	}
}

const jamoNone = 127

var cgk2uChoseong = [30]int{
	0, 1, jamoNone, 2, jamoNone, jamoNone, 3, 4,
	5, jamoNone, jamoNone, jamoNone, jamoNone, jamoNone, jamoNone, jamoNone,
	6, 7, 8, jamoNone, 9, 10, 11, 12,
	13, 14, 15, 16, 17, 18,
}

var cgk2uJongseong = [30]int{
	1, 2, 3, 4, 5, 6, 7, jamoNone,
	8, 9, 10, 11, 12, 13, 14, 15,
	16, 17, jamoNone, 18, 19, 20, 21, 22,
	jamoNone, 23, 24, 25, 26, 27,
}

// step decodes one character at b[0:]. It returns the number of bytes
// consumed and the output, or an error length (errLen > 0), or toofew when
// the input ends inside a sequence (MBERR_TOOFEW).
func (c *trieCodec) step(b []byte, out []rune) (n int, res []rune, errLen int, toofew bool) {
	node := &c.nodes[0]
	for d := 0; ; d++ {
		if d >= len(b) {
			return 0, out, 0, true
		}
		e := node[b[d]]
		switch e.kind {
		case kOut:
			if e.n == 1 {
				out = append(out, rune(e.val))
			} else {
				out = append(out, c.extra[e.val:e.val+uint32(e.n)]...)
			}
			return d + 1, out, 0, false
		case kErr:
			return 0, out, int(e.n), false
		case kMore:
			node = &c.nodes[e.val]
		case kGB4:
			return c.stepGB4(b, out)
		case kJamo:
			return stepJamo(b, out)
		}
	}
}

// stepGB4 is the four-byte branch of CPython's gb18030 decoder.
func (c *trieCodec) stepGB4(b []byte, out []rune) (int, []rune, int, bool) {
	if len(b) < 4 {
		return 0, out, 0, true
	}
	c1, c2, c3, c4 := int(b[0]), int(b[1]), int(b[2]), int(b[3])
	if c1 < 0x81 || c1 > 0xfe || c3 < 0x81 || c3 > 0xfe || c4 < 0x30 || c4 > 0x39 {
		return 0, out, 1, false
	}
	c1 -= 0x81
	c2 -= 0x30
	c3 -= 0x81
	c4 -= 0x30
	if c1 < 4 {
		lseq := (c1*10+c2)*1260 + c3*10 + c4
		if lseq < 39420 {
			gb4Once.Do(loadGB4)
			return 4, append(out, gb4BMP[lseq]), 0, false
		}
	} else if c1 >= 15 {
		lseq := 0x10000 + ((c1-15)*10+c2)*1260 + c3*10 + c4
		if lseq <= 0x10ffff {
			return 4, append(out, rune(lseq)), 0, false
		}
	}
	return 0, out, 1, false
}

// stepJamo is euc_kr's KS X 1001:1998 Annex 3 make-up sequence.
func stepJamo(b []byte, out []rune) (int, []rune, int, bool) {
	if len(b) < 8 {
		return 0, out, 0, true
	}
	if b[2] != 0xa4 || b[4] != 0xa4 || b[6] != 0xa4 {
		return 0, out, 1, false
	}
	cho, jung, jong := jamoNone, jamoNone, jamoNone
	if c := b[3]; c >= 0xa1 && c <= 0xbe {
		cho = cgk2uChoseong[c-0xa1]
	}
	if c := b[5]; c >= 0xbf && c <= 0xd3 {
		jung = int(c) - 0xbf
	}
	if c := b[7]; c == 0xd4 {
		jong = 0
	} else if c >= 0xa1 && c <= 0xbe {
		jong = cgk2uJongseong[c-0xa1]
	}
	if cho == jamoNone || jung == jamoNone || jong == jamoNone {
		return 0, out, 1, false
	}
	return 8, append(out, rune(0xac00+cho*588+jung*28+jong)), 0, false
}

func (c *trieCodec) Decode(b []byte, ignore bool) ([]rune, bool) {
	out := make([]rune, 0, len(b))
	for i := 0; i < len(b); {
		n, res, errLen, toofew := c.step(b[i:], out)
		out = res
		switch {
		case toofew:
			// incomplete multibyte sequence: the error spans the rest.
			if !ignore {
				return nil, false
			}
			return out, true
		case errLen > 0:
			if !ignore {
				return nil, false
			}
			i += errLen
		default:
			i += n
		}
	}
	return out, true
}
