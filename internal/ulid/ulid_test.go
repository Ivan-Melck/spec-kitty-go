package ulid

import (
	"testing"
	"time"
)

func TestMonotonic(t *testing.T) {
	fixed := time.UnixMilli(1700000000000)
	Now = func() time.Time { return fixed }
	defer func() { Now = time.Now }()
	a, b := New(), New()
	if !Valid(a) || !Valid(b) || !(a < b) {
		t.Fatalf("not monotonic/valid: %s %s", a, b)
	}
	ts, _ := Time(a)
	if !ts.Equal(fixed) {
		t.Fatalf("time %v", ts)
	}
}

func TestEncodeMatchesPython(t *testing.T) {
	var r [10]byte
	for i := range r {
		r[i] = byte(6 + i)
	}
	ms := uint64(0x000102030405)
	if got := encode(ms, r); got != "00041061050R3GG28A1C60T3GF" {
		t.Fatalf("got %s", got)
	}
}
