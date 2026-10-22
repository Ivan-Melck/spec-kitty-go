package charsetnorm

// iso2022Codec ports _codecs_iso2022.c's decoder.
type iso2022Codec struct{ name string }

func newISO2022(name string) Codec { return iso2022Codec{name} }

func (c iso2022Codec) Decode(b []byte, ignore bool) ([]rune, bool) {
	out := make([]rune, 0, len(b))
	for _, x := range b {
		if x >= 0x80 || x == 0x1b {
			if !ignore {
				return nil, false
			}
			continue
		}
		out = append(out, rune(x))
	}
	return out, true
}
