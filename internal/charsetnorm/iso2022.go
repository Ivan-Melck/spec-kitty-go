package charsetnorm

import "sync"

// Port of CPython's _codecs_iso2022.c decoder. The per-charset maps are
// probed from CPython's own iso2022_* codecs (data/iso2022.bin).

const (
	csASCII        = 'B'
	csISO8859_1    = 'A'
	csISO8859_7    = 'F'
	csJISX0201K    = 'I'
	csJISX0201R    = 'J'
	csDBCS         = 0x80
	csGB2312       = 'A' | csDBCS
	csJISX0208     = 'B' | csDBCS
	csKSX1001      = 'C' | csDBCS
	csJISX0212     = 'D' | csDBCS
	csJISX0213_1_0 = 'O' | csDBCS
	csJISX0213_2   = 'P' | csDBCS
	csJISX0213_1_4 = 'Q' | csDBCS
	csJISX0208O    = '@' | csDBCS

	isoNoShift  = 0x01
	isoUseG2    = 0x02
	isoJISX0208 = 0x04
)

type isoDesignation struct {
	mark  int
	width int
	table string // "" = dummy decoder (always unmappable)
}

type isoConfig struct {
	flags int
	dsg   []isoDesignation
}

var isoConfigs = map[string]isoConfig{
	"iso2022_kr": {0, []isoDesignation{{csKSX1001, 2, "ksx1001"}}},
	"iso2022_jp": {isoNoShift | isoJISX0208, []isoDesignation{
		{csJISX0208, 2, "jisx0208"}, {csJISX0201R, 1, "jisx0201_r"}, {csJISX0208O, 2, "jisx0208"}}},
	"iso2022_jp_1": {isoNoShift | isoJISX0208, []isoDesignation{
		{csJISX0208, 2, "jisx0208"}, {csJISX0212, 2, "jisx0212"}, {csJISX0201R, 1, "jisx0201_r"}, {csJISX0208O, 2, "jisx0208"}}},
	"iso2022_jp_2": {isoNoShift | isoUseG2 | isoJISX0208, []isoDesignation{
		{csJISX0208, 2, "jisx0208"}, {csJISX0212, 2, "jisx0212"}, {csKSX1001, 2, "ksx1001"}, {csGB2312, 2, "gb2312"},
		{csJISX0201R, 1, "jisx0201_r"}, {csJISX0208O, 2, "jisx0208"}, {csISO8859_1, 1, ""}, {csISO8859_7, 1, ""}}},
	"iso2022_jp_2004": {isoNoShift | isoJISX0208, []isoDesignation{
		{csJISX0213_1_4, 2, "jisx0213_2004_1"}, {csJISX0208, 2, "jisx0208"}, {csJISX0213_1_4, 2, "jisx0213_2004_1"}, {csJISX0213_2, 2, "jisx0213_2004_2"}}},
	"iso2022_jp_3": {isoNoShift | isoJISX0208, []isoDesignation{
		{csJISX0213_1_0, 2, "jisx0213_2000_1"}, {csJISX0208, 2, "jisx0208"}, {csJISX0213_1_0, 2, "jisx0213_2000_1"}, {csJISX0213_2, 2, "jisx0213_2000_2"}}},
	"iso2022_jp_ext": {isoNoShift | isoJISX0208, []isoDesignation{
		{csJISX0208, 2, "jisx0208"}, {csJISX0212, 2, "jisx0212"}, {csJISX0201R, 1, "jisx0201_r"}, {csJISX0201K, 1, "jisx0201_k"}, {csJISX0208O, 2, "jisx0208"}}},
}

// isoTable maps a 1- or 2-byte 7-bit sequence (first byte 0x20..0x7f) to
// its output (nil = MAP_UNMAPPABLE).
type isoTable struct {
	width int
	out   [][]rune
}

var (
	isoOnce   sync.Once
	isoTables map[string]*isoTable
)

func loadISOTables() {
	r := &varintReader{b: loadData("iso2022.bin")}
	n := r.int()
	isoTables = make(map[string]*isoTable, n)
	for i := 0; i < n; i++ {
		name := r.str()
		w := r.int()
		count := 0x60
		if w == 2 {
			count = 0x60 * 256
		}
		t := &isoTable{width: w, out: make([][]rune, count)}
		prev := 0
		for j := 0; j < count; j++ {
			k := r.int()
			if k == 0 {
				continue
			}
			rs := make([]rune, k)
			for m := range rs {
				prev += r.zigzag()
				rs[m] = rune(prev)
			}
			t.out[j] = rs
		}
		isoTables[name] = t
	}
}

func (t *isoTable) lookup(b []byte) []rune {
	c1 := int(b[0])
	if c1 < 0x20 || c1 > 0x7f {
		return nil
	}
	if t.width == 1 {
		return t.out[c1-0x20]
	}
	return t.out[(c1-0x20)*256+int(b[1])]
}

type iso2022Codec struct {
	cfg isoConfig
}

func newISO2022(name string) Codec {
	isoOnce.Do(loadISOTables)
	return iso2022Codec{isoConfigs[name]}
}

const (
	fShifted       = 0x01
	fEscThroughout = 0x02
	mberrToofew    = -2
)

func isEscEnd(c byte) bool { return (c >= 'A' && c <= 'Z') || c == '@' }

func isISO2022Esc(c byte) bool { return c == '(' || c == ')' || c == '$' || c == '.' || c == '&' }

type isoState struct {
	g     [4]int
	flags int
}

// processEsc is iso2022processesc: returns consumed length (>0) on
// success, or an error code (errLen > 0 / mberrToofew) with n == 0.
func (c iso2022Codec) processEsc(st *isoState, b []byte) (n int, errLen int) {
	esclen := 0
	inleft := len(b)
	for i := 1; i < 16; i++ {
		if i >= inleft {
			return 0, mberrToofew
		}
		if isEscEnd(b[i]) {
			esclen = i + 1
			break
		} else if c.cfg.flags&isoJISX0208 != 0 && i+1 < inleft && b[i] == '&' && b[i+1] == '@' {
			i += 2
		}
	}
	var charset, designation int
	switch esclen {
	case 0:
		return 0, 1
	case 3:
		if b[1] == '$' {
			charset = int(b[2]) | csDBCS
			designation = 0
		} else {
			charset = int(b[2])
			switch {
			case b[1] == '(':
				designation = 0
			case b[1] == ')':
				designation = 1
			case c.cfg.flags&isoUseG2 != 0 && b[1] == '.':
				designation = 2
			default:
				return 0, 3
			}
		}
	case 4:
		if b[1] != '$' {
			return 0, 4
		}
		charset = int(b[3]) | csDBCS
		switch b[2] {
		case '(':
			designation = 0
		case ')':
			designation = 1
		default:
			return 0, 4
		}
	case 6:
		if c.cfg.flags&isoJISX0208 != 0 && b[3] == 0x1b && b[4] == '$' && b[5] == 'B' {
			charset = 'B' | csDBCS
			designation = 0
		} else {
			return 0, 6
		}
	default:
		return 0, esclen
	}
	if charset != csASCII {
		found := false
		for _, d := range c.cfg.dsg {
			if d.mark == charset {
				found = true
				break
			}
		}
		if !found {
			return 0, esclen
		}
	}
	st.g[designation] = charset
	return esclen, 0
}

func iso8859_7(c int) (rune, bool) {
	switch {
	case c < 0xa0:
		return rune(c), true
	case c < 0xc0 && (0x288f3bc9&(1<<(c-0xa0))) != 0:
		return rune(c), true
	case c >= 0xb4 && c <= 0xfe && (c >= 0xd4 || (0xbffffd77&(1<<(c-0xb4))) != 0):
		return rune(0x02d0 + c), true
	case c == 0xa1:
		return 0x2018, true
	case c == 0xa2:
		return 0x2019, true
	case c == 0xaf:
		return 0x2015, true
	}
	return 0, false
}

func (c iso2022Codec) Decode(b []byte, ignore bool) ([]rune, bool) {
	st := &isoState{g: [4]int{csASCII, csASCII, csASCII, 0}}
	out := make([]rune, 0, len(b))
	i := 0
	// fail handles an error of length e (mberrToofew = rest of input).
	fail := func(e int) bool {
		if !ignore {
			return false
		}
		if e == mberrToofew {
			i = len(b)
		} else {
			i += e
		}
		return true
	}
	for i < len(b) {
		ch := b[i]
		rest := b[i:]
		if st.flags&fEscThroughout != 0 {
			out = append(out, rune(ch))
			i++
			if isEscEnd(ch) {
				st.flags &^= fEscThroughout
			}
			continue
		}
		switch {
		case ch == 0x1b:
			if len(rest) < 2 {
				if !fail(mberrToofew) {
					return nil, false
				}
				continue
			}
			if isISO2022Esc(rest[1]) {
				n, e := c.processEsc(st, rest)
				if e != 0 {
					if !fail(e) {
						return nil, false
					}
					continue
				}
				i += n
			} else if c.cfg.flags&isoUseG2 != 0 && rest[1] == 'N' {
				if len(rest) < 3 {
					if !fail(mberrToofew) {
						return nil, false
					}
					continue
				}
				b3 := int(rest[2])
				ok := true
				switch st.g[2] {
				case csISO8859_1:
					if b3 < 0x80 {
						out = append(out, rune(b3+0x80))
					} else {
						ok = false
					}
				case csISO8859_7:
					if r, good := iso8859_7(b3 ^ 0x80); good {
						out = append(out, r)
					} else {
						ok = false
					}
				case csASCII:
					if b3&0x80 != 0 {
						ok = false
					} else {
						out = append(out, rune(b3))
					}
				default:
					// MBERR_INTERNAL: CPython raises RuntimeError; not
					// reachable with the shipped configurations.
					return nil, false
				}
				if !ok {
					if !fail(3) {
						return nil, false
					}
					continue
				}
				i += 3
			} else {
				out = append(out, 0x1b)
				st.flags |= fEscThroughout
				i++
			}
		case ch == 0x0f && c.cfg.flags&isoNoShift == 0: // SI
			st.flags &^= fShifted
			i++
		case ch == 0x0e && c.cfg.flags&isoNoShift == 0: // SO
			st.flags |= fShifted
			i++
		case ch == 0x0a:
			st.flags &^= fShifted
			out = append(out, 0x0a)
			i++
		case ch < 0x20:
			out = append(out, rune(ch))
			i++
		case ch >= 0x80:
			if !fail(1) {
				return nil, false
			}
		default:
			charset := st.g[0]
			if st.flags&fShifted != 0 {
				charset = st.g[1]
			}
			if charset == csASCII {
				out = append(out, rune(ch))
				i++
				continue
			}
			var dsg isoDesignation
			for _, d := range c.cfg.dsg {
				if d.mark == charset {
					dsg = d
					break
				}
			}
			if len(rest) < dsg.width {
				if !fail(mberrToofew) {
					return nil, false
				}
				continue
			}
			var dec []rune
			if dsg.table != "" {
				dec = isoTables[dsg.table].lookup(rest)
			}
			if dec == nil {
				if !fail(dsg.width) {
					return nil, false
				}
				continue
			}
			out = append(out, dec...)
			i += dsg.width
		}
	}
	return out, true
}
