package pyjson

import "fmt"

// UnicodeError is UnicodeDecodeError for the 'utf-8' codec; Error() is
// str(exc), e.g. "'utf-8' codec can't decode byte 0xff in position 0:
// invalid start byte".
type UnicodeError struct {
	Start, End int // byte range [Start, End)
	Byte       byte
	Reason     string
}

func (e *UnicodeError) Error() string {
	if e.End-e.Start == 1 {
		return fmt.Sprintf("'utf-8' codec can't decode byte 0x%02x in position %d: %s", e.Byte, e.Start, e.Reason)
	}
	return fmt.Sprintf("'utf-8' codec can't decode bytes in position %d-%d: %s", e.Start, e.End-1, e.Reason)
}

func cont(b byte) bool { return b >= 0x80 && b <= 0xbf }

// DecodeUTF8 is bytes.decode("utf-8") (strict), reporting the first error
// exactly like CPython.
func DecodeUTF8(b []byte) (string, error) {
	n := len(b)
	for i := 0; i < n; {
		c := b[i]
		if c < 0x80 {
			i++
			continue
		}
		bad := func(length int, reason string) error {
			return &UnicodeError{Start: i, End: i + length, Byte: c, Reason: reason}
		}
		var need int
		lo, hi := byte(0x80), byte(0xbf)
		switch {
		case c >= 0xc2 && c <= 0xdf:
			need = 1
		case c >= 0xe0 && c <= 0xef:
			need = 2
			if c == 0xe0 {
				lo = 0xa0
			} else if c == 0xed {
				hi = 0x9f
			}
		case c >= 0xf0 && c <= 0xf4:
			need = 3
			if c == 0xf0 {
				lo = 0x90
			} else if c == 0xf4 {
				hi = 0x8f
			}
		default:
			return "", bad(1, "invalid start byte")
		}
		// second byte (range-checked)
		for k := 1; k <= need; k++ {
			if i+k >= n {
				return "", bad(n-i, "unexpected end of data")
			}
			x := b[i+k]
			ok := cont(x)
			if k == 1 {
				ok = x >= lo && x <= hi
			}
			if !ok {
				return "", bad(k, "invalid continuation byte")
			}
		}
		i += need + 1
	}
	return string(b), nil
}
