// Package ulid generates monotonic ULIDs compatible with python-ulid output
// (26-char uppercase Crockford base32; strictly increasing within a ms).
package ulid

import (
	"crypto/rand"
	"regexp"
	"sync"
	"time"
)

const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Pattern validates a ULID string.
var Pattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

var (
	mu       sync.Mutex
	lastMS   uint64
	lastRand [10]byte
)

// Now is the clock used for ULID timestamps (overridable in tests).
var Now = time.Now

// New returns a new ULID string.
func New() string {
	mu.Lock()
	defer mu.Unlock()
	ms := uint64(Now().UnixMilli())
	var r [10]byte
	if ms == lastMS {
		r = lastRand
		for i := 9; i >= 0; i-- {
			r[i]++
			if r[i] != 0 {
				break
			}
		}
	} else {
		if _, err := rand.Read(r[:]); err != nil {
			panic(err)
		}
	}
	lastMS, lastRand = ms, r
	return encode(ms, r)
}

func encode(ms uint64, r [10]byte) string {
	var b [16]byte
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	copy(b[6:], r[:])
	out := make([]byte, 26)
	// 128 bits -> 26 chars of 5 bits (first char carries 3 bits).
	var acc uint64
	bits := 0
	pos := 25
	for i := 15; i >= 0; i-- {
		acc |= uint64(b[i]) << bits
		bits += 8
		for bits >= 5 && pos >= 0 {
			out[pos] = alphabet[acc&31]
			acc >>= 5
			bits -= 5
			pos--
		}
	}
	if pos >= 0 {
		out[pos] = alphabet[acc&31]
	}
	return string(out)
}

// Valid reports whether s is a ULID.
func Valid(s string) bool { return Pattern.MatchString(s) }

// Time decodes the timestamp component of a ULID.
func Time(s string) (time.Time, bool) {
	if !Valid(s) {
		return time.Time{}, false
	}
	var ms uint64
	for _, c := range s[:10] {
		ms = ms<<5 | uint64(indexOf(byte(c)))
	}
	return time.UnixMilli(int64(ms)).UTC(), true
}

func indexOf(c byte) int {
	for i := 0; i < len(alphabet); i++ {
		if alphabet[i] == c {
			return i
		}
	}
	return 0
}
