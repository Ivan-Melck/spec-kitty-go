package charsetnorm

// Stateful decoders: utf_7 (unicodeobject.c PyUnicode_DecodeUTF7Stateful)
// and hz (_codecs_cn.c). The iso2022_* family lives in iso2022.go.

type utf7Codec struct{}

func isBase64(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/'
}

func fromBase64(c byte) uint64 {
	switch {
	case c >= 'A' && c <= 'Z':
		return uint64(c - 'A')
	case c >= 'a' && c <= 'z':
		return uint64(c-'a') + 26
	case c >= '0' && c <= '9':
		return uint64(c-'0') + 52
	case c == '+':
		return 62
	}
	return 63
}

func (utf7Codec) Decode(b []byte, ignore bool) ([]rune, bool) {
	out := make([]rune, 0, len(b))
	if len(b) == 0 {
		return out, true
	}
	inShift := false
	var base64bits uint
	var base64buffer uint64
	var surrogate rune
	startinpos := 0
	s := 0
	e := len(b)
	for s < e {
		ch := b[s]
		failed := false
		if inShift {
			if isBase64(ch) {
				base64buffer = base64buffer<<6 | fromBase64(ch)
				base64bits += 6
				s++
				if base64bits >= 16 {
					outCh := rune(base64buffer >> (base64bits - 16))
					base64bits -= 16
					base64buffer &= (1 << base64bits) - 1
					if surrogate != 0 {
						if outCh >= 0xdc00 && outCh <= 0xdfff {
							out = append(out, 0x10000+((surrogate-0xd800)<<10)+(outCh-0xdc00))
							surrogate = 0
							continue
						}
						out = append(out, surrogate)
						surrogate = 0
					}
					if outCh >= 0xd800 && outCh <= 0xdbff {
						surrogate = outCh
					} else {
						out = append(out, outCh)
					}
				}
			} else {
				inShift = false
				if base64bits > 0 {
					if base64bits >= 6 {
						s++
						failed = true
					} else if base64buffer != 0 {
						s++
						failed = true
					}
				}
				if !failed {
					if surrogate != 0 && ch <= 127 && ch != '+' {
						out = append(out, surrogate)
					}
					surrogate = 0
					if ch == '-' {
						s++
					}
				}
			}
		} else if ch == '+' {
			startinpos = s
			s++
			if s < e && b[s] == '-' {
				s++
				out = append(out, '+')
			} else if s < e && !isBase64(b[s]) {
				s++
				failed = true
			} else {
				inShift = true
				surrogate = 0
				base64bits = 0
				base64buffer = 0
			}
		} else if ch <= 127 {
			s++
			out = append(out, rune(ch))
		} else {
			s++
			failed = true
		}
		if failed && !ignore {
			// utf7Error; with errors="ignore" decoding resumes at endinpos == s.
			return nil, false
		}
	}
	if inShift && (surrogate != 0 || base64bits >= 6 || (base64bits > 0 && base64buffer != 0)) {
		// unterminated shift sequence
		if !ignore {
			return nil, false
		}
	}
	_ = startinpos
	return out, true
}

type hzCodec struct{}

func (hzCodec) Decode(b []byte, ignore bool) ([]rune, bool) {
	gb := LookupCodec("gb2312").(*trieCodec)
	out := make([]rune, 0, len(b))
	gbMode := false
	fail := func() bool { return !ignore }
	for i := 0; i < len(b); {
		c := b[i]
		if c == '~' {
			if len(b)-i < 2 {
				if fail() {
					return nil, false
				}
				return out, true
			}
			c2 := b[i+1]
			switch {
			case c2 == '~' && !gbMode:
				out = append(out, '~')
			case c2 == '{' && !gbMode:
				gbMode = true
			case c2 == '\n' && !gbMode:
			case c2 == '}' && gbMode:
				gbMode = false
			default:
				if fail() {
					return nil, false
				}
				i++
				continue
			}
			i += 2
			continue
		}
		if c&0x80 != 0 {
			if fail() {
				return nil, false
			}
			i++
			continue
		}
		if !gbMode {
			out = append(out, rune(c))
			i++
			continue
		}
		if len(b)-i < 2 {
			if fail() {
				return nil, false
			}
			return out, true
		}
		c2 := b[i+1]
		if c2 < 0x80 {
			if e := gb.nodes[0][c|0x80]; e.kind == kMore {
				if e2 := gb.nodes[e.val][c2|0x80]; e2.kind == kOut && e2.n == 1 {
					out = append(out, rune(e2.val))
					i += 2
					continue
				}
			}
		}
		if fail() {
			return nil, false
		}
		i++
	}
	return out, true
}
